package directruntime

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
)

const (
	// InterpositionConditionsFile records the named interposition readiness conditions in the
	// connectivity state directory, so probe failures surface the exact failed invariant.
	InterpositionConditionsFile = "interposition-conditions"
	// TransportInterfaceName is the sidecar-owned identity of the preserved CNI interface. The
	// device never receives this name and must never own this interface.
	TransportInterfaceName = "c9s0"
	// RouterInterfaceName is the sidecar-owned router leg: the far end of the synthetic device
	// pair. It carries the management gateway address and identity, answers ARP for every
	// remote peer, and routes peer-bound management traffic to the mesh tunnel endpoint.
	RouterInterfaceName = "c9sr0"
	// MeshVTEPName is the sidecar-owned management mesh tunnel endpoint: a routed interface with
	// learning off, whose link-layer identity derives from the Pod's own management address, and
	// whose forwarding state is exactly one neighbor and one forwarding entry per peer.
	MeshVTEPName = "c9sm0"
	// interpositionTransportTable is the sidecar-owned policy routing table carrying Kubernetes
	// transport, distinct from the source-specific management tables.
	interpositionTransportTable = 20_000
	// interpositionGatewayReturnRulePriority delivers gateway-bound traffic that enters on the
	// router leg locally, ahead of the hairpin below: the second crossing of a hairpinned reply,
	// or a device's own exchange with its gateway. Installed with the hairpin only.
	interpositionGatewayReturnRulePriority = 894
	// interpositionGatewayHairpinRulePriority sends gateway-bound traffic that did not enter on
	// the router leg across the synthetic pair (the transport table carries the gateway as a
	// host route via the device leg) instead of delivering it locally. Every inbound translated
	// flow carries the gateway as its client identity, and its translation state lives on the
	// sidecar legs. A single-namespace device that forwards management ports to a nested guest
	// (vrnetlab) returns the guest's reply to the gateway through the pod kernel's own
	// forwarding path, where the gateway is a local address: without the hairpin that reply is
	// delivered locally and never reaches the state that would return it to the client. Only a
	// kernel-held address installs it; a device with its own stack answers through the pair.
	interpositionGatewayHairpinRulePriority = 895
	// interpositionDeviceLegRulePriority blackholes traffic that re-enters on the device leg for a
	// management address the device holds in its own stack (a raw-socket dataplane that shares
	// the pod namespace): the device has already consumed the frame, and the kernel copy would
	// otherwise be forwarded back through the router leg in a loop. It precedes every lookup.
	interpositionDeviceLegRulePriority = 896
	// interpositionIngressRulePriority steers traffic that arrives from the mesh tunnel endpoint
	// or from the Kubernetes transport for a management address the pod kernel itself holds
	// into the transport table, ahead of local delivery: it then crosses the synthetic pair and
	// arrives on the device leg, where a shared segment would have delivered it and where a
	// device's interface-scoped filters and forwarders expect it.
	interpositionIngressRulePriority = 897
	// interpositionLocalRulePriority is where the sidecar keeps the local-table lookup. The
	// kernel installs it at priority 0, which nothing can precede; the sidecar re-homes it here
	// so only the ingress rule above runs before local delivery, and so a device that moves the
	// lookup behind its own rules (SONiC re-inserts it at 1001) cannot push gateway-directed or
	// device-leg traffic into the transport rules.
	interpositionLocalRulePriority = 898
	// interpositionRouterRulePriority selects the transport table for device-originated traffic
	// entering through the router leg.
	interpositionRouterRulePriority = 900
	// interpositionTransportRulePriority selects the transport table for Pod-sourced traffic:
	// transport replies and the fabric VTEPs (whose encapsulation sources the Pod address).
	// Scoping by source keeps a kernel-dataplane device's own data routes in main authoritative.
	interpositionTransportRulePriority = 901
	// interpositionManagementRulePriority selects the transport table for traffic to the Pod's
	// own management address when the pod kernel does not hold that address itself (the
	// device runs its own stack behind the device leg), so application hooks and
	// mesh-delivered peer traffic reach the device even when a device stripped or rewrote the
	// main table. For a kernel-held address the local table delivers, and this rule must be
	// absent: routed back out the router leg the packet would only loop.
	interpositionManagementRulePriority = 902
)

