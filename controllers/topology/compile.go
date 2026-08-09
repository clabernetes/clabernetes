package topology

import (
	"fmt"

	clabernetesapis "github.com/clabernetes/clabernetes/apis"
	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	claberneteserrors "github.com/clabernetes/clabernetes/errors"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
)

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
	if topology.Spec.Definition.Containerlab == "" {
		return nil, fmt.Errorf(
			"%w: topology definition must include a containerlab topology",
			claberneteserrors.ErrReconcile,
		)
	}

	return compileContainerlabDefinition(logger, topology.Spec.Definition.Containerlab)
}

// GetTopologyKind returns the "kind" of topology this CR represents.
func GetTopologyKind(_ *clabernetesapisv1alpha1.Topology) string {
	return clabernetesapis.TopologyKindContainerlab
}
