//nolint:testpackage // Exercise internal reconciliation and cache boundaries.
package topology

import (
	"context"
	"fmt"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetescompiler "github.com/clabernetes/clabernetes/compiler"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesinternaldirectpod "github.com/clabernetes/clabernetes/internal/directpod"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestStartupBatchesResumeAndDoNotWaitForCrossBatchLinks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	topology := conflictTestTopology()
	topology.Spec.Rollout = &clabernetesapisv1alpha1.TopologyRollout{BatchSize: 2}
	compiled := &clabernetescompiler.CompiledTopology{
		Nodes: map[string]*clabernetesutilcontainerlab.NodeDefinition{},
	}
	objects := make([]ctrlruntimeclient.Object, 0, 6)
	for i := range 6 {
		name := fmt.Sprintf("node-%d", i)
		compiled.Nodes[name] = &clabernetesutilcontainerlab.NodeDefinition{}
		node := &clabernetesapisv1alpha1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: topology.Namespace,
				UID:       apimachinerytypes.UID(name),
				Annotations: map[string]string{
					clabernetesconstants.AnnotationStartupHold: string(topology.UID),
				},
				OwnerReferences: []metav1.OwnerReference{
					*metav1.NewControllerRef(topology, clabernetesapisv1alpha1.SchemeGroupVersion.WithKind("Topology")),
				},
			},
		}
		if i == 5 {
			node.Spec.NetworkMode = "container:node-0"
		}
		objects = append(objects, node)
	}
	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(conflictTestScheme(t)).
		WithObjects(objects...).
		Build()
	r := &Reconciler{Client: client}
	advance := func(want int) {
		t.Helper()
		if _, err := r.advanceStartupBatch(ctx, topology, compiled); err != nil {
			t.Fatal(err)
		}
		list := &clabernetesapisv1alpha1.NodeList{}
		if err := client.List(ctx, list); err != nil {
			t.Fatal(err)
		}
		admitted := 0
		for _, node := range list.Items {
			if node.Annotations[clabernetesconstants.AnnotationStartupHold] == "" {
				admitted++
			}
		}
		if admitted != want {
			t.Fatalf("admitted %d Nodes, want %d", admitted, want)
		}
	}
	advance(
		3,
	) // Two primary workloads, including the shared-network secondary.
	r = &Reconciler{Client: client} // Simulate controller restart with no retained admission state.
	advance(3)
	for i := range 2 {
		pod := &k8scorev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("pod-%d", i),
				Namespace: topology.Namespace,
				Labels: map[string]string{
					clabernetesconstants.LabelDirectWorkload: fmt.Sprintf("node-%d", i),
				},
				Annotations: map[string]string{
					clabernetesinternaldirectpod.NodeUIDAnnotation: fmt.Sprintf("node-%d", i),
				},
			},
			Status: k8scorev1.PodStatus{
				Conditions: []k8scorev1.PodCondition{
					{Type: k8scorev1.PodReadyToStartContainers, Status: k8scorev1.ConditionTrue},
					{Type: k8scorev1.PodReady, Status: k8scorev1.ConditionFalse},
				},
			},
		}
		if err := client.Create(ctx, pod); err != nil {
			t.Fatal(err)
		}
	}
	advance(5)
	advance(5) // The second batch has no sandboxes yet.
	topology.Spec.Rollout = &clabernetesapisv1alpha1.TopologyRollout{BatchSize: 0}
	advance(6) // Disabling batching releases the remaining workload.
}

