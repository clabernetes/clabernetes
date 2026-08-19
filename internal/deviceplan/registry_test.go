package deviceplan_test

import (
	"testing"

	clabernetesdeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
)

func TestContainerlabRegistryIsDiscoveredLive(t *testing.T) {
	registry := clabernetesdeviceplan.NewContainerlabRegistry()
	kinds := registry.GetRegisteredNodeKindNames()
	if len(kinds) == 0 {
		t.Fatal("imported containerlab registry is empty")
	}

	for _, kind := range kinds {
		if registry.Kind(kind) == nil {
			t.Errorf("registry.Kind(%q) returned nil", kind)
		}
		if node, nodeErr := registry.NewNodeOfKind(kind); nodeErr != nil || node == nil {
			t.Errorf("registry.NewNodeOfKind(%q) = (%T, %v), want a node", kind, node, nodeErr)
		}
	}
}
