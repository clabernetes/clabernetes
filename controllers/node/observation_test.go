//nolint:testpackage // Exercise internal reconciliation and cache boundaries.
package node

import (
	"context"
	"testing"
	"time"

	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetescontrollers "github.com/clabernetes/clabernetes/controllers"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	k8scorev1 "k8s.io/api/core/v1"
	ctrlruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestStatusObservationSkipsPlanningAndAuthoritativeEntropyRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	node := planInputTestNode(
		"future-a",
		"uid-future-a",
		"future-package-kind",
		"registry.example/device:1",
	)
	client, reconciler := newDirectProbeTestHarness(
		t,
		node,
		directProbeTestProfile(node.Namespace, "admin"),
	)
	reconciler.observations = &clabernetescontrollers.ObservationCache[*directObservation]{}
	reconcileDirectTestDeployment(ctx, t, reconciler, client, node)
	if err := client.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(node), node); err != nil {
		t.Fatal(err)
	}
	snapshot, _, valid := reconciler.observations.Load(ctrlruntimeclient.ObjectKeyFromObject(node))
	if !valid || snapshot == nil {
		t.Fatal("successful full reconcile did not retain observation inputs")
	}
	// Any attempt to re-enter planning/entropy validation now fails; observation must still run.
	reconciler.DirectRuntimeImage = ""
	reconciler.EntropyReconciler = nil
	if err := reconciler.Reconcile(ctx, node); err != nil {
		t.Fatal(err)
	}
	if snapshot.revalidateAt.Before(time.Now()) {
		t.Fatal("unexpected expired fixture")
	}
	before := snapshot.revalidateAt
	if delay := reconciler.observationRequeueAfter(node); delay > time.Until(before)+time.Second {
		t.Fatal("status updates postponed full watchdog validation")
	}
	deployment := snapshot.deployment.DeepCopy()
	deployment.Spec.Template.Spec.Containers = append(
		deployment.Spec.Template.Spec.Containers,
		k8scorev1.Container{Name: "foreign", Image: "foreign"},
	)
	if err := client.Update(ctx, deployment); err != nil {
		t.Fatal(err)
	}
	if handled, err := reconciler.refreshObservedStatus(ctx, node, snapshot); err != nil ||
		handled {
		t.Fatalf("deployment drift reused status snapshot: handled=%v err=%v", handled, err)
	}
	node.Generation++
	if handled, err := reconciler.refreshObservedStatus(ctx, node, snapshot); err != nil ||
		handled {
		t.Fatalf("new Node generation reused snapshot: handled=%v err=%v", handled, err)
	}
}

func TestHeldNodeDoesNotEnterPlanning(t *testing.T) {
	t.Parallel()
	node := planInputTestNode("held", "held-uid", "linux", "busybox")
	node.Annotations = map[string]string{clabernetesconstants.AnnotationStartupHold: "topology-uid"}
	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(plannerTestScheme(t)).
		WithObjects(node).
		Build()
	controller := &Controller{
		BaseController: &clabernetescontrollers.BaseController{
			Client: client,
			Log:    &claberneteslogging.FakeInstance{},
		},
	}
	result, err := controller.Reconcile(
		context.Background(),
		ctrlruntime.Request{NamespacedName: ctrlruntimeclient.ObjectKeyFromObject(node)},
	)
	if err != nil || result.RequeueAfter != directRequeueInterval {
		t.Fatalf("held Node reached planning: result=%v err=%v", result, err)
	}
}
