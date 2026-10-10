package compiler

import (
	"fmt"
	"maps"
	"slices"

	clabernetesapis "github.com/clabernetes/clabernetes/apis"
	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	claberneteserrors "github.com/clabernetes/clabernetes/errors"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
	clabcompile "github.com/srl-labs/containerlab/labruntime/clabernetes/compile"
	clabtypes "github.com/srl-labs/containerlab/types"
	"gopkg.in/yaml.v3"
	k8scorev1 "k8s.io/api/core/v1"
)

// Diagnostic describes one source construct that c9s cannot faithfully preserve. It is the
// clabernetes view of the compile engine's diagnostic; the compiler converts engine diagnostics
// into this type so callers keep seeing the structured errors they always have.
type Diagnostic = clabcompile.Diagnostic

// UnsupportedFieldPolicy is retained for callers that explicitly requested strict compilation.
// Error is the only supported policy; Topology compilation no longer has a warning mode.
type UnsupportedFieldPolicy = clabcompile.UnsupportedFieldPolicy

const (
	// UnsupportedFieldPolicyError rejects every source field c9s cannot preserve.
	UnsupportedFieldPolicyError = clabcompile.UnsupportedFieldPolicyError
)

// CompileOptions is retained for strict external compiler callers.
type CompileOptions = clabcompile.Options

// UnsupportedFeaturesError reports all unsupported source constructs found in one compile pass.
type UnsupportedFeaturesError = clabcompile.UnsupportedFeaturesError

// CompiledLink holds a single wire of a compiled topology definition -- exactly the payload of
// a Link spec.
type CompiledLink struct {
	// EndpointA is the "a" side of the wire.
	EndpointA clabernetesapisv1alpha1.LinkEndpointSpec
	// EndpointB is the "b" side of the wire.
	EndpointB clabernetesapisv1alpha1.LinkEndpointSpec
	// MTU is the mtu of the wire (zero means unset).
	MTU int
}

// CompiledTopology is what a Topology definition compiles down to: flat, self contained node
// definitions (topology defaults/kinds expanded into every node), the wires between them, and
// the topology level management network settings. The compiler emits this as Node and Link
// objects (plus NodeProfiles for deployment policy) -- all actual reconciliation
// happens in the node/link controllers, identically for compiled and hand written objects.
//
// The compile itself lives in containerlab's compile engine; this type hydrates the engine's
// result into the clabernetes vocabulary.
type CompiledTopology struct {
	// Kind is the topology definition kind -- containerlab.
	Kind string
	// Nodes maps (containerlab) node name to its flattened node definition.
	Nodes map[string]*clabernetesutilcontainerlab.NodeDefinition
	// AppProtocols maps node names to c9s-specific application-protocol intent consumed from the
	// flattened containerlab labels. It stays outside Nodes so NodeDefinition remains containerlab
	// vocabulary.
	AppProtocols map[string][]clabernetesapisv1alpha1.NodeAppProtocol
	// Links holds the wires of the topology.
	Links []CompiledLink
	// Mgmt holds the containerlab management network settings (if any).
	Mgmt *clabernetesutilcontainerlab.MgmtNet
	// NodeNameSources maps the name of every node that had to be renamed for Kubernetes back to
	// the name the definition uses. Node-keyed policy on the Topology (files, resources, probes)
	// is written against the definition's names, so the renderers translate through this.
	NodeNameSources map[string]string
}

// SourceNodeName returns the name the definition uses for a compiled node. It is the compiled name
// itself for every node Kubernetes could carry as written.
func (t *CompiledTopology) SourceNodeName(nodeName string) string {
	if sourceName, renamed := t.NodeNameSources[nodeName]; renamed {
		return sourceName
	}

	return nodeName
}

// CompileTopology parses and compiles the given Topology's definition.
func CompileTopology(
	logger claberneteslogging.Instance,
	topology *clabernetesapisv1alpha1.Topology,
) (*CompiledTopology, error) {
	if topology.Spec.Definition.Containerlab == "" {
		return nil, fmt.Errorf(
			"%w: topology definition must include a containerlab topology",
			claberneteserrors.ErrReconcile,
		)
	}

	return CompileTopologyWithOptions(logger, topology, CompileOptions{})
}

// CompileTopologyWithOptions is the entry point for external compiler callers, such as the
// containerlab c9s runtime. An omitted policy and Error both select the same fail-closed
// compiler.
func CompileTopologyWithOptions(
	logger claberneteslogging.Instance,
	topology *clabernetesapisv1alpha1.Topology,
	options CompileOptions,
) (*CompiledTopology, error) {
	input := compileEngineInput(topology)

	compiled, err := clabcompile.CompileWithOptions(
		clabLoggingInstance{Instance: logger},
		input,
		options,
	)
	if err != nil {
		return nil, err
	}

	return hydrateCompiledTopology(compiled)
}

// GetTopologyKind returns the "kind" of topology this CR represents.
func GetTopologyKind(_ *clabernetesapisv1alpha1.Topology) string {
	return clabernetesapis.TopologyKindContainerlab
}

