//go:build linux

//nolint:testpackage // exercises the unexported neighbor record helpers.
package directruntime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vishvananda/netlink"
)

// TestTransportNeighborsRecordRoundTrip exercises the sidecar-owned neighbor record without
// netlink: the atomic write must leave exactly the record on disk (no stray staging file), the
// recall must round-trip it, and a corrupt record must degrade to an empty recall instead of
// failing every pass from then on.
func TestTransportNeighborsRecordRoundTrip(t *testing.T) {
	state := t.TempDir()

	if recalled := recallTransportNeighbors(state); len(recalled) != 0 {
		t.Fatalf("recall without a record returned %v, want empty", recalled)
	}

	entries := []capturedNeighbor{{
		IP:     "169.254.1.1",
		MAC:    "02:c9:00:00:01:01",
		Family: netlink.FAMILY_V4,
	}}

	if _, err := rememberTransportNeighbors(state, entries); err != nil {
		t.Fatalf("remembering transport neighbors: %v", err)
	}

	remaining, err := os.ReadDir(state)
	if err != nil {
		t.Fatalf("listing the state directory: %v", err)
	}

	if len(remaining) != 1 || remaining[0].Name() != transportNeighborsRecordName {
		t.Fatalf(
			"state directory after the write = %v, want only %q",
			remaining, transportNeighborsRecordName,
		)
	}

	if recalled := recallTransportNeighbors(state); len(recalled) != 1 ||
		recalled[0] != entries[0] {
		t.Fatalf("recalled transport neighbors = %+v, want %+v", recalled, entries)
	}

	if err := os.WriteFile(
		filepath.Join(state, transportNeighborsRecordName),
		[]byte(`[{"ip":"169.254.1.1"`),
		0o600,
	); err != nil {
		t.Fatalf("writing a corrupt record: %v", err)
	}

	if recalled := recallTransportNeighbors(state); len(recalled) != 0 {
		t.Fatalf("recall on a corrupt record returned %v, want empty", recalled)
	}
}
