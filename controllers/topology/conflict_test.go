package topology //nolint:testpackage // tests exercise unexported conflict helpers

import (
	"context"
	"reflect"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	k8sappsv1 "k8s.io/api/apps/v1"
	k8scorev1 "k8s.io/api/core/v1"
	k8srbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestFindChildResourceConflictsReportsAllKindsSorted(t *testing.T) {
	t.Parallel()

	topology := conflictTestTopology()
	scheme := conflictTestScheme(t)
	labels := conflictTestGeneratedLabels(topology)

	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(
			&clabernetesapisv1alpha1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "node-a",
					Namespace: topology.GetNamespace(),
				},
			},
			&clabernetesapisv1alpha1.Link{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "link-a",
					Namespace: topology.GetNamespace(),
				},
			},
			&clabernetesapisv1alpha1.NodeProfile{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "profile-a",
					Namespace: topology.GetNamespace(),
				},
			},
			&clabernetesapisv1alpha1.NodeProfile{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "profile-owned",
					Namespace: topology.GetNamespace(),
					Labels:    labels,
				},
			},
			&clabernetesapisv1alpha1.Link{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "link-owned",
					Namespace: topology.GetNamespace(),
					Labels:    labels,
				},
			},
			&clabernetesapisv1alpha1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "node-owned",
					Namespace: topology.GetNamespace(),
					Labels:    labels,
				},
			},
		).
		Build()

	reconciler := &Reconciler{Client: client}

	conflicts, err := reconciler.findChildResourceConflicts(
		context.Background(),
		topology,
		renderedChildren{
			nodeProfiles: []*clabernetesapisv1alpha1.NodeProfile{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "profile-a",
						Namespace: topology.GetNamespace(),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "profile-owned",
						Namespace: topology.GetNamespace(),
					},
				},
			},
			links: []*clabernetesapisv1alpha1.Link{
				{ObjectMeta: metav1.ObjectMeta{Name: "link-a", Namespace: topology.GetNamespace()}},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "link-owned",
						Namespace: topology.GetNamespace(),
					},
				},
			},
			nodes: []*clabernetesapisv1alpha1.Node{
				{ObjectMeta: metav1.ObjectMeta{Name: "node-a", Namespace: topology.GetNamespace()}},
				{ObjectMeta: metav1.ObjectMeta{
					Name:      "node-owned",
					Namespace: topology.GetNamespace(),
				}},
			},
		},
	)
	if err != nil {
		t.Fatalf("finding child resource conflicts failed: %s", err)
	}

	expected := []string{
		"link/link-a",
		"node/node-a",
		"nodeprofile/profile-a",
	}
	if !reflect.DeepEqual(conflicts, expected) {
		t.Fatalf("conflicts = %v, want %v", conflicts, expected)
	}
}

func TestReconcileChildConflictBlocksChildrenAndClearsAfterResolution(t *testing.T) {
	t.Parallel()

	topology := conflictTestTopology()
	scheme := conflictTestScheme(t)

	foreignNode := &clabernetesapisv1alpha1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "frr1",
			Namespace: topology.GetNamespace(),
		},
	}
	foreignLink := &clabernetesapisv1alpha1.Link{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "frr1-eth1-frr2-eth1",
			Namespace: topology.GetNamespace(),
		},
	}
	foreignProfile := &clabernetesapisv1alpha1.NodeProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "lab",
			Namespace: topology.GetNamespace(),
		},
	}

	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(topology, foreignNode, foreignLink, foreignProfile).
		WithStatusSubresource(&clabernetesapisv1alpha1.Topology{}).
		Build()
	reconciler := &Reconciler{
		Log:                 &claberneteslogging.FakeInstance{},
		Client:              client,
		configManagerGetter: clabernetesconfig.GetFakeManager,
	}

	result, err := reconciler.Reconcile(context.Background(), topology)
	if err != nil {
		t.Fatalf("reconciling conflicted topology failed: %s", err)
	}

	if result.RequeueAfter <= 0 {
		t.Fatalf("expected conflicted topology to requeue, got %+v", result)
	}

	expectedError := "duplicate resources found in the clabernetes namespace: " +
		"link/frr1-eth1-frr2-eth1, node/frr1, nodeprofile/lab\n" +
		"create the topology in a different namespace or disambiguate node names."
	if topology.Status.Error != expectedError {
		t.Fatalf("status error = %q, want %q", topology.Status.Error, expectedError)
	}

	assertOnlyForeignChildren(t, client, foreignNode, foreignLink, foreignProfile)
	deleteConflictingChildren(t, client, foreignNode, foreignLink, foreignProfile)

	result, err = reconciler.Reconcile(context.Background(), topology)
	if err != nil {
		t.Fatalf("reconciling resolved topology failed: %s", err)
	}

	if result.RequeueAfter != 0 {
		t.Fatalf("expected no conflict requeue after resolution, got %+v", result)
	}

	if topology.Status.Error != "" {
		t.Fatalf("expected conflict error to clear, got %q", topology.Status.Error)
	}

	assertGeneratedNodeCount(t, client, 2)
}

