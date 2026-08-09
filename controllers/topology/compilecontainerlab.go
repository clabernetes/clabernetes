package topology

import (
	"fmt"
	"maps"
	"strings"

	clabernetesapis "github.com/srl-labs/clabernetes/apis"
	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	claberneteserrors "github.com/srl-labs/clabernetes/errors"
	claberneteslogging "github.com/srl-labs/clabernetes/logging"
	clabernetesutilcontainerlab "github.com/srl-labs/clabernetes/util/containerlab"
	"gopkg.in/yaml.v3"
)

// compileContainerlabDefinition compiles a containerlab topology definition: every node gets
// the topology defaults and its kind expanded *into* its definition (so the emitted Node
// objects are self contained), and the links section becomes the compiled wire list.
func compileContainerlabDefinition(
	logger claberneteslogging.Instance,
	definition string,
) (*CompiledTopology, error) {
	containerlabConfig, err := clabernetesutilcontainerlab.LoadContainerlabConfig(definition)
	if err != nil {
		logger.Criticalf("failed parsing containerlab config, error: %s", err)

		return nil, err
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
	}

	compiled.Links, err = compileContainerlabLinks(containerlabConfig)
	if err != nil {
		logger.Criticalf("failed compiling containerlab links, error: %s", err)

		return nil, err
	}

	return compiled, nil
}

// flattenNodeDefinition merges the topology defaults, the node's kind, and the node's own
// definition into a single self contained node definition -- following containerlab's own
// inheritance rules: the most specific value wins for scalar fields, maps (env/labels/sysctls)
// merge with the most specific entry winning, and binds/ports extend (defaults + kind + node).
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
// than replaces (env, labels, sysctls, binds, ports) are handled explicitly.
func overlayNodeDefinition(
	base,
	layer *clabernetesutilcontainerlab.NodeDefinition,
) error {
	mergedEnv := mergeMaps(base.Env, layer.Env)
	mergedLabels := mergeMaps(base.Labels, layer.Labels)
	mergedSysctls := mergeMaps(base.Sysctls, layer.Sysctls)
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
	base.Labels = mergedLabels
	base.Sysctls = mergedSysctls
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
