package deviceplan_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
)

const (
	persistenceTestArtifact = "generated/imported.conf"
	persistenceLedgerName   = "staging-ledger.json"
)

func preparePersistenceFixture(
	t *testing.T,
) (clabernetesinternaldeviceplan.Input, clabernetesinternaldeviceplan.Plan, clabernetesinternaldeviceplan.Preparer, string) {
	t.Helper()

	input := singleNodeInput(syntheticKind, "example/future:1")
	adapter := clabernetesinternaldeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "preparer-v1",
	}

	plan, err := adapter.Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	preparer := clabernetesinternaldeviceplan.Preparer{
		Adapter:           adapter,
		PersistentNodeIDs: map[string]bool{"node-a": true},
	}

	return input, *plan, preparer, t.TempDir()
}

func persistenceArtifactPath(root string) string {
	return filepath.Join(
		root,
		clabernetesinternaldeviceplan.ArtifactNodeDirectory("node-a"),
		filepath.FromSlash(persistenceTestArtifact),
	)
}

func persistenceLedgerPath(root string) string {
	return filepath.Join(
		root,
		clabernetesinternaldeviceplan.ArtifactNodeDirectory("node-a"),
		persistenceLedgerName,
	)
}

func readPersistenceArtifact(t *testing.T, root string) string {
	t.Helper()

	content, err := os.ReadFile(persistenceArtifactPath(root))
	if err != nil {
		t.Fatal(err)
	}

	return string(content)
}

