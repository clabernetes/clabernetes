package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// LinkHostNodeName is the reserved endpoint node name representing "the launcher pod itself" --
// links with a `host` endpoint are materialized verbatim by the launcher owning the other
// endpoint (this mirrors containerlab's own host link syntax).
const LinkHostNodeName = "host"

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Link represents a single point-to-point "wire" between two (containerlab) nodes. Links are a
// primary clabernetes API -- like Nodes they can be created by users directly or emitted by the
// (optional) Topology compiler. The spec holds only the wire as the user drew it: two endpoints
// (Node object names in the same namespace plus interface names) and an optional mtu. The link
// controller allocates a tunnel id into the status for links that cross launcher pods; links
// between nodes co-located in one launcher pod and links to the reserved `host` node need no
// tunnel and are materialized directly by the owning launcher. Launchers select the links
// terminating on their nodes with *field selectors* on the endpoint node names (which requires
// kubernetes 1.31+) -- no labels are required, ever, and no launcher watches more than its own
// links. Storing one object per wire keeps every persisted object O(1) regardless of topology
// size.
// +k8s:openapi-gen=true
// +kubebuilder:resource:path="links",shortName="c9slink"
// +kubebuilder:selectablefield:JSONPath=`.spec.endpointA.nodeName`
// +kubebuilder:selectablefield:JSONPath=`.spec.endpointB.nodeName`
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
	// NodeName is the name of the Node object (and therefore containerlab node) this side of the
	// link resides on -- or the reserved name `host` for a (node local) host link.
	// +kubebuilder:validation:MinLength=1
	NodeName string `json:"nodeName"`
	// InterfaceName is the name of the interface on the node this side of the link is on.
	// +kubebuilder:validation:MinLength=1
	InterfaceName string `json:"interfaceName"`
}

// LinkSpec is the spec for a Link resource -- the wire as the user drew it, nothing else.
// Anything operational (the allocated tunnel id) lives in the status, and anything derivable
// (i.e. the remote launcher's fabric service) is derived by the launchers.
type LinkSpec struct {
	// EndpointA is the "a" side of this link.
	EndpointA LinkEndpointSpec `json:"endpointA"`
	// EndpointB is the "b" side of this link.
	EndpointB LinkEndpointSpec `json:"endpointB"`
	// MTU is the mtu for the link -- launchers apply this to the (node side of the) link
	// termination they create; zero means "unset" (use the containerlab default).
	// +optional
	MTU int `json:"mtu,omitempty"`
}

// LinkStatus is the status for a Link resource.
type LinkStatus struct {
	// TunnelID is the id number of the tunnel (vxlan vnid or slurpeeth segment id) the controller
	// allocated for this link -- both sides of the link use the same id. This is an allocation
	// rather than user intent, hence it living in the status; zero means "not allocated (yet)"
	// (launchers skip such links until the controller has filled the id in).
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=16000000
	// +optional
	TunnelID int `json:"tunnelID,omitempty"`
	// Error holds the reason this link cannot currently be realized. An empty value means the
	// link is eligible for materialization (a cross-launcher link can still be waiting for its
	// tunnel id); invalid links and deterministic endpoint-conflict losers carry an error and are
	// ignored by node controllers and launchers until their spec or conflicting links change.
	// +optional
	Error string `json:"error,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// LinkList is a list of Link objects.
type LinkList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []Link `json:"items"`
}
