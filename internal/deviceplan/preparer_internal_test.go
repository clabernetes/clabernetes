package deviceplan

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPlanArtifactDirectoriesFinalizeDescendantsBeforeParents(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("requires an unprivileged process to enforce directory write permissions")
	}
	root := t.TempDir()
	parent := filepath.Join(root, ArtifactNodeDirectory("node-a"), "parent")
	t.Cleanup(func() {
		_ = os.Chmod(parent, 0o700) //nolint:gosec // Restore directory traversal for cleanup.
	})
	// Canonical ID order puts generated/10 before generated/9, regardless of path ancestry.
	plan := Plan{Files: []FilePlan{
		{
			ID:           "generated/10",
			NodeID:       "node-a",
			SourceKind:   FileSourcePlanArtifact,
			ArtifactKind: ArtifactDirectory,
			ArtifactPath: "parent/child",
			Mode:         0o700,
		},
		{
			ID:           "generated/9",
			NodeID:       "node-a",
			SourceKind:   FileSourcePlanArtifact,
			ArtifactKind: ArtifactDirectory,
			ArtifactPath: "parent",
			Mode:         0o500,
		},
	}}
	if err := (Preparer{PayloadRoot: t.TempDir()}).stagePlanArtifacts(plan, root, nil); err != nil {
		t.Fatalf("directory order followed file IDs instead of ancestry: %v", err)
	}
}
