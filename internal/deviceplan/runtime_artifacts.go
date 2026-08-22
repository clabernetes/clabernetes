//nolint:mnd // protocol and platform literals are clearest inline.
package deviceplan

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// runtimeArtifactDigestsName is the per-Node artifact-root record of generator files whose
// content was re-rendered with the Pod's runtime management identity during preparation. It
// lives beside the staged artifacts (outside every package-declared mount subpath) and shares
// their trust domain: later boundaries accept these digests exactly where they would accept the
// staged bytes themselves.
const runtimeArtifactDigestsName = "runtime-artifacts.json"

type runtimeArtifactRecord struct {
	// Files maps a generator FilePlan's ArtifactPath to the digest of its runtime-rendered
	// content.
	Files map[string]string `json:"files"`
}

// writeRuntimeArtifactDigests persists the runtime-rendered digests for one Node. An empty map
// removes any stale record.
func writeRuntimeArtifactDigests(
	artifactRoot,
	nodeID string,
	digests map[string]string,
) error {
	path := filepath.Join(
		filepath.Clean(artifactRoot),
		ArtifactNodeDirectory(nodeID),
		runtimeArtifactDigestsName,
	)
	if len(digests) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return planningError(
				ErrorSideEffect,
				"preparation.runtimeArtifacts",
				"cannot clear runtime artifact record",
				err,
			)
		}

		return nil
	}

	payload, err := json.Marshal(runtimeArtifactRecord{Files: digests})
	if err != nil {
		return planningError(
			ErrorSerialization,
			"preparation.runtimeArtifacts",
			"cannot serialize runtime artifact record",
			err,
		)
	}

	if err = os.WriteFile(path, payload, 0o644); err != nil { //nolint:gosec // Digest record.
		return planningError(
			ErrorSideEffect,
			"preparation.runtimeArtifacts",
			"cannot write runtime artifact record",
			err,
		)
	}

	return nil
}

// LoadRuntimeArtifactDigests returns the preparation-recorded runtime digests for one Node, or
// an empty map when preparation rendered nothing management-dependent.
func LoadRuntimeArtifactDigests(artifactRoot, nodeID string) map[string]string {
	raw, err := os.ReadFile(
		filepath.Join(
			filepath.Clean(artifactRoot),
			ArtifactNodeDirectory(nodeID),
			runtimeArtifactDigestsName,
		),
	)
	if err != nil {
		return map[string]string{}
	}

	record := runtimeArtifactRecord{}
	if json.Unmarshal(raw, &record) != nil || record.Files == nil {
		return map[string]string{}
	}

	return record.Files
}
