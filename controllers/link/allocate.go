package link

import (
	"fmt"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	claberneteserrors "github.com/srl-labs/clabernetes/errors"
	clabernetesutilcontainerlab "github.com/srl-labs/clabernetes/util/containerlab"
)

// maxTunnelID is the (very generous) ceiling for tunnel id allocation -- vxlan vnids can go to
// ~16 million. slurpeeth segment ids are uint16, but since allocation always hands out the
// lowest free id per namespace that ceiling only matters once a single namespace holds >65k
// concurrent links.
const maxTunnelID = 16_000_000

// ValidateLink checks the parts of a link spec that the crd schema cannot express. A non-nil
// error means the spec is terminally invalid -- there is nothing to retry until the spec
// changes.
func ValidateLink(link *clabernetesapisv1alpha1.Link) error {
	endpointA, endpointB := link.Spec.EndpointA, link.Spec.EndpointB

	if endpointA.NodeName == clabernetesapisv1alpha1.LinkHostNodeName &&
		endpointB.NodeName == clabernetesapisv1alpha1.LinkHostNodeName {
		return fmt.Errorf(
			"%w: both endpoints are %q links, one endpoint must be a node",
			claberneteserrors.ErrInvalidData,
			clabernetesapisv1alpha1.LinkHostNodeName,
		)
	}

	if endpointA.NodeName == endpointB.NodeName &&
		endpointA.InterfaceName == endpointB.InterfaceName {
		return fmt.Errorf(
			"%w: link connects interface '%s:%s' to itself",
			claberneteserrors.ErrInvalidData,
			endpointA.NodeName,
			endpointA.InterfaceName,
		)
	}

	return nil
}

// IsHostLink returns true if either side of the link is a (reserved node name) host endpoint.
func IsHostLink(link *clabernetesapisv1alpha1.Link) bool {
	return link.Spec.EndpointA.NodeName == clabernetesapisv1alpha1.LinkHostNodeName ||
		link.Spec.EndpointB.NodeName == clabernetesapisv1alpha1.LinkHostNodeName
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

		primary := clabernetesutilcontainerlab.ParseNetworkModeContainer(node.Spec.NetworkMode)
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

// IsSameLauncherLink returns true if both endpoints of the link resolve to the same launcher
// (pod) -- such links are materialized as direct containerlab links by that launcher and need
// no tunnel id.
func IsSameLauncherLink(
	link *clabernetesapisv1alpha1.Link,
	nodes map[string]*clabernetesapisv1alpha1.Node,
) bool {
	return ResolveLauncherNode(nodes, link.Spec.EndpointA.NodeName) ==
		ResolveLauncherNode(nodes, link.Spec.EndpointB.NodeName)
}

// endpointKey returns the identity of a (non host) endpoint for conflict checking.
func endpointKey(endpoint clabernetesapisv1alpha1.LinkEndpointSpec) string {
	return fmt.Sprintf("%s:%s", endpoint.NodeName, endpoint.InterfaceName)
}

// FindEndpointConflict checks if any *other* link in the namespace claims an endpoint (node +
// interface) of the given link and has precedence over it (lexically smaller name -- a
// deterministic, clock-free tie break). It returns the name of the winning conflicting link, or
// an empty string when the given link owns its endpoints.
func FindEndpointConflict(
	link *clabernetesapisv1alpha1.Link,
	namespaceLinks []clabernetesapisv1alpha1.Link,
) string {
	claimed := map[string]bool{}

	for _, endpoint := range []clabernetesapisv1alpha1.LinkEndpointSpec{
		link.Spec.EndpointA, link.Spec.EndpointB,
	} {
		if endpoint.NodeName == clabernetesapisv1alpha1.LinkHostNodeName {
			// host "endpoints" are not exclusive -- many links may terminate on the host side
			continue
		}

		claimed[endpointKey(endpoint)] = true
	}

	for idx := range namespaceLinks {
		other := &namespaceLinks[idx]

		if other.GetName() == link.GetName() {
			continue
		}

		if other.GetName() > link.GetName() {
			// the other link loses the tie break, not us
			continue
		}

		for _, endpoint := range []clabernetesapisv1alpha1.LinkEndpointSpec{
			other.Spec.EndpointA, other.Spec.EndpointB,
		} {
			if claimed[endpointKey(endpoint)] {
				return other.GetName()
			}
		}
	}

	return ""
}

// ResolveDesiredTunnelID determines the tunnel id the given link should hold in its status:
//
//   - host links and same-launcher links need no tunnel -- 0.
//   - a valid existing id is retained unless a lexically-smaller-named link claims the same id
//     (retention is what keeps "rewires" -- endpoint changes on an existing link -- as live
//     tunnel moves rather than re-allocations).
//   - otherwise the lowest id not used by any other link in the namespace is allocated.
func ResolveDesiredTunnelID(
	link *clabernetesapisv1alpha1.Link,
	namespaceLinks []clabernetesapisv1alpha1.Link,
	namespaceNodes []clabernetesapisv1alpha1.Node,
) (int, error) {
	if IsHostLink(link) {
		return 0, nil
	}

	nodes := make(map[string]*clabernetesapisv1alpha1.Node, len(namespaceNodes))

	for idx := range namespaceNodes {
		nodes[namespaceNodes[idx].GetName()] = &namespaceNodes[idx]
	}

	if IsSameLauncherLink(link, nodes) {
		return 0, nil
	}

	usedIDs := map[int]bool{}

	ownIDContested := false

	for idx := range namespaceLinks {
		other := &namespaceLinks[idx]

		if other.GetName() == link.GetName() {
			continue
		}

		if other.Status.TunnelID <= 0 {
			continue
		}

		usedIDs[other.Status.TunnelID] = true

		if other.Status.TunnelID == link.Status.TunnelID && other.GetName() < link.GetName() {
			ownIDContested = true
		}
	}

	if link.Status.TunnelID >= 1 && link.Status.TunnelID <= maxTunnelID && !ownIDContested {
		return link.Status.TunnelID, nil
	}

	for candidate := 1; candidate <= maxTunnelID; candidate++ {
		if !usedIDs[candidate] {
			return candidate, nil
		}
	}

	return 0, fmt.Errorf(
		"%w: no tunnel ids remain in range 1-%d",
		claberneteserrors.ErrInvalidData,
		maxTunnelID,
	)
}
