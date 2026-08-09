package node_test

import (
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetescontrollersnode "github.com/clabernetes/clabernetes/controllers/node"
)

func TestConfigDigest(t *testing.T) {
	nodeA := &clabernetesapisv1alpha1.Node{}
	nodeA.Name = "srl1"
	nodeA.Spec.Image = "ghcr.io/nokia/srlinux:latest"

	nodes := map[string]*clabernetesapisv1alpha1.Node{"srl1": nodeA}

	baseline, err := clabernetescontrollersnode.ConfigDigest(
		[]string{"srl1"},
		nodes,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	repeat, err := clabernetescontrollersnode.ConfigDigest([]string{"srl1"}, nodes, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if baseline != repeat {
		t.Fatal("expected digest to be deterministic")
	}

	changedNode := nodeA.DeepCopy()
	changedNode.Spec.StartupConfig = "set / system name host-name changed"

	changed, err := clabernetescontrollersnode.ConfigDigest(
		[]string{"srl1"},
		map[string]*clabernetesapisv1alpha1.Node{"srl1": changedNode},
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if changed == baseline {
		t.Fatal("expected digest to change when a member node definition changes")
	}
}
