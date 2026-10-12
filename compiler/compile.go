// Copyright 2020 Nokia
// Licensed under the BSD 3-Clause License.
// SPDX-License-Identifier: BSD-3-Clause

// Package compiler renders containerlab topologies into clabernetes (c9s) primitive resources:
// Node, Link, and NodeProfile manifests.
//
// The compiler is the clabernetes-owned contract: it takes a rendered containerlab topology
// definition plus the deployment policy the clabernetes Topology resource carries, and emits
// self contained primitive manifests that the c9s API server and controllers accept. Both the
// c9s controller and containerlab's direct c9s runtime consume this package, so the two compile
// identically; the contract lives here, in clabernetes.
package compiler

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	clabernetesapis "github.com/clabernetes/clabernetes/apis"
	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	claberneteserrors "github.com/clabernetes/clabernetes/errors"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
	clabtypes "github.com/srl-labs/containerlab/types"
	"gopkg.in/yaml.v3"
	k8scorev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Logger is the minimal logging surface the compiler needs. Both containerlab's charmbracelet
// logger and clabernetes' logging instances satisfy it.
type Logger interface {
	Debugf(format string, args ...any)
	Warnf(format string, args ...any)
	Errorf(format string, args ...any)
}

// Diagnostic describes one source construct the compile cannot faithfully preserve.
type Diagnostic struct {
	Code    string
	Path    string
	Line    int
	Message string
	// Warning marks a construct that is accepted with an adjusted or ignored meaning instead
	// of failing the compile: the source stays valid, and the diagnostic tells the author what
	// changed. Only constructs whose loss cannot silently change lab behavior may be warnings.
	Warning bool
}

// UnsupportedFieldPolicy is retained for callers that explicitly requested strict compilation.
// Error is the only supported policy; Topology compilation no longer has a warning mode.
type UnsupportedFieldPolicy string

const (
	// UnsupportedFieldPolicyError rejects every source field the compile cannot preserve.
	UnsupportedFieldPolicyError UnsupportedFieldPolicy = "error"
)

// Options are retained for strict external compiler callers.
type Options struct {
	UnsupportedFieldPolicy UnsupportedFieldPolicy
}

// CompileOptions is the clabernetes-level alias of the strict compiler options.
type CompileOptions = Options

// UnsupportedFeaturesError reports all unsupported source constructs found in one compile pass.
// Diagnostics are sorted so CLI errors and tests remain stable across map iteration order.
type UnsupportedFeaturesError struct {
	Diagnostics []Diagnostic
}

func (e *UnsupportedFeaturesError) Error() string {
	if e == nil || len(e.Diagnostics) == 0 {
		return "topology contains features unsupported by c9s"
	}

	parts := make([]string, 0, len(e.Diagnostics))
	for _, diagnostic := range e.Diagnostics {
		parts = append(parts, formatDiagnostic(diagnostic))
	}

	return "topology contains features unsupported by c9s: " + strings.Join(parts, "; ")
}

func formatDiagnostic(diagnostic Diagnostic) string {
	location := diagnostic.Path
	if location == "" {
		location = "topology"
	}

	if diagnostic.Line > 0 {
		location = fmt.Sprintf("%s (line %d)", location, diagnostic.Line)
	}

	return fmt.Sprintf("%s: %s", location, diagnostic.Message)
}

type compileDiagnostics struct {
	diagnostics []Diagnostic
}

func newCompileDiagnostics() *compileDiagnostics {
	return &compileDiagnostics{}
}

func (d *compileDiagnostics) add(diagnostic Diagnostic) {
	d.diagnostics = append(d.diagnostics, diagnostic)
}

func (d *compileDiagnostics) warnings() []Diagnostic {
	warnings := []Diagnostic(nil)

	for _, diagnostic := range d.diagnostics {
		if diagnostic.Warning {
			warnings = append(warnings, diagnostic)
		}
	}

	return warnings
}

