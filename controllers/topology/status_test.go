package topology //nolint:testpackage // tests exercise bounded aggregate status reconciliation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcileStatusRemainsBounded(t *testing.T) {
	scheme := apimachineryruntime.NewScheme()

	err := clabernetesapisv1alpha1.AddToScheme(scheme)
	if err != nil {
		t.Fatalf("failed adding clabernetes scheme: %s", err)
	}

	topology := &clabernetesapisv1alpha1.Topology{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "large-lab",
			Namespace: "clabernetes",
			UID:       "topology-uid",
		},
	}
	compiled := &CompiledTopology{
		Kind:  "containerlab",
		Nodes: make(map[string]*clabernetesutilcontainerlab.NodeDefinition),
		Links: make([]CompiledLink, 127),
	}
	objects := make([]ctrlruntimeclient.Object, 1, 129)
	objects[0] = topology
	controller := true

	for idx := range 128 {
		nodeName := fmt.Sprintf("node-%03d", idx)
		compiled.Nodes[nodeName] = &clabernetesutilcontainerlab.NodeDefinition{}
		objects = append(objects, &clabernetesapisv1alpha1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:      nodeName,
				Namespace: topology.GetNamespace(),
				Labels: map[string]string{
					clabernetesconstants.LabelTopologyOwner: topology.GetName(),
				},
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: clabernetesapisv1alpha1.SchemeGroupVersion.String(),
					Kind:       "Topology",
					Name:       topology.GetName(),
					UID:        topology.GetUID(),
					Controller: &controller,
				}},
			},
			Status: clabernetesapisv1alpha1.NodeStatus{
				Readiness: clabernetesconstants.NodeStatusReady,
			},
		})
	}

	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		Build()
	reconciler := &Reconciler{
		Log:    &claberneteslogging.FakeInstance{},
		Client: client,
	}

	err = reconciler.reconcileStatus(context.Background(), topology, compiled)
	if err != nil {
		t.Fatalf("reconciling topology status failed: %s", err)
	}

	if topology.Status.NodeCount != 128 || topology.Status.ReadyNodeCount != 128 ||
		topology.Status.LinkCount != 127 || !topology.Status.TopologyReady {
		t.Fatalf("unexpected aggregate status: %+v", topology.Status)
	}

	statusJSON, err := json.Marshal(topology.Status)
	if err != nil {
		t.Fatalf("marshaling topology status failed: %s", err)
	}

	if strings.Contains(string(statusJSON), "node-") {
		t.Fatalf("aggregate status copied per-node data: %s", statusJSON)
	}

	if len(statusJSON) > 2_000 {
		t.Fatalf("aggregate status unexpectedly grew with child count: %d bytes", len(statusJSON))
	}
}
