package directruntime

// InterpositionPortMap declares one inbound management port translated from the Pod address to
// the interposed device management address.
type InterpositionPortMap struct {
	// Protocol is "tcp" or "udp".
	Protocol string
	// PodPort is the port clients dial on the Pod address.
	PodPort uint16
	// DevicePort is the port the device binds on its management address.
	DevicePort uint16
}

// InterpositionNATSpec is the complete translation state for one interposed management identity.
// All addresses are IPv4; IPv6 translation is introduced together with its conformance
// evidence.
type InterpositionNATSpec struct {
	// PodAddress is the bare kubelet-assigned Pod IPv4 address.
	PodAddress string
	// ManagementAddress is the bare device management IPv4 address.
	ManagementAddress string
	// ManagementSubnet is the management prefix in CIDR form.
	ManagementSubnet string
	// GatewayAddress is the bare Pod-local management gateway IPv4 address. Inbound translated
	// connections are source-translated to it, so a device whose management stack holds only
	// the connected management route (SR OS reads its routes from a Docker-shaped environment
	// c9s does not have) can always reply.
	GatewayAddress string
	// TransportInterface is the sidecar-owned preserved CNI interface name.
	TransportInterface string
	// DeviceInterface is the device-facing synthetic interface name.
	DeviceInterface string
	// InboundPorts lists declared management ports reachable at the Pod address.
	InboundPorts []InterpositionPortMap
}

// NATOperations is the packet-translation boundary for interposed management traffic. It owns a
// dedicated per-namespace translation table and never mutates device- or CNI-owned filter or NAT
// state. Implementations must be idempotent: EnsureInterpositionNAT reconciles the owned table to
// exactly the requested spec.
type NATOperations interface {
	// EnsureInterpositionNAT reconciles the sidecar-owned translation table to the spec:
	// masquerade for management-sourced flows forwarded out the transport interface,
	// first-traversal source translation for locally-originated hairpin flows leaving the device
	// interface for destinations outside the management subnet, destination translation for
	// each declared inbound port, and gateway source translation for those inbound flows so the
	// device sees an on-subnet peer it can always answer.
	EnsureInterpositionNAT(spec InterpositionNATSpec) error
	// DeleteInterpositionNAT removes the sidecar-owned translation table entirely.
	DeleteInterpositionNAT() error
}
