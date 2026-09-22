package containerlab

import (
	"fmt"
	"sort"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	claberneteserrors "github.com/clabernetes/clabernetes/errors"
)

// ValidateLink checks the parts of a link spec that the CRD schema cannot express.
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

// FindEndpointConflict returns the lexically first valid link that has already claimed either
// endpoint of link. Resolving the whole namespace in name order avoids conflict chains where a
// rejected link would otherwise reject another link through its unused second endpoint.
func FindEndpointConflict(
	link *clabernetesapisv1alpha1.Link,
	namespaceLinks []clabernetesapisv1alpha1.Link,
) string {
	candidates := make([]clabernetesapisv1alpha1.Link, 0, len(namespaceLinks)+1)
	candidates = append(candidates, namespaceLinks...)

	var found bool

	for idx := range candidates {
		if candidates[idx].GetName() == link.GetName() {
			candidates[idx] = *link.DeepCopy()
			found = true

			break
		}
	}

	if !found {
		candidates = append(candidates, *link.DeepCopy())
	}

	return EndpointConflicts(candidates)[link.GetName()]
}

// EndpointConflicts resolves endpoint ownership for a whole namespace in one sorted pass.
// Rejected links do not reserve their unused endpoint, so conflict chains stay deterministic.
func EndpointConflicts(namespaceLinks []clabernetesapisv1alpha1.Link) map[string]string {
	candidates := append([]clabernetesapisv1alpha1.Link(nil), namespaceLinks...)
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].GetName() < candidates[j].GetName()
	})

	claimed := map[string]string{}
	conflicts := map[string]string{}

	for idx := range candidates {
		candidate := &candidates[idx]
		if ValidateLink(candidate) != nil {
			continue
		}

		conflict := conflictForClaimedEndpoints(candidate, claimed)
		if conflict != "" {
			conflicts[candidate.GetName()] = conflict

			continue
		}

		claimLinkEndpoints(candidate, claimed)
	}

	return conflicts
}

func endpointKey(endpoint clabernetesapisv1alpha1.LinkEndpointSpec) string {
	return fmt.Sprintf("%s:%s", endpoint.NodeName, endpoint.InterfaceName)
}

func conflictForClaimedEndpoints(
	link *clabernetesapisv1alpha1.Link,
	claimed map[string]string,
) string {
	for _, endpoint := range []clabernetesapisv1alpha1.LinkEndpointSpec{
		link.Spec.EndpointA,
		link.Spec.EndpointB,
	} {
		if endpoint.NodeName == clabernetesapisv1alpha1.LinkHostNodeName {
			continue
		}

		if owner := claimed[endpointKey(endpoint)]; owner != "" {
			return owner
		}
	}

	return ""
}

func claimLinkEndpoints(
	link *clabernetesapisv1alpha1.Link,
	claimed map[string]string,
) {
	for _, endpoint := range []clabernetesapisv1alpha1.LinkEndpointSpec{
		link.Spec.EndpointA,
		link.Spec.EndpointB,
	} {
		if endpoint.NodeName == clabernetesapisv1alpha1.LinkHostNodeName {
			continue
		}

		claimed[endpointKey(endpoint)] = link.GetName()
	}
}