func TestPreparerPreservesDeviceModifiedArtifactOnPersistentVolume(t *testing.T) {
	t.Parallel()

	input, plan, preparer, root := preparePersistenceFixture(t)

	if err := preparer.Prepare(context.Background(), input, plan, root); err != nil {
		t.Fatal(err)
	}

	// the device rewrites a planned file between Pod starts
	if err := os.WriteFile(
		persistenceArtifactPath(root),
		[]byte("device-written\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	output := &bytes.Buffer{}
	preparer.Output = output

	if err := preparer.Prepare(context.Background(), input, plan, root); err != nil {
		t.Fatal(err)
	}

	if got := readPersistenceArtifact(t, root); got != "device-written\n" {
		t.Fatalf("device-modified artifact was overwritten, content = %q", got)
	}

	if !strings.Contains(output.String(), "preserved device-modified artifact") {
		t.Fatalf("preparation did not report the preserved artifact: %q", output.String())
	}

	if !clabernetesinternaldeviceplan.PreservedDeviceArtifact(
		root,
		"node-a",
		persistenceTestArtifact,
	) {
		t.Fatal("preserved artifact is not recorded in the staging ledger")
	}
}

func TestPreparerRepublishesUntouchedArtifactOnPersistentVolume(t *testing.T) {
	t.Parallel()

	input, plan, preparer, root := preparePersistenceFixture(t)

	if err := preparer.Prepare(context.Background(), input, plan, root); err != nil {
		t.Fatal(err)
	}

	// Simulate a previous release having staged different content that the device never
	// touched: destination and ledger agree, but the plan renders something else.
	stale := []byte("stale-plan-content\n")
	if err := os.WriteFile(persistenceArtifactPath(root), stale, 0o600); err != nil {
		t.Fatal(err)
	}

	ledger := map[string]any{
		"files": map[string]string{
			persistenceTestArtifact: clabernetesinternaldeviceplan.Digest(stale),
		},
	}

	raw, err := json.Marshal(ledger)
	if err != nil {
		t.Fatal(err)
	}

	if err = os.WriteFile(persistenceLedgerPath(root), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	if err = preparer.Prepare(context.Background(), input, plan, root); err != nil {
		t.Fatal(err)
	}

	if got := readPersistenceArtifact(t, root); got != "generated\n" {
		t.Fatalf("untouched artifact did not follow the plan, content = %q", got)
	}
}

func TestPreparerMissingLedgerPreservesDifferingArtifact(t *testing.T) {
	t.Parallel()

	input, plan, preparer, root := preparePersistenceFixture(t)

	if err := preparer.Prepare(context.Background(), input, plan, root); err != nil {
		t.Fatal(err)
	}

	// An upgrade from a release without the ledger: artifacts exist, the ledger does not.
	if err := os.Remove(persistenceLedgerPath(root)); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(
		persistenceArtifactPath(root),
		[]byte("saved-before-upgrade\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	if err := preparer.Prepare(context.Background(), input, plan, root); err != nil {
		t.Fatal(err)
	}

	if got := readPersistenceArtifact(t, root); got != "saved-before-upgrade\n" {
		t.Fatalf("missing-ledger preparation clobbered device content, content = %q", got)
	}

	// A fresh ledger must exist again after the run.
	if _, err := os.Stat(persistenceLedgerPath(root)); err != nil {
		t.Fatalf("preparation did not re-establish the staging ledger: %v", err)
	}
}

func TestPreparerOverwritesModifiedArtifactWithoutPersistence(t *testing.T) {
	t.Parallel()

	input, plan, preparer, root := preparePersistenceFixture(t)
	preparer.PersistentNodeIDs = nil

	if err := preparer.Prepare(context.Background(), input, plan, root); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(
		persistenceArtifactPath(root),
		[]byte("device-written\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	if err := preparer.Prepare(context.Background(), input, plan, root); err != nil {
		t.Fatal(err)
	}

	if got := readPersistenceArtifact(t, root); got != "generated\n" {
		t.Fatalf("ephemeral preparation preserved device content, content = %q", got)
	}
}

func TestPreparerEnforcedStartupConfigRestagesDeviceModifiedArtifact(t *testing.T) {
	t.Parallel()

	input, plan, preparer, root := preparePersistenceFixture(t)
	for index := range plan.Nodes {
		plan.Nodes[index].EnforceStartupConfig = true
	}

	if err := preparer.Prepare(context.Background(), input, plan, root); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(
		persistenceArtifactPath(root),
		[]byte("device-written\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	if err := preparer.Prepare(context.Background(), input, plan, root); err != nil {
		t.Fatal(err)
	}

	if got := readPersistenceArtifact(t, root); got != "generated\n" {
		t.Fatalf("enforced preparation preserved device content, content = %q", got)
	}
}

func TestPreparerDeviceStateResetWipesAndReseeds(t *testing.T) {
	t.Parallel()

	input, plan, preparer, root := preparePersistenceFixture(t)

	if err := preparer.Prepare(context.Background(), input, plan, root); err != nil {
		t.Fatal(err)
	}

	deviceFile := filepath.Join(
		root,
		clabernetesinternaldeviceplan.ArtifactNodeDirectory("node-a"),
		"generated",
		"device-extra.cfg",
	)

	if err := os.WriteFile(
		persistenceArtifactPath(root),
		[]byte("device-written\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(deviceFile, []byte("extra\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	preparer.ResetTokens = map[string]string{"node-a": "reset-1"}

	if err := preparer.Prepare(context.Background(), input, plan, root); err != nil {
		t.Fatal(err)
	}

	if got := readPersistenceArtifact(t, root); got != "generated\n" {
		t.Fatalf("reset did not re-seed the planned artifact, content = %q", got)
	}

	if _, err := os.Stat(deviceFile); !os.IsNotExist(err) {
		t.Fatalf("reset left a device-written file in place: %v", err)
	}

	// The same token must never wipe twice: later device changes survive further restarts.
	if err := os.WriteFile(
		persistenceArtifactPath(root),
		[]byte("post-reset-save\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	if err := preparer.Prepare(context.Background(), input, plan, root); err != nil {
		t.Fatal(err)
	}

	if got := readPersistenceArtifact(t, root); got != "post-reset-save\n" {
		t.Fatalf("acknowledged reset token wiped again, content = %q", got)
	}

	// A new token wipes again.
	preparer.ResetTokens = map[string]string{"node-a": "reset-2"}

	if err := preparer.Prepare(context.Background(), input, plan, root); err != nil {
		t.Fatal(err)
	}

	if got := readPersistenceArtifact(t, root); got != "generated\n" {
		t.Fatalf("new reset token did not re-seed, content = %q", got)
	}
}
