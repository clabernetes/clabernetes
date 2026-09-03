package deviceplan

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
)

// nodeStagingState tracks one Node's persistence-aware publication decisions across a single
// preparation run.
type nodeStagingState struct {
	persistent bool
	enforce    bool
	ledger     stagingLedger
	preserved  map[string]bool
}

// stagingPolicy decides whether preparation may publish each verified artifact over its
// destination. On a persistent artifact volume a planned file whose current digest differs from
// the digest recorded at its last staging belongs to the device and is preserved; everything
// else republishes so declared spec updates keep propagating. Enforced startup configuration
// and an unacknowledged device-state reset publish unconditionally.
type stagingPolicy struct {
	states map[string]*nodeStagingState
	output io.Writer
}

func (p Preparer) newStagingPolicy(
	input Input,
	plan Plan,
	artifactRoot string,
) (*stagingPolicy, error) {
	policy := &stagingPolicy{states: map[string]*nodeStagingState{}, output: p.Output}
	if policy.output == nil {
		policy.output = io.Discard
	}

	enforced := map[string]bool{}
	for _, node := range plan.Nodes {
		enforced[node.ID] = node.EnforceStartupConfig
	}

	for _, node := range input.Nodes {
		state := &nodeStagingState{
			persistent: p.PersistentNodeIDs[node.ID],
			enforce:    enforced[node.ID],
			preserved:  map[string]bool{},
		}
		state.ledger, _ = loadStagingLedger(artifactRoot, node.ID)

		if token := p.ResetTokens[node.ID]; token != "" &&
			token != state.ledger.AcknowledgedResetToken {
			if err := wipeNodeArtifacts(artifactRoot, node.ID); err != nil {
				return nil, withNodeID(err, node.ID)
			}

			state.ledger = stagingLedger{
				Files:                  map[string]string{},
				AcknowledgedResetToken: token,
			}

			_, _ = fmt.Fprintf(
				policy.output,
				"honored device-state reset for Node %s; plan-owned artifacts re-seed\n",
				node.ID,
			)
		}

		policy.states[node.ID] = state
	}

	return policy, nil
}

// shouldPublish reports whether the verified artifact may be published over its destination.
// A false result with a nil error preserves device-written content and records the skip.
func (s *stagingPolicy) shouldPublish(
	artifactRoot,
	nodeID string,
	kind ArtifactKind,
	artifactPath,
	publishDigest string,
) (bool, error) {
	state := s.states[nodeID]
	if state == nil || !state.persistent || state.enforce {
		return true, nil
	}

	destination := filepath.Join(
		artifactRoot,
		ArtifactNodeDirectory(nodeID),
		filepath.FromSlash(artifactPath),
	)

	info, err := os.Lstat(destination)
	if os.IsNotExist(err) {
		return true, nil
	}

	if err != nil {
		return false, planningError(
			ErrorSideEffect,
			fieldPreparationDestination,
			"cannot inspect artifact destination",
			err,
		)
	}

	if current, matchesKind := destinationContentDigest(destination, info, kind); matchesKind {
		if recorded, has := state.ledger.Files[artifactPath]; has {
			if current == recorded {
				return true, nil
			}
		} else if current == publishDigest {
			// No ledger entry -- a release without the ledger, or a corrupt record. Content
			// identical to what would publish is safe to (re)adopt; anything else is treated
			// as device-written and preserved.
			return true, nil
		}
	}

	state.preserved[artifactPath] = true

	_, _ = fmt.Fprintf(
		s.output,
		"preserved device-modified artifact %s for Node %s\n",
		artifactPath,
		nodeID,
	)

	return false, nil
}

// recordPublished records the digest preparation just published for one artifact path.
func (s *stagingPolicy) recordPublished(nodeID, artifactPath, digest string) {
	state := s.states[nodeID]
	if state == nil {
		return
	}

	if state.ledger.Files == nil {
		state.ledger.Files = map[string]string{}
	}

	state.ledger.Files[artifactPath] = digest
	delete(state.preserved, artifactPath)
}

