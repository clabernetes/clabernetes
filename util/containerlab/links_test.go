package containerlab_test

import (
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
)

func testConflictLink(
	name,
	nodeA,
	interfaceA,
	nodeB,
	interfaceB string,
) clabernetesapisv1alpha1.Link {
	link := clabernetesapisv1alpha1.Link{}
	link.Name = name
	link.Namespace = "clabernetes"
	link.Spec.EndpointA = clabernetesapisv1alpha1.LinkEndpointSpec{
		NodeName:      nodeA,
		InterfaceName: interfaceA,
	}
	link.Spec.EndpointB = clabernetesapisv1alpha1.LinkEndpointSpec{
		NodeName:      nodeB,
		InterfaceName: interfaceB,
	}

	return link
}

func TestFindEndpointConflict(t *testing.T) {
	links := []clabernetesapisv1alpha1.Link{
		testConflictLink("b-conflict", "srl1", "e1-1", "srl3", "e1-1"),
		testConflictLink("a-winner", "srl1", "e1-1", "srl2", "e1-1"),
		// b-conflict loses its first endpoint, so its second endpoint remains available.
		testConflictLink("c-chain", "srl3", "e1-1", "srl4", "e1-1"),
	}

	if conflict := clabernetesutilcontainerlab.FindEndpointConflict(
		&links[0],
		links,
	); conflict != "a-winner" {
		t.Fatalf("expected b-conflict to lose to a-winner, got %q", conflict)
	}

	if conflict := clabernetesutilcontainerlab.FindEndpointConflict(
		&links[2],
		links,
	); conflict != "" {
		t.Fatalf("expected conflict chain endpoint to remain usable, got conflict %q", conflict)
	}
}
