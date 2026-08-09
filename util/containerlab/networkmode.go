package containerlab

import "strings"

// NetworkModeContainerPrefix is the prefix of the `network-mode` node setting expressing that a
// node shares the network namespace of another (containerlab) node.
const NetworkModeContainerPrefix = "container:"

// ParseNetworkModeContainer parses a network-mode value and returns the referenced (primary)
// node name if it is a container network-mode (i.e. "container:node-a" returns "node-a"), or an
// empty string otherwise.
func ParseNetworkModeContainer(networkMode string) string {
	if !strings.HasPrefix(networkMode, NetworkModeContainerPrefix) {
		return ""
	}

	return strings.TrimPrefix(networkMode, NetworkModeContainerPrefix)
}
