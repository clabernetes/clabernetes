//nolint:err113,gocyclo,testpackage // Internal request lifecycle fixtures exercise unexported planner boundaries.
package node

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func poolTestPod(name string) *k8scorev1.Pod {
	return &k8scorev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "manager",
			UID:       apimachinerytypes.UID(name),
			Labels:    map[string]string{plannerPoolLabel: "c9s"},
		},
		Spec: k8scorev1.PodSpec{
			Containers: []k8scorev1.Container{
				{Name: plannerContainerName, Image: "example/manager:1"},
			},
		},
		Status: k8scorev1.PodStatus{
			Phase: k8scorev1.PodRunning,
			Conditions: []k8scorev1.PodCondition{
				{Type: k8scorev1.PodReady, Status: k8scorev1.ConditionTrue},
			},
		},
	}
}

func TestPlannerPoolReusesPodAcrossNodesAndCachesResults(t *testing.T) {
	ctx := context.Background()
	pod := poolTestPod("worker")
	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(plannerTestScheme(t)).
		WithObjects(pod).
		Build()
	pool := &PlannerPool{
		Client:    client,
		Reader:    client,
		Namespace: "manager",
		AppName:   "c9s",
		Image:     "example/manager:1",
	}
	var calls atomic.Int32
	pool.Execute = func(_ context.Context, namespace, name, _ string, input io.Reader, output, _ io.Writer) error {
		if namespace != pod.Namespace || name != pod.Name {
			return errors.New("unexpected worker")
		}
		calls.Add(1)
		var size uint64
		if err := binary.Read(input, binary.BigEndian, &size); err != nil {
			return err
		}
		var bootstrap clabernetesinternaldeviceplan.PoolBootstrap
		if err := json.NewDecoder(io.LimitReader(input, int64(size))).Decode(&bootstrap); err != nil {
			return err
		}
		initial, err := clabernetesinternaldeviceplan.NewSessionFrameDecoder(input, 1<<20).Next()
		if err != nil {
			return err
		}
		if clabernetesinternaldeviceplan.Digest(bootstrap.Entropy) != initial.Input.EntropyDigest {
			return errors.New("wrong request entropy")
		}
		plan := validPlannerResult(t, *initial.Input, "test")

		return clabernetesinternaldeviceplan.WriteSessionFrame(
			output,
			clabernetesinternaldeviceplan.SessionFrame{
				Version: clabernetesinternaldeviceplan.SessionProtocolVersion, Type: clabernetesinternaldeviceplan.SessionFrameResult,
				SessionDigest: initial.SessionDigest, Sequence: 1,
				Result: &clabernetesinternaldeviceplan.SessionResult{
					Input: *initial.Input,
					Plan:  plan,
				},
			},
		)
	}
	reconciler := &PlannerReconciler{Client: client, Pool: pool}
	for _, name := range []string{"router-a", "router-b"} {
		node := planTestNode(name)
		node.UID = apimachinerytypes.UID(name)
		if err := client.Create(ctx, node); err != nil {
			t.Fatal(err)
		}
		entropy, err := (&EntropyReconciler{Client: client, Reader: client}).Resolve(ctx, node)
		if err != nil {
			t.Fatal(err)
		}
		input := validInput()
		input.EntropyDigest = entropy.Digest
		attempt := PlannerAttempt{
			Node:              node,
			Input:             input,
			Image:             pool.Image,
			PlannerRevision:   "test",
			EntropySecretName: entropy.SecretName,
		}
		for range 2 {
			result, planErr := reconciler.Reconcile(ctx, attempt)
			if planErr != nil {
				t.Fatal(planErr)
			}
			if result.State != PlannerStateSucceeded {
				t.Fatalf("planner state: %s", result.State)
			}
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("executed %d requests, expected one per Node", calls.Load())
	}
	pods := &k8scorev1.PodList{}
	if err := client.List(ctx, pods); err != nil {
		t.Fatal(err)
	}
	if len(pods.Items) != 1 || pods.Items[0].UID != pod.UID {
		t.Fatal("planning created or replaced a worker Pod")
	}
}

func TestPlannerPoolBoundsConcurrentRequests(t *testing.T) {
	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(plannerTestScheme(t)).
		WithObjects(poolTestPod("one"), poolTestPod("two")).
		Build()
	pool := &PlannerPool{
		Client:    client,
		Namespace: "manager",
		AppName:   "c9s",
		Image:     "example/manager:1",
	}
	results := make(chan *k8scorev1.Pod, 8)
	for range 8 {
		go func() { pod, _ := pool.acquire(context.Background()); results <- pod }()
	}
	var acquired []*k8scorev1.Pod
	for range 8 {
		if pod := <-results; pod != nil {
			acquired = append(acquired, pod)
		}
	}
	if len(acquired) != 2 || acquired[0].UID == acquired[1].UID {
		t.Fatalf("pool allocated %d workers", len(acquired))
	}
	pool.release(acquired[0])
	if _, err := pool.acquire(context.Background()); err != nil {
		t.Fatalf("released worker unavailable: %v", err)
	}
}

func TestPlannerPoolExecFailureAndCancellationUnblockInput(t *testing.T) {
	for _, mode := range []string{"failed-exec", "cancelled-exec"} {
		t.Run(mode, func(t *testing.T) {
			pool := &PlannerPool{
				Execute: func(ctx context.Context, _, _, _ string, _ io.Reader, _, _ io.Writer) error {
					if mode == "cancelled-exec" {
						<-ctx.Done()
					}

					return errors.New("transport unavailable")
				},
			}
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := pool.converse(
					ctx,
					poolTestPod("worker"),
					planTestNode("router"),
					PlannerPodInput{},
					clabernetesinternaldeviceplan.PoolBootstrap{Input: validInput()},
				)
				done <- err
			}()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("expected transport failure")
				}
			case <-time.After(time.Second):
				t.Fatal("failed/cancelled exec left request writer blocked")
			}
		})
	}
}
