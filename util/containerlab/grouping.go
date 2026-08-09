package containerlab

import (
	"sort"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
)

// NodesByName maps clabernetes Node objects by their (containerlab node) name.
func NodesByName(
	nodes []clabernetesapisv1alpha1.Node,
) map[string]*clabernetesapisv1alpha1.Node {
	byName := make(map[string]*clabernetesapisv1alpha1.Node, len(nodes))

	for idx := range nodes {
		byName[nodes[idx].GetName()] = &nodes[idx]
	}

	return byName
}

// ResolveLauncherNode resolves the name of the launcher (pod) hosting the given (containerlab)
// node -- for "grouped" nodes (network-mode: container:<primary>) this walks to the group's
// primary node; for anything else (including nodes that have no Node object (yet)) the node
// name itself is returned.
func ResolveLauncherNode(
	nodes map[string]*clabernetesapisv1alpha1.Node,
	nodeName string,
) string {
	seen := map[string]bool{}

	current := nodeName

	for {
		node, ok := nodes[current]
		if !ok {
			return current
		}

		primary := ParseNetworkModeContainer(node.Spec.NetworkMode)
		if primary == "" {
			return current
		}

		if seen[primary] {
			// cycle in network-mode references -- return where we are rather than looping
			return current
		}

		seen[current] = true

		current = primary
	}
}

// ResolveGroupMembers returns the (sorted) names of all nodes hosted by the given launcher node
// -- the launcher node itself first, then any nodes grouped onto it via network-mode.
func ResolveGroupMembers(
	nodes map[string]*clabernetesapisv1alpha1.Node,
	launcherNode string,
) []string {
	members := []string{launcherNode}

	for nodeName := range nodes {
		if nodeName == launcherNode {
			continue
		}

		if ResolveLauncherNode(nodes, nodeName) == launcherNode {
			members = append(members, nodeName)
		}
	}

	sort.Slice(members[1:], func(i, j int) bool { return members[i+1] < members[j+1] })

	return members
}
