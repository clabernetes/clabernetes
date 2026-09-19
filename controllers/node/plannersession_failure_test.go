//nolint:err113,testpackage // Reproduce an internal attach transport failure.
package node

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	k8scorev1 "k8s.io/api/core/v1"
	ctrlruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestPlannerSessionAttachFailureHonorsCancellation(t *testing.T) {
	input := validInput()
	canonical, err := input.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := input.Digest()
	if err != nil {
		t.Fatal(err)
	}
	node := planTestNode("router-session")
	inputConfigMap, _, err := (&PlannerInputConfigMapReconciler{}).Render(
		node, PlannerInputArtifact{CanonicalInput: canonical},
	)
	if err != nil {
		t.Fatal(err)
	}
	pod, err := RenderPlannerPod(PlannerPodInput{
		Node: node, Name: "router-session-planner", Image: "example/manager:1",
		InputConfigMapName: inputConfigMap.GetName(), InputDigest: digest,
		PlannerRevision: "session-v1", MaxInputBytes: 1 << 20,
		DeadlineSeconds: 1, Session: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	pod.Status.Phase = k8scorev1.PodRunning
	client := ctrlruntimefake.NewClientBuilder().WithScheme(plannerTestScheme(t)).
		WithObjects(node, inputConfigMap, pod).Build()
	attached := make(chan io.Closer, 1)
	reconciler := &PlannerSessionReconciler{
		Client: client, Reader: client,
		Attach: func(_ context.Context, _, _, _ string, input io.Reader, _, _ io.Writer) error {
			closer, ok := input.(io.Closer)
			if !ok {
				return errors.New("fixture requires closable input")
			}
			attached <- closer

			return errors.New("simulated attach transport failure before stdin is read")
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, reconcileErr := reconciler.Reconcile(ctx, ctrlruntime.Request{
			NamespacedName: plannerObjectKey(pod.GetNamespace(), pod.GetName()),
		})
		done <- reconcileErr
	}()
	var inputPipe io.Closer
	select {
	case inputPipe = <-attached:
	case <-time.After(5 * time.Second):
		t.Fatal("fixture did not reach attach")
	}
	cancel()
	select {
	case <-done:
		_ = inputPipe.Close()
	case <-time.After(1500 * time.Millisecond):
		t.Error(
			"reconcile remains blocked after attach failure, context cancellation, and the 1-second session deadline",
		)
		// Release the blocked writer so this diagnostic leaves no goroutine behind.
		_ = inputPipe.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("closing the input pipe did not release reconciliation")
		}
	}
}
