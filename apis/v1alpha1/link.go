package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
)

// LinkHostNodeName is the reserved endpoint node name representing a worker-host endpoint. The
// connectivity sidecar of the Pod owning the other endpoint materializes it.
const LinkHostNodeName = "host"

const (
	// LinkConditionAccepted reports whether both endpoint identities and any required direct
	// connectivity allocation are valid and current.
	LinkConditionAccepted = "Accepted"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Link represents a single point-to-point "wire" between two (containerlab) nodes. Links are a
// primary clabernetes API -- like Nodes they can be created by users directly or emitted by the
// (optional) Topology compiler. The spec holds only the wire as the user drew it: two endpoints
// (Node object names in the same namespace plus interface names) and an optional MTU. The Link
// controller allocates a wire ID into status for cross-Pod transports; same-Pod, loopback, and
// host Links need no wire allocation. Direct connectivity reconcilers select only Links
// terminating on their Nodes with endpoint field selectors. Storing one object per wire keeps
// every persisted object O(1) regardless of topology size.
// +k8s:openapi-gen=true
// +kubebuilder:resource:path="links",shortName="c9slink"
// +kubebuilder:selectablefield:JSONPath=`.spec.endpointA.nodeName`
// +kubebuilder:selectablefield:JSONPath=`.spec.endpointB.nodeName`
// +kubebuilder:printcolumn:JSONPath=".spec.endpointA.nodeName",name=Node-A,type=string
// +kubebuilder:printcolumn:JSONPath=".spec.endpointA.interfaceName",name=Interface-A,type=string
// +kubebuilder:printcolumn:JSONPath=".spec.endpointB.nodeName",name=Node-B,type=string
// +kubebuilder:printcolumn:JSONPath=".spec.endpointB.interfaceName",name=Interface-B,type=string
// +kubebuilder:printcolumn:JSONPath=".status.wireID",name=Wire-ID,type=integer
// +kubebuilder:printcolumn:JSONPath=".status.conditions[?(@.type=='Accepted')].status",name=Accepted,type=string
// +kubebuilder:printcolumn:JSONPath=".status.conditions[?(@.type=='Accepted')].message",name=Message,type=string,priority=1
// +kubebuilder:subresource:status
type Link struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   LinkSpec   `json:"spec,omitempty"`
	Status LinkStatus `json:"status,omitempty"`
}

// LinkEndpointSpec holds information about one side of a Link.
// +kubebuilder:validation:XValidation:rule="self.nodeName != 'host' || !has(self.netem)",message="a host endpoint cannot have a netem impairment -- impair the Node endpoint instead"
type LinkEndpointSpec struct {
	// NodeName is the name of the Node object (and therefore containerlab node) this side of the
	// link resides on -- or the reserved name `host` for a (node local) host link.
	// +kubebuilder:validation:MinLength=1
	NodeName string `json:"nodeName"`
	// InterfaceName is the name of the interface on the node this side of the link is on.
	// +kubebuilder:validation:MinLength=1
	InterfaceName string `json:"interfaceName"`
	// IPv4 is the optional IPv4 CIDR prefix containerlab assigns to this endpoint. It is cold-plan
	// configuration for kinds that consume endpoint addresses; changing it on a realized Node
	// requires recreation. It is invalid on the reserved host endpoint.
	// +optional
	IPv4 string `json:"ipv4,omitempty"`
	// IPv6 is the optional IPv6 CIDR prefix containerlab assigns to this endpoint. It is cold-plan
	// configuration for kinds that consume endpoint addresses; changing it on a realized Node
	// requires recreation. It is invalid on the reserved host endpoint.
	// +optional
	IPv6 string `json:"ipv6,omitempty"`
	// Netem is the link impairment of the packets this side of the link sends -- the same
	// impairment containerlab's endpoint `netem` key declares. The connectivity sidecar owning
	// this endpoint programs it as the interface's root qdisc and re-asserts it while the Pod
	// runs, so it survives the interface being recreated. Changing it is always applied live.
	// +optional
	Netem *LinkNetemSpec `json:"netem,omitempty"`
}