func (d *compileDiagnostics) err() error {
	diagnostics := []Diagnostic(nil)

	for _, diagnostic := range d.diagnostics {
		if !diagnostic.Warning {
			diagnostics = append(diagnostics, diagnostic)
		}
	}

	if len(diagnostics) == 0 {
		return nil
	}

	sort.SliceStable(diagnostics, func(i, j int) bool {
		if diagnostics[i].Path != diagnostics[j].Path {
			return diagnostics[i].Path < diagnostics[j].Path
		}

		if diagnostics[i].Line != diagnostics[j].Line {
			return diagnostics[i].Line < diagnostics[j].Line
		}

		if diagnostics[i].Code != diagnostics[j].Code {
			return diagnostics[i].Code < diagnostics[j].Code
		}

		return diagnostics[i].Message < diagnostics[j].Message
	})

	return &UnsupportedFeaturesError{Diagnostics: diagnostics}
}

// CompiledLink holds a single wire of a compiled topology definition -- exactly the payload of
// a Link spec.
type CompiledLink struct {
	// EndpointA is the "a" side of the wire.
	EndpointA Endpoint
	// EndpointB is the "b" side of the wire.
	EndpointB Endpoint
	// MTU is the mtu of the wire (zero means unset).
	MTU int
}

// Endpoint identifies one side of a compiled wire.
type Endpoint struct {
	// NodeName is the node the endpoint sits on, or LinkHostNodeName for a host link.
	NodeName string
	// InterfaceName is the interface name on the node.
	InterfaceName string
}

// AppProtocol is the application-protocol intent for one expose Service destination port.
type AppProtocol struct {
	// Port is the destination port and transport in canonical "<number>/tcp" or "<number>/udp"
	// form.
	Port string
	// AppProtocol is a Kubernetes qualified name, or empty to suppress a built-in hint.
	AppProtocol string
}