// clabLoggingInstance adapts a clabernetes logging instance to the compile engine's minimal
// logger.
type clabLoggingInstance struct {
	Instance claberneteslogging.Instance
}

func (l clabLoggingInstance) Debugf(format string, args ...any) {
	l.Instance.Debugf(format, args...)
}

func (l clabLoggingInstance) Warnf(format string, args ...any) {
	l.Instance.Warnf(format, args...)
}

// Errorf maps the engine's error logging onto the clabernetes logger's critical level, which is
// the severity the clabernetes logger carries where containerlab's logger carries error.
func (l clabLoggingInstance) Errorf(format string, args ...any) {
	l.Instance.Criticalf(format, args...)
}

// compileEngineInput maps the clabernetes Topology onto the compile engine's input contract: the
// rendered definition plus the deployment policy fields the engine consumes.
func compileEngineInput(topology *clabernetesapisv1alpha1.Topology) *clabcompile.Input {
	spec := topology.Spec

	input := &clabcompile.Input{
		Name:              topology.GetName(),
		Namespace:         topology.GetNamespace(),
		Definition:        spec.Definition.Containerlab,
		Deployment:        compileDeploymentPolicy(&spec.Deployment),
		DisableManagement: spec.DisableManagement,
		Expose: clabcompile.Expose{
			DisableAutoExpose:      spec.Expose.DisableAutoExpose,
			ExposeType:             spec.Expose.ExposeType,
			UseNodeMgmtIpv4Address: spec.Expose.UseNodeMgmtIpv4Address,
			UseNodeMgmtIpv6Address: spec.Expose.UseNodeMgmtIpv6Address,
		},
		ImagePull: clabcompile.ImagePull{
			Policy:      spec.ImagePull.Policy,
			PullSecrets: spec.ImagePull.PullSecrets,
		},
		StatusProbes: compileStatusProbes(&spec.StatusProbes),
	}

	return input
}

// compileDeploymentPolicy converts the clabernetes deployment policy into the engine's shapes.
// The two shapes are field-for-field identical; the conversion exists because the CRD types and
// the engine contract are separate Go types.
func compileDeploymentPolicy(
	deployment *clabernetesapisv1alpha1.Deployment,
) clabcompile.Deployment {
	out := clabcompile.Deployment{
		Resources:          perNodeResources(deployment.Resources),
		Scheduling:         compileScheduling(&deployment.Scheduling),
		Persistence:        compilePersistence(&deployment.Persistence),
		FilesFromConfigMap: map[string][]clabcompile.FileFromConfigMap{},
		FilesFromSecret:    map[string][]clabcompile.FileFromSecret{},
		FilesFromURL:       map[string][]clabcompile.FileFromURL{},
	}

	for nodeName, files := range deployment.FilesFromConfigMap {
		converted := make([]clabcompile.FileFromConfigMap, 0, len(files))
		for _, file := range files {
			converted = append(converted, clabcompile.FileFromConfigMap{
				FilePath:      file.FilePath,
				ConfigMapName: file.ConfigMapName,
				ConfigMapPath: file.ConfigMapPath,
				Mode:          file.Mode,
			})
		}

		out.FilesFromConfigMap[nodeName] = converted
	}

	for nodeName, files := range deployment.FilesFromSecret {
		converted := make([]clabcompile.FileFromSecret, 0, len(files))
		for _, file := range files {
			converted = append(converted, clabcompile.FileFromSecret{
				FilePath:   file.FilePath,
				SecretName: file.SecretName,
				SecretPath: file.SecretPath,
				Mode:       file.Mode,
			})
		}

		out.FilesFromSecret[nodeName] = converted
	}

	for nodeName, files := range deployment.FilesFromURL {
		converted := make([]clabcompile.FileFromURL, 0, len(files))
		for _, file := range files {
			converted = append(converted, clabcompile.FileFromURL{
				FilePath: file.FilePath,
				URL:      file.URL,
				Digest:   file.Digest,
			})
		}

		out.FilesFromURL[nodeName] = converted
	}

	return out
}

func compileScheduling(scheduling *clabernetesapisv1alpha1.Scheduling) *clabcompile.Scheduling {
	return &clabcompile.Scheduling{
		NodeSelector: maps.Clone(scheduling.NodeSelector),
		Tolerations:  slices.Clone(scheduling.Tolerations),
		Affinity:     scheduling.Affinity,
	}
}

func compilePersistence(persistence *clabernetesapisv1alpha1.Persistence) *clabcompile.Persistence {
	return &clabcompile.Persistence{
		Enabled:          persistence.Enabled,
		ClaimSize:        persistence.ClaimSize,
		StorageClassName: persistence.StorageClassName,
		Reclaim:          persistence.Reclaim,
	}
}

