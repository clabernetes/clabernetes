package deviceplan

import (
	"os"
	"path/filepath"
	"testing"

	clabtypes "github.com/srl-labs/containerlab/types"
)

func TestPayloadPathRewritesPreserveConfigVars(t *testing.T) {
	t.Parallel()

	const (
		nodeID      = "node-a"
		payloadID   = "startup-input"
		destination = "/inputs/startup.cfg"
	)

	payload := PayloadInput{
		ID: payloadID, NodeID: nodeID, Kind: PayloadConfigMap,
		Reference: "lab/device-config:startup.cfg", Digest: Digest([]byte("startup\n")),
		Destination: destination, Mode: 0o444,
	}
	definition := func() *clabtypes.NodeDefinition {
		return &clabtypes.NodeDefinition{
			StartupConfig: destination,
			Env:           map[string]string{"PATH_VALUE": destination},
			Config: &clabtypes.ConfigDispatcher{Vars: map[string]any{
				"literal": destination,
				"nested":  map[string]any{"path": destination},
			}},
		}
	}
	assertRewritten := func(t *testing.T, definition *clabtypes.NodeDefinition, expectedPath string) {
		t.Helper()

		if definition.StartupConfig != expectedPath ||
			definition.Env["PATH_VALUE"] != expectedPath {
			t.Fatalf(
				"declared paths were not rewritten: startup=%q env=%q, want %q",
				definition.StartupConfig,
				definition.Env["PATH_VALUE"],
				expectedPath,
			)
		}
		if definition.Config.Vars["literal"] != destination {
			t.Fatalf(
				"literal config var = %q, want %q",
				definition.Config.Vars["literal"],
				destination,
			)
		}
		nested, ok := definition.Config.Vars["nested"].(map[string]any)
		if !ok {
			t.Fatalf("nested config var has type %T", definition.Config.Vars["nested"])
		}
		if nested["path"] != destination {
			t.Fatalf("nested config var = %q, want %q", nested["path"], destination)
		}
	}

	t.Run("explicit planning payload", func(t *testing.T) {
		t.Parallel()

		payloadRoot := t.TempDir()
		projectionDir := filepath.Join(payloadRoot, ArtifactNodeDirectory(payloadID))
		if err := os.MkdirAll(projectionDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(projectionDir, "source"), []byte("startup\n"), 0o400,
		); err != nil {
			t.Fatal(err)
		}

		definition := definition()
		err := rewriteExplicitPayloadPaths(
			nodeID,
			definition,
			[]PayloadInput{payload},
			payloadRoot,
		)
		if err != nil {
			t.Fatal(err)
		}

		assertRewritten(t, definition, filepath.Join(projectionDir, "source"))
	})

	t.Run("staged lifecycle payload", func(t *testing.T) {
		t.Parallel()

		artifactRoot := t.TempDir()
		definition := definition()
		err := rewriteStagedPayloadPaths(
			nodeID,
			definition,
			Input{Payloads: []PayloadInput{payload}},
			Plan{Files: []FilePlan{{
				NodeID: nodeID, SourceKind: FileSourcePayload, SourceReference: payloadID,
				ArtifactPath: "payloads/startup.cfg",
			}}},
			artifactRoot,
		)
		if err != nil {
			t.Fatal(err)
		}

		assertRewritten(t, definition, filepath.Join(
			artifactRoot,
			ArtifactNodeDirectory(nodeID),
			"payloads", "startup.cfg",
		))
	})
}
