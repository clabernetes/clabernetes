package deviceplan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	clabtypes "github.com/srl-labs/containerlab/types"
)

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