// flush persists every Node's staging ledger, including the paths preserved in this run.
func (s *stagingPolicy) flush(artifactRoot string) error {
	for nodeID, state := range s.states {
		ledger := state.ledger
		ledger.Preserved = slices.Sorted(maps.Keys(state.preserved))

		if err := writeStagingLedger(artifactRoot, nodeID, ledger); err != nil {
			return withNodeID(err, nodeID)
		}
	}

	return nil
}

// destinationContentDigest returns the digest of the destination's current content when its
// file type matches the planned artifact kind. A type mismatch reports false and is treated as
// device-written content.
func destinationContentDigest(
	destination string,
	info os.FileInfo,
	kind ArtifactKind,
) (string, bool) {
	switch kind {
	case ArtifactRegular:
		if !info.Mode().IsRegular() {
			return "", false
		}

		file, err := os.Open(destination) //nolint:gosec // reads are confined to plan-scoped roots.
		if err != nil {
			return "", false
		}

		defer func() { _ = file.Close() }()

		hash := sha256.New()
		if _, err = io.Copy(hash, file); err != nil {
			return "", false
		}

		return "sha256:" + hex.EncodeToString(hash.Sum(nil)), true
	case ArtifactSymlink:
		if info.Mode()&os.ModeSymlink == 0 {
			return "", false
		}

		target, err := os.Readlink(destination)
		if err != nil {
			return "", false
		}

		return Digest([]byte(target)), true
	case ArtifactDirectory:
		return "", false
	default:
		return "", false
	}
}

// wipeNodeArtifacts removes everything below one Node's artifact root while keeping the root
// itself, which is a volume mount point, in place.
func wipeNodeArtifacts(artifactRoot, nodeID string) error {
	nodeRoot := filepath.Join(filepath.Clean(artifactRoot), ArtifactNodeDirectory(nodeID))

	entries, err := os.ReadDir(nodeRoot)
	if os.IsNotExist(err) {
		return nil
	}

	if err != nil {
		return planningError(
			ErrorSideEffect,
			fieldPreparationDestination,
			"cannot read Node artifact root",
			err,
		)
	}

	for _, entry := range entries {
		if err = removeAllForced(filepath.Join(nodeRoot, entry.Name())); err != nil {
			return planningError(
				ErrorSideEffect,
				fieldPreparationDestination,
				"cannot wipe Node artifact "+entry.Name(),
				err,
			)
		}
	}

	return nil
}

// removeAllForced removes one artifact tree even when the device wrote directories the
// preparation identity cannot read: preparation drops DAC_OVERRIDE and keeps only CHOWN and
// FOWNER, so unreadable device-owned directories are first re-owned and re-opened for the
// removal walk.
func removeAllForced(path string) error {
	err := os.RemoveAll(path)
	if err == nil {
		return nil
	}

	if chmodErr := reopenTreeForRemoval(path); chmodErr != nil {
		return err
	}

	return os.RemoveAll(path)
}

func reopenTreeForRemoval(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}

	if err != nil {
		return err
	}

	if !info.IsDir() {
		return nil
	}

	// CHOWN re-owns the directory to the preparation identity and FOWNER permits the mode
	// change; both are exactly the retained preparation capabilities.
	_ = os.Chown(path, os.Getuid(), os.Getgid())
	_ = os.Chmod(path, 0o700) //nolint:gosec,mnd // owner-only access to walk a doomed tree.

	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if err = reopenTreeForRemoval(filepath.Join(path, entry.Name())); err != nil {
			return err
		}
	}

	return nil
}

// PreservedDeviceArtifact reports whether the last preparation run preserved device-written
// content at one planned artifact path instead of republishing the plan's bytes. Later
// boundaries use it to accept that device content exactly where they would accept staged bytes.
func PreservedDeviceArtifact(artifactRoot, nodeID, artifactPath string) bool {
	ledger, ok := loadStagingLedger(artifactRoot, nodeID)

	return ok && slices.Contains(ledger.Preserved, artifactPath)
}
