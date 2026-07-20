package topology

import (
	"sort"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
)

// AllocateTunnelIDs processes the given tunnels and allocates vnids. This function updates the
// given status object by iterating over the freshly processed tunnels (as processed during a
// reconciliation) and assigning any tunnels in the status without a vnid the next valid vnid.
func AllocateTunnelIDs(
	previousTunnels map[string][]*clabernetesapisv1alpha1.PointToPointTunnel,
	processedTunnels map[string][]*clabernetesapisv1alpha1.PointToPointTunnel,
) {
	// we want to allocate ids deterministically, so lets iterate over the maps in *order* by
	// getting a sorted list of keys and then iterating over those
	processedTunnelsSortedKeys := make([]string, len(processedTunnels))

	var processedIdx int

	for k := range processedTunnels {
		processedTunnelsSortedKeys[processedIdx] = k

		processedIdx++
	}

	sort.Strings(processedTunnelsSortedKeys)

	// iterate over stored (in status) tunnels and allocate previously assigned ids to any relevant
	// tunnels -- while doing so, make a map of all allocated tunnel ids so we can make sure to not
	// re-use things.
	allocatedTunnelIDs := make(map[int]bool)

	for nodeName, nodeTunnels := range processedTunnels {
		existingNodeTunnels, ok := previousTunnels[nodeName]
		if !ok {
			continue
		}

		for _, newTunnel := range nodeTunnels {
			for _, existingTunnel := range existingNodeTunnels {
				if newTunnel.LocalInterface == existingTunnel.LocalInterface &&
					newTunnel.RemoteInterface == existingTunnel.RemoteInterface &&
					newTunnel.RemoteNode == existingTunnel.RemoteNode {
					newTunnel.TunnelID = existingTunnel.TunnelID

					allocatedTunnelIDs[newTunnel.TunnelID] = true

					break
				}
			}
		}
	}

	for _, nodeName := range processedTunnelsSortedKeys {
		nodeTunnels := processedTunnels[nodeName]

		for _, tunnel := range nodeTunnels {
			if tunnel.TunnelID != 0 {
				continue
			}

			// iterate over the tunnels to see if this tunnels remote pair already has a vnid set,
			// if *yes* we need to re-use that vnid obviously!
			idToAssign := findAllocatedIDIfExists(
				nodeName,
				tunnel.RemoteNode,
				tunnel.LocalInterface,
				processedTunnelsSortedKeys,
				processedTunnels,
			)

			if idToAssign != 0 {
				tunnel.TunnelID = idToAssign
				allocatedTunnelIDs[idToAssign] = true

				continue
			}

			// dear lord this is probably ridiculous but should be fine for now... :)
			for i := 1; i < 16_000_000; i++ {
				_, ok := allocatedTunnelIDs[i]
				if ok {
					// already allocated
					continue
				}

				tunnel.TunnelID = i
				allocatedTunnelIDs[i] = true

				break
			}
		}
	}
}

func findAllocatedIDIfExists(
	nodeName, tunnelRemoteNodeName, tunnelLocalLinkName string,
	sortedKeys []string,
	processedTunnels map[string][]*clabernetesapisv1alpha1.PointToPointTunnel,
) int {
	for _, remoteNodeName := range sortedKeys {
		if nodeName == remoteNodeName {
			// this is us, next...
			continue
		}

		if tunnelRemoteNodeName != remoteNodeName {
			// this remote node name doesnt match our current tunnels remote node name
			// so not our remote end, next...
			continue
		}

		for _, remoteTunnel := range processedTunnels[remoteNodeName] {
			if remoteTunnel.RemoteNode != nodeName {
				// tunnel not between this node pair
				continue
			}

			if tunnelLocalLinkName != remoteTunnel.RemoteInterface {
				// this specific tunnel does not match our local tunnel
				continue
			}

			if remoteTunnel.TunnelID == 0 {
				// we found our remote tunnel but vnid is not set yet, so we'll just keep
				// doing our thing
				return 0
			}

			return remoteTunnel.TunnelID
		}
	}

	return 0
}
