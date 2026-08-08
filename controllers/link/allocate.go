package link

import (
	"fmt"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	claberneteserrors "github.com/srl-labs/clabernetes/errors"
	clabernetesutilcontainerlab "github.com/srl-labs/clabernetes/util/containerlab"
)

// maxVXLANTunnelID is the ceiling retained from the Link status API. Slurpeeth uses a smaller
// uint16 segment identifier, selected per Link below.
const maxVXLANTunnelID = 16_000_000

// ValidateLink checks the parts of a link spec that the crd schema cannot express. A non-nil
// error means the spec is terminally invalid -- there is nothing to retry until the spec
// changes.
func ValidateLink(link *clabernetesapisv1alpha1.Link) error {
	return clabernetesutilcontainerlab.ValidateLink(link)
}

// ValidateLinkEndpoints verifies that every non-host endpoint resolves to a Node in the Link's
// namespace.
func ValidateLinkEndpoints(
	link *clabernetesapisv1alpha1.Link,
	nodes map[string]*clabernetesapisv1alpha1.Node,
) error {
	for _, endpoint := range []clabernetesapisv1alpha1.LinkEndpointSpec{
		link.Spec.EndpointA,
		link.Spec.EndpointB,
	} {
		if endpoint.NodeName == clabernetesapisv1alpha1.LinkHostNodeName {
			continue
		}

		if _, exists := nodes[endpoint.NodeName]; !exists {
			return fmt.Errorf(
				"%w: endpoint Node %q does not exist",
				claberneteserrors.ErrInvalidData,
				endpoint.NodeName,
			)
		}
	}

	return nil
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

// LinksWithResolvedEndpoints filters unresolved Links out of deterministic endpoint conflict
// resolution. An unresolved Link cannot reserve an interface from a realizable Link.
func LinksWithResolvedEndpoints(
	namespaceLinks []clabernetesapisv1alpha1.Link,
	nodes map[string]*clabernetesapisv1alpha1.Node,
) []clabernetesapisv1alpha1.Link {
	resolved := make([]clabernetesapisv1alpha1.Link, 0, len(namespaceLinks))

	for idx := range namespaceLinks {
		if ValidateLinkEndpoints(&namespaceLinks[idx], nodes) == nil {
			resolved = append(resolved, namespaceLinks[idx])
		}
	}

	return resolved
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

	maxTunnelID := maxVXLANTunnelID
	if link.Spec.NormalizedConnectivity() ==
		clabernetesapisv1alpha1.LinkConnectivitySlurpeeth {
		maxTunnelID = clabernetesapisv1alpha1.SlurpeethMaxSegmentID
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
