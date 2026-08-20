//nolint:testpackage // The digest helper is deliberately internal.
package compatibility

import (
	"os"
	"path/filepath"
	"testing"
)

func TestComputeInvalidationIsDeterministic(t *testing.T) {
	t.Parallel()

	first, err := ComputeInvalidation("../..")
	if err != nil {
		t.Fatalf("ComputeInvalidation() error = %v", err)
	}

	second, err := ComputeInvalidation("../..")
	if err != nil {
		t.Fatalf("ComputeInvalidation() error = %v", err)
	}

	if first != second {
		t.Fatalf("invalidation digests are not deterministic: %#v != %#v", first, second)
	}

	for name, value := range map[string]string{
		"planner":      first.Planner,
		"renderer":     first.Renderer,
		"preparation":  first.Preparation,
		"connectivity": first.Connectivity,
	} {
		if len(value) != len("sha256:")+16 {
			t.Fatalf("invalidation.%s digest %q has unexpected shape", name, value)
		}
	}
}

func TestDigestSourceTreesRetiresEvidenceOnChange(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tree := filepath.Join(root, "component")

	err := os.MkdirAll(tree, 0o755)
	if err != nil {
		t.Fatalf("creating component tree: %v", err)
	}

	write := func(name, content string) {
		t.Helper()

		err = os.WriteFile(filepath.Join(tree, name), []byte(content), 0o600)
		if err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}

	write("component.go", "package component\n")

	initial, err := digestSourceTrees(root, []string{"component"})
	if err != nil {
		t.Fatalf("digestSourceTrees() error = %v", err)
	}

	// Test-only edits must not retire recorded evidence.
	write("component_test.go", "package component\n")

	afterTestEdit, err := digestSourceTrees(root, []string{"component"})
	if err != nil {
		t.Fatalf("digestSourceTrees() error = %v", err)
	}

	if afterTestEdit != initial {
		t.Fatal("test-only change retired the recorded evidence digest")
	}

	write("component.go", "package component\n\nconst changed = true\n")

	afterChange, err := digestSourceTrees(root, []string{"component"})
	if err != nil {
		t.Fatalf("digestSourceTrees() error = %v", err)
	}

	if afterChange == initial {
		t.Fatal("production change did not retire the recorded evidence digest")
	}
}
