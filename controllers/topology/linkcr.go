package topology

import (
	"fmt"
	"maps"
	"reflect"
	"regexp"
	"sort"
	"strings"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/srl-labs/clabernetes/config"
	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	claberneteslogging "github.com/srl-labs/clabernetes/logging"
	clabernetesutil "github.com/srl-labs/clabernetes/util"
	clabernetesutilkubernetes "github.com/srl-labs/clabernetes/util/kubernetes"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
)

// maxTunnelID is the (very generous) ceiling for tunnel id allocation -- vxlan vnids can go to
// ~16 million.
const maxTunnelID = 16_000_000

var linkNameInvalidChars = regexp.MustCompile(`[^a-z0-9-]`) //nolint:gochecknoglobals

// sanitizeLinkNamePart makes an interface name safe for use *inside* a kubernetes object name --
// note that this deliberately does not enforce the leading/trailing alpha requirements of a full
// dns label (a name part may start/end with a digit, i.e. "e1-1") as uniqueness of the resulting
// name matters more than prettiness here.
func sanitizeLinkNamePart(part string) string {
	part = linkNameInvalidChars.ReplaceAllString(strings.ToLower(part), "-")

	part = strings.Trim(part, "-")

	if part == "" {
		part = "x"
	}

	return part
}

// LinkResourceName returns the name of the Link resource for the given (canonically ordered)
// endpoints of the given topology.
func LinkResourceName(
	owningTopology *clabernetesapisv1alpha1.Topology,
	endpointA,
	endpointB clabernetesapisv1alpha1.LinkEndpointSpec,
) string {
	nameParts := make([]string, 0)

	if !ResolveTopologyRemovePrefix(owningTopology) {
		nameParts = append(nameParts, owningTopology.GetName())
	}

	nameParts = append(
		nameParts,
		endpointA.NodeName,
		sanitizeLinkNamePart(endpointA.InterfaceName),
		endpointB.NodeName,
		sanitizeLinkNamePart(endpointB.InterfaceName),
	)

	return clabernetesutilkubernetes.SafeConcatNameKubernetes(nameParts...)
}

// LinkReconciler is a subcomponent of the "TopologyReconciler" but is exposed for testing
// purposes. This is the component responsible for rendering/validating the Link crs for a
// clabernetes topology resource.
type LinkReconciler struct {
	log                 claberneteslogging.Instance
	configManagerGetter clabernetesconfig.ManagerGetterFunc
}

// NewLinkReconciler returns an instance of LinkReconciler.
func NewLinkReconciler(
	log claberneteslogging.Instance,
	configManagerGetter clabernetesconfig.ManagerGetterFunc,
) *LinkReconciler {
	return &LinkReconciler{
		log:                 log,
		configManagerGetter: configManagerGetter,
	}
}

// tunnelHalf is a little helper struct pairing a "local view" tunnel with the launcher node it
// was resolved for.
type tunnelHalf struct {
	launcherNode string
	tunnel       *clabernetesapisv1alpha1.PointToPointTunnel
}

// tunnelHalfKey returns a key for the given local/remote endpoint info of one side of a tunnel --
// the mirrored side of the tunnel produces the mirrored key, which is how the two halves of a
// link get paired up.
func tunnelHalfKey(localNode, localInterface, remoteNode, remoteInterface string) string {
	return fmt.Sprintf("%s:%s<->%s:%s", localNode, localInterface, remoteNode, remoteInterface)
}

// RenderAll accepts the owning topology and the resolved (per launcher node, "local view")
// tunnels and renders the final link crs for the topology -- pairing up the two halves of each
// tunnel into a single Link object. Note that tunnel ids are *not* set here, see
// AllocateTunnelIDs. The returned links are sorted by name so rendering is deterministic.
func (r *LinkReconciler) RenderAll(
	owningTopology *clabernetesapisv1alpha1.Topology,
	tunnels map[string][]*clabernetesapisv1alpha1.PointToPointTunnel,
) []*clabernetesapisv1alpha1.Link {
	halvesByKey := make(map[string]*tunnelHalf)

	for launcherNode, launcherTunnels := range tunnels {
		for _, tunnel := range launcherTunnels {
			key := tunnelHalfKey(
				tunnel.LocalNode,
				tunnel.LocalInterface,
				tunnel.RemoteNode,
				tunnel.RemoteInterface,
			)

			halvesByKey[key] = &tunnelHalf{
				launcherNode: launcherNode,
				tunnel:       tunnel,
			}
		}
	}

	links := make([]*clabernetesapisv1alpha1.Link, 0, len(halvesByKey)/2) //nolint:mnd

	for key, half := range halvesByKey {
		mirrorKey := tunnelHalfKey(
			half.tunnel.RemoteNode,
			half.tunnel.RemoteInterface,
			half.tunnel.LocalNode,
			half.tunnel.LocalInterface,
		)

		if key > mirrorKey {
			// each link is represented by two halves (one per side); only render the link when
			// visiting the "lesser" (canonically first) half so we end up with exactly one link
			// per tunnel pair
			continue
		}

		mirrorHalf, ok := halvesByKey[mirrorKey]
		if !ok {
			r.log.Warnf(
				"no mirrored tunnel found for link '%s' of topology '%s', skipping link,"+
					" this is probably a bug",
				key,
				owningTopology.GetName(),
			)

			continue
		}

		links = append(links, r.renderLink(owningTopology, half, mirrorHalf))
	}

	sort.Slice(links, func(i, j int) bool { return links[i].Name < links[j].Name })

	return links
}

