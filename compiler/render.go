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

// kindSpecificComponentsKey is the one kind-specific config key c9s carries as a typed field
// instead of the generic passthrough: the component inventory drives c9s' own device rendering
// (chassis component DNS aliases), and the Node CRD exposes it as a validated, typed vocabulary.
const kindSpecificComponentsKey = "components"

// RenderAll renders the Node, Link, and NodeProfile objects for the compiled topology through
// the engine once and decodes every manifest into the typed clabernetes objects. Each list is
// sorted by name. The rendered node names are the containerlab node names, sanitized only where
// Kubernetes cannot carry them -- the namespace is the topology boundary. Node-keyed policy on
// the Topology is written against the definition's names, so it is looked up by the node's
// source name.
//
// The single engine pass is deliberate: every primitive list a reconcile enforces comes from one
// render, and the render is not free (management networks, expose services, profiles).
//
// A failure means the engine produced something the c9s vocabulary cannot carry, which is a
// contract violation rather than a user error, so it panics instead of silently dropping an
// object -- the same contract decodeManifestOrPanic carries.
func RenderAll(
	topology *clabernetesapisv1alpha1.Topology,
	compiled *CompiledTopology,
	configManagerGetter clabernetesconfig.ManagerGetterFunc,
) (
	nodes []*clabernetesapisv1alpha1.Node,
	links []*clabernetesapisv1alpha1.Link,
	profiles []*clabernetesapisv1alpha1.NodeProfile,
) {
	unstructuredNodes, unstructuredLinks, unstructuredProfiles, err := renderAll(
		topology,
		compiled,
		configManagerGetter,
	)
	if err != nil {
		panic(manifestContractError(err))
	}

	nodes = make([]*clabernetesapisv1alpha1.Node, 0, len(unstructuredNodes))

	for i := range unstructuredNodes {
		node := &clabernetesapisv1alpha1.Node{}
		decodeManifestOrPanic(unstructuredNodes[i].Object, node)

		nodes = append(nodes, node)
	}

	links = make([]*clabernetesapisv1alpha1.Link, 0, len(unstructuredLinks))

	for i := range unstructuredLinks {
		link := &clabernetesapisv1alpha1.Link{}
		decodeManifestOrPanic(unstructuredLinks[i].Object, link)

		links = append(links, link)
	}

	profiles = make([]*clabernetesapisv1alpha1.NodeProfile, 0, len(unstructuredProfiles))

	for i := range unstructuredProfiles {
		profile := &clabernetesapisv1alpha1.NodeProfile{}
		decodeManifestOrPanic(unstructuredProfiles[i].Object, profile)

		profiles = append(profiles, profile)
	}

	return nodes, links, profiles
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

	engineCompiled, err := engineCompiledTopology(compiled)
	if err != nil {
		return nil, nil, nil, err
	}

	return clabcompile.CompileTopology(input, engineCompiled)
}

// engineCompiledTopology maps the clabernetes compiled topology back onto the engine's shape so
// the engine's renderers can run against it. A failure is a vocabulary round-trip contract
// violation; renderAll's callers report it through the same contract path as manifest decoding.
func engineCompiledTopology(
	compiled *CompiledTopology,
) (*clabcompile.CompiledTopology, error) {
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
		exported, err := exportNodeDefinition(nodeDefinition)
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", nodeName, err)
		}

		out.Nodes[nodeName] = exported
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

	return out, nil
}

// exportNodeDefinition encodes one clabernetes node definition back into containerlab vocabulary
// for the engine's renderers. The vocabulary's marshaler emits the kind-specific config under
// its wrapper key; the engine's flattened definition carries the kind keys directly, so the
// wrapper is unwrapped here.
func exportNodeDefinition(
	nodeDefinition *clabernetesutilcontainerlab.NodeDefinition,
) (*clabtypes.NodeDefinition, error) {
	raw, err := yaml.Marshal(nodeDefinition)
	if err != nil {
		return nil, fmt.Errorf("exporting node definition: %w", err)
	}

	exported := &clabtypes.NodeDefinition{}

	err = yaml.Unmarshal(raw, exported)
	if err != nil {
		return nil, fmt.Errorf("importing exported node definition: %w", err)
	}

	wrapperKey := clabernetesutilcontainerlab.KindSpecificConfigWrapperKey

	if wrapper, wrapped := exported.KindSpecificConfig[wrapperKey]; wrapped {
		entries := clabernetesutilcontainerlab.KindSpecificConfigWrapperEntries(wrapper)
		if entries != nil {
			delete(exported.KindSpecificConfig, wrapperKey)
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

	return exported, nil
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