func TestStartupBatchPartialAdmissionDoesNotReleaseAnotherBatch(t *testing.T) {
	t.Parallel()
	topology := conflictTestTopology()
	topology.Spec.Rollout = &clabernetesapisv1alpha1.TopologyRollout{BatchSize: 2}
	client := ctrlruntimefake.NewClientBuilder().WithScheme(conflictTestScheme(t)).
		WithObjects(topology).WithStatusSubresource(topology).Build()
	patches := 0
	failed := false
	faulty := interceptor.NewClient(client, interceptor.Funcs{
		Patch: func(ctx context.Context, c ctrlruntimeclient.WithWatch, obj ctrlruntimeclient.Object, patch ctrlruntimeclient.Patch, opts ...ctrlruntimeclient.PatchOption) error {
			patches++
			if patches == 2 {
				failed = true

				return errInjectedTopologyConflict
			}

			return c.Patch(ctx, obj, patch, opts...)
		},
	})
	r := NewReconciler(
		&claberneteslogging.FakeInstance{},
		faulty,
		client,
		clabernetesconfig.GetFakeManager,
	)
	if _, err := r.Reconcile(t.Context(), topology); err == nil || !failed {
		t.Fatalf("expected injected admission failure, got %v", err)
	}
	// Restart after one durable admission and one failed patch. The existing Pod group
	// must pass the sandbox barrier before another group can be admitted.
	r = NewReconciler(
		&claberneteslogging.FakeInstance{},
		client,
		client,
		clabernetesconfig.GetFakeManager,
	)
	if _, err := r.Reconcile(t.Context(), topology); err != nil {
		t.Fatal(err)
	}
	nodes := &clabernetesapisv1alpha1.NodeList{}
	if err := client.List(t.Context(), nodes); err != nil {
		t.Fatal(err)
	}
	held := 0
	for _, node := range nodes.Items {
		if node.Annotations[clabernetesconstants.AnnotationStartupHold] != "" {
			held++
		}
	}
	if len(nodes.Items) != 2 || held != 1 {
		t.Fatalf("partial batch escaped barrier: %d Nodes, %d held", len(nodes.Items), held)
	}
	links := &clabernetesapisv1alpha1.LinkList{}
	if err := client.List(t.Context(), links); err != nil {
		t.Fatal(err)
	}
	if len(links.Items) == 0 {
		t.Fatal("batch admission hid later-batch link definitions")
	}
}

func TestStartupBatchWaitsForCompleteInventory(t *testing.T) {
	t.Parallel()
	topology := conflictTestTopology()
	topology.Spec.Rollout = &clabernetesapisv1alpha1.TopologyRollout{BatchSize: 100}
	compiled := &clabernetescompiler.CompiledTopology{
		Nodes: map[string]*clabernetesutilcontainerlab.NodeDefinition{"present": {}, "missing": {}},
	}
	node := &clabernetesapisv1alpha1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "present",
			Namespace: topology.Namespace,
			Annotations: map[string]string{
				clabernetesconstants.AnnotationStartupHold: string(topology.UID),
			},
			OwnerReferences: []metav1.OwnerReference{{UID: topology.UID}},
		},
	}
	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(conflictTestScheme(t)).
		WithObjects(node).
		Build()
	r := &Reconciler{Client: client}
	if pending, err := r.advanceStartupBatch(context.Background(), topology, compiled); err != nil ||
		!pending {
		t.Fatalf("pending=%v err=%v", pending, err)
	}
	if err := client.Get(context.Background(), ctrlruntimeclient.ObjectKeyFromObject(node), node); err != nil {
		t.Fatal(err)
	}
	if node.Annotations[clabernetesconstants.AnnotationStartupHold] == "" {
		t.Fatal("admitted against partial management/link inventory")
	}
}

func TestTopologyNodeUpdatePreservesGlobalStartupAdmission(t *testing.T) {
	t.Parallel()
	topology := conflictTestTopology()
	node := &clabernetesapisv1alpha1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "device", Namespace: topology.Namespace, UID: "device-uid",
			Annotations: map[string]string{
				clabernetesconstants.AnnotationStartupAdmitted: "device-uid",
			},
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(topology, clabernetesapisv1alpha1.SchemeGroupVersion.WithKind("Topology")),
			},
		},
	}
	client := ctrlruntimefake.NewClientBuilder().WithScheme(conflictTestScheme(t)).
		WithObjects(node).Build()
	r := &Reconciler{Client: client, Log: &claberneteslogging.FakeInstance{}}
	rendered := node.DeepCopy()
	rendered.Annotations = nil
	rendered.Spec.Image = "new-image"
	if err := r.reconcileNodes(t.Context(), topology, []*clabernetesapisv1alpha1.Node{rendered}); err != nil {
		t.Fatal(err)
	}
	if err := client.Get(t.Context(), ctrlruntimeclient.ObjectKeyFromObject(node), node); err != nil {
		t.Fatal(err)
	}
	if node.Spec.Image != "new-image" ||
		node.Annotations[clabernetesconstants.AnnotationStartupAdmitted] != string(node.UID) {
		t.Fatalf(
			"Node update lost global admission: spec=%+v annotations=%v",
			node.Spec,
			node.Annotations,
		)
	}
}
