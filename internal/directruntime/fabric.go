package directruntime

// fabricEncapsulationOverhead is the VXLAN-over-IPv4 headroom the underlay consumes for one
// encapsulated frame (outer IPv4 + UDP + VXLAN headers).
const fabricEncapsulationOverhead = 50

// FabricEndpointSpec is the Pod-local realization request for one cross-Pod Link endpoint: a
// device-facing veth leg stitched to an in-Pod VXLAN VTEP terminating on the preserved
// Kubernetes underlay.
type FabricEndpointSpec struct {
	// InterfaceID is the stable plan interface identity; sidecar-owned link names derive from
	// it.
	InterfaceID string
	// InterfaceName is the device-facing endpoint name from the plan.
	InterfaceName string
	// Owner is the ownership marker for every link this endpoint creates.
	Owner string
	// OwnerPrefix identifies transport state owned by this Pod across Link revisions.
	OwnerPrefix string
	// TunnelID is the VXLAN network identifier shared by both Link ends.
	TunnelID int
	// MTU is the requested endpoint MTU; the realization clamps it to what the underlay can
	// carry encapsulated.
	MTU int
	// PeerTransport is the peer's stable fabric transport name (a headless Service DNS name) or
	// address.
	PeerTransport string
	// PodAddress is this Pod's bare underlay address, the local VTEP endpoint.
	PodAddress string
}

// FabricEndpointResult reports one endpoint's transport state: an unready endpoint keeps its
// device-facing leg prepared while reconciliation retries the peer.
type FabricEndpointResult struct {
	Ready  bool
	Reason string
	// EffectiveMTU is the realized endpoint MTU after bounding the requested value to what the
	// Pod underlay can carry encapsulated; every interface of the endpoint chain carries this
	// value. Zero means the realization failed before MTU evaluation.
	EffectiveMTU int
	// UnderlayMTU is the observed Pod underlay MTU backing the encapsulation bound; zero means
	// the underlay interface was not identifiable.
	UnderlayMTU int
}

// HostInterfaceSpec is the Pod-local realization request for one host Link: a veth pair whose
// worker-side end is placed into the worker network namespace through the sidecar's read-only
// namespace handle. The pair dies with the Pod namespace, so no worker residue can outlive the
// Pod.
type HostInterfaceSpec struct {
	// InterfaceID is the stable plan interface identity.
	InterfaceID string
	// InterfaceName is the device-facing endpoint name in the Pod namespace.
	InterfaceName string
	// HostInterface is the interface name presented in the worker namespace.
	HostInterface string
	// Owner is the ownership marker for the created links.
	Owner string
	// OwnerPrefix identifies transport state owned by this Pod across Link revisions.
	OwnerPrefix string
	// MTU is the requested MTU for both ends.
	MTU int
}
