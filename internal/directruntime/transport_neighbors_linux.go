//go:build linux

package directruntime

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/vishvananda/netlink"
)

// transportNeighborsRecordName is the state file carrying the captured permanent neighbor
// entries of the CNI-installed interface.
const transportNeighborsRecordName = "transport-neighbors"

// capturedNeighbor is one CNI-installed permanent neighbor entry snapshotted before the
// transport interface is set down for the rename. The kernel's neighbor type does not survive
// a JSON round-trip through sidecar-owned state, so the record carries the fields the restore
// needs: the address, the link-layer identity, and the family, type, and flags the kernel
// reported for the entry.
type capturedNeighbor struct {
	IP     string `json:"ip"`
	MAC    string `json:"mac"`
	Family int    `json:"family"`
	Type   int    `json:"type,omitempty"`
	Flags  int    `json:"flags,omitempty"`
}

// captureTransportNeighbors snapshots the permanent neighbor entries on the CNI interface before
// it is renamed. Taking a link down flushes them (the Amazon VPC CNI installs its virtual gateway,
// 169.254.1.1, as a permanent entry) and bringing the link back up does not restore them. Dynamic
// entries are re-learnt normally: only permanent entries carrying an address and a link-layer
// identity are kept.
func captureTransportNeighbors(linkIndex int) ([]capturedNeighbor, error) {
	entries, err := netlink.NeighList(linkIndex, netlink.FAMILY_ALL)
	if err != nil {
		return nil, fmt.Errorf("capturing transport neighbors: %w", err)
	}

	permanent := make([]capturedNeighbor, 0, len(entries))

	for _, entry := range entries {
		if entry.State&netlink.NUD_PERMANENT == 0 || entry.IP == nil ||
			len(entry.HardwareAddr) == 0 {
			continue
		}

		permanent = append(permanent, capturedNeighbor{
			IP:     entry.IP.String(),
			MAC:    entry.HardwareAddr.String(),
			Family: entry.Family,
			Type:   entry.Type,
			Flags:  entry.Flags,
		})
	}

	return permanent, nil
}

// rememberTransportNeighbors persists a freshly captured neighbor set and recalls the recorded
// one on later passes (a device may flush the shared neighbor table after boot). The record is
// written before the flush, not after the pass converges like the routes: the flush is not
// recapturable (the next look at the interface finds the steady-state shape), so a sidecar that
// dies between the flush and the restore must find the entries on disk to re-assert them.
func rememberTransportNeighbors(
	stateDirectory string,
	captured []capturedNeighbor,
) []capturedNeighbor {
	if stateDirectory == "" {
		return captured
	}

	record := filepath.Join(filepath.Clean(stateDirectory), transportNeighborsRecordName)

	if len(captured) != 0 {
		if raw, err := json.Marshal(captured); err == nil {
			//nolint:gosec,mnd // non-sensitive sidecar-owned state, standard mode.
			_ = os.WriteFile(record, raw, 0o644)
		}

		return captured
	}

	raw, err := os.ReadFile(record)
	if err != nil {
		return nil
	}

	recalled := []capturedNeighbor{}
	if err := json.Unmarshal(raw, &recalled); err != nil {
		return nil
	}

	return recalled
}

// restoreTransportNeighbors re-installs captured permanent neighbors on the given interface. One
// entry the kernel refuses does not stop the rest: the CNI reaches its gateway through the
// recorded set, and a single rejected entry must not leave the Pod without the others. The
// failures are returned together for the pass to report.
func restoreTransportNeighbors(linkIndex int, entries []capturedNeighbor) error {
	var failures []error

	for _, entry := range entries {
		address := net.ParseIP(entry.IP)

		mac, macErr := net.ParseMAC(entry.MAC)
		if address == nil || macErr != nil {
			// A record the sidecar wrote parses by construction; a damaged one is skipped so a
			// single corrupt entry cannot fail every pass from here on.
			fmt.Fprintf(
				os.Stderr,
				"connectivity: skipping unparsable recorded transport neighbor %q\n",
				entry.IP,
			)

			continue
		}

		restored := netlink.Neigh{
			LinkIndex:    linkIndex,
			Family:       entry.Family,
			Type:         entry.Type,
			Flags:        entry.Flags,
			State:        netlink.NUD_PERMANENT,
			IP:           address,
			HardwareAddr: mac,
		}

		if err := netlink.NeighSet(&restored); err != nil {
			if entry.Family == netlink.FAMILY_V6 {
				// IPv6 neighbor state is best effort, like the IPv6 management identity: a
				// device that disables IPv6 in the namespace leaves the entry un-restorable,
				// and failing the IPv4 transport closed over it would loop the sidecar on an
				// error no pass can clear.
				fmt.Fprintf(
					os.Stderr,
					"connectivity: best-effort transport neighbor %q: %v\n",
					entry.IP,
					err,
				)

				continue
			}

			failures = append(
				failures,
				fmt.Errorf("restoring transport neighbor %q: %w", entry.IP, err),
			)
		}
	}

	return errors.Join(failures...)
}
