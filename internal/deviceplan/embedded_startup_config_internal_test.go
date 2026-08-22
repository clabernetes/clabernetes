package deviceplan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	clabtypes "github.com/srl-labs/containerlab/types"
)

func TestPreserveStartupConfigPartialMarkerAcrossPayloadRewrite(t *testing.T) {
	t.Parallel()

	content := "/configure port 1/1/c1 admin-state enable\n"
	rewritten := filepath.Join(t.TempDir(), "source")

	if err := os.WriteFile(rewritten, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	config := &clabtypes.NodeConfig{
		LabDir:        filepath.Join(t.TempDir(), "workspace"),
		StartupConfig: rewritten,
	}

	err := preserveStartupConfigPartialMarker(
		"node-a",
		"/clabernetes/startup-config.partial.cfg",
		config,
	)
	if err != nil {
		t.Fatalf("preserveStartupConfigPartialMarker() error = %v", err)
	}

	if !strings.HasPrefix(config.StartupConfig, config.LabDir) ||
		!strings.Contains(strings.ToUpper(config.StartupConfig), ".PARTIAL") {
		t.Fatalf(
			"rewritten partial config lost its marker: %q",
			config.StartupConfig,
		)
	}

	written, err := os.ReadFile(config.StartupConfig)
	if err != nil {
		t.Fatal(err)
	}

	if string(written) != content {
		t.Fatalf("staged partial content = %q, want original bytes", string(written))
	}
}

func TestPreserveStartupConfigPartialMarkerLeavesNonPartialAlone(t *testing.T) {
	t.Parallel()

	for name, declared := range map[string]string{
		"full-config-path":    "/clabernetes/startup-config",
		"embedded-content":    "a\nb\n",
		"already-partial-out": "/x/kept.partial.cfg",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rewritten := "/proj/abc/source"
			if name == "already-partial-out" {
				rewritten = "/proj/abc/kept.partial.cfg"
			}

			config := &clabtypes.NodeConfig{
				LabDir:        t.TempDir(),
				StartupConfig: rewritten,
			}

			if err := preserveStartupConfigPartialMarker("node-a", declared, config); err != nil {
				t.Fatalf("preserveStartupConfigPartialMarker() error = %v", err)
			}

			if config.StartupConfig != rewritten {
				t.Fatalf(
					"startup config %q was rewritten to %q, want untouched",
					rewritten,
					config.StartupConfig,
				)
			}
		})
	}
}

func TestMaterializeEmbeddedStartupConfigWritesWorkspaceFile(t *testing.T) {
	t.Parallel()

	content := "set / interface ethernet-1/1\nset / network-instance default\n"
	config := &clabtypes.NodeConfig{
		LabDir:        filepath.Join(t.TempDir(), "workspace"),
		StartupConfig: content,
	}

	if err := materializeEmbeddedStartupConfig("node-a", config); err != nil {
		t.Fatalf("materializeEmbeddedStartupConfig() error = %v", err)
	}

	if !strings.HasPrefix(config.StartupConfig, config.LabDir) ||
		!strings.HasSuffix(config.StartupConfig, embeddedStartupConfigFilename) {
		t.Fatalf("startup config was not repointed at the workspace: %q", config.StartupConfig)
	}

	written, err := os.ReadFile(config.StartupConfig)
	if err != nil {
		t.Fatal(err)
	}

	if string(written) != content {
		t.Fatalf("materialized content = %q, want the embedded blob", written)
	}
}

func TestMaterializeEmbeddedStartupConfigLeavesPathReferencesUntouched(t *testing.T) {
	t.Parallel()

	config := &clabtypes.NodeConfig{
		LabDir:        t.TempDir(),
		StartupConfig: "/mounted/startup-config.cfg",
	}

	if err := materializeEmbeddedStartupConfig("node-a", config); err != nil {
		t.Fatalf("materializeEmbeddedStartupConfig() error = %v", err)
	}

	if config.StartupConfig != "/mounted/startup-config.cfg" {
		t.Fatalf("path reference was rewritten: %q", config.StartupConfig)
	}
}
