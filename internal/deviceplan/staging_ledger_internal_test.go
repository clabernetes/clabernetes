package deviceplan

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStagingLedgerRoundTrip(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	ledger := stagingLedger{
		Files: map[string]string{
			"config/config.json": "sha256:aaaa",
			"topology.yml":       "sha256:bbbb",
		},
		AcknowledgedResetToken: "reset-1",
		Preserved:              []string{"config/config.json"},
	}

	if err := writeStagingLedger(root, "node-a", ledger); err != nil {
		t.Fatal(err)
	}

	loaded, ok := loadStagingLedger(root, "node-a")
	if !ok {
		t.Fatal("written ledger did not load")
	}

	if loaded.AcknowledgedResetToken != "reset-1" {
		t.Fatalf("acknowledged token = %q", loaded.AcknowledgedResetToken)
	}

	if loaded.Files["config/config.json"] != "sha256:aaaa" ||
		loaded.Files["topology.yml"] != "sha256:bbbb" {
		t.Fatalf("ledger files = %#v", loaded.Files)
	}

	if len(loaded.Preserved) != 1 || loaded.Preserved[0] != "config/config.json" {
		t.Fatalf("ledger preserved = %#v", loaded.Preserved)
	}
}

func TestStagingLedgerMissingIsNotReadable(t *testing.T) {
	t.Parallel()

	ledger, ok := loadStagingLedger(t.TempDir(), "node-a")
	if ok {
		t.Fatal("missing ledger reported readable")
	}

	if ledger.Files == nil {
		t.Fatal("missing ledger must still return a usable empty record")
	}
}

func TestStagingLedgerCorruptIsNotReadable(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := stagingLedgerPath(root, "node-a")

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, ok := loadStagingLedger(root, "node-a"); ok {
		t.Fatal("corrupt ledger reported readable")
	}
}

func TestPreservedDeviceArtifactRequiresLedgerEntry(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	if PreservedDeviceArtifact(root, "node-a", "config/config.json") {
		t.Fatal("missing ledger reported a preserved artifact")
	}

	if err := writeStagingLedger(root, "node-a", stagingLedger{
		Files:     map[string]string{},
		Preserved: []string{"config/config.json"},
	}); err != nil {
		t.Fatal(err)
	}

	if !PreservedDeviceArtifact(root, "node-a", "config/config.json") {
		t.Fatal("recorded preserved artifact was not reported")
	}

	if PreservedDeviceArtifact(root, "node-a", "topology.yml") {
		t.Fatal("unrecorded artifact reported preserved")
	}
}
