package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
)

// LinkHostNodeName is the reserved endpoint node name representing a worker-host endpoint. The
// direct host-endpoint daemon materializes it for the Pod owning the other endpoint.
const LinkHostNodeName = "host"

const (
	// LinkConditionAccepted reports whether both endpoint identities and any required direct
	// connectivity allocation are valid and current.
	LinkConditionAccepted = "Accepted"
)

// LinkHostEndpointFinalizer keeps a host Link present until its node-local host object has been
// removed by the direct host-endpoint daemon. Force-removing the finalizer is recoverable because
// every host object also carries immutable Link, Node, and Pod ownership metadata and is swept.
const LinkHostEndpointFinalizer = "c9s.run/host-endpoint"

// LinkConnectivity is the connectivity flavor used to realize a cross-Pod Link.
// +kubebuilder:validation:Enum=vxlan;slurpeeth
type LinkConnectivity string

const (
	// LinkConnectivityVXLAN realizes a Link with a VXLAN tunnel.
	LinkConnectivityVXLAN LinkConnectivity = "vxlan"
	// LinkConnectivitySlurpeeth realizes a Link with a slurpeeth TCP segment.
	LinkConnectivitySlurpeeth LinkConnectivity = "slurpeeth"

	// SlurpeethMaxSegmentID is the largest segment identifier slurpeeth can represent.
	SlurpeethMaxSegmentID = 1<<16 - 1
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Link represents a single point-to-point "wire" between two (containerlab) nodes. Links are a
// primary clabernetes API -- like Nodes they can be created by users directly or emitted by the
// (optional) Topology compiler. The spec holds only the wire as the user drew it: two endpoints
// (Node object names in the same namespace plus interface names), connectivity flavor, and an
// optional MTU. The Link controller allocates a tunnel ID into status for cross-Pod transports;
// same-Pod, loopback, and host Links need no tunnel allocation. Direct connectivity reconcilers
// select only Links terminating on their Nodes with endpoint field selectors. Storing one object
// per wire keeps every persisted object O(1) regardless of topology size.
// +k8s:openapi-gen=true
// +kubebuilder:resource:path="links",shortName="c9slink"
// +kubebuilder:selectablefield:JSONPath=`.spec.endpointA.nodeName`
// +kubebuilder:selectablefield:JSONPath=`.spec.endpointB.nodeName`
// +kubebuilder:printcolumn:JSONPath=".spec.endpointA.nodeName",name=Node-A,type=string
// +kubebuilder:printcolumn:JSONPath=".spec.endpointA.interfaceName",name=Interface-A,type=string
// +kubebuilder:printcolumn:JSONPath=".spec.endpointB.nodeName",name=Node-B,type=string
// +kubebuilder:printcolumn:JSONPath=".spec.endpointB.interfaceName",name=Interface-B,type=string
// +kubebuilder:printcolumn:JSONPath=".spec.connectivity",name=Connectivity,type=string
// +kubebuilder:printcolumn:JSONPath=".status.tunnelID",name=Tunnel-ID,type=integer
// +kubebuilder:printcolumn:JSONPath=".status.conditions[?(@.type=='Accepted')].status",name=Accepted,type=string
// +kubebuilder:printcolumn:JSONPath=".status.error",name=Error,type=string,priority=1
// +kubebuilder:subresource:status
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
// Anything operational (the allocated tunnel ID and resolved identities) lives in status, and
// current peer transport identity is derived by direct connectivity reconcilers.
type LinkSpec struct {
	// EndpointA is the "a" side of this link.
	EndpointA LinkEndpointSpec `json:"endpointA"`
	// EndpointB is the "b" side of this link.
	EndpointB LinkEndpointSpec `json:"endpointB"`
	// MTU is the MTU for both direct endpoint interfaces; zero means unset.
	// +optional
	MTU int `json:"mtu,omitempty"`
	// Connectivity is the flavor used to realize this Link across direct Pods. Empty values on
	// objects stored before API defaulting was introduced are normalized to VXLAN by consumers.
	// +kubebuilder:default=vxlan
	// +optional
	Connectivity LinkConnectivity `json:"connectivity,omitempty"`
}

// NormalizedConnectivity returns the effective connectivity flavor, including the VXLAN default
// for objects stored before API defaulting was introduced.
func (s *LinkSpec) NormalizedConnectivity() LinkConnectivity {
	if s.Connectivity == "" {
		return LinkConnectivityVXLAN
	}

	return s.Connectivity
}

// LinkStatus is the status for a Link resource.
type LinkStatus struct {
	// TunnelID is the id number of the tunnel (vxlan vnid or slurpeeth segment id) the controller
	// allocated for this link -- both sides of the link use the same id. This is an allocation
	// rather than user intent, hence it living in the status; zero means "not allocated (yet)"
	// (direct connectivity reconcilers wait until the controller has filled the ID in).
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=16000000
	// +optional
	TunnelID int `json:"tunnelID,omitempty"`
	// ResolvedEndpoints identifies the exact Nodes to which this Link is bound. The controller
	// sets both endpoints atomically after every non-host endpoint resolves. A host endpoint is
	// recorded by name with an empty UID because it does not refer to a Node object.
	// +optional
	ResolvedEndpoints *LinkResolvedEndpointsStatus `json:"resolvedEndpoints,omitempty"`
	// Error is a compact compatibility field containing the current Accepted=False message. New
	// consumers should prefer Conditions.
	// +optional
	Error string `json:"error,omitempty"`
	// Conditions contains bounded controller observations for this Link. It does not claim
	// dataplane readiness, which remains observable through endpoint Node connectivity status.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// LinkResolvedEndpointsStatus identifies both endpoint Node identities observed by the Link
// controller. Keeping exactly two endpoint objects makes Link status size independent of topology
// size.
type LinkResolvedEndpointsStatus struct {
	// EndpointA is the resolved identity corresponding to spec.endpointA.
	EndpointA LinkResolvedEndpointStatus `json:"endpointA"`
	// EndpointB is the resolved identity corresponding to spec.endpointB.
	EndpointB LinkResolvedEndpointStatus `json:"endpointB"`
}

// LinkResolvedEndpointStatus identifies the Node object resolved for one Link endpoint.
type LinkResolvedEndpointStatus struct {
	// NodeName is the observed endpoint Node name, or the reserved name "host".
	NodeName string `json:"nodeName"`
	// UID distinguishes replacement Nodes that reuse a name. It is empty only for host endpoints.
	// +optional
	UID apimachinerytypes.UID `json:"uid,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// LinkList is a list of Link objects.
type LinkList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []Link `json:"items"`
}
