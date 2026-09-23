package link

import (
	"fmt"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	claberneteserrors "github.com/clabernetes/clabernetes/errors"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
)

// maxWireID is the Link status API's allocation ceiling -- an arbitrary sane bound, far below
// the wire protocol's 32-bit link-id space.
const maxWireID = 16_000_000

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

// IsSamePodLink returns true if both endpoints of the link resolve to the same primary node
// (pod) -- such links are materialized inside that pod and need no wire id.
func IsSamePodLink(
	link *clabernetesapisv1alpha1.Link,
	nodes map[string]*clabernetesapisv1alpha1.Node,
) bool {
	return clabernetesutilcontainerlab.ResolvePrimaryNode(nodes, link.Spec.EndpointA.NodeName) ==
		clabernetesutilcontainerlab.ResolvePrimaryNode(nodes, link.Spec.EndpointB.NodeName)
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

// ResolveDesiredWireID determines the wire id the given link should hold in its status:
//
//   - host links and same-pod links never touch the wire -- 0.
//   - a valid existing id is retained unless a lexically-smaller-keyed link claims the same id
//     (retention is what keeps "rewires" -- endpoint changes on an existing link -- as live
//     wire moves rather than re-allocations).
//   - otherwise the lowest id not used by any other link in the namespace is allocated: wire
//     ids dispatch inside one receiving sidecar from a validated source, so the namespace is
//     the whole allocation domain and identical ids in other namespaces can never meet.
func ResolveDesiredWireID(
	link *clabernetesapisv1alpha1.Link,
	namespaceLinks []clabernetesapisv1alpha1.Link,
	namespaceNodes []clabernetesapisv1alpha1.Node,
) (int, error) {
	return newWireReservations(namespaceLinks).desired(link,
		clabernetesutilcontainerlab.NodesByName(namespaceNodes))
}

// wireReservations is local to a namespace pass, never shared with event handlers. Existing
// IDs (including rejected/terminating Links) stay reserved until a successful status write
// releases them. A failed or ambiguous write aborts the pass and forces a fresh API snapshot.
type wireReservations struct {
	owners map[int]map[string]bool
	next   int
}

func newWireReservations(links []clabernetesapisv1alpha1.Link) *wireReservations {
	reservations := &wireReservations{owners: map[int]map[string]bool{}, next: 1}
	for idx := range links {
		reservations.record(&links[idx], 0)
	}

	return reservations
}

func (r *wireReservations) record(link *clabernetesapisv1alpha1.Link, previous int) {
	if previous == link.Status.WireID {
		return
	}
	key := link.GetNamespace() + "/" + link.GetName()
	if previous > 0 {
		delete(r.owners[previous], key)
		if len(r.owners[previous]) == 0 {
			delete(r.owners, previous)
			r.next = min(r.next, previous)
		}
	}
	id := link.Status.WireID
	if id > 0 {
		if r.owners[id] == nil {
			r.owners[id] = map[string]bool{}
		}
		r.owners[id][key] = true
	}
}

func (r *wireReservations) desired(
	link *clabernetesapisv1alpha1.Link,
	nodes map[string]*clabernetesapisv1alpha1.Node,
) (int, error) {
	if IsHostLink(link) || IsSamePodLink(link, nodes) {
		return 0, nil
	}
	key := link.GetNamespace() + "/" + link.GetName()
	contested := false
	for owner := range r.owners[link.Status.WireID] {
		if owner < key {
			contested = true

			break
		}
	}
	if link.Status.WireID >= 1 && link.Status.WireID <= maxWireID && !contested {
		return link.Status.WireID, nil
	}
	for r.next <= maxWireID {
		owners := r.owners[r.next]
		if len(owners) == 0 || (len(owners) == 1 && owners[key]) {
			return r.next, nil
		}
		r.next++
	}

	return 0, fmt.Errorf("%w: no wire ids remain in range 1-%d",
		claberneteserrors.ErrInvalidData, maxWireID)
}
