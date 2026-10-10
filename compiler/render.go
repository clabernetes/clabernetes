package compiler

import (
	"fmt"
	"maps"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
	clabcompile "github.com/srl-labs/containerlab/labruntime/clabernetes/compile"
	clabtypes "github.com/srl-labs/containerlab/types"
	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
)

// kindSpecificWrapperKey is the yaml key c9s' Node vocabulary uses to carry merged kind-specific
// config. A definition may write it explicitly; the compiler unwraps it so the same key never
// appears as kind-owned config.
const kindSpecificWrapperKey = "kind-specific-config"

// kindSpecificComponentsKey is the one kind-specific config key c9s carries as a typed field
// instead of the generic passthrough: the component inventory drives c9s' own device rendering
// (chassis component DNS aliases), and the Node CRD exposes it as a validated, typed vocabulary.
const kindSpecificComponentsKey = "components"

// RenderNodes renders the Node objects for the compiled topology, sorted by name. The emitted
// node names are the containerlab node names, sanitized only where Kubernetes cannot carry them
// -- the namespace is the topology boundary. Node-keyed policy on the Topology is written against
// the definition's names, so it is looked up by the node's source name.
//
// The rendering itself lives in containerlab's compile engine; this function renders through the
// engine and decodes the unstructured manifests into the clabernetes types.
func RenderNodes(
	topology *clabernetesapisv1alpha1.Topology,
	compiled *CompiledTopology,
	configManagerGetter clabernetesconfig.ManagerGetterFunc,
) []*clabernetesapisv1alpha1.Node {
	nodes, _, _, err := renderAll(topology, compiled, configManagerGetter)
	if err != nil {
		panic(manifestContractError(err))
	}

	typedNodes := make([]*clabernetesapisv1alpha1.Node, 0, len(nodes))

	for i := range nodes {
		node := &clabernetesapisv1alpha1.Node{}
		decodeManifestOrPanic(nodes[i].Object, node)

		typedNodes = append(typedNodes, node)
	}

	return typedNodes
}

// RenderLinks renders the Link objects for the compiled topology, sorted by name.
func RenderLinks(
	topology *clabernetesapisv1alpha1.Topology,
	compiled *CompiledTopology,
	configManagerGetter clabernetesconfig.ManagerGetterFunc,
) []*clabernetesapisv1alpha1.Link {
	_, links, _, err := renderAll(topology, compiled, configManagerGetter)
	if err != nil {
		panic(manifestContractError(err))
	}

	typedLinks := make([]*clabernetesapisv1alpha1.Link, 0, len(links))

	for i := range links {
		link := &clabernetesapisv1alpha1.Link{}
		decodeManifestOrPanic(links[i].Object, link)

		typedLinks = append(typedLinks, link)
	}

	return typedLinks
}

// RenderNodeProfiles renders one shared topology NodeProfile plus a complete dedicated
// profile for each compiled Node with distinct profile policy.
func RenderNodeProfiles(
	topology *clabernetesapisv1alpha1.Topology,
	compiled *CompiledTopology,
	configManagerGetter clabernetesconfig.ManagerGetterFunc,
) []*clabernetesapisv1alpha1.NodeProfile {
	_, _, profiles, err := renderAll(topology, compiled, configManagerGetter)
	if err != nil {
		panic(manifestContractError(err))
	}

	typedProfiles := make([]*clabernetesapisv1alpha1.NodeProfile, 0, len(profiles))

	for i := range profiles {
		profile := &clabernetesapisv1alpha1.NodeProfile{}
		decodeManifestOrPanic(profiles[i].Object, profile)

		typedProfiles = append(typedProfiles, profile)
	}

	return typedProfiles
}

// manifestContractError marks an engine render failure as a contract violation: the engine
// produced something the c9s vocabulary cannot carry, which is a programming error rather than
// a user error.
func manifestContractError(err error) string {
	return fmt.Sprintf("clabernetes compiler: %v", err)
}

// renderAll renders the compiled topology through the engine once and returns every primitive
// manifest.
func renderAll(
	topology *clabernetesapisv1alpha1.Topology,
	compiled *CompiledTopology,
	configManagerGetter clabernetesconfig.ManagerGetterFunc,
) (nodes, links, nodeProfiles []unstructured.Unstructured, err error) {
	input := compileEngineInput(topology)

	if configManagerGetter != nil {
		annotations, globalLabels := configManagerGetter().GetAllMetadata()

		input.Annotations = annotations
		input.Labels = globalLabels
	}

	return clabcompile.CompileTopology(input, engineCompiledTopology(compiled))
}

