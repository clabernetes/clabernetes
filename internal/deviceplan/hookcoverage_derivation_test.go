package deviceplan_test

import (
	"context"
	"slices"
	"testing"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	clabnodes "github.com/srl-labs/containerlab/nodes"
)

// TestRefusedBehaviorsAreDerivedFromTheImportedImplementation proves kind reliance is derived, not
// listed: a kind is reported only when its own implementation chain declares the refused method.
func TestRefusedBehaviorsAreDerivedFromTheImportedImplementation(t *testing.T) {
	t.Parallel()

	registry := clabernetesinternaldeviceplan.NewContainerlabRegistry()

	cases := []struct {
		kind    string
		methods []string
	}{
		// tailscale declares PreDestroy itself; every kind carries the topology-level refusal.
		{"tailscale", []string{"nodes.Node.PreDestroy", "nodes.Node.RunExecFromConfig"}},
		// VM-backed kinds inherit the pre-stop contract through their embedded VM node.
		{"vr-cisco_xrv", []string{"nodes.Node.RunExecFromConfig", "nodes.NodeOverwrites.PreStop"}},
		// A container-based SR-SIM node declares neither refused method.
		{"nokia_srsim", []string{"nodes.Node.RunExecFromConfig"}},
		// A plain container kind declares neither refused method.
		{"linux", []string{"nodes.Node.RunExecFromConfig"}},
		{"srl", []string{"nodes.Node.RunExecFromConfig"}},
	}
	for _, testCase := range cases {
		refusals, err := clabernetesinternaldeviceplan.RefusedBehaviorsForKind(
			registry,
			testCase.kind,
		)
		if err != nil {
			t.Fatalf("deriving refusals for %q: %v", testCase.kind, err)
		}
		methods := make([]string, 0, len(refusals))
		for _, refusal := range refusals {
			methods = append(methods, refusal.Interface+"."+refusal.Method)
		}
		if !slices.Equal(methods, testCase.methods) {
			t.Fatalf("refused behaviors for %q are %v, want %v",
				testCase.kind, methods, testCase.methods)
		}
	}
}

// TestRefusedBehaviorsReportAKindThatStartsDeclaringTheMethod is the drift regression: a kind that
// begins to declare a refused method is reported without editing the inventory.
func TestRefusedBehaviorsReportAKindThatStartsDeclaringTheMethod(t *testing.T) {
	t.Parallel()

	registry := clabnodes.NewNodeRegistry()
	if err := registry.Register(
		[]string{"synthetic-prestop"},
		func() clabnodes.Node { return &preStopSyntheticNode{} },
		clabnodes.NewNodeRegistryEntryAttributes(nil, nil, nil).WithPrivilegedByDefault(false),
	); err != nil {
		t.Fatal(err)
	}

	refusals, err := clabernetesinternaldeviceplan.RefusedBehaviorsForKind(
		registry,
		"synthetic-prestop",
	)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, refusal := range refusals {
		if refusal.Interface+"."+refusal.Method == "nodes.NodeOverwrites.PreStop" {
			found = true
		}
	}
	if !found {
		t.Fatalf("a kind that started declaring PreStop was not reported: %#v", refusals)
	}

	// The same kind without the declaration is not reported, so the derivation is not blanket.
	plain, err := clabernetesinternaldeviceplan.RefusedBehaviorsForKind(
		clabernetesinternaldeviceplan.NewContainerlabRegistry(),
		"linux",
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, refusal := range plain {
		if refusal.Method == "PreStop" {
			t.Fatalf("a kind that never declares PreStop was reported: %#v", plain)
		}
	}
}

// preStopSyntheticNode models an imported kind that starts relying on a refused method.
type preStopSyntheticNode struct {
	*syntheticImportedNode
}

func (*preStopSyntheticNode) PreStop(context.Context) error { return nil }
