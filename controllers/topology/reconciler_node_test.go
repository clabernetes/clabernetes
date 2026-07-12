package topology_test

import (
	"testing"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/srl-labs/clabernetes/config"
	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	clabernetescontrollerstopology "github.com/srl-labs/clabernetes/controllers/topology"
	claberneteslogging "github.com/srl-labs/clabernetes/logging"
	clabernetesutilcontainerlab "github.com/srl-labs/clabernetes/util/containerlab"
	"gopkg.in/yaml.v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimeclientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcileNodesRepairsMalformedExistingConfig(t *testing.T) {
	const topologyName = "malformed-child"

	topology := &clabernetesapisv1alpha1.Topology{
		ObjectMeta: metav1.ObjectMeta{
			Name: topologyName, Namespace: "default", UID: "topology-uid",
		},
	}
	existing := &clabernetesapisv1alpha1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: topologyName + "-r1", Namespace: "default",
			Labels: map[string]string{
				clabernetesconstants.LabelTopologyOwner: topologyName,
				clabernetesconstants.LabelTopologyNode:  "r1",
			},
		},
		Spec: clabernetesapisv1alpha1.NodeSpec{Config: "topology: [unterminated"},
	}
	desiredConfig := &clabernetesutilcontainerlab.Config{
		Topology: &clabernetesutilcontainerlab.Topology{},
	}

	reconcileData, err := clabernetescontrollerstopology.NewReconcileData(topology)
	if err != nil {
		t.Fatal(err)
	}

	reconcileData.ResolvedConfigs["r1"] = desiredConfig

	scheme := apimachineryruntime.NewScheme()

	err = clabernetesapisv1alpha1.AddToScheme(scheme)
	if err != nil {
		t.Fatal(err)
	}

	fakeClient := ctrlruntimeclientfake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(existing).
		Build()
	reconciler := clabernetescontrollerstopology.NewReconciler(
		&claberneteslogging.FakeInstance{},
		fakeClient,
		"clabernetes",
		"clabernetes",
		"containerd",
		clabernetesconfig.GetFakeManager,
	)

	err = reconciler.ReconcileNodes(t.Context(), topology, reconcileData)
	if err != nil {
		t.Fatalf("expected malformed controller-owned Node to be repaired, got: %v", err)
	}

	updated := &clabernetesapisv1alpha1.Node{}

	err = fakeClient.Get(
		t.Context(),
		ctrlruntimeclient.ObjectKeyFromObject(existing),
		updated,
	)
	if err != nil {
		t.Fatal(err)
	}

	parsed := &clabernetesutilcontainerlab.Config{}

	err = yaml.Unmarshal([]byte(updated.Spec.Config), parsed)
	if err != nil {
		t.Fatalf("repaired Node still has invalid config: %v", err)
	}

	if !reconcileData.NodesNeedingReboot.Contains("r1") {
		t.Fatal("expected repaired Node launcher to be marked for restart")
	}
}
