package topology

import (
	"fmt"
	"sort"
	"strings"

	clabernetesapis "github.com/clabernetes/clabernetes/apis"
	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	claberneteserrors "github.com/clabernetes/clabernetes/errors"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
)

// UnsupportedFieldPolicy controls how the compiler handles source vocabulary that c9s can
// parse, but cannot preserve with the same semantics. The in-cluster compatibility controller
// uses Warn; callers promising a strict containerlab-compatible runtime use Error.
type UnsupportedFieldPolicy string

const (
	// UnsupportedFieldPolicyWarn preserves compatibility and logs lossy source fields.
	UnsupportedFieldPolicyWarn UnsupportedFieldPolicy = "warn"
	// UnsupportedFieldPolicyError rejects any source field c9s cannot preserve.
	UnsupportedFieldPolicyError UnsupportedFieldPolicy = "error"
)

// CompileOptions controls compatibility behavior while compiling a source Topology.
type CompileOptions struct {
	UnsupportedFieldPolicy UnsupportedFieldPolicy
}

// CompilerDiagnostic describes one source construct that c9s cannot faithfully preserve.
type CompilerDiagnostic struct {
	Code    string
	Path    string
	Line    int
	Message string
}

// UnsupportedFeaturesError reports all unsupported source constructs found in one compile pass.
// Diagnostics are sorted so CLI errors and tests remain stable across map iteration order.
type UnsupportedFeaturesError struct {
	Diagnostics []CompilerDiagnostic
}

func (e *UnsupportedFeaturesError) Error() string {
	if e == nil || len(e.Diagnostics) == 0 {
		return "topology contains features unsupported by c9s"
	}

	parts := make([]string, 0, len(e.Diagnostics))
	for _, diagnostic := range e.Diagnostics {
		parts = append(parts, formatCompilerDiagnostic(diagnostic))
	}

	return "topology contains features unsupported by c9s: " + strings.Join(parts, "; ")
}

func formatCompilerDiagnostic(diagnostic CompilerDiagnostic) string {
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
	logger      claberneteslogging.Instance
	policy      UnsupportedFieldPolicy
	diagnostics []CompilerDiagnostic
	forceError  bool
}

func newCompileDiagnostics(
	logger claberneteslogging.Instance,
	options CompileOptions,
) *compileDiagnostics {
	policy := options.UnsupportedFieldPolicy
	if policy == "" {
		policy = UnsupportedFieldPolicyWarn
	}

	return &compileDiagnostics{logger: logger, policy: policy}
}

func (d *compileDiagnostics) add(diagnostic CompilerDiagnostic, alwaysError bool) {
	d.diagnostics = append(d.diagnostics, diagnostic)
	d.forceError = d.forceError || alwaysError

	if d.policy == UnsupportedFieldPolicyWarn && !alwaysError {
		d.logger.Warn(formatCompilerDiagnostic(diagnostic))
	}
}

func (d *compileDiagnostics) err() error {
	if len(d.diagnostics) == 0 ||
		(d.policy != UnsupportedFieldPolicyError && !d.forceError) {
		return nil
	}

	diagnostics := append([]CompilerDiagnostic(nil), d.diagnostics...)
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
	EndpointA clabernetesapisv1alpha1.LinkEndpointSpec
	// EndpointB is the "b" side of the wire.
	EndpointB clabernetesapisv1alpha1.LinkEndpointSpec
	// MTU is the mtu of the wire (zero means unset).
	MTU int
}

// CompiledTopology is what a Topology definition compiles down to: flat, self contained node
// definitions (topology defaults/kinds expanded into every node), the wires between them, and
// the topology level management network settings. The compiler emits this as Node and Link
// objects (plus LauncherProfiles for deployment policy) -- all actual reconciliation
// happens in the node/link controllers, identically for compiled and hand written objects.
type CompiledTopology struct {
	// Kind is the topology definition kind -- containerlab.
	Kind string
	// Nodes maps (containerlab) node name to its flattened node definition.
	Nodes map[string]*clabernetesutilcontainerlab.NodeDefinition
	// Links holds the wires of the topology.
	Links []CompiledLink
	// Mgmt holds the containerlab management network settings (if any).
	Mgmt *clabernetesutilcontainerlab.MgmtNet
}

// CompileTopology parses and compiles the given Topology's definition.
func CompileTopology(
	logger claberneteslogging.Instance,
	topology *clabernetesapisv1alpha1.Topology,
) (*CompiledTopology, error) {
	return CompileTopologyWithOptions(logger, topology, CompileOptions{
		UnsupportedFieldPolicy: UnsupportedFieldPolicyWarn,
	})
}

// CompileTopologyWithOptions parses and compiles a Topology using the requested compatibility
// policy. Structurally impossible constructs remain errors under every policy.
func CompileTopologyWithOptions(
	logger claberneteslogging.Instance,
	topology *clabernetesapisv1alpha1.Topology,
	options CompileOptions,
) (*CompiledTopology, error) {
	if topology.Spec.Definition.Containerlab == "" {
		return nil, fmt.Errorf(
			"%w: topology definition must include a containerlab topology",
			claberneteserrors.ErrReconcile,
		)
	}

	return compileContainerlabDefinition(
		logger,
		topology.Spec.Definition.Containerlab,
		newCompileDiagnostics(logger, options),
	)
}

// GetTopologyKind returns the "kind" of topology this CR represents.
func GetTopologyKind(_ *clabernetesapisv1alpha1.Topology) string {
	return clabernetesapis.TopologyKindContainerlab
}
