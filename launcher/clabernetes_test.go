package launcher //nolint:testpackage // tests exercise group readiness helpers

import (
	"context"
	"reflect"
	"testing"
)

func TestLauncherGroupMembers(t *testing.T) {
	t.Parallel()

	got := launcherGroupMembers("primary", "secondary-b, secondary-a,primary,,secondary-a")
	want := []string{"primary", "secondary-a", "secondary-b"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("launcherGroupMembers() = %v, want %v", got, want)
	}
}

func TestGetGroupContainerReadinessIncludesSecondaryNodes(t *testing.T) {
	t.Parallel()

	checked := []string{}

	member, ready, err := getGroupContainerReadiness(
		context.Background(),
		map[string]string{"primary": "primary-id", "secondary": "secondary-id"},
		func(_ context.Context, containerID string) (bool, error) {
			checked = append(checked, containerID)

			return containerID != "secondary-id", nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if ready || member != "secondary" {
		t.Fatalf("group readiness = (%q, %t), want secondary false", member, ready)
	}

	if !reflect.DeepEqual(checked, []string{"primary-id", "secondary-id"}) {
		t.Fatalf("checked containers = %v, want primary and secondary", checked)
	}
}

func TestGetGroupContainerReadinessIncludesExpandedComponents(t *testing.T) {
	t.Parallel()

	checked := []string{}

	member, ready, err := getGroupContainerReadiness(
		context.Background(),
		map[string]string{
			"srsim-1": "line-card-id",
			"srsim-a": "cpm-id",
			"srsim-b": "backup-id",
		},
		func(_ context.Context, containerID string) (bool, error) {
			checked = append(checked, containerID)

			return true, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if !ready || member != "" {
		t.Fatalf("component readiness = (%q, %t), want all ready", member, ready)
	}

	if !reflect.DeepEqual(checked, []string{"line-card-id", "cpm-id", "backup-id"}) {
		t.Fatalf("checked components = %v", checked)
	}
}

func TestNodeProbeContainerIDUsesNamespaceOwner(t *testing.T) {
	t.Parallel()

	got := nodeProbeContainerID(
		"srsim",
		map[string]string{"srsim": "line-card-id"},
	)
	if got != "line-card-id" {
		t.Fatalf("probe container = %q, want line-card-id", got)
	}
}

func TestSortedContainerNames(t *testing.T) {
	t.Parallel()

	got := sortedContainerNames(map[string]string{
		"secondary": "container-b",
		"primary":   "container-a",
	})
	want := []string{"primary", "secondary"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sortedContainerNames() = %v, want %v", got, want)
	}
}
