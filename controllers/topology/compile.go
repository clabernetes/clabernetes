package topology

import (
	"fmt"

	clabernetesapis "github.com/srl-labs/clabernetes/apis"
	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	claberneteserrors "github.com/srl-labs/clabernetes/errors"
	claberneteslogging "github.com/srl-labs/clabernetes/logging"
	clabernetesutilcontainerlab "github.com/srl-labs/clabernetes/util/containerlab"
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
// objects (plus a NodeProfile for the deployment policy knobs) -- all actual reconciliation
// happens in the node/link controllers, identically for compiled and hand written objects.
type CompiledTopology struct {
	// Kind is the topology definition kind -- containerlab or kne.
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
	switch {
	case topology.Spec.Definition.Containerlab != "":
		return compileContainerlabDefinition(logger, topology.Spec.Definition.Containerlab)
	case topology.Spec.Definition.Kne != "":
		return compileKneDefinition(logger, topology.Spec.Definition.Kne)
	default:
		return nil, fmt.Errorf(
			"%w: unknown or unsupported topology definition kind, this is *probably* a bug",
			claberneteserrors.ErrReconcile,
		)
	}
}

// GetTopologyKind returns the "kind" of topology this CR represents -- typically this will be
// "containerlab", but may be "kne" as well.
func GetTopologyKind(t *clabernetesapisv1alpha1.Topology) string {
	if t.Spec.Definition.Kne != "" {
		return clabernetesapis.TopologyKindKne
	}

	return clabernetesapis.TopologyKindContainerlab
}