// LinkNetemSpec is the link impairment of one Link endpoint in containerlab's vocabulary. Each
// set field adds its impairment to the packets the endpoint's interface sends; an impairment
// without any set field leaves the interface unimpaired.
// +kubebuilder:validation:XValidation:rule="!has(self.jitter) || has(self.delay)",message="jitter cannot be set without setting delay"
type LinkNetemSpec struct {
	// Delay is the time packets are delayed by, as a Go duration -- i.e. 50ms. The connectivity
	// sidecar programs the qdisc in 32-bit kernel ticks, which caps the delay at 4m.
	// +kubebuilder:validation:Pattern=`^([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+$`
	// +kubebuilder:validation:XValidation:rule="duration(self) <= duration('4m')",message="delay must be at most 4m"
	// +optional
	Delay string `json:"delay,omitempty"`
	// Jitter is the delay variation, as a Go duration -- i.e. 5ms. It needs a delay, and the
	// kernel caps it at 2.147483647s, so a longer jitter is rejected rather than silently capped.
	// +kubebuilder:validation:Pattern=`^([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+$`
	// +kubebuilder:validation:XValidation:rule="duration(self) <= duration('2147483647ns')",message="jitter must be at most 2.147483647s"
	// +optional
	Jitter string `json:"jitter,omitempty"`
	// Loss is the random packet loss in percent -- i.e. 0.5 for 0.5%.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	// +optional
	Loss float64 `json:"loss,omitempty"`
	// Rate is the rate limit in kbit/s. The maximum is the highest rate containerlab accepts,
	// the highest one whose rate in bytes/s fits 64 bits; it is a rule rather than a schema
	// maximum because the schema stores maximums as floats, which can't hold it exactly.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:XValidation:rule="self <= 18446744073709551",message="rate must be at most 18446744073709551 kbit/s"
	// +optional
	Rate int64 `json:"rate,omitempty"`
	// Corruption is the random packet corruption in percent -- i.e. 0.1 for 0.1%.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	// +optional
	Corruption float64 `json:"corruption,omitempty"`
}

// LinkSpec is the spec for a Link resource -- the wire as the user drew it, nothing else.
// Anything operational (the allocated wire ID and resolved identities) lives in status, and
// current peer transport identity is derived by direct connectivity reconcilers.
type LinkSpec struct {
	// EndpointA is the "a" side of this link.
	EndpointA LinkEndpointSpec `json:"endpointA"`
	// EndpointB is the "b" side of this link.
	EndpointB LinkEndpointSpec `json:"endpointB"`
	// MTU is the MTU for both direct endpoint interfaces; zero means unset.
	// +optional
	MTU int `json:"mtu,omitempty"`
}

// LinkStatus is the status for a Link resource.
type LinkStatus struct {
	// WireID is the identity this link carries on the fabric wire between its two endpoint
	// sidecars -- both sides of the link use the same id, and it is unique among the Links of
	// this namespace (wire datagrams dispatch inside one receiving sidecar from a validated
	// source, so cross-namespace uniqueness is meaningless). This is an allocation rather than
	// user intent, hence it living in the status; zero means "not allocated (yet)" (direct
	// connectivity reconcilers wait until the controller has filled the ID in).
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=16000000
	// +optional
	WireID int `json:"wireID,omitempty"`
	// ResolvedEndpoints identifies the exact Nodes to which this Link is bound. The controller
	// sets both endpoints atomically after every non-host endpoint resolves. A host endpoint is
	// recorded by name with an empty UID because it does not refer to a Node object.
	// +optional
	ResolvedEndpoints *LinkResolvedEndpointsStatus `json:"resolvedEndpoints,omitempty"`
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
