//nolint:testpackage // tests exercise unexported reconciliation helpers.
package node

import (
	"testing"

	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
)

func TestDirectMetadataCopiesOriginalNodeNameLabel(t *testing.T) {
	t.Parallel()

	r := &Reconciler{configManagerGetter: clabernetesconfig.GetFakeManager}
	node := admissionTestNode("lab", "r1")
	node.Labels = map[string]string{
		clabernetesconstants.LabelOriginalNodeName: "R1",
		clabernetesconstants.LabelTopologyOwner:    "lab",
		clabernetesconstants.LabelIgnoreReconcile:  "true",
		"example.com/user":                         "kept",
	}

	labels, _ := r.directMetadata(node)

	for key, want := range map[string]string{
		clabernetesconstants.LabelOriginalNodeName: "R1",
		clabernetesconstants.LabelTopologyNode:     "r1",
		clabernetesconstants.LabelTopologyOwner:    "lab",
		"example.com/user":                         "kept",
	} {
		if labels[key] != want {
			t.Fatalf("Pod label %s = %q, want %q (labels %v)", key, labels[key], want, labels)
		}
	}

	if _, ok := labels[clabernetesconstants.LabelIgnoreReconcile]; ok {
		t.Fatalf("Pod labels carry other c9s.run labels: %v", labels)
	}
}
