//nolint:testpackage // Exercise internal drift and startup batch helpers.
package topology

import (
	"context"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetescompiler "github.com/clabernetes/clabernetes/compiler"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesinternaldirectpod "github.com/clabernetes/clabernetes/internal/directpod"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func runStateTestNode(
	topology *clabernetesapisv1alpha1.Topology,
	name string,
	runState clabernetesapisv1alpha1.NodeRunState,
) *clabernetesapisv1alpha1.Node {
	return &clabernetesapisv1alpha1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: topology.Namespace, UID: apimachinerytypes.UID("uid-" + name),
			Labels: map[string]string{
				clabernetesconstants.LabelApp:           clabernetesconstants.Clabernetes,
				clabernetesconstants.LabelTopologyOwner: topology.Name,
			},
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(
					topology,
					clabernetesapisv1alpha1.SchemeGroupVersion.WithKind("Topology"),
				),
			},
		},
		Spec: clabernetesapisv1alpha1.NodeSpec{
			NodeDefinition: clabernetesapisv1alpha1.NodeDefinition{
				Kind: "linux", Image: "alpine:3",
			},
			RunState: runState,
		},
	}
}

// The compiler never renders runState, so an existing Running (schema default) or Stopped Node
// must not count as drift; otherwise every Topology reconcile would update the Node back.
func TestEmittedNodeConformsIgnoresRunState(t *testing.T) {
	t.Parallel()

	topology := conflictTestTopology()

	for _, runState := range []clabernetesapisv1alpha1.NodeRunState{
		clabernetesapisv1alpha1.NodeRunStateRunning,
		clabernetesapisv1alpha1.NodeRunStateStopped,
	} {
		existing := runStateTestNode(topology, "srl1", runState)
		rendered := runStateTestNode(topology, "srl1", "")

		if !emittedNodeConforms(existing, rendered) {
			t.Fatalf("runState %q reported as drift", runState)
		}

		if rendered.Spec.RunState != "" {
			t.Fatalf("conforms check mutated the rendered Node: %q", rendered.Spec.RunState)
		}

		rendered.Spec.Image = "alpine:4"
		if emittedNodeConforms(existing, rendered) {
			t.Fatalf("image drift hidden behind runState %q", runState)
		}
	}
}

// A drifted, stopped Node is updated to the compiled definition and stays stopped, keeping its
// pending restart and device-state reset requests.
func TestReconcileNodesUpdatesStoppedNodeAndKeepsOperationalState(t *testing.T) {
	t.Parallel()

	topology := conflictTestTopology()
	existing := runStateTestNode(topology, "srl1", clabernetesapisv1alpha1.NodeRunStateStopped)
	existing.Annotations = map[string]string{
		clabernetesinternaldirectpod.RestartRequestedAnnotation: "restart-1",
		clabernetesinternaldirectpod.DeviceStateResetAnnotation: "reset-1",
		"example.com/user": "kept-only-if-compiled",
	}
	existing.Status.Readiness = clabernetesconstants.NodeStatusNotReady
	client := ctrlruntimefake.NewClientBuilder().WithScheme(conflictTestScheme(t)).
		WithObjects(existing).Build()
	r := &Reconciler{Client: client, Log: &claberneteslogging.FakeInstance{}}

	rendered := runStateTestNode(topology, "srl1", "")
	rendered.Spec.Image = "alpine:4"

	if err := r.reconcileNodes(
		t.Context(), topology, []*clabernetesapisv1alpha1.Node{rendered},
	); err != nil {
		t.Fatal(err)
	}

	actual := &clabernetesapisv1alpha1.Node{}
	if err := client.Get(
		t.Context(), ctrlruntimeclient.ObjectKeyFromObject(existing), actual,
	); err != nil {
		t.Fatal(err)
	}

	if actual.Spec.Image != "alpine:4" {
		t.Fatalf("stopped Node was not updated: image %q", actual.Spec.Image)
	}

	if !actual.Spec.IsStopped() {
		t.Fatalf("drift update reset the run state to %q", actual.Spec.RunState)
	}

	for key, want := range map[string]string{
		clabernetesinternaldirectpod.RestartRequestedAnnotation: "restart-1",
		clabernetesinternaldirectpod.DeviceStateResetAnnotation: "reset-1",
	} {
		if actual.Annotations[key] != want {
			t.Fatalf("drift update dropped %s: %v", key, actual.Annotations)
		}
	}

	if actual.Status.Readiness != clabernetesconstants.NodeStatusNotReady {
		t.Fatalf("drift update dropped the Node status: %+v", actual.Status)
	}
}