// InterpositionSpec is the complete pod-namespace state one interposed management identity
// requires, derived entirely from the plan, the Pod's downward-API identity, and the mounted
// peer directory.
type InterpositionSpec struct {
	// PodAddress is the bare kubelet-assigned Pod IPv4 address.
	PodAddress string
	// TransportInterface is the sidecar-owned name for the preserved CNI interface.
	TransportInterface string
	// RouterInterface is the sidecar-owned router leg name.
	RouterInterface string
	// DeviceInterface is the synthetic device-leg name the device expects.
	DeviceInterface string
	// DeviceMAC optionally pins the device-leg MAC address.
	DeviceMAC string
	// ManagementIPv4 is the allocated management address in CIDR form.
	ManagementIPv4 string
	// GatewayIPv4 is the bare management gateway address carried by the router leg.
	GatewayIPv4 string
	// ManagementIPv6 optionally carries an allocated IPv6 management address in CIDR form; it is
	// assigned to the device leg without translation.
	ManagementIPv6 string
	// GatewayIPv6 optionally carries the bare IPv6 management gateway for the router leg.
	GatewayIPv6 string
	// StateDirectory is the sidecar-owned state root where the captured transport gateway is
	// persisted, so re-assertion survives a device stripping routes from every table.
	StateDirectory string
	// MeshTunnelID is the VNI of the namespace's management mesh.
	MeshTunnelID int
	// MeshGatewayMAC is the deterministic gateway link-layer identity pinned on the router leg.
	MeshGatewayMAC string
	// MeshMAC is the Pod's own mesh tunnel-endpoint link-layer identity, derived from its
	// management IPv4 address exactly as every peer derives it.
	MeshMAC string
	// MeshPeers is the current peer set from the mounted directory: every other node holding a
	// management identity and realized by a Pod with an address.
	MeshPeers []MeshPeer
	// ReconcileMeshPeers asks for the per-peer forwarding state to be converged on this pass.
	// Peer state is exact and static, so it is converged when the directory changes and on a
	// slow resync rather than on every tick.
	ReconcileMeshPeers bool
}

// MeshPeer is one remote node's mesh location: its management addresses and the Pod that
// realizes it. The peer's tunnel-endpoint identity derives from its management IPv4 address.
type MeshPeer struct {
	// ManagementIPv4 is the peer's bare management IPv4 address.
	ManagementIPv4 string
	// ManagementIPv6 optionally carries the peer's bare management IPv6 address.
	ManagementIPv6 string
	// PodAddress is the bare IPv4 address of the Pod realizing the peer.
	PodAddress string
}

var errInterposition = errors.New("management interposition invariant failed")

// interposedManagementEntry selects the single interposed management entry of a Pod plan. A Pod
// has exactly one synthetic management leg; more than one interposed entry is a planning
// invariant violation.
func interposedManagementEntry(
	plan clabernetesinternaldeviceplan.Plan,
) (*clabernetesinternaldeviceplan.ManagementPlan, error) {
	var selected *clabernetesinternaldeviceplan.ManagementPlan

	for index := range plan.Management {
		entry := &plan.Management[index]
		if entry.InterfaceSelector != clabernetesinternaldeviceplan.ManagementInterfaceInterposed {
			continue
		}

		if selected != nil {
			return nil, fmt.Errorf(
				"%w: plan carries more than one interposed management entry",
				errInterposition,
			)
		}

		selected = entry
	}

	return selected, nil
}

// interpositionSpecForEntry validates and converts one interposed plan entry into the namespace
// spec. It fails closed on every incomplete identity rather than degrading.
func interpositionSpecForEntry(
	entry *clabernetesinternaldeviceplan.ManagementPlan,
	podAddress string,
) (InterpositionSpec, InterpositionNATSpec, error) {
	spec := InterpositionSpec{
		PodAddress:         strings.TrimSpace(podAddress),
		TransportInterface: TransportInterfaceName,
		RouterInterface:    RouterInterfaceName,
	}

	if entry.Interposition == nil {
		return spec, InterpositionNATSpec{}, fmt.Errorf(
			"%w: interposed entry %q carries no contract",
			errInterposition,
			entry.ID,
		)
	}

	if spec.PodAddress == "" {
		return spec, InterpositionNATSpec{}, fmt.Errorf(
			"%w: Pod address is required for interposition",
			errInterposition,
		)
	}

	managementPrefix, err := netip.ParsePrefix(entry.IPv4)
	if err != nil {
		return spec, InterpositionNATSpec{}, fmt.Errorf(
			"%w: interposed entry %q management address %q is invalid",
			errInterposition,
			entry.ID,
			entry.IPv4,
		)
	}

	gateway, err := netip.ParseAddr(entry.IPv4Gateway)
	if err != nil || !managementPrefix.Masked().Contains(gateway) {
		return spec, InterpositionNATSpec{}, fmt.Errorf(
			"%w: interposed entry %q gateway %q is invalid for %q",
			errInterposition,
			entry.ID,
			entry.IPv4Gateway,
			entry.IPv4,
		)
	}

	if entry.Interposition.Mesh == nil {
		return spec, InterpositionNATSpec{}, fmt.Errorf(
			"%w: interposed entry %q carries no management mesh membership",
			errInterposition,
			entry.ID,
		)
	}

	meshMAC, err := ManagementMeshMAC(managementPrefix.Addr())
	if err != nil {
		return spec, InterpositionNATSpec{}, fmt.Errorf(
			"%w: interposed entry %q: %w",
			errInterposition,
			entry.ID,
			err,
		)
	}

	spec.DeviceInterface = entry.Interposition.DeviceInterface
	spec.DeviceMAC = entry.Interposition.DeviceMAC
	spec.ManagementIPv4 = entry.IPv4
	spec.GatewayIPv4 = entry.IPv4Gateway
	spec.ManagementIPv6 = entry.IPv6
	spec.GatewayIPv6 = entry.IPv6Gateway
	spec.MeshTunnelID = entry.Interposition.Mesh.TunnelID
	spec.MeshGatewayMAC = entry.Interposition.Mesh.GatewayMAC
	spec.MeshMAC = meshMAC.String()

	natSpec := InterpositionNATSpec{
		PodAddress:         spec.PodAddress,
		ManagementAddress:  managementPrefix.Addr().String(),
		ManagementSubnet:   managementPrefix.Masked().String(),
		GatewayAddress:     entry.IPv4Gateway,
		TransportInterface: spec.TransportInterface,
		DeviceInterface:    spec.DeviceInterface,
	}

	for _, port := range entry.Interposition.InboundPorts {
		natSpec.InboundPorts = append(natSpec.InboundPorts, InterpositionPortMap{
			Protocol:   port.Protocol,
			PodPort:    port.PodPort,
			DevicePort: port.DevicePort,
		})
	}

	return spec, natSpec, nil
}

