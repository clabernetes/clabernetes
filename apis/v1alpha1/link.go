package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Link is an object that represents a single point-to-point link between two (containerlab) nodes
// of a clabernetes Topology. Links are created and managed by the clabernetes controller -- one
// per inter-launcher link -- and hold everything both launcher pods need to establish the tunnel
// (vxlan or slurpeeth) for the link. Storing this data per-link (rather than in one big
// connectivity object) means no single object grows with the size of the topology, which keeps
// clabernetes clear of the etcd max object size limits for very large topologies. The
// "clabernetes/linkEndpointA" and "clabernetes/linkEndpointB" labels hold the launcher nodes
// that terminate each side (the primary node for grouped nodes) -- launchers select "their"
// links by those labels and derive the remote fabric service from them.
// +k8s:openapi-gen=true
// +kubebuilder:resource:path="links",shortName="c9slink"
// +kubebuilder:printcolumn:JSONPath=".spec.topologyName",name=Topology,type=string
// +kubebuilder:printcolumn:JSONPath=".spec.endpointA.nodeName",name=Node-A,type=string
// +kubebuilder:printcolumn:JSONPath=".spec.endpointA.interfaceName",name=Interface-A,type=string
// +kubebuilder:printcolumn:JSONPath=".spec.endpointB.nodeName",name=Node-B,type=string
// +kubebuilder:printcolumn:JSONPath=".spec.endpointB.interfaceName",name=Interface-B,type=string
// +kubebuilder:printcolumn:JSONPath=".status.tunnelID",name=Tunnel-ID,type=integer
type Link struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   LinkSpec   `json:"spec,omitempty"`
	Status LinkStatus `json:"status,omitempty"`
}

// LinkEndpointSpec holds information about one side of a Link.
type LinkEndpointSpec struct {
	// NodeName is the name of the (containerlab) node this side of the link resides on.
	NodeName string `json:"nodeName"`
	// InterfaceName is the name of the interface on the node this side of the link is on.
	InterfaceName string `json:"interfaceName"`
}

// LinkSpec is the spec for a Link resource. It holds only the "wire as the user drew it" --
// anything operational (like the allocated tunnel id) lives in the status or is derived by the
// launchers.
type LinkSpec struct {
	// TopologyName is the name of the Topology this Link belongs to.
	TopologyName string `json:"topologyName"`
	// EndpointA is the "a" side of this link.
	EndpointA LinkEndpointSpec `json:"endpointA"`
	// EndpointB is the "b" side of this link.
	EndpointB LinkEndpointSpec `json:"endpointB"`
	// MTU is the mtu for the link as set in the original topology definition -- launchers apply
	// this to the (node side of the) link termination they create; zero means "unset" (use the
	// containerlab default).
	// +optional
	MTU int `json:"mtu,omitempty"`
}

// LinkStatus is the status for a Link resource.
type LinkStatus struct {
	// TunnelID is the id number of the tunnel (vnid or segment id) the controller allocated for
	// this link -- both sides of the link use the same id. This is an allocation rather than user
	// intent, hence it living in the status; zero means "not allocated yet" (launchers skip such
	// links until the controller has filled the id in).
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=16000000
	// +optional
	TunnelID int `json:"tunnelID,omitempty"`
}

// PointToPointTunnel holds the *local view* of a tunnel between two interfaces on different nodes
// of a clabernetes Topology -- launchers derive this view from the Link objects relevant to their
// node. This connection can be established by using clab tools (vxlan) or the experimental
// slurpeeth (tcp tunnel magic).
type PointToPointTunnel struct {
	// TunnelID is the id number of the tunnel (vnid or segment id).
	TunnelID int `json:"tunnelID"`
	// Destination is the destination service to connect to (qualified k8s service name) --
	// launchers derive this from the link's topology name and the remote launcher node (which
	// they read from the link's endpoint labels).
	Destination string `json:"destination,omitempty"`
	// LocalNodeName is the name (in the clabernetes topology) of the local node for this side of
	// the tunnel.
	LocalNode string `json:"localNode"`
	// LocalInterface is the local termination of this tunnel.
	LocalInterface string `json:"localInterface"`
	// RemoteNode is the name (in the clabernetes topology) of the remote node for this side of the
	// tunnel.
	RemoteNode string `json:"remoteNode"`
	// RemoteInterface is the remote termination interface of this tunnel -- necessary to store so
	// can properly align tunnels (and ids!) between nodes; basically to know which tunnels are
	// "paired up".
	RemoteInterface string `json:"remoteInterface"`
	// MTU is the mtu for the link this tunnel realizes; zero means "unset" (use the containerlab
	// default).
	MTU int `json:"mtu,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// LinkList is a list of Link objects.
type LinkList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []Link `json:"items"`
}