// Changing only the run state is not drift: the Topology controller must not write the Node at
// all, so it cannot loop or race the user's patch.
func TestReconcileNodesDoesNotUpdateRunStateOnlyChange(t *testing.T) {
	t.Parallel()

	topology := conflictTestTopology()
	existing := runStateTestNode(topology, "srl1", clabernetesapisv1alpha1.NodeRunStateStopped)
	updates := 0
	client := interceptor.NewClient(
		ctrlruntimefake.NewClientBuilder().WithScheme(conflictTestScheme(t)).
			WithObjects(existing).Build(),
		interceptor.Funcs{
			Update: func(
				ctx context.Context,
				client ctrlruntimeclient.WithWatch,
				obj ctrlruntimeclient.Object,
				opts ...ctrlruntimeclient.UpdateOption,
			) error {
				updates++

				return client.Update(ctx, obj, opts...)
			},
		},
	)
	r := &Reconciler{Client: client, Log: &claberneteslogging.FakeInstance{}}

	for range 2 {
		rendered := runStateTestNode(topology, "srl1", "")
		if err := r.reconcileNodes(
			t.Context(), topology, []*clabernetesapisv1alpha1.Node{rendered},
		); err != nil {
			t.Fatal(err)
		}
	}

	if updates != 0 {
		t.Fatalf("run state difference caused %d Node updates", updates)
	}
}

// A held, stopped group starts no Pod: it must not consume a batch slot or block the batch, and
// it must keep its hold so a later start is admitted through the batch order.
func TestStartupBatchSkipsStoppedGroups(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	topology := conflictTestTopology()
	topology.Spec.Rollout = &clabernetesapisv1alpha1.TopologyRollout{BatchSize: 1}
	compiled := &clabernetescompiler.CompiledTopology{
		Nodes: map[string]*clabernetesutilcontainerlab.NodeDefinition{},
	}
	objects := make([]ctrlruntimeclient.Object, 0, 2)
	for _, node := range []*clabernetesapisv1alpha1.Node{
		runStateTestNode(topology, "a-stopped", clabernetesapisv1alpha1.NodeRunStateStopped),
		runStateTestNode(topology, "b-running", clabernetesapisv1alpha1.NodeRunStateRunning),
	} {
		node.Annotations = map[string]string{
			clabernetesconstants.AnnotationStartupHold: string(topology.UID),
		}
		compiled.Nodes[node.Name] = &clabernetesutilcontainerlab.NodeDefinition{}
		objects = append(objects, node)
	}
	client := ctrlruntimefake.NewClientBuilder().WithScheme(conflictTestScheme(t)).
		WithObjects(objects...).Build()
	r := &Reconciler{Client: client}

	held := func(name string) bool {
		t.Helper()

		node := &clabernetesapisv1alpha1.Node{}
		if err := client.Get(
			ctx, ctrlruntimeclient.ObjectKey{Namespace: topology.Namespace, Name: name}, node,
		); err != nil {
			t.Fatal(err)
		}

		return node.Annotations[clabernetesconstants.AnnotationStartupHold] != ""
	}

	pending, err := r.advanceStartupBatch(ctx, topology, compiled)
	if err != nil {
		t.Fatal(err)
	}

	if !pending || held("b-running") || !held("a-stopped") {
		t.Fatalf(
			"batch of one did not admit the running group ahead of the stopped one: "+
				"pending=%t running held=%t stopped held=%t",
			pending, held("b-running"), held("a-stopped"),
		)
	}

	// The admitted running group has no Pod sandbox yet, but only stopped groups still hold:
	// nothing is pending, so the Topology must not requeue every second.
	pending, err = r.advanceStartupBatch(ctx, topology, compiled)
	if err != nil {
		t.Fatal(err)
	}

	if pending || !held("a-stopped") {
		t.Fatalf("stopped group kept the batch pending=%t or lost its hold", pending)
	}
}
