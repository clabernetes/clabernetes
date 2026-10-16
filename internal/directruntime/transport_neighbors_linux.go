//go:build linux

package directruntime

import (
	"fmt"

	"github.com/vishvananda/netlink"
)

// captureTransportNeighbors snapshots the permanent neighbor entries on the CNI interface before
// it is renamed. Taking a link down flushes them (the Amazon VPC CNI installs its virtual gateway,
// 169.254.1.1, as a permanent entry) and bringing the link back up does not restore them.
func captureTransportNeighbors(linkIndex int) ([]netlink.Neigh, error) {
	entries, err := netlink.NeighList(linkIndex, netlink.FAMILY_ALL)
	if err != nil {
		return nil, fmt.Errorf("capturing transport neighbors: %w", err)
	}

	permanent := make([]netlink.Neigh, 0, len(entries))

	for _, entry := range entries {
		if entry.State&netlink.NUD_PERMANENT == 0 || entry.IP == nil ||
			len(entry.HardwareAddr) == 0 {
			continue
		}

		permanent = append(permanent, entry)
	}

	return permanent, nil
}

// restoreTransportNeighbors re-installs captured permanent neighbors on the renamed interface.
func restoreTransportNeighbors(linkIndex int, entries []netlink.Neigh) error {
	for _, entry := range entries {
		restored := entry
		restored.LinkIndex = linkIndex

		if err := netlink.NeighSet(&restored); err != nil {
			return fmt.Errorf("restoring transport neighbor %q: %w", entry.IP, err)
		}
	}

	return nil
}
