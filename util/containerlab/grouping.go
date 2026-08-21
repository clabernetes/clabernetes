package containerlab

import (
	"sort"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
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

// ResolvePrimaryNode resolves the name of the primary node whose Pod hosts the given (containerlab)
// node -- for "grouped" nodes (network-mode: container:<primary>) this walks to the group's
// primary node; for anything else (including nodes that have no Node object (yet)) the node
// name itself is returned.
func ResolvePrimaryNode(
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

// ResolveGroupMembers returns the (sorted) names of all nodes hosted by the given primary node
// -- the primary node itself first, then any nodes grouped onto it via network-mode.
func ResolveGroupMembers(
	nodes map[string]*clabernetesapisv1alpha1.Node,
	primaryNode string,
) []string {
	members := []string{primaryNode}

	for nodeName := range nodes {
		if nodeName == primaryNode {
			continue
		}

		if ResolvePrimaryNode(nodes, nodeName) == primaryNode {
			members = append(members, nodeName)
		}
	}

	sort.Slice(members[1:], func(i, j int) bool { return members[i+1] < members[j+1] })

	return members
}
