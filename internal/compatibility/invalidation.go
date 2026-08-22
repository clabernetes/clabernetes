package compatibility

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// invalidationComponents maps each invalidation boundary to the repository trees whose change
// invalidates recorded conformance evidence for that boundary. The digests deliberately cover
// only production Go sources: test edits must not silently retire evidence, and fixture-driven
// suites re-record their own goldens.
var invalidationComponents = map[string][]string{ //nolint:gochecknoglobals // Static inventory.
	"planner":      {"internal/deviceplan"},
	"renderer":     {"internal/directpod"},
	"preparation":  {"internal/directruntime"},
	"connectivity": {"internal/directruntime", "controllers/link"},
}

// ComputeInvalidation digests the current implementation of every invalidation boundary so a
// change to the planner, renderer, preparation, or connectivity code observably retires the
// recorded conformance evidence until the baseline is consciously refreshed.
func ComputeInvalidation(root string) (Invalidation, error) {
	digests := map[string]string{}

	for component, trees := range invalidationComponents {
		digest, err := digestSourceTrees(root, trees)
		if err != nil {
			return Invalidation{}, fmt.Errorf("digesting %s sources: %w", component, err)
		}

		digests[component] = digest
	}

	return Invalidation{
		Planner:      digests["planner"],
		Renderer:     digests["renderer"],
		Preparation:  digests["preparation"],
		Connectivity: digests["connectivity"],
	}, nil
}

func digestSourceTrees(root string, trees []string) (string, error) {
	entries := []string{}

	for _, tree := range trees {
		base := filepath.Join(root, tree)

		err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			if entry.IsDir() || !strings.HasSuffix(path, ".go") ||
				strings.HasSuffix(path, "_test.go") {
				return nil
			}

			content, readErr := os.ReadFile(path) //nolint:gosec // root scopes this read.
			if readErr != nil {
				return readErr
			}

			relative, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}

			fileDigest := sha256.Sum256(content)
			entries = append(
				entries,
				fmt.Sprintf("%s\x00%x", filepath.ToSlash(relative), fileDigest),
			)

			return nil
		})
		if err != nil {
			return "", err
		}
	}

	sort.Strings(entries)
	digest := sha256.Sum256([]byte(strings.Join(entries, "\n")))

	return fmt.Sprintf("sha256:%x", digest[:8]), nil
}
