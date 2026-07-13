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
	return clabernetesutilcontainerlab.ValidateLink(link)
}

// IsHostLink returns true if either side of the link is a (reserved node name) host endpoint.
func IsHostLink(link *clabernetesapisv1alpha1.Link) bool {
	return link.Spec.EndpointA.NodeName == clabernetesapisv1alpha1.LinkHostNodeName ||
		link.Spec.EndpointB.NodeName == clabernetesapisv1alpha1.LinkHostNodeName
}

// IsSameLauncherLink returns true if both endpoints of the link resolve to the same launcher
// (pod) -- such links are materialized as direct containerlab links by that launcher and need
// no tunnel id.
func IsSameLauncherLink(
	link *clabernetesapisv1alpha1.Link,
	nodes map[string]*clabernetesapisv1alpha1.Node,
) bool {
	return clabernetesutilcontainerlab.ResolveLauncherNode(nodes, link.Spec.EndpointA.NodeName) ==
		clabernetesutilcontainerlab.ResolveLauncherNode(nodes, link.Spec.EndpointB.NodeName)
}

// FindEndpointConflict checks if any *other* link in the namespace claims an endpoint (node +
// interface) of the given link and has precedence over it (lexically smaller name -- a
// deterministic, clock-free tie break). It returns the name of the winning conflicting link, or
// an empty string when the given link owns its endpoints.
func FindEndpointConflict(
	link *clabernetesapisv1alpha1.Link,
	namespaceLinks []clabernetesapisv1alpha1.Link,
) string {
	return clabernetesutilcontainerlab.FindEndpointConflict(link, namespaceLinks)
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
