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
// clabernetes clear of the etcd max object size limits for very large topologies.
// +k8s:openapi-gen=true
// +kubebuilder:resource:path="links",shortName="c9slink"
// +kubebuilder:printcolumn:JSONPath=".spec.topologyName",name=Topology,type=string
// +kubebuilder:printcolumn:JSONPath=".spec.endpointA.nodeName",name=Node-A,type=string
// +kubebuilder:printcolumn:JSONPath=".spec.endpointA.interfaceName",name=Interface-A,type=string
// +kubebuilder:printcolumn:JSONPath=".spec.endpointB.nodeName",name=Node-B,type=string
// +kubebuilder:printcolumn:JSONPath=".spec.endpointB.interfaceName",name=Interface-B,type=string
// +kubebuilder:printcolumn:JSONPath=".spec.tunnelID",name=Tunnel-ID,type=integer
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
	// LauncherNode is the name of the (containerlab) node whose launcher pod terminates this side
	// of the link -- for "grouped" nodes this is the primary node of the group, otherwise this is
	// simply the same as NodeName.
	LauncherNode string `json:"launcherNode"`
	// Destination is the qualified kubernetes service name over which this side of the link can
	// be reached (that is, the service the *other* side of the link connects to).
	Destination string `json:"destination"`
}

// LinkSpec is the spec for a Link resource.
type LinkSpec struct {
	// TopologyName is the name of the Topology this Link belongs to.
	TopologyName string `json:"topologyName"`
	// EndpointA is the "a" side of this link.
	EndpointA LinkEndpointSpec `json:"endpointA"`
	// EndpointB is the "b" side of this link.
	EndpointB LinkEndpointSpec `json:"endpointB"`
	// TunnelID is the id number of the tunnel (vnid or segment id) for this link -- both sides of
	// the link use the same id.
	TunnelID int `json:"tunnelID"`
}

// LinkStatus is the status for a Link resource.
type LinkStatus struct{}

// PointToPointTunnel holds the *local view* of a tunnel between two interfaces on different nodes
// of a clabernetes Topology -- launchers derive this view from the Link objects relevant to their
// node. This connection can be established by using clab tools (vxlan) or the experimental
// slurpeeth (tcp tunnel magic).
type PointToPointTunnel struct {
	// TunnelID is the id number of the tunnel (vnid or segment id).
	TunnelID int `json:"tunnelID"`
	// Destination is the destination service to connect to (qualified k8s service name).
	Destination string `json:"destination"`
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
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// LinkList is a list of Link objects.
type LinkList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []Link `json:"items"`
}
