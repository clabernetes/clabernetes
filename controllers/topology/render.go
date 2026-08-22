package topology

import (
	"crypto/sha256"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strings"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesutilkubernetes "github.com/clabernetes/clabernetes/util/kubernetes"
	k8scorev1 "k8s.io/api/core/v1"
	apimachineryequality "k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var linkNameInvalidChars = regexp.MustCompile(`[^a-z0-9-]`)

// sanitizeLinkNamePart makes an interface name safe for use inside a kubernetes object name.
// Any lossy normalization includes a hash of the raw value so distinct interfaces remain
// distinct.
func sanitizeLinkNamePart(part string) string {
	rawPart := part
	part = linkNameInvalidChars.ReplaceAllString(strings.ToLower(rawPart), "-")

	part = strings.Trim(part, "-")

	if part == "" {
		part = "x"
	}

	if part != rawPart {
		digest := sha256.Sum256([]byte(rawPart))
		part = fmt.Sprintf("%s-%x", part, digest[:4])
	}

	return part
}

// LinkResourceName returns the (deterministic) name of the Link object for the given wire.
func LinkResourceName(endpointA, endpointB clabernetesapisv1alpha1.LinkEndpointSpec) string {
	return clabernetesutilkubernetes.SafeConcatNameKubernetes(
		endpointA.NodeName,
		sanitizeLinkNamePart(endpointA.InterfaceName),
		endpointB.NodeName,
		sanitizeLinkNamePart(endpointB.InterfaceName),
	)
}

// topologyOwnedObjectMetadata returns the base metadata for objects the compiler emits for the
// given topology.
func topologyOwnedObjectMetadata(
	topology *clabernetesapisv1alpha1.Topology,
	name string,
	configManagerGetter clabernetesconfig.ManagerGetterFunc,
) metav1.ObjectMeta {
	annotations, globalLabels := configManagerGetter().GetAllMetadata()

	labels := map[string]string{
		clabernetesconstants.LabelApp:           clabernetesconstants.Clabernetes,
		clabernetesconstants.LabelName:          name,
		clabernetesconstants.LabelTopologyOwner: topology.GetName(),
		clabernetesconstants.LabelTopologyKind:  GetTopologyKind(topology),
	}

	maps.Copy(labels, globalLabels)

	return metav1.ObjectMeta{
		Name:        name,
		Namespace:   topology.GetNamespace(),
		Annotations: annotations,
		Labels:      labels,
	}
}

// RenderNodes renders the Node objects for the compiled topology, sorted by name. The emitted
// node names are the containerlab node names verbatim -- the namespace is the topology
// boundary.
func RenderNodes(
	topology *clabernetesapisv1alpha1.Topology,
	compiled *CompiledTopology,
	configManagerGetter clabernetesconfig.ManagerGetterFunc,
) []*clabernetesapisv1alpha1.Node {
	nodes := make([]*clabernetesapisv1alpha1.Node, 0, len(compiled.Nodes))

	for nodeName, nodeDefinition := range compiled.Nodes {
		profileName := nodeProfileNameForNode(topology, nodeName)
		node := &clabernetesapisv1alpha1.Node{
			ObjectMeta: topologyOwnedObjectMetadata(topology, nodeName, configManagerGetter),
			Spec: clabernetesapisv1alpha1.NodeSpec{
				NodeDefinition: *nodeDefinition.DeepCopy(),
				ProfileRef:     &k8scorev1.LocalObjectReference{Name: profileName},
				FilesFromConfigMap: slices.Clone(
					topology.Spec.Deployment.FilesFromConfigMap[nodeName],
				),
				FilesFromSecret: slices.Clone(
					topology.Spec.Deployment.FilesFromSecret[nodeName],
				),
				FilesFromURL: slices.Clone(topology.Spec.Deployment.FilesFromURL[nodeName]),
			},
		}

		// Retain this label for human selection and compatibility; profile attachment is explicit.
		node.Labels[clabernetesconstants.LabelTopologyNode] = nodeName

		// The containerlab group name is organizational metadata; it rides along as a label so
		// operators can select by it, exactly as they would filter by group in containerlab.
		if nodeDefinition.Group != "" {
			node.Labels[clabernetesconstants.LabelTopologyGroup] = nodeDefinition.Group
		}

		// containerlab node labels are kubernetes labels here rather than docker labels on the
		// node container. The compiler has already dropped any that kubernetes would reject or
		// that sit in c9s' own namespace, so nothing here can shadow the labels above.
		maps.Copy(node.Labels, nodeDefinition.Labels)

		nodes = append(nodes, node)
	}

	sort.Slice(nodes, func(i, j int) bool { return nodes[i].GetName() < nodes[j].GetName() })

	return nodes
}

func nodeProfileNameForNode(
	topology *clabernetesapisv1alpha1.Topology,
	nodeName string,
) string {
	if hasDistinctProfilePolicy(topology, nodeName) {
		return clabernetesutilkubernetes.SafeConcatNameKubernetes(topology.GetName(), nodeName)
	}

	return topology.GetName()
}

// hasDistinctProfilePolicy reports whether a Node needs a dedicated NodeProfile. Today the
// only per-node profile policy exposed by Topology is deployment.resources; payload maps are
// rendered directly onto Nodes and therefore do not create one-off profiles.
func hasDistinctProfilePolicy(
	topology *clabernetesapisv1alpha1.Topology,
	nodeName string,
) bool {
	nodeResources, hasNodeResources := topology.Spec.Deployment.Resources[nodeName]
	if !hasNodeResources {
		return false
	}

	defaultResources, hasDefaultResources := topology.Spec.Deployment.
		Resources[clabernetesconstants.Default]
	if !hasDefaultResources {
		// The map entry is itself meaningful, including an explicitly empty resource policy.
		return true
	}

	return !apimachineryequality.Semantic.DeepEqual(nodeResources, defaultResources)
}

// RenderLinks renders the Link objects for the compiled topology, sorted by name.
func RenderLinks(
	topology *clabernetesapisv1alpha1.Topology,
	compiled *CompiledTopology,
	configManagerGetter clabernetesconfig.ManagerGetterFunc,
) []*clabernetesapisv1alpha1.Link {
	links := make([]*clabernetesapisv1alpha1.Link, 0, len(compiled.Links))

	for _, compiledLink := range compiled.Links {
		links = append(links, &clabernetesapisv1alpha1.Link{
			ObjectMeta: topologyOwnedObjectMetadata(
				topology,
				LinkResourceName(compiledLink.EndpointA, compiledLink.EndpointB),
				configManagerGetter,
			),
			Spec: clabernetesapisv1alpha1.LinkSpec{
				EndpointA: compiledLink.EndpointA,
				EndpointB: compiledLink.EndpointB,
				MTU:       compiledLink.MTU,
			},
		})
	}

	sort.Slice(links, func(i, j int) bool { return links[i].GetName() < links[j].GetName() })

	return links
}

// RenderNodeProfiles renders one shared topology NodeProfile plus a complete dedicated
// profile for each compiled Node with distinct profile policy.
func RenderNodeProfiles(
	topology *clabernetesapisv1alpha1.Topology,
	compiled *CompiledTopology,
	configManagerGetter clabernetesconfig.ManagerGetterFunc,
) []*clabernetesapisv1alpha1.NodeProfile {
	perNodeNames := make([]string, 0)
	sharedProfileNeeded := false

	for nodeName := range compiled.Nodes {
		if !hasDistinctProfilePolicy(topology, nodeName) {
			sharedProfileNeeded = true

			continue
		}

		perNodeNames = append(perNodeNames, nodeName)
	}

	sort.Strings(perNodeNames)

	profileCount := len(perNodeNames)
	if sharedProfileNeeded {
		profileCount++
	}

	profiles := make([]*clabernetesapisv1alpha1.NodeProfile, 0, profileCount)

	if sharedProfileNeeded {
		profiles = append(
			profiles,
			renderTopologyNodeProfile(topology, compiled, configManagerGetter),
		)
	}

	for _, nodeName := range perNodeNames {
		profiles = append(
			profiles,
			renderPerNodeNodeProfile(
				topology,
				compiled,
				nodeName,
				configManagerGetter,
			),
		)
	}

	return profiles
}

// renderTopologyNodeProfile compiles topology-wide policy into the shared profile. Only
// values represented by the Topology API are copied; omitted pointer/collection values continue
// to inherit Config defaults.
func renderTopologyNodeProfile(
	topology *clabernetesapisv1alpha1.Topology,
	compiled *CompiledTopology,
	configManagerGetter clabernetesconfig.ManagerGetterFunc,
) *clabernetesapisv1alpha1.NodeProfile {
	spec := clabernetesapisv1alpha1.NodeProfileSpec{
		Expose: &clabernetesapisv1alpha1.NodeProfileExpose{
			DisableExpose: new(topology.Spec.Expose.DisableExpose),
			DisableAutoExpose: new(
				topology.Spec.Expose.DisableAutoExpose,
			),
			ExposeType: topology.Spec.Expose.ExposeType,
			UseNodeMgmtIpv4Address: new(
				topology.Spec.Expose.UseNodeMgmtIpv4Address,
			),
			UseNodeMgmtIpv6Address: new(
				topology.Spec.Expose.UseNodeMgmtIpv6Address,
			),
		},
	}

	imagePull := &clabernetesapisv1alpha1.NodeProfileImagePull{
		Policy:      topology.Spec.ImagePull.Policy,
		PullSecrets: slices.Clone(topology.Spec.ImagePull.PullSecrets),
	}

	if !reflectValueIsZero(imagePull) {
		spec.ImagePull = imagePull
	}

	defaultResources, hasDefaultResources := topology.Spec.Deployment.
		Resources[clabernetesconstants.Default]
	if hasDefaultResources {
		spec.Resources = defaultResources.DeepCopy()
	}

	if topology.Spec.Deployment.Scheduling.NodeSelector != nil ||
		topology.Spec.Deployment.Scheduling.Tolerations != nil ||
		topology.Spec.Deployment.Scheduling.Affinity != nil {
		spec.Scheduling = topology.Spec.Deployment.Scheduling.DeepCopy()
	}

	deployment := &clabernetesapisv1alpha1.NodeProfileDeployment{}

	if topology.Spec.Deployment.Persistence.Enabled {
		deployment.Persistence = topology.Spec.Deployment.Persistence.DeepCopy()
	}

	if !reflectValueIsZero(deployment) {
		spec.Deployment = deployment
	}

	spec.StatusProbes = topology.Spec.StatusProbes.DeepCopy()

	if compiled.Mgmt != nil {
		spec.Mgmt = &clabernetesapisv1alpha1.ManagementPolicy{
			IPv4Subnet: compiled.Mgmt.IPv4Subnet,
			IPv4Gw:     compiled.Mgmt.IPv4Gw,
			IPv4Range:  compiled.Mgmt.IPv4Range,
			IPv6Subnet: compiled.Mgmt.IPv6Subnet,
			IPv6Gw:     compiled.Mgmt.IPv6Gw,
			IPv6Range:  compiled.Mgmt.IPv6Range,
		}
	}

	profile := &clabernetesapisv1alpha1.NodeProfile{
		ObjectMeta: topologyOwnedObjectMetadata(
			topology,
			topology.GetName(),
			configManagerGetter,
		),
		Spec: spec,
	}

	return profile
}

// renderPerNodeNodeProfile creates a complete policy profile for a Node with a distinct
// resource override. NodeProfiles do not inherit from each other, so the shared policy is
// copied before replacing Resources.
func renderPerNodeNodeProfile(
	topology *clabernetesapisv1alpha1.Topology,
	compiled *CompiledTopology,
	nodeName string,
	configManagerGetter clabernetesconfig.ManagerGetterFunc,
) *clabernetesapisv1alpha1.NodeProfile {
	profile := renderTopologyNodeProfile(topology, compiled, configManagerGetter)
	profile.ObjectMeta = topologyOwnedObjectMetadata(
		topology,
		clabernetesutilkubernetes.SafeConcatNameKubernetes(topology.GetName(), nodeName),
		configManagerGetter,
	)

	if nodeResources, ok := topology.Spec.Deployment.Resources[nodeName]; ok {
		profile.Spec.Resources = nodeResources.DeepCopy()
	}

	return profile
}

// reflectValueIsZero returns true when the struct behind the pointer only holds zero values --
// used to skip emitting empty policy blocks.
func reflectValueIsZero(value any) bool {
	switch typed := value.(type) {
	case *clabernetesapisv1alpha1.NodeProfileImagePull:
		return imagePullIsZero(typed)
	case *clabernetesapisv1alpha1.NodeProfileDeployment:
		return deploymentIsZero(typed)
	default:
		return false
	}
}

func imagePullIsZero(imagePull *clabernetesapisv1alpha1.NodeProfileImagePull) bool {
	return imagePull.Policy == "" && imagePull.PullSecrets == nil
}

func deploymentIsZero(deployment *clabernetesapisv1alpha1.NodeProfileDeployment) bool {
	return deployment.Persistence == nil
}