// engineCompiledTopology maps the clabernetes compiled topology back onto the engine's shape so
// the engine's renderers can run against it.
func engineCompiledTopology(compiled *CompiledTopology) *clabcompile.CompiledTopology {
	out := &clabcompile.CompiledTopology{
		Kind: compiled.Kind,
		Nodes: make(
			map[string]*clabtypes.NodeDefinition,
			len(compiled.Nodes),
		),
		AppProtocols:    make(map[string][]clabcompile.AppProtocol, len(compiled.AppProtocols)),
		Links:           make([]clabcompile.CompiledLink, 0, len(compiled.Links)),
		Mgmt:            compiled.Mgmt,
		NodeNameSources: maps.Clone(compiled.NodeNameSources),
	}

	for nodeName, nodeDefinition := range compiled.Nodes {
		out.Nodes[nodeName] = exportNodeDefinition(nodeDefinition)
	}

	for nodeName, appProtocols := range compiled.AppProtocols {
		protocols := make([]clabcompile.AppProtocol, 0, len(appProtocols))
		for _, appProtocol := range appProtocols {
			protocols = append(protocols, clabcompile.AppProtocol{
				Port:        appProtocol.Port,
				AppProtocol: appProtocol.AppProtocol,
			})
		}

		out.AppProtocols[nodeName] = protocols
	}

	for _, compiledLink := range compiled.Links {
		out.Links = append(out.Links, clabcompile.CompiledLink{
			EndpointA: clabcompile.Endpoint{
				NodeName:      compiledLink.EndpointA.NodeName,
				InterfaceName: compiledLink.EndpointA.InterfaceName,
			},
			EndpointB: clabcompile.Endpoint{
				NodeName:      compiledLink.EndpointB.NodeName,
				InterfaceName: compiledLink.EndpointB.InterfaceName,
			},
			MTU: compiledLink.MTU,
		})
	}

	return out
}

// exportNodeDefinition encodes one clabernetes node definition back into containerlab vocabulary
// for the engine's renderers. The vocabulary's marshaler emits the kind-specific config under
// its wrapper key; the engine's flattened definition carries the kind keys directly, so the
// wrapper is unwrapped here.
func exportNodeDefinition(
	nodeDefinition *clabernetesutilcontainerlab.NodeDefinition,
) *clabtypes.NodeDefinition {
	exported := &clabtypes.NodeDefinition{}

	raw, err := yaml.Marshal(nodeDefinition)
	if err != nil {
		return exported
	}

	if err := yaml.Unmarshal(raw, exported); err != nil {
		return exported
	}

	if wrapper, wrapped := exported.KindSpecificConfig[kindSpecificWrapperKey]; wrapped {
		if entries := kindSpecificWrapperEntries(wrapper); entries != nil {
			delete(exported.KindSpecificConfig, kindSpecificWrapperKey)
			maps.Copy(exported.KindSpecificConfig, entries)
		}
	}

	// An explicitly cleared component inventory must survive the vocabulary round trip: the
	// vocabulary's marshaler omits an empty list, so the engine's kind map gets the explicit
	// clearing back directly.
	if nodeDefinition.Components != nil {
		if exported.KindSpecificConfig == nil {
			exported.KindSpecificConfig = map[string]any{}
		}

		if _, present := exported.KindSpecificConfig[kindSpecificComponentsKey]; !present {
			exported.KindSpecificConfig[kindSpecificComponentsKey] = []any{}
		}
	}

	return exported
}

// kindSpecificWrapperEntries flattens a kind-specific config wrapper mapping into per-key
// entries. The wrapper value arrives through either yaml unmarshaler, so both mapping shapes are
// accepted.
func kindSpecificWrapperEntries(value any) map[string]any {
	switch wrapper := value.(type) {
	case map[string]any:
		return wrapper
	case map[any]any:
		entries := make(map[string]any, len(wrapper))
		for key, entryValue := range wrapper {
			keyText, ok := key.(string)
			if !ok {
				return nil
			}

			entries[keyText] = entryValue
		}

		return entries
	default:
		return nil
	}
}

// decodeManifestOrPanic decodes an engine manifest into a typed clabernetes object. A failure
// means the engine produced a manifest the c9s vocabulary cannot carry, which is a contract
// violation rather than a user error, so it panics instead of silently dropping an object.
func decodeManifestOrPanic(object map[string]any, typed any) {
	err := apimachineryruntime.DefaultUnstructuredConverter.FromUnstructured(object, typed)
	if err != nil {
		panic(manifestContractError(fmt.Errorf("decoding compiled manifest: %w", err)))
	}
}