func (r *LinkReconciler) renderLink(
	owningTopology *clabernetesapisv1alpha1.Topology,
	halfA,
	halfB *tunnelHalf,
) *clabernetesapisv1alpha1.Link {
	owningTopologyName := owningTopology.GetName()

	// each halfs tunnel destination is the service of the *remote* side, so the "local" service
	// for an endpoint (the service the other side dials) comes from the mirrored half
	endpointA := clabernetesapisv1alpha1.LinkEndpointSpec{
		NodeName:      halfA.tunnel.LocalNode,
		InterfaceName: halfA.tunnel.LocalInterface,
		LauncherNode:  halfA.launcherNode,
		Destination:   halfB.tunnel.Destination,
	}

	endpointB := clabernetesapisv1alpha1.LinkEndpointSpec{
		NodeName:      halfB.tunnel.LocalNode,
		InterfaceName: halfB.tunnel.LocalInterface,
		LauncherNode:  halfB.launcherNode,
		Destination:   halfA.tunnel.Destination,
	}

	annotations, globalLabels := r.configManagerGetter().GetAllMetadata()

	labels := map[string]string{
		clabernetesconstants.LabelApp:           clabernetesconstants.Clabernetes,
		clabernetesconstants.LabelName:          owningTopologyName,
		clabernetesconstants.LabelTopologyOwner: owningTopologyName,
		clabernetesconstants.LabelTopologyKind:  GetTopologyKind(owningTopology),
		clabernetesconstants.LabelLinkEndpointA: endpointA.LauncherNode,
		clabernetesconstants.LabelLinkEndpointB: endpointB.LauncherNode,
	}

	maps.Copy(labels, globalLabels)

	return &clabernetesapisv1alpha1.Link{
		ObjectMeta: metav1.ObjectMeta{
			Name:        LinkResourceName(owningTopology, endpointA, endpointB),
			Namespace:   owningTopology.GetNamespace(),
			Annotations: annotations,
			Labels:      labels,
		},
		Spec: clabernetesapisv1alpha1.LinkSpec{
			TopologyName: owningTopologyName,
			EndpointA:    endpointA,
			EndpointB:    endpointB,
		},
	}
}

// AllocateTunnelIDs assigns tunnel ids to the rendered links -- links that already exist (by
// name) keep their previously allocated id, any remaining links get the lowest free id. Because
// link names are derived from the link endpoints, ids are stable across reconciles.
func AllocateTunnelIDs(
	existingLinks map[string]*clabernetesapisv1alpha1.Link,
	renderedLinks []*clabernetesapisv1alpha1.Link,
) {
	allocatedIDs := make(map[int]bool)

	for _, link := range renderedLinks {
		existingLink, ok := existingLinks[link.Name]
		if !ok || existingLink.Spec.TunnelID == 0 {
			continue
		}

		link.Spec.TunnelID = existingLink.Spec.TunnelID
		allocatedIDs[link.Spec.TunnelID] = true
	}

	nextID := 1

	for _, link := range renderedLinks {
		if link.Spec.TunnelID != 0 {
			continue
		}

		for ; nextID < maxTunnelID; nextID++ {
			if !allocatedIDs[nextID] {
				break
			}
		}

		link.Spec.TunnelID = nextID
		allocatedIDs[nextID] = true
	}
}

// Resolve accepts the rendered links and a list of link crs that are -- by owner reference
// and/or labels -- associated with the topology. It returns a ObjectDiffer object (keyed by link
// name) that contains the missing, extra, and current link crs for the topology.
func (r *LinkReconciler) Resolve(
	ownedLinks *clabernetesapisv1alpha1.LinkList,
	renderedLinks []*clabernetesapisv1alpha1.Link,
) *clabernetesutil.ObjectDiffer[*clabernetesapisv1alpha1.Link] {
	links := &clabernetesutil.ObjectDiffer[*clabernetesapisv1alpha1.Link]{
		Current: map[string]*clabernetesapisv1alpha1.Link{},
	}

	for i := range ownedLinks.Items {
		links.Current[ownedLinks.Items[i].Name] = &ownedLinks.Items[i]
	}

	allLinkNames := make([]string, len(renderedLinks))

	for idx, link := range renderedLinks {
		allLinkNames[idx] = link.Name
	}

	links.SetMissing(allLinkNames)
	links.SetExtra(allLinkNames)

	return links
}

// Conforms checks if the existing link cr conforms to the rendered expectation.
func (r *LinkReconciler) Conforms(
	existingLink,
	renderedLink *clabernetesapisv1alpha1.Link,
	expectedOwnerUID apimachinerytypes.UID,
) bool {
	if !reflect.DeepEqual(existingLink.Spec, renderedLink.Spec) {
		return false
	}

	if !clabernetesutilkubernetes.ExistingMapStringStringContainsAllExpectedKeyValues(
		existingLink.ObjectMeta.Annotations,
		renderedLink.ObjectMeta.Annotations,
	) {
		return false
	}

	if !clabernetesutilkubernetes.ExistingMapStringStringContainsAllExpectedKeyValues(
		existingLink.ObjectMeta.Labels,
		renderedLink.ObjectMeta.Labels,
	) {
		return false
	}

	if len(existingLink.ObjectMeta.OwnerReferences) != 1 {
		// we should have only one owner reference, the owning topology
		return false
	}

	if existingLink.ObjectMeta.OwnerReferences[0].UID != expectedOwnerUID {
		// owner ref uid is not us
		return false
	}

	return true
}
