//nolint:mnd // filesystem mode literals are clearest inline.
package deviceplan

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// stagingLedgerName is the per-Node artifact-root record of every artifact preparation
// published, keyed by artifact path and carrying the published content digest. Like
// runtime-artifacts.json it lives beside the staged artifacts, outside every package-declared
// mount subpath, and shares their trust domain. On a persistent artifact volume the ledger is
// how preparation distinguishes a file the device wrote (current digest differs from the
// recorded staging digest) from one it may safely republish.
const stagingLedgerName = "staging-ledger.json"

const fieldPreparationStagingLedger = "preparation.stagingLedger"

// stagingLedger is the deserialized per-Node staging record.
type stagingLedger struct {
	// Files maps a published artifact's path to the digest of the content preparation last
	// staged there. Symbolic links record the digest of their target string.
	Files map[string]string `json:"files"`
	// AcknowledgedResetToken is the device-state reset token most recently honored for this
	// Node. A projected token equal to this value never wipes again.
	AcknowledgedResetToken string `json:"acknowledgedResetToken,omitempty"`
	// Preserved lists the planned artifact paths whose device-written content the most recent
	// preparation run left in place instead of republishing the plan's bytes.
	Preserved []string `json:"preserved,omitempty"`
}

// loadStagingLedger returns the recorded staging ledger for one Node. The second return is
// false when no readable ledger exists -- a fresh volume, a release without the ledger, or a
// corrupt record -- in which case preparation must fall back to preserve-first semantics for
// planned paths whose content differs from the plan.
func loadStagingLedger(artifactRoot, nodeID string) (stagingLedger, bool) {
	raw, err := os.ReadFile(stagingLedgerPath(artifactRoot, nodeID))
	if err != nil {
		return stagingLedger{Files: map[string]string{}}, false
	}

	ledger := stagingLedger{}
	if json.Unmarshal(raw, &ledger) != nil || ledger.Files == nil {
		return stagingLedger{Files: map[string]string{}}, false
	}

	return ledger, true
}

// writeStagingLedger persists the staging ledger for one Node beside its staged artifacts.
func writeStagingLedger(artifactRoot, nodeID string, ledger stagingLedger) error {
	if ledger.Files == nil {
		ledger.Files = map[string]string{}
	}

	payload, err := json.Marshal(ledger)
	if err != nil {
		return planningError(
			ErrorSerialization,
			fieldPreparationStagingLedger,
			"cannot serialize staging ledger",
			err,
		)
	}

	path := stagingLedgerPath(artifactRoot, nodeID)

	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return planningError(
			ErrorSideEffect,
			fieldPreparationStagingLedger,
			"cannot create staging ledger directory",
			err,
		)
	}

	if err = os.WriteFile(path, payload, 0o644); err != nil { //nolint:gosec // digest record.
		return planningError(
			ErrorSideEffect,
			fieldPreparationStagingLedger,
			"cannot write staging ledger",
			err,
		)
	}

	return nil
}

func stagingLedgerPath(artifactRoot, nodeID string) string {
	return filepath.Join(
		filepath.Clean(artifactRoot),
		ArtifactNodeDirectory(nodeID),
		stagingLedgerName,
	)
}
