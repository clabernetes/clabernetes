package topology

import (
	"crypto/sha256"
	"fmt"
	"maps"
	"regexp"
	"sort"
	"strings"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/srl-labs/clabernetes/config"
	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	clabernetesutil "github.com/srl-labs/clabernetes/util"
	clabernetesutilkubernetes "github.com/srl-labs/clabernetes/util/kubernetes"
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
		node := &clabernetesapisv1alpha1.Node{
			ObjectMeta: topologyOwnedObjectMetadata(topology, nodeName, configManagerGetter),
			Spec: clabernetesapisv1alpha1.NodeSpec{
				NodeDefinition: *nodeDefinition.DeepCopy(),
				FilesFromURL:   topology.Spec.Deployment.FilesFromURL[nodeName],
			},
		}

		// the per-node label is what per-node profiles (and humans) select on
		node.Labels[clabernetesconstants.LabelTopologyNode] = nodeName

		nodes = append(nodes, node)
	}

	sort.Slice(nodes, func(i, j int) bool { return nodes[i].GetName() < nodes[j].GetName() })

	return nodes
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

// RenderNodeProfiles renders the NodeProfile objects compiling the topology's deployment policy
// knobs: one topology wide profile (named after the topology, selecting the topology owner
// label), plus one per-node profile for every node that has per-node resources or files from
// configmap configured (selecting the topology node label, at a higher priority).
func RenderNodeProfiles(
	topology *clabernetesapisv1alpha1.Topology,
	compiled *CompiledTopology,
	configManagerGetter clabernetesconfig.ManagerGetterFunc,
) []*clabernetesapisv1alpha1.NodeProfile {
	perNodeNames := make([]string, 0)

	for nodeName := range topology.Spec.Deployment.Resources {
		if nodeName == clabernetesconstants.Default {
			continue
		}

		perNodeNames = append(perNodeNames, nodeName)
	}

	for nodeName := range topology.Spec.Deployment.FilesFromConfigMap {
		if _, alreadyListed := topology.Spec.Deployment.Resources[nodeName]; alreadyListed {
			continue
		}

		perNodeNames = append(perNodeNames, nodeName)
	}

	sort.Strings(perNodeNames)

	profiles := make([]*clabernetesapisv1alpha1.NodeProfile, 0, 1+len(perNodeNames))
	profiles = append(profiles, renderTopologyProfile(topology, compiled, configManagerGetter))

	for _, nodeName := range perNodeNames {
		profiles = append(profiles, renderPerNodeProfile(topology, nodeName, configManagerGetter))
	}

	return profiles
}