func assertOnlyForeignChildren(
	t *testing.T,
	client ctrlruntimeclient.Client,
	foreignNode *clabernetesapisv1alpha1.Node,
	foreignLink *clabernetesapisv1alpha1.Link,
	foreignProfile *clabernetesapisv1alpha1.NodeProfile,
) {
	t.Helper()

	var nodes clabernetesapisv1alpha1.NodeList

	err := client.List(context.Background(), &nodes)
	if err != nil {
		t.Fatalf("listing Nodes failed: %s", err)
	}

	if len(nodes.Items) != 1 || nodes.Items[0].GetName() != foreignNode.GetName() {
		t.Fatalf("expected no generated Nodes, got %v", nodes.Items)
	}

	var links clabernetesapisv1alpha1.LinkList

	err = client.List(context.Background(), &links)
	if err != nil {
		t.Fatalf("listing Links failed: %s", err)
	}

	if len(links.Items) != 1 || links.Items[0].GetName() != foreignLink.GetName() {
		t.Fatalf("expected no generated Links, got %v", links.Items)
	}

	var profiles clabernetesapisv1alpha1.NodeProfileList

	err = client.List(context.Background(), &profiles)
	if err != nil {
		t.Fatalf("listing NodeProfiles failed: %s", err)
	}

	if len(profiles.Items) != 1 || profiles.Items[0].GetName() != foreignProfile.GetName() {
		t.Fatalf("expected no generated NodeProfiles, got %v", profiles.Items)
	}
}

func deleteConflictingChildren(
	t *testing.T,
	client ctrlruntimeclient.Client,
	foreignNode *clabernetesapisv1alpha1.Node,
	foreignLink *clabernetesapisv1alpha1.Link,
	foreignProfile *clabernetesapisv1alpha1.NodeProfile,
) {
	t.Helper()

	for _, object := range []ctrlruntimeclient.Object{foreignNode, foreignLink, foreignProfile} {
		err := client.Delete(context.Background(), object)
		if err != nil {
			t.Fatalf(
				"deleting conflict %s/%s failed",
				object.GetNamespace(),
				object.GetName(),
			)
		}
	}
}

func assertGeneratedNodeCount(t *testing.T, client ctrlruntimeclient.Client, expectedCount int) {
	t.Helper()

	var nodes clabernetesapisv1alpha1.NodeList

	err := client.List(context.Background(), &nodes)
	if err != nil {
		t.Fatalf("listing generated Nodes failed: %s", err)
	}

	if len(nodes.Items) != expectedCount {
		t.Fatalf(
			"expected %d generated Nodes after conflict resolution, got %d",
			expectedCount,
			len(nodes.Items),
		)
	}
}

func conflictTestTopology() *clabernetesapisv1alpha1.Topology {
	return &clabernetesapisv1alpha1.Topology{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "lab",
			Namespace: "clabernetes",
			UID:       "topology-uid",
		},
		Spec: clabernetesapisv1alpha1.TopologySpec{
			Definition: clabernetesapisv1alpha1.Definition{
				Containerlab: `
name: lab
topology:
  nodes:
    frr1:
      kind: linux
      image: ghcr.io/example/linux:latest
    frr2:
      kind: linux
      image: ghcr.io/example/linux:latest
  links:
    - endpoints:
        - frr1:eth1
        - frr2:eth1
`,
			},
		},
	}
}

func conflictTestGeneratedLabels(
	topology *clabernetesapisv1alpha1.Topology,
) map[string]string {
	return map[string]string{
		clabernetesconstants.LabelApp:           clabernetesconstants.Clabernetes,
		clabernetesconstants.LabelTopologyOwner: topology.GetName(),
	}
}

func conflictTestScheme(t *testing.T) *apimachineryruntime.Scheme {
	t.Helper()

	scheme := apimachineryruntime.NewScheme()

	err := clabernetesapisv1alpha1.AddToScheme(scheme)
	if err != nil {
		t.Fatalf("adding clabernetes scheme failed: %s", err)
	}

	for _, addToScheme := range []func(*apimachineryruntime.Scheme) error{
		k8sappsv1.AddToScheme,
		k8scorev1.AddToScheme,
		k8srbacv1.AddToScheme,
	} {
		err = addToScheme(scheme)
		if err != nil {
			t.Fatalf("adding Kubernetes scheme failed: %s", err)
		}
	}

	return scheme
}
