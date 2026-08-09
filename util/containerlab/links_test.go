package containerlab_test

import (
	"reflect"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
)

func TestActiveLinks(t *testing.T) {
	links := []clabernetesapisv1alpha1.Link{
		testDigestLink("b-conflict", "srl1", "e1-1", "srl3", "e1-1"),
		testDigestLink("a-winner", "srl1", "e1-1", "srl2", "e1-1"),
		// b-conflict is rejected, so its otherwise-unused second endpoint remains available.
		testDigestLink("c-chain", "srl3", "e1-1", "srl4", "e1-1"),
		// An invalid, lexically earlier link must not reserve an endpoint from a valid link.
		testDigestLink("d-invalid", "srl5", "e1-1", "srl5", "e1-1"),
		testDigestLink("e-after-invalid", "srl5", "e1-1", "srl6", "e1-1"),
		testDigestLink("f-rejected", "srl7", "e1-1", "srl8", "e1-1"),
	}
	links[5].Status.Error = "endpoint already claimed remotely"

	active := clabernetesutilcontainerlab.ActiveLinks(links)
	activeNames := make([]string, len(active))

	for idx := range active {
		activeNames[idx] = active[idx].GetName()
	}

	expectedNames := []string{"a-winner", "c-chain", "e-after-invalid"}
	if !reflect.DeepEqual(activeNames, expectedNames) {
		t.Fatalf("expected active links %v, got %v", expectedNames, activeNames)
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