// renderTopologyProfile compiles the topology-wide policy knobs into the topology's main
// profile. Only knobs actually set on the Topology are compiled in -- unset knobs stay unset on
// the profile too, deferring down the chain to the global config exactly as they did on the
// Topology.
func renderTopologyProfile(
	topology *clabernetesapisv1alpha1.Topology,
	compiled *CompiledTopology,
	configManagerGetter clabernetesconfig.ManagerGetterFunc,
) *clabernetesapisv1alpha1.NodeProfile {
	spec := clabernetesapisv1alpha1.NodeProfileSpec{
		NodeSelector: metav1.LabelSelector{
			MatchLabels: map[string]string{
				clabernetesconstants.LabelTopologyOwner: topology.GetName(),
			},
		},
		Expose: &clabernetesapisv1alpha1.NodeProfileExpose{
			DisableExpose: clabernetesutil.ToPointer(topology.Spec.Expose.DisableExpose),
			DisableAutoExpose: clabernetesutil.ToPointer(
				topology.Spec.Expose.DisableAutoExpose,
			),
			ExposeType: topology.Spec.Expose.ExposeType,
			UseNodeMgmtIpv4Address: clabernetesutil.ToPointer(
				topology.Spec.Expose.UseNodeMgmtIpv4Address,
			),
			UseNodeMgmtIpv6Address: clabernetesutil.ToPointer(
				topology.Spec.Expose.UseNodeMgmtIpv6Address,
			),
		},
		Connectivity: topology.Spec.Connectivity,
	}

	imagePull := &clabernetesapisv1alpha1.NodeProfileImagePull{
		InsecureRegistries:  topology.Spec.ImagePull.InsecureRegistries,
		PullThroughOverride: topology.Spec.ImagePull.PullThroughOverride,
		PullSecrets:         topology.Spec.ImagePull.PullSecrets,
		DockerDaemonConfig:  topology.Spec.ImagePull.DockerDaemonConfig,
		DockerConfig:        topology.Spec.ImagePull.DockerConfig,
	}

	if !reflectValueIsZero(imagePull) {
		spec.ImagePull = imagePull
	}

	defaultResources, hasDefaultResources := topology.Spec.Deployment.
		Resources[clabernetesconstants.Default]
	if hasDefaultResources {
		spec.Resources = defaultResources.DeepCopy()
	}

	if len(topology.Spec.Deployment.Scheduling.NodeSelector) != 0 ||
		topology.Spec.Deployment.Scheduling.Tolerations != nil {
		spec.Scheduling = topology.Spec.Deployment.Scheduling.DeepCopy()
	}

	deployment := &clabernetesapisv1alpha1.NodeProfileDeployment{
		PrivilegedLauncher:      topology.Spec.Deployment.PrivilegedLauncher,
		ContainerlabDebug:       topology.Spec.Deployment.ContainerlabDebug,
		ContainerlabTimeout:     topology.Spec.Deployment.ContainerlabTimeout,
		ContainerlabVersion:     topology.Spec.Deployment.ContainerlabVersion,
		LauncherImage:           topology.Spec.Deployment.LauncherImage,
		LauncherImagePullPolicy: topology.Spec.Deployment.LauncherImagePullPolicy,
		LauncherLogLevel:        topology.Spec.Deployment.LauncherLogLevel,
		ExtraEnv:                topology.Spec.Deployment.ExtraEnv,
	}

	if topology.Spec.Deployment.Persistence.Enabled {
		deployment.Persistence = topology.Spec.Deployment.Persistence.DeepCopy()
	}

	if !reflectValueIsZero(deployment) {
		spec.Deployment = deployment
	}

	spec.StatusProbes = topology.Spec.StatusProbes.DeepCopy()

	if compiled.Mgmt != nil {
		spec.Mgmt = compiled.Mgmt.DeepCopy()
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

// renderPerNodeProfile compiles per-node deployment knobs (per node resources / files from
// configmap) into a higher priority profile selecting exactly that node.
func renderPerNodeProfile(
	topology *clabernetesapisv1alpha1.Topology,
	nodeName string,
	configManagerGetter clabernetesconfig.ManagerGetterFunc,
) *clabernetesapisv1alpha1.NodeProfile {
	profile := &clabernetesapisv1alpha1.NodeProfile{
		ObjectMeta: topologyOwnedObjectMetadata(
			topology,
			clabernetesutilkubernetes.SafeConcatNameKubernetes(topology.GetName(), nodeName),
			configManagerGetter,
		),
		Spec: clabernetesapisv1alpha1.NodeProfileSpec{
			NodeSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					clabernetesconstants.LabelTopologyOwner: topology.GetName(),
					clabernetesconstants.LabelTopologyNode:  nodeName,
				},
			},
			// beat the topology wide profile per field
			Priority: 1,
		},
	}

	if nodeResources, ok := topology.Spec.Deployment.Resources[nodeName]; ok {
		profile.Spec.Resources = nodeResources.DeepCopy()
	}

	if files, ok := topology.Spec.Deployment.FilesFromConfigMap[nodeName]; ok {
		profile.Spec.Deployment = &clabernetesapisv1alpha1.NodeProfileDeployment{
			FilesFromConfigMap: files,
		}
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
	return imagePull.InsecureRegistries == nil && imagePull.PullThroughOverride == "" &&
		imagePull.PullSecrets == nil && imagePull.DockerDaemonConfig == "" &&
		imagePull.DockerConfig == ""
}

func deploymentIsZero(deployment *clabernetesapisv1alpha1.NodeProfileDeployment) bool {
	return deployment.PrivilegedLauncher == nil && deployment.Persistence == nil &&
		deployment.ContainerlabDebug == nil && deployment.ContainerlabTimeout == "" &&
		deployment.ContainerlabVersion == "" && deployment.LauncherImage == "" &&
		deployment.LauncherImagePullPolicy == "" && deployment.LauncherLogLevel == "" &&
		deployment.ExtraEnv == nil && deployment.FilesFromConfigMap == nil
}
