package topology

import (
	"fmt"
	"maps"
	"strings"

	clabernetesapis "github.com/clabernetes/clabernetes/apis"
	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	claberneteserrors "github.com/clabernetes/clabernetes/errors"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
	"gopkg.in/yaml.v3"
	k8svalidation "k8s.io/apimachinery/pkg/util/validation"
)

// compileContainerlabDefinition compiles a containerlab topology definition: every node gets
// the topology defaults and its kind expanded *into* its definition (so the emitted Node
// objects are self contained), and the links section becomes the compiled wire list.
func compileContainerlabDefinition(
	logger claberneteslogging.Instance,
	definition string,
) (*CompiledTopology, error) {
	containerlabConfig, unknownFields, err := clabernetesutilcontainerlab.LoadContainerlabConfig(
		definition,
	)
	if err != nil {
		logger.Criticalf("failed parsing containerlab config, error: %s", err)

		return nil, err
	}

	for _, unknownField := range unknownFields {
		logger.Warn(unknownField)
	}

	compiled := &CompiledTopology{
		Kind: clabernetesapis.TopologyKindContainerlab,
		Nodes: make(
			map[string]*clabernetesutilcontainerlab.NodeDefinition,
			len(containerlabConfig.Topology.Nodes),
		),
		Mgmt: containerlabConfig.Mgmt,
	}

	for nodeName := range containerlabConfig.Topology.Nodes {
		compiled.Nodes[nodeName], err = flattenNodeDefinition(
			containerlabConfig.Topology,
			nodeName,
		)
		if err != nil {
			return nil, err
		}

		normalizeNodePorts(logger, nodeName, compiled.Nodes[nodeName])
		dropUnusableNodeLabels(logger, nodeName, compiled.Nodes[nodeName])
	}

	compiled.Links, err = compileContainerlabLinks(containerlabConfig)
	if err != nil {
		logger.Criticalf("failed compiling containerlab links, error: %s", err)

		return nil, err
	}

	return compiled, nil
}

// normalizeNodePorts rewrites the docker style "host:container" port entries that pasted
// containerlab topologies carry into the destination-only form Nodes accept -- the pod side port
// is an allocation clabernetes owns, so a pinned host side cannot be honored.
func normalizeNodePorts(
	logger claberneteslogging.Instance,
	nodeName string,
	nodeDefinition *clabernetesutilcontainerlab.NodeDefinition,
) {
	for idx, portDefinition := range nodeDefinition.Ports {
		normalized := clabernetesutilcontainerlab.NormalizePortDefinition(portDefinition)
		if normalized == portDefinition {
			continue
		}

		logger.Warnf(
			"node %q port %q declares a host side port, which clabernetes allocates itself --"+
				" using destination port %q instead",
			nodeName,
			portDefinition,
			normalized,
		)

		nodeDefinition.Ports[idx] = normalized
	}
}

// dropUnusableNodeLabels removes containerlab node labels that cannot be carried onto the emitted
// Node's metadata. containerlab labels become docker labels, which accept far more than a
// kubernetes label does, so an unusable one has to be dropped here rather than making the Node
// rejected on create. clabernetes' own namespace and the individual keys its controllers reserve
// stay off limits too, since those labels carry meaning to reconciliation and selectors.
func dropUnusableNodeLabels(
	logger claberneteslogging.Instance,
	nodeName string,
	nodeDefinition *clabernetesutilcontainerlab.NodeDefinition,
) {
	for key, value := range nodeDefinition.Labels {
		var reason string

		switch {
		case isReservedNodeLabel(key):
			reason = "the label is reserved by clabernetes"
		default:
			problems := append(
				k8svalidation.IsQualifiedName(key),
				k8svalidation.IsValidLabelValue(value)...,
			)
			if len(problems) == 0 {
				continue
			}

			reason = strings.Join(problems, "; ")
		}

		logger.Warnf(
			"node %q label %q cannot become a kubernetes label and was omitted -- %s",
			nodeName,
			key,
			reason,
		)

		delete(nodeDefinition.Labels, key)
	}
}

func isReservedNodeLabel(key string) bool {
	if strings.HasPrefix(key, clabernetesconstants.Clabernetes+"/") {
		return true
	}

	switch key {
	case clabernetesconstants.LabelKubernetesName,
		clabernetesconstants.LabelApp,
		clabernetesconstants.LabelName,
		clabernetesconstants.LabelTopologyOwner,
		clabernetesconstants.LabelTopologyKind,
		clabernetesconstants.LabelTopologyNode:
		return true
	default:
		return false
	}
}