// CompiledTopology is what a Topology definition compiles down to: flat, self contained node
// definitions (topology defaults/kinds expanded into every node), the wires between them, and
// the topology level management network settings. The compiler emits this as Node and Link
// objects (plus NodeProfiles for deployment policy) -- all actual reconciliation
// happens in the node/link controllers, identically for compiled and hand written objects.
//
// The compile itself is clabernetes-owned; this type carries the result hydrated into the
// clabernetes vocabulary: flattened containerlab definitions decode into the c9s Node vocabulary
// (unknown keys land in its kind-specific config, exactly as a definition written by hand would).
type CompiledTopology struct {
	// Kind is the topology definition kind -- containerlab.
	Kind string
	// Nodes maps (containerlab) node name to its flattened node definition.
	Nodes map[string]*clabernetesutilcontainerlab.NodeDefinition
	// AppProtocols maps node names to c9s-specific application-protocol intent consumed from the
	// flattened containerlab labels. It stays outside Nodes so NodeDefinition remains containerlab
	// vocabulary.
	AppProtocols map[string][]AppProtocol
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

// CompiledDefinition is the compiled containerlab definition: the engine-shaped result the
// compile produces before the clabernetes vocabulary hydrates it. Nodes carry containerlab's
// own definition vocabulary; the vocabulary round trip happens at the hydration boundary.
type CompiledDefinition struct {
	// Kind is the topology definition kind -- containerlab.
	Kind string
	// Nodes maps (containerlab) node name to its flattened node definition.
	Nodes map[string]*clabtypes.NodeDefinition
	// AppProtocols maps node names to c9s-specific application-protocol intent consumed from the
	// flattened containerlab labels. It stays outside Nodes so NodeDefinition remains containerlab
	// vocabulary.
	AppProtocols map[string][]AppProtocol
	// Links holds the wires of the topology.
	Links []CompiledLink
	// Mgmt holds the containerlab management network settings (if any).
	Mgmt *clabtypes.MgmtNet
	// NodeNameSources maps the name of every node that had to be renamed for Kubernetes back to
	// the name the definition uses. Node-keyed policy on the Topology (files, resources, probes)
	// is written against the definition's names, so the renderers translate through this.
	NodeNameSources map[string]string
}

// SourceNodeName returns the name the definition uses for a compiled node. It is the compiled name
// itself for every node Kubernetes could carry as written.
func (t *CompiledDefinition) SourceNodeName(nodeName string) string {
	if sourceName, renamed := t.NodeNameSources[nodeName]; renamed {
		return sourceName
	}

	return nodeName
}

// errUnsupportedFieldPolicy marks a strict compiler caller requesting an unknown field policy.
var errUnsupportedFieldPolicy = errors.New("unsupported topology compiler field policy")

// TopologyKindContainerlab is the kind marker the compiler reports for containerlab definitions.
const TopologyKindContainerlab = "containerlab"

// Compile parses and compiles the containerlab topology definition carried by the input into
// primitive manifests. The definition must be self contained in containerlab vocabulary; every
// emitted manifest carries the flattened node definition, so no topology defaults or kinds
// survive into the API.
func Compile(logger Logger, input *Input) (*CompiledDefinition, error) {
	if input.Definition == "" {
		return nil, fmt.Errorf("topology definition must include a containerlab topology")
	}

	return compileContainerlabDefinition(logger, input, newCompileDiagnostics())
}

// CompileWithOptions is the entry point for strict external compiler callers. An omitted policy
// and Error both select the same fail-closed compiler.
func CompileWithOptions(
	logger Logger,
	input *Input,
	options Options,
) (*CompiledDefinition, error) {
	if options.UnsupportedFieldPolicy != "" &&
		options.UnsupportedFieldPolicy != UnsupportedFieldPolicyError {
		return nil, fmt.Errorf(
			"%w %q; only %q is supported",
			errUnsupportedFieldPolicy,
			options.UnsupportedFieldPolicy,
			UnsupportedFieldPolicyError,
		)
	}

	return Compile(logger, input)
}

// RenderTopology renders the compiled definition into the primitive manifests the c9s API server
// accepts: NodeProfiles first, then Nodes, then Links, every object as unstructured.
func RenderTopology(
	input *Input,
	compiled *CompiledDefinition,
) (nodes, links, nodeProfiles []unstructured.Unstructured, err error) {
	return renderManifests(input, compiled)
}

// clabLoggingInstance adapts a clabernetes logging instance to the compiler's minimal logger.
type clabLoggingInstance struct {
	Instance claberneteslogging.Instance
}

func (l clabLoggingInstance) Debugf(format string, args ...any) {
	l.Instance.Debugf(format, args...)
}

func (l clabLoggingInstance) Warnf(format string, args ...any) {
	l.Instance.Warnf(format, args...)
}

// Errorf maps the compiler's error logging onto the clabernetes logger's critical level, which is
// the severity the clabernetes logger carries where containerlab's logger carries error.
func (l clabLoggingInstance) Errorf(format string, args ...any) {
	l.Instance.Criticalf(format, args...)
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

	compiled, err := CompileWithOptions(
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

// compileEngineInput maps the clabernetes Topology onto the compiler's input contract: the
// rendered definition plus the deployment policy fields the compiler consumes.
func compileEngineInput(topology *clabernetesapisv1alpha1.Topology) *Input {
	spec := topology.Spec

	input := &Input{
		Name:              topology.GetName(),
		Namespace:         topology.GetNamespace(),
		Definition:        spec.Definition.Containerlab,
		Deployment:        compileDeploymentPolicy(&spec.Deployment),
		DisableManagement: spec.DisableManagement,
		Expose: Expose{
			DisableAutoExpose:      spec.Expose.DisableAutoExpose,
			ExposeType:             spec.Expose.ExposeType,
			UseNodeMgmtIpv4Address: spec.Expose.UseNodeMgmtIpv4Address,
			UseNodeMgmtIpv6Address: spec.Expose.UseNodeMgmtIpv6Address,
		},
		ImagePull: ImagePull{
			Policy:      spec.ImagePull.Policy,
			PullSecrets: spec.ImagePull.PullSecrets,
		},
		StatusProbes: compileStatusProbes(&spec.StatusProbes),
	}

	return input
}

// compileDeploymentPolicy converts the clabernetes deployment policy into the compiler's shapes.
// The two shapes are field-for-field identical; the conversion exists because the CRD types and
// the compiler contract are separate Go types.
func compileDeploymentPolicy(
	deployment *clabernetesapisv1alpha1.Deployment,
) Deployment {
	out := Deployment{
		Resources:          perNodeResources(deployment.Resources),
		Scheduling:         compileScheduling(&deployment.Scheduling),
		Persistence:        compilePersistence(&deployment.Persistence),
		FilesFromConfigMap: map[string][]FileFromConfigMap{},
		FilesFromSecret:    map[string][]FileFromSecret{},
		FilesFromURL:       map[string][]FileFromURL{},
	}

	for nodeName, files := range deployment.FilesFromConfigMap {
		converted := make([]FileFromConfigMap, 0, len(files))
		for _, file := range files {
			converted = append(converted, FileFromConfigMap{
				FilePath:      file.FilePath,
				ConfigMapName: file.ConfigMapName,
				ConfigMapPath: file.ConfigMapPath,
				Mode:          file.Mode,
			})
		}

		out.FilesFromConfigMap[nodeName] = converted
	}

	for nodeName, files := range deployment.FilesFromSecret {
		converted := make([]FileFromSecret, 0, len(files))
		for _, file := range files {
			converted = append(converted, FileFromSecret{
				FilePath:   file.FilePath,
				SecretName: file.SecretName,
				SecretPath: file.SecretPath,
				Mode:       file.Mode,
			})
		}

		out.FilesFromSecret[nodeName] = converted
	}

	for nodeName, files := range deployment.FilesFromURL {
		converted := make([]FileFromURL, 0, len(files))
		for _, file := range files {
			converted = append(converted, FileFromURL{
				FilePath: file.FilePath,
				URL:      file.URL,
				Digest:   file.Digest,
			})
		}

		out.FilesFromURL[nodeName] = converted
	}

	return out
}

func compileScheduling(scheduling *clabernetesapisv1alpha1.Scheduling) *Scheduling {
	return &Scheduling{
		NodeSelector: maps.Clone(scheduling.NodeSelector),
		Tolerations:  slices.Clone(scheduling.Tolerations),
		Affinity:     scheduling.Affinity,
	}
}

func compilePersistence(persistence *clabernetesapisv1alpha1.Persistence) *Persistence {
	return &Persistence{
		Enabled:          persistence.Enabled,
		ClaimSize:        persistence.ClaimSize,
		StorageClassName: persistence.StorageClassName,
		Reclaim:          persistence.Reclaim,
	}
}

func compileStatusProbes(
	statusProbes *clabernetesapisv1alpha1.StatusProbes,
) StatusProbes {
	nodeConfigurations := make(
		map[string]ProbeConfiguration,
		len(statusProbes.NodeProbeConfigurations),
	)

	for nodeName, probeConfiguration := range statusProbes.NodeProbeConfigurations {
		nodeConfigurations[nodeName] = compileProbeConfiguration(probeConfiguration)
	}

	return StatusProbes{
		Enabled:                 statusProbes.Enabled,
		ExcludedNodes:           slices.Clone(statusProbes.ExcludedNodes),
		NodeProbeConfigurations: nodeConfigurations,
		ProbeConfiguration:      compileProbeConfiguration(statusProbes.ProbeConfiguration),
	}
}

func compileProbeConfiguration(
	probeConfiguration clabernetesapisv1alpha1.ProbeConfiguration,
) ProbeConfiguration {
	out := ProbeConfiguration{
		StartupSeconds: probeConfiguration.StartupSeconds,
	}

	if ssh := probeConfiguration.SSHProbeConfiguration; ssh != nil {
		out.SSHProbeConfiguration = &SSHProbeConfiguration{
			Username: ssh.Username,
			Password: ssh.Password,
			Port:     ssh.Port,
		}
	}

	if tcp := probeConfiguration.TCPProbeConfiguration; tcp != nil {
		out.TCPProbeConfiguration = &TCPProbeConfiguration{
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

// hydrateCompiledTopology converts the compiler's result into the clabernetes vocabulary:
// flattened containerlab definitions decode into the c9s Node vocabulary (unknown keys land in
// its kind-specific config, exactly as a definition written by hand would), and the compiled
// endpoints become clabernetes link endpoints.
func hydrateCompiledTopology(
	compiled *CompiledDefinition,
) (*CompiledTopology, error) {
	out := &CompiledTopology{
		Kind: compiled.Kind,
		Nodes: make(
			map[string]*clabernetesutilcontainerlab.NodeDefinition,
			len(compiled.Nodes),
		),
		AppProtocols:    compiled.AppProtocols,
		Links:           compiled.Links,
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

	return out, nil
}

// importNodeDefinition decodes one flattened containerlab node definition into the clabernetes
// Node vocabulary. The definition payload already carries the vocabulary's named fields with the
// kind-specific config wrapped under its own key, so the vocabulary's unmarshaler is the single
// conversion step; it absorbs nothing unexpected because the compiler has already rejected the
// fields the vocabulary cannot carry.
func importNodeDefinition(
	flattened *clabtypes.NodeDefinition,
) (*clabernetesutilcontainerlab.NodeDefinition, error) {
	payload, err := DefinitionPayload(flattened)
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
