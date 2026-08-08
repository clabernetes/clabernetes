package v1alpha1

import (
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
)

const (
	// NodeConditionLauncherProfileResolved reports whether the Node's effective
	// LauncherProfile reference resolved successfully.
	NodeConditionLauncherProfileResolved = "LauncherProfileResolved"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Node represents a single containerlab node ("device") and is realized as a single launcher
// pod. Nodes are a primary clabernetes API -- they can be created by users directly, emitted by
// the (optional) Topology compiler, or created by any other machinery (i.e. a containerlab
// runtime); the node controller treats all of these identically. The object name *is* the
// containerlab node name -- the launcher pod hostname and the node's services (`<name>` for
// exposed ports, `<name>-vx` for the inter-node fabric) all derive from it, which also means the
// namespace is the topology boundary. The spec is simply what a human would write for the node
// in a containerlab topology file (plus per-node payload and launcherProfileRef); wiring lives
// exclusively on Link objects and everything operational is stamped by the controller into
// status.
// +k8s:openapi-gen=true
// +kubebuilder:resource:path="nodes",shortName="c9snode"
// +kubebuilder:printcolumn:JSONPath=".spec.kind",name=Kind,type=string
// +kubebuilder:printcolumn:JSONPath=".spec.image",name=Image,type=string
// +kubebuilder:printcolumn:JSONPath=".status.readiness",name=Readiness,type=string
// +kubebuilder:printcolumn:JSONPath=".metadata.creationTimestamp",name=Age,type=date
type Node struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NodeSpec   `json:"spec,omitempty"`
	Status NodeStatus `json:"status,omitempty"`
}

// NodeSpec is the spec for a Node resource. It is a *flat containerlab node definition* --
// verbatim containerlab vocabulary, no wrapper -- plus clabernetes-side per-node payload fields
// and an optional LauncherProfile reference. The definition must be self-contained: expanding
// topology defaults/kinds into the node is the emitter's job (the Topology compiler and
// clabverter do this for you). Anything that is deployment *policy* rather than node payload --
// expose behavior, image pull config, launcher resources, scheduling, privileges -- lives on
// LauncherProfile objects explicitly referenced by Nodes. Unknown (i.e. newer containerlab
// vocabulary) fields are preserved by the api server but are not (yet) interpreted by
// clabernetes.
// +kubebuilder:pruning:PreserveUnknownFields
type NodeSpec struct {
	NodeDefinition `json:",inline" yaml:",inline"`

	// LauncherProfileRef optionally names the same-namespace LauncherProfile supplying launcher
	// policy. When omitted, global Config defaults are used.
	// +optional
	//nolint:lll // The qualified type and serialization tags form one declaration.
	LauncherProfileRef *k8scorev1.LocalObjectReference `json:"launcherProfileRef,omitempty" yaml:"-"`
	// FilesFromConfigMap holds files mounted from ConfigMaps into the launcher responsible for
	// this Node.
	// +listType=atomic
	// +optional
	FilesFromConfigMap []FileFromConfigMap `json:"filesFromConfigMap,omitempty" yaml:"-"`
	// FilesFromURL holds any files that the launcher for this node should fetch from a URL prior
	// to launching the node.
	// +listType=atomic
	// +optional
	FilesFromURL []FileFromURL `json:"filesFromURL,omitempty" yaml:"-"`
}

// NodeStatus is the status for a Node resource. Everything in here is an *allocation* or an
// *observation* made by the controller -- user intent never lives in the status.
type NodeStatus struct {
	// Readiness is the readiness of this node as reported by its launcher deployment -- one of
	// "ready", "notready" or "unknown".
	// +kubebuilder:validation:Enum=ready;notready;unknown
	// +optional
	Readiness string `json:"readiness,omitempty"`
	// ProbeStatuses holds the per-probe status information for this node.
	// +optional
	ProbeStatuses *NodeProbeStatuses `json:"probeStatuses,omitempty"`
	// ExposedPorts holds the expose port *allocations* for this node -- the controller assigns
	// an expose port for every (spec or auto-expose default) port and programs the node's expose
	// service from this very field; the launcher reads it to publish the ports on the pod.
	// +optional
	ExposedPorts *NodeExposedPorts `json:"exposedPorts,omitempty"`
	// Conditions contains the current conditions for this Node.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// AppliedLauncherProfile identifies the LauncherProfile successfully applied to the launcher
	// workload. It is nil when the Node uses only global Config defaults.
	// +optional
	AppliedLauncherProfile *AppliedLauncherProfileStatus `json:"appliedLauncherProfile,omitempty"`
}

// AppliedLauncherProfileStatus identifies the exact LauncherProfile revision applied to a Node.
type AppliedLauncherProfileStatus struct {
	// Name is the LauncherProfile name.
	Name string `json:"name"`
	// UID distinguishes replacement profiles that reuse a name.
	UID apimachinerytypes.UID `json:"uid"`
	// Generation is the applied LauncherProfile generation.
	Generation int64 `json:"generation"`
}

// NodeExposedPorts holds the resolved expose port allocations (and the resulting load balancer
// address if applicable) for a Node.
type NodeExposedPorts struct {
	// LoadBalancerAddress is the address of the load balancer exposing the node's ports (if the
	// expose service is of the LoadBalancer flavor).
	// +optional
	LoadBalancerAddress string `json:"loadBalancerAddress,omitempty"`
	// Ports holds the individual port allocations for the node.
	// +listType=atomic
	// +optional
	Ports []NodeExposedPort `json:"ports,omitempty"`
}

// NodeExposedPort holds a single expose port allocation for a Node.
type NodeExposedPort struct {
	// ExposePort is the allocated (or user provided) port published on the launcher pod (and
	// targeted by the expose service).
	ExposePort int `json:"exposePort"`
	// DestinationPort is the port on the (containerlab) node itself -- this is the port the
	// expose service listens on.
	DestinationPort int `json:"destinationPort"`
	// Protocol is the protocol of the port -- TCP or UDP.
	// +kubebuilder:validation:Enum=TCP;UDP
	Protocol string `json:"protocol"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// NodeList is a list of Node objects.
type NodeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []Node `json:"items"`
}
