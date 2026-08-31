package directruntime

import (
	"errors"
	"fmt"

	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
)

var errTransportFilter = errors.New("transport filter invariant failed")

// TransportFilterSpec lists the UDP destination ports of the sidecar's own transports (the
// management mesh VTEP and the fabric wire socket) that must stay deliverable across every
// packet filter programmed in the shared Pod network namespace.
type TransportFilterSpec struct {
	// UDPPorts are the transport destination ports to keep accepted; both directions of every
	// transport address one of these ports, so destination matching covers inbound and outbound.
	UDPPorts []uint16
}

// TransportFilterOperations keeps the sidecar's UDP transports exempt from device-programmed
// packet filters. This is the one sanctioned write into chains the sidecar does not own: a
// device that filters the shared namespace (EOS installs an INPUT policy drop) severs the
// transports beneath its own management plane, which no physical wire is subject to -- a
// control-plane ACL on hardware never sees the wire carrying it. An accept in a sidecar-owned
// base chain cannot help, because netfilter continues evaluating every other base chain at the
// hook and any drop there is final, so the accepts must live at the head of the foreign chains
// themselves. Implementations must be idempotent and must tolerate a device flushing or
// rebuilding its filter concurrently; the revision tick re-asserts displaced accepts.
type TransportFilterOperations interface {
	// EnsureTransportFilterAccepts asserts one accept rule per spec port at the head of every
	// filter-type input and output base chain the sidecar does not own, across the ip, ip6, and
	// inet families. Rules already present are left untouched.
	EnsureTransportFilterAccepts(spec TransportFilterSpec) error
}

// reconcileTransportFilter derives the active transport ports from the plan and asserts their
// accepts. It runs beside reconcileInterposition on the cold pass and on every revision tick:
// wire-only Pods carry no interposed management entry but still depend on the fabric wire port.
func reconcileTransportFilter(
	plan clabernetesinternaldeviceplan.Plan,
	options ConnectivityOptions,
) error {
	entry, err := interposedManagementEntry(plan)
	if err != nil {
		return err
	}

	var ports []uint16

	if entry != nil && entry.Interposition != nil && entry.Interposition.Mesh != nil {
		ports = append(ports, clabernetesconstants.ManagementMeshVXLANPort)
	}

	if hasRemoteInterfaces(plan) {
		ports = append(ports, clabernetesconstants.FabricWireServicePort)
	}

	if len(ports) == 0 {
		return nil
	}

	if options.FilterOperations == nil {
		return fmt.Errorf("%w: transport filter operations are unavailable", errTransportFilter)
	}

	if err := options.FilterOperations.EnsureTransportFilterAccepts(TransportFilterSpec{
		UDPPorts: ports,
	}); err != nil {
		return fmt.Errorf("ensuring transport filter accepts: %w", err)
	}

	return nil
}