// flattenNodeDefinition merges the topology defaults, the node's kind, and the node's own
// definition into a single self contained node definition -- following containerlab's own
// inheritance rules: the most specific value wins for scalar fields, maps (env/sysctls) merge
// with the most specific entry winning, and binds/ports extend (defaults + kind + node).
func flattenNodeDefinition(
	topology *clabernetesutilcontainerlab.Topology,
	nodeName string,
) (*clabernetesutilcontainerlab.NodeDefinition, error) {
	flattened := &clabernetesutilcontainerlab.NodeDefinition{}

	nodeDefinition := topology.Nodes[nodeName]

	kindName := topology.Defaults.Kind
	if nodeDefinition.Kind != "" {
		kindName = nodeDefinition.Kind
	}

	// layer from least to most specific: defaults, kind, node
	layers := []*clabernetesutilcontainerlab.NodeDefinition{topology.Defaults}

	if kindDefinition, ok := topology.Kinds[kindName]; ok {
		layers = append(layers, kindDefinition)
	}

	layers = append(layers, nodeDefinition)

	for _, layer := range layers {
		if layer == nil {
			continue
		}

		err := overlayNodeDefinition(flattened, layer)
		if err != nil {
			return nil, err
		}
	}

	flattened.Kind = kindName

	return flattened, nil
}

// overlayNodeDefinition overlays the given layer onto the base node definition. Rather than
// hand-writing (and hand-maintaining) per-field merge logic over the whole containerlab
// vocabulary, the layer is marshaled and unmarshaled *onto* the base -- yaml unmarshal only
// touches fields present in the layer, which gives exactly the "most specific value wins"
// semantic for scalars and pointers. The map/extend style fields containerlab merges rather
// than replaces (env, sysctls, labels, binds, ports) are handled explicitly.
func overlayNodeDefinition(
	base,
	layer *clabernetesutilcontainerlab.NodeDefinition,
) error {
	mergedEnv := mergeMaps(base.Env, layer.Env)
	mergedSysctls := mergeMaps(base.Sysctls, layer.Sysctls)
	mergedLabels := mergeMaps(base.Labels, layer.Labels)
	mergedBinds := mergeSlices(base.Binds, layer.Binds)
	mergedPorts := mergeSlices(base.Ports, layer.Ports)

	layerYAML, err := yaml.Marshal(layer)
	if err != nil {
		return err
	}

	err = yaml.Unmarshal(layerYAML, base)
	if err != nil {
		return err
	}

	base.Env = mergedEnv
	base.Sysctls = mergedSysctls
	base.Labels = mergedLabels
	base.Binds = mergedBinds
	base.Ports = mergedPorts

	return nil
}

func mergeMaps(base, layer map[string]string) map[string]string {
	if len(base) == 0 && len(layer) == 0 {
		return nil
	}

	merged := make(map[string]string, len(base)+len(layer))

	maps.Copy(merged, base)
	maps.Copy(merged, layer)

	return merged
}

func mergeSlices(base, layer []string) []string {
	merged := make([]string, 0, len(base)+len(layer))
	seen := make(map[string]bool, len(base)+len(layer))

	for _, entry := range append(append([]string{}, base...), layer...) {
		if seen[entry] {
			continue
		}

		seen[entry] = true

		merged = append(merged, entry)
	}

	return merged
}

// compileContainerlabLinks converts the containerlab links section into compiled wires.
func compileContainerlabLinks(
	containerlabConfig *clabernetesutilcontainerlab.Config,
) ([]CompiledLink, error) {
	links := make([]CompiledLink, 0, len(containerlabConfig.Topology.Links))

	for _, link := range containerlabConfig.Topology.Links {
		if len(link.Endpoints) != clabernetesapisv1alpha1.LinkEndpointElementCount {
			return nil, fmt.Errorf(
				"%w: endpoint '%q' has wrong syntax, unexpected number of items",
				claberneteserrors.ErrParse,
				link.Endpoints,
			)
		}

		endpointAParts := strings.Split(link.Endpoints[0], ":")
		endpointBParts := strings.Split(link.Endpoints[1], ":")

		if len(endpointAParts) != clabernetesapisv1alpha1.LinkEndpointElementCount ||
			len(endpointBParts) != clabernetesapisv1alpha1.LinkEndpointElementCount {
			return nil, fmt.Errorf(
				"%w: endpoint '%q' has wrong syntax, bad node:interface config",
				claberneteserrors.ErrParse,
				link.Endpoints,
			)
		}

		links = append(links, CompiledLink{
			EndpointA: clabernetesapisv1alpha1.LinkEndpointSpec{
				NodeName:      endpointAParts[0],
				InterfaceName: endpointAParts[1],
			},
			EndpointB: clabernetesapisv1alpha1.LinkEndpointSpec{
				NodeName:      endpointBParts[0],
				InterfaceName: endpointBParts[1],
			},
			MTU: link.MTU,
		})
	}

	return links, nil
}
