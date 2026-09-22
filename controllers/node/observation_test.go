//nolint:testpackage // Exercise internal reconciliation and cache boundaries.
package node

import (
	"context"
	"testing"
	"time"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
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
	assertObservationRejectsExpiryAndMemberDrift(t, reconciler, client, node, snapshot)
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

func TestExpiredObservationUsesWatchdogPace(t *testing.T) {
	t.Parallel()
	node := planInputTestNode("pending", "pending-uid", "linux", "busybox")
	cache := &clabernetescontrollers.ObservationCache[*directObservation]{}
	key := ctrlruntimeclient.ObjectKeyFromObject(node)
	_, token, _ := cache.Load(key)
	cache.Store(key, token, &directObservation{revalidateAt: time.Now().Add(-time.Minute)})
	reconciler := &Reconciler{observations: cache}
	for range 3 {
		if got := reconciler.observationRequeueAfter(node); got != directRequeueInterval {
			t.Fatalf("expired snapshot retry = %s, want %s", got, directRequeueInterval)
		}
	}
}

func TestDependencyRetriesSlowDownAndReset(t *testing.T) {
	t.Parallel()
	controller := &Controller{}
	node := planInputTestNode("pending", "pending-uid", "linux", "busybox")
	for range 2 {
		for range dependencyFastRetries {
			if got := controller.dependencyRetryAfter(node, nil); got != plannerPoolRetryDelay {
				t.Fatalf("initial retry = %s", got)
			}
		}
		for range 100 {
			if got := controller.dependencyRetryAfter(node, nil); got != directRequeueInterval {
				t.Fatalf("persistent dependency retry = %s", got)
			}
		}
		controller.resetDependencyRetry(ctrlruntimeclient.ObjectKeyFromObject(node))
	}
	for range dependencyFastRetries {
		controller.dependencyRetryAfter(node, nil)
	}
	node.Generation++
	if got := controller.dependencyRetryAfter(node, nil); got != plannerPoolRetryDelay {
		t.Fatalf("spec edit did not reset retries: %s", got)
	}
}

func assertObservationRejectsExpiryAndMemberDrift(
	t *testing.T,
	reconciler *Reconciler,
	client ctrlruntimeclient.Client,
	node *clabernetesapisv1alpha1.Node,
	snapshot *directObservation,
) {
	t.Helper()
	ctx := t.Context()
	expired := *snapshot
	expired.revalidateAt = time.Now().Add(-time.Second)
	if handled, err := reconciler.refreshObservedStatus(ctx, node, &expired); err != nil ||
		handled {
		t.Fatalf("expired snapshot reused: handled=%v err=%v", handled, err)
	}
	member := node.DeepCopy()
	member.Name, member.ResourceVersion, member.UID = "member", "", "member-uid"
	if err := client.Create(ctx, member); err != nil {
		t.Fatal(err)
	}
	withMember := *snapshot
	withMember.members = append([]string{member.Name}, snapshot.members...)
	withMember.nodes = map[string]*clabernetesapisv1alpha1.Node{member.Name: member.DeepCopy()}
	member.Spec.Image = "changed-image"
	if err := client.Update(ctx, member); err != nil {
		t.Fatal(err)
	}
	if handled, err := reconciler.refreshObservedStatus(ctx, node, &withMember); err != nil ||
		handled {
		t.Fatalf("member drift reused snapshot: handled=%v err=%v", handled, err)
	}
}

func TestBusyPoolRetriesStayFastWhileWorkersComplete(t *testing.T) {
	t.Parallel()
	pool := &PlannerPool{}
	controller := &Controller{reconciler: &Reconciler{
		PlannerReconciler: &PlannerReconciler{Pool: pool},
	}}
	node := planInputTestNode("pending", "pending-uid", "linux", "busybox")
	for range 20 {
		for range dependencyFastRetries {
			if got := controller.dependencyRetryAfter(node, ErrPlannerPoolBusy); got != plannerPoolRetryDelay {
				t.Fatalf("healthy saturation slowed to %s", got)
			}
		}
		if got := controller.dependencyRetryAfter(node, ErrPlannerPoolBusy); got != directRequeueInterval {
			t.Fatalf("stalled pool retry = %s", got)
		}
		pool.release(&k8scorev1.Pod{})
	}
}
