//nolint:testpackage // Exercise the controller's namespace snapshot and conflict boundary.
package node

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesinternaldirectpod "github.com/clabernetes/clabernetes/internal/directpod"
	clabernetesinternaldirectruntime "github.com/clabernetes/clabernetes/internal/directruntime"
	k8scorev1 "k8s.io/api/core/v1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryschema "k8s.io/apimachinery/pkg/runtime/schema"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestPeerDirectoryRetriesAgainstFreshReader(t *testing.T) {
	t.Parallel()
	for _, missing := range []bool{false, true} {
		t.Run(
			map[bool]string{false: "stale resource version", true: "stale missing object"}[missing],
			func(t *testing.T) {
				t.Parallel()
				current := &k8scorev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "directory",
						Namespace:       "lab",
						ResourceVersion: "2",
						Labels:          map[string]string{"external": "preserved"},
					},
					Data: map[string]string{"peers.json": "old"},
				}
				base := ctrlruntimefake.NewClientBuilder().
					WithScheme(nodeReconcileTestScheme(t)).
					WithObjects(current).
					Build()
				var reads int
				cached := interceptor.NewClient(
					base,
					interceptor.Funcs{
						Get: func(_ context.Context, _ ctrlruntimeclient.WithWatch, key ctrlruntimeclient.ObjectKey, obj ctrlruntimeclient.Object, _ ...ctrlruntimeclient.GetOption) error {
							reads++
							if missing {
								return apimachineryerrors.NewNotFound(
									apimachineryschema.GroupResource{Resource: "configmaps"},
									key.Name,
								)
							}
							stale := current.DeepCopy()
							stale.ResourceVersion = "1"
							target, ok := obj.(*k8scorev1.ConfigMap)
							if !ok {
								t.Fatalf("unexpected cached type %T", obj)
							}
							stale.DeepCopyInto(target)

							return nil
						},
					},
				)
				reconciler := newPeerDirectoryReconciler(cached)
				reconciler.apiReader = base
				desired := current.DeepCopy()
				desired.ResourceVersion = ""
				desired.Labels = map[string]string{"managed": "yes"}
				desired.Data = map[string]string{
					clabernetesinternaldirectruntime.PeerDirectoryConfigMapKey: "new",
				}
				if err := reconciler.reconcileDirectPeerDirectoryShard(t.Context(), desired); err != nil {
					t.Fatal(err)
				}
				if reads != 1 {
					t.Fatalf("used stale cache %d times", reads)
				}
				stored := &k8scorev1.ConfigMap{}
				if err := base.Get(t.Context(), ctrlruntimeclient.ObjectKeyFromObject(current), stored); err != nil {
					t.Fatal(err)
				}
				if stored.Data[clabernetesinternaldirectruntime.PeerDirectoryConfigMapKey] != "new" ||
					stored.Labels["external"] != "preserved" ||
					stored.Labels["managed"] != "yes" {
					t.Fatalf("lost desired data or unrelated metadata: %+v", stored)
				}
			},
		)
	}
}

func TestPeerDirectorySerializesSnapshotsAndReleasesNamespaceWriters(t *testing.T) {
	t.Parallel()
	node := planInputTestNode("router", "uid-router", "linux", "example/linux:1")
	pod := &k8scorev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "router-pod",
			Namespace: node.Namespace,
			Labels:    map[string]string{clabernetesconstants.LabelDirectWorkload: "router"},
			Annotations: map[string]string{
				clabernetesinternaldirectpod.NodeUIDAnnotation: string(node.UID),
			},
		},
		Status: k8scorev1.PodStatus{PodIP: "10.244.0.1"},
	}
	base := ctrlruntimefake.NewClientBuilder().
		WithScheme(nodeReconcileTestScheme(t)).
		WithObjects(node, pod).
		WithStatusSubresource(pod).
		Build()
	firstWrite, release := make(chan struct{}), make(chan struct{})
	var first sync.Once
	defer first.Do(func() { close(release) })
	var createOnce sync.Once
	var snapshots atomic.Int32
	client := interceptor.NewClient(base, interceptor.Funcs{
		List: func(ctx context.Context, c ctrlruntimeclient.WithWatch, list ctrlruntimeclient.ObjectList, opts ...ctrlruntimeclient.ListOption) error {
			if _, ok := list.(*clabernetesapisv1alpha1.NodeList); ok {
				snapshots.Add(1)
			}

			return c.List(ctx, list, opts...)
		},
		Create: func(ctx context.Context, c ctrlruntimeclient.WithWatch, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.CreateOption) error {
			createOnce.Do(func() {
				close(firstWrite)
				select {
				case <-release:
				case <-ctx.Done():
				}
			})

			return c.Create(ctx, obj, opts...)
		},
	})
	reconciler := newPeerDirectoryReconciler(client)
	reconciler.apiReader = base
	const workers = 16
	results := make(chan error, workers)
	go func() { results <- reconciler.refreshDirectPeerDirectory(t.Context(), node.Namespace, nil) }()
	select {
	case <-firstWrite:
	case <-time.After(5 * time.Second):
		t.Fatal("writer did not start")
	}
	for range workers - 1 {
		go func() { results <- reconciler.refreshDirectPeerDirectory(t.Context(), node.Namespace, nil) }()
	}
	// Wait until all callers have registered at the writer, without relying on a sleep to
	// make them overlap. None may capture a stale snapshot before obtaining that writer.
	deadline := time.Now().Add(5 * time.Second)
	for {
		reconciler.peerDirectoryMu.Lock()
		waiting := reconciler.peerDirectoryWriters[node.Namespace].users
		reconciler.peerDirectoryMu.Unlock()
		if waiting == workers {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("concurrent callers did not register")
		}
		time.Sleep(time.Millisecond)
	}
	if got := snapshots.Load(); got != 1 {
		t.Fatalf("%d snapshots escaped the namespace writer", got)
	}
	pod.Status.PodIP = "10.244.0.2"
	if err := base.Status().Update(t.Context(), pod); err != nil {
		t.Fatal(err)
	}
	first.Do(func() { close(release) })
	for range workers {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	peerDirectoryShardVersions(
		t,
		base,
		node.Namespace,
		map[string]string{node.Name: pod.Status.PodIP},
	)
	if len(reconciler.peerDirectoryWriters) != 0 {
		t.Fatal("completed namespace writer retained")
	}
}