func compileStatusProbes(
	statusProbes *clabernetesapisv1alpha1.StatusProbes,
) clabcompile.StatusProbes {
	nodeConfigurations := make(
		map[string]clabcompile.ProbeConfiguration,
		len(statusProbes.NodeProbeConfigurations),
	)

	for nodeName, probeConfiguration := range statusProbes.NodeProbeConfigurations {
		nodeConfigurations[nodeName] = compileProbeConfiguration(probeConfiguration)
	}

	return clabcompile.StatusProbes{
		Enabled:                 statusProbes.Enabled,
		ExcludedNodes:           slices.Clone(statusProbes.ExcludedNodes),
		NodeProbeConfigurations: nodeConfigurations,
		ProbeConfiguration:      compileProbeConfiguration(statusProbes.ProbeConfiguration),
	}
}

func compileProbeConfiguration(
	probeConfiguration clabernetesapisv1alpha1.ProbeConfiguration,
) clabcompile.ProbeConfiguration {
	out := clabcompile.ProbeConfiguration{
		StartupSeconds: probeConfiguration.StartupSeconds,
	}

	if ssh := probeConfiguration.SSHProbeConfiguration; ssh != nil {
		out.SSHProbeConfiguration = &clabcompile.SSHProbeConfiguration{
			Username: ssh.Username,
			Password: ssh.Password,
			Port:     ssh.Port,
		}
	}

	if tcp := probeConfiguration.TCPProbeConfiguration; tcp != nil {
		out.TCPProbeConfiguration = &clabcompile.TCPProbeConfiguration{
			Port: tcp.Port,
		}
	}

	return out
}

func perNodeResources(
	resources map[string]k8scorev1.ResourceRequirements,
) map[string]*k8scorev1.ResourceRequirements {
	perNode := make(map[string]*k8scorev1.ResourceRequirements, len(resources))

	for nodeName, resourceRequirements := range resources {
		requirements := resourceRequirements

		perNode[nodeName] = &requirements
	}

	return perNode
}

// hydrateCompiledTopology converts the engine's compile result into the clabernetes vocabulary:
// flattened containerlab definitions decode into the c9s Node vocabulary (unknown keys land in
// its kind-specific config, exactly as a definition written by hand would), and the engine's
// endpoints become clabernetes link endpoints.
func hydrateCompiledTopology(
	compiled *clabcompile.CompiledTopology,
) (*CompiledTopology, error) {
	out := &CompiledTopology{
		Kind: compiled.Kind,
		Nodes: make(
			map[string]*clabernetesutilcontainerlab.NodeDefinition,
			len(compiled.Nodes),
		),
		AppProtocols: make(
			map[string][]clabernetesapisv1alpha1.NodeAppProtocol,
			len(compiled.AppProtocols),
		),
		Links:           make([]CompiledLink, 0, len(compiled.Links)),
		Mgmt:            compiled.Mgmt,
		NodeNameSources: maps.Clone(compiled.NodeNameSources),
	}

	for nodeName, nodeDefinition := range compiled.Nodes {
		imported, err := importNodeDefinition(nodeDefinition)
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", nodeName, err)
		}

		out.Nodes[nodeName] = imported
	}

	for nodeName, appProtocols := range compiled.AppProtocols {
		protocols := make([]clabernetesapisv1alpha1.NodeAppProtocol, 0, len(appProtocols))
		for _, appProtocol := range appProtocols {
			protocols = append(protocols, clabernetesapisv1alpha1.NodeAppProtocol{
				Port:        appProtocol.Port,
				AppProtocol: appProtocol.AppProtocol,
			})
		}

		out.AppProtocols[nodeName] = protocols
	}

	for _, compiledLink := range compiled.Links {
		out.Links = append(out.Links, CompiledLink{
			EndpointA: clabernetesapisv1alpha1.LinkEndpointSpec{
				NodeName:      compiledLink.EndpointA.NodeName,
				InterfaceName: compiledLink.EndpointA.InterfaceName,
			},
			EndpointB: clabernetesapisv1alpha1.LinkEndpointSpec{
				NodeName:      compiledLink.EndpointB.NodeName,
				InterfaceName: compiledLink.EndpointB.InterfaceName,
			},
			MTU: compiledLink.MTU,
		})
	}

	return out, nil
}

// importNodeDefinition decodes one flattened containerlab node definition into the clabernetes
// Node vocabulary. The engine's definition payload already carries the vocabulary's named fields
// with the kind-specific config wrapped under its own key, so the vocabulary's unmarshaler is
// the single conversion step; it absorbs nothing unexpected because the engine has already
// rejected the fields the vocabulary cannot carry.
func importNodeDefinition(
	flattened *clabtypes.NodeDefinition,
) (*clabernetesutilcontainerlab.NodeDefinition, error) {
	payload, err := clabcompile.DefinitionPayload(flattened)
	if err != nil {
		return nil, err
	}

	raw, err := yaml.Marshal(payload)
	if err != nil {
		return nil, err
	}

	imported := &clabernetesutilcontainerlab.NodeDefinition{}

	err = yaml.Unmarshal(raw, imported)
	if err != nil {
		return nil, err
	}

	return imported, nil
}