// meshPeersForSpec projects the directory onto the spec's peer set: every entry realized by a
// Pod with an address, other than the Pod's own identity. Entries without a Pod address only
// contribute name resolution and are left out here.
func meshPeersForSpec(spec InterpositionSpec, peers []PeerIdentity) []MeshPeer {
	ownManagement := ""
	if prefix, err := netip.ParsePrefix(spec.ManagementIPv4); err == nil {
		ownManagement = prefix.Addr().String()
	}

	result := make([]MeshPeer, 0, len(peers))

	for _, peer := range peers {
		if peer.Pod == "" || peer.IPv4 == "" || peer.IPv4 == ownManagement ||
			peer.Pod == spec.PodAddress {
			continue
		}

		result = append(result, MeshPeer{
			ManagementIPv4: peer.IPv4,
			ManagementIPv6: peer.IPv6,
			PodAddress:     peer.Pod,
		})
	}

	return result
}

// reconcileInterposition converges the Pod namespace to the plan's interposed management
// identity. It runs before any device container starts and again on every revision tick so
// sidecar-owned state displaced by a device is re-asserted; the per-peer mesh state is
// converged when asked (directory change, cold pass, periodic resync). It never mutates
// device-owned state; the one sanctioned write into device-programmed chains is the
// transport-port accept assertion, which lives in reconcileTransportFilter with its rationale.
func reconcileInterposition(
	plan clabernetesinternaldeviceplan.Plan,
	options ConnectivityOptions,
	operations LinkOperations,
	peers []PeerIdentity,
	reconcilePeers bool,
) error {
	entry, err := interposedManagementEntry(plan)
	if err != nil {
		return err
	}

	if entry == nil {
		return nil
	}

	spec, natSpec, err := interpositionSpecForEntry(entry, options.PodAddress)
	if err != nil {
		return err
	}

	spec.StateDirectory = options.StateDirectory
	spec.MeshPeers = meshPeersForSpec(spec, peers)
	spec.ReconcileMeshPeers = reconcilePeers

	if err := operations.EnsureInterposition(spec); err != nil {
		recordInterpositionConditions(options.StateDirectory, err, nil)

		return fmt.Errorf("ensuring management interposition: %w", err)
	}

	if options.NATOperations == nil {
		err := fmt.Errorf("%w: translation operations are unavailable", errInterposition)
		recordInterpositionConditions(options.StateDirectory, nil, err)

		return err
	}

	if err := options.NATOperations.EnsureInterpositionNAT(natSpec); err != nil {
		recordInterpositionConditions(options.StateDirectory, nil, err)

		return fmt.Errorf("ensuring management translation: %w", err)
	}

	recordInterpositionConditions(options.StateDirectory, nil, nil)

	return nil
}

// recordInterpositionConditions persists the two named interposition conditions. Recording is
// best-effort observability: the fail-closed path is the returned error itself.
func recordInterpositionConditions(stateDirectory string, underlayErr, translationErr error) {
	if stateDirectory == "" {
		return
	}

	condition := func(name string, failure error, blocked bool) string {
		switch {
		case failure != nil:
			return name + "=False: " + failure.Error() + "\n"
		case blocked:
			return name + "=Unknown: blocked by an earlier condition\n"
		default:
			return name + "=True\n"
		}
	}

	content := condition("CNIUnderlayPreserved", underlayErr, false) +
		condition("ManagementTranslationReady", translationErr, underlayErr != nil)

	//nolint:gosec,mnd // non-sensitive sidecar-owned observability record, standard file mode.
	_ = os.WriteFile(
		filepath.Join(filepath.Clean(stateDirectory), InterpositionConditionsFile),
		[]byte(content),
		0o644,
	)
}
