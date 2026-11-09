package deviceplan

import (
	"context"
	"testing"

	clabruntime "github.com/srl-labs/containerlab/runtime"
)

type namespacePathRuntime struct {
	clabruntime.ContainerRuntime

	runtimeID string
}

func (r *namespacePathRuntime) GetNSPath(_ context.Context, runtimeID string) (string, error) {
	r.runtimeID = runtimeID

	return "/proc/self/ns/net", nil
}

func TestDeploymentConstructionDelegatesNamespaceObservation(t *testing.T) {
	t.Parallel()

	live := &namespacePathRuntime{}
	runtime := newImportedDeploymentConstructionRuntime(
		live, nil, nil, t.TempDir(),
	)
	runtime.beginDeployment()

	path, err := runtime.GetNSPath(context.Background(), "holder")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/proc/self/ns/net" || live.runtimeID != "holder" {
		t.Fatalf("namespace lookup path %q, target %q", path, live.runtimeID)
	}
	if boundaryErr := runtime.BoundaryFailure(); boundaryErr != nil {
		t.Fatalf("delegated namespace observation retained boundary failure: %v", boundaryErr)
	}
}

func TestConstructedContainerInventoryMatchesTargetNode(t *testing.T) {
	t.Parallel()

	plan := Plan{Containers: []ContainerPlan{
		{NodeID: "target", RuntimeID: "root"},
		{NodeID: "target", RuntimeID: "component"},
		{NodeID: "peer", RuntimeID: "other"},
	}}
	for _, test := range []struct {
		name string
		ids  []string
		want bool
	}{
		{"creation order differs", []string{"component", "root"}, true},
		{"missing component", []string{"root"}, false},
		{"extra component", []string{"root", "component", "extra"}, false},
		{"foreign component", []string{"root", "other"}, false},
		{"duplicate component", []string{"root", "root"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			containers := make([]RecordedContainer, 0, len(test.ids))
			for _, id := range test.ids {
				containers = append(containers, RecordedContainer{RuntimeID: id})
			}
			if err := verifyConstructedContainerIDs("target", plan, containers); (err == nil) != test.want {
				t.Fatalf("inventory check = %v, want accepted=%v", err, test.want)
			}
		})
	}
}
