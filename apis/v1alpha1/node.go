package v1alpha1

import (
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
)

const (
	// NodeConditionProfileResolved reports whether the Node's effective
	// NodeProfile reference resolved successfully.
	NodeConditionProfileResolved = "NodeProfileResolved"
	// NodeConditionPlanApplied reports whether status was derived from the currently accepted plan.
	NodeConditionPlanApplied = "PlanApplied"
	// NodeConditionPrepared reports whether the direct preparation init container completed.
	NodeConditionPrepared = "Prepared"
	// NodeConditionConnectivityReady reports whether cold-start direct connectivity converged.
	NodeConditionConnectivityReady = "ConnectivityReady"
	// NodeConditionContainersReady reports aggregate readiness of this logical Node's direct
	// application containers.
	NodeConditionContainersReady = "ContainersReady"
	// NodeConditionLinkLifecycleAction reports the planner-declared action selected for the latest
	// direct Link-only transition. ConnectivityReady and ContainersReady report convergence.
	NodeConditionLinkLifecycleAction = "LinkLifecycleAction"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Node represents one logical containerlab node realized in a direct Kubernetes workload. Nodes
// are a primary clabernetes API -- they can be created by users directly, emitted by
// the (optional) Topology compiler, or created by any other machinery (i.e. a containerlab
// runtime); the Node controller treats all of these identically. The object name is the
// containerlab Node name and the namespace is the topology boundary. The spec is the flattened
// containerlab Node definition plus per-node payload and profileRef; wiring lives on Link
// objects and bounded allocations and observations live in status.
// +k8s:openapi-gen=true
// +kubebuilder:resource:path="nodes",shortName="c9snode"
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:JSONPath=".spec.kind",name=Kind,type=string
// +kubebuilder:printcolumn:JSONPath=".spec.image",name=Image,type=string
// +kubebuilder:printcolumn:JSONPath=".status.readiness",name=Readiness,type=string
// +kubebuilder:printcolumn:JSONPath=".status.conditions[?(@.type=='ContainersReady')].status",name=Containers,type=string,priority=1
// +kubebuilder:printcolumn:JSONPath=".status.conditions[?(@.type=='Prepared')].status",name=Prepared,type=string,priority=1
// +kubebuilder:printcolumn:JSONPath=".status.conditions[?(@.type=='ConnectivityReady')].status",name=Connectivity,type=string,priority=1
// +kubebuilder:printcolumn:JSONPath=".status.planDigest",name=Plan,type=string,priority=1
// +kubebuilder:printcolumn:JSONPath=".status.appliedProfile.name",name=Profile,type=string,priority=1
// +kubebuilder:printcolumn:JSONPath=".metadata.creationTimestamp",name=Age,type=date
type Node struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NodeSpec   `json:"spec,omitempty"`
	Status NodeStatus `json:"status,omitempty"`
}

// NodeSpec is the spec for a Node resource. It is a *flat containerlab node definition* --
// containerlab vocabulary, no wrapper -- plus clabernetes-side per-node payload fields and an
// optional NodeProfile reference. The definition must be self-contained: expanding topology
// defaults/kinds into the node is the emitter's job (the Topology compiler and clabverter do
// this for you). Anything that is deployment *policy* rather than node payload -- expose
// behavior, image pull defaults, generic resources, scheduling, and probes -- lives on
// NodeProfile objects explicitly referenced by Nodes. The containerlab vocabulary here is a
// curated subset (see NodeDefinition): fields the direct runtime cannot realize are absent, and
// unknown fields are rejected rather than silently ignored.
type NodeSpec struct {
	NodeDefinition `json:",inline" yaml:",inline"`

	// ProfileRef optionally names the same-namespace NodeProfile supplying direct
	// workload policy. When omitted, built-in defaults and supported global Config defaults are
	// used.
	// +optional
	ProfileRef *k8scorev1.LocalObjectReference `json:"profileRef,omitempty" yaml:"-"`
	// FilesFromConfigMap holds files staged from ConfigMaps for this Node's application containers.
	// +listType=atomic
	// +optional
	FilesFromConfigMap []FileFromConfigMap `json:"filesFromConfigMap,omitempty" yaml:"-"`
	// FilesFromSecret holds sensitive files projected from same-namespace Secrets into this Node's
	// direct application container.
	// +listType=atomic
	// +optional
	FilesFromSecret []FileFromSecret `json:"filesFromSecret,omitempty" yaml:"-"`
	// FilesFromURL holds files preparation must fetch and verify before the Node starts.
	// +listType=atomic
	// +optional
	FilesFromURL []FileFromURL `json:"filesFromURL,omitempty" yaml:"-"`
}

// NodeStatus is the status for a Node resource. Everything in here is an *allocation* or an
// *observation* made by the controller -- user intent never lives in the status.
type NodeStatus struct {
	// Readiness is the controller-observed readiness of this Node's active runtime -- one of
	// "ready", "notready" or "unknown".
	// +kubebuilder:validation:Enum=ready;notready;unknown
	// +optional
	Readiness string `json:"readiness,omitempty"`
	// ExposedPorts holds expose-port allocations for this Node. The controller assigns and programs
	// the direct Pod Service from this field.
	// +optional
	ExposedPorts *NodeExposedPorts `json:"exposedPorts,omitempty"`
	// Conditions contains the current conditions for this Node.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// AppliedProfile identifies the NodeProfile revision successfully applied to the
	// direct workload. It is nil when the Node uses built-in and supported global Config defaults.
	// +optional
	AppliedProfile *AppliedProfileStatus `json:"appliedProfile,omitempty"`
	// PlanDigest identifies the immutable direct device plan observed by this status.
	// +optional
	PlanDigest string `json:"planDigest,omitempty"`
	// DirectContainers contains bounded Kubernetes observations for application containers that
	// represent this logical Node. It contains no user intent or full plan data.
	// +listType=map
	// +listMapKey=id
	// +optional
	DirectContainers []NodeDirectContainerStatus `json:"directContainers,omitempty"`
	// DirectManagement contains the bounded management allocation from the applied direct plan.
	// It contains no credentials or kind-specific configuration.
	// +optional
	DirectManagement *NodeDirectManagementStatus `json:"directManagement,omitempty"`
}

// NodeDirectManagementStatus is the controller-allocated direct management identity for one
// logical Node.
type NodeDirectManagementStatus struct {
	// InterfaceName is the package-selected management interface in the Pod namespace.
	InterfaceName string `json:"interfaceName"`
	// IPv4 is the allocated IPv4 address and prefix.
	// +optional
	IPv4 string `json:"ipv4,omitempty"`
	// IPv4Gateway is the source-specific IPv4 gateway.
	// +optional
	IPv4Gateway string `json:"ipv4Gateway,omitempty"`
	// IPv6 is the allocated IPv6 address and prefix.
	// +optional
	IPv6 string `json:"ipv6,omitempty"`
	// IPv6Gateway is the source-specific IPv6 gateway.
	// +optional
	IPv6Gateway string `json:"ipv6Gateway,omitempty"`
}

// NodeDirectContainerStatus is one plan-addressed application-container observation.
type NodeDirectContainerStatus struct {
	// ID is the stable runtime-neutral container identity from the applied plan.
	ID string `json:"id"`
	// Name is the deterministic Kubernetes container name used with kubectl's -c option.
	Name string `json:"name"`
	// ComponentID is the imported component identity when this is not the logical primary.
	// +optional
	ComponentID string `json:"componentID,omitempty"`
	// State is one of unknown, waiting, running, or terminated.
	State string `json:"state"`
	// Ready is the Kubernetes application-container readiness observation.
	Ready bool `json:"ready"`
	// RestartCount is the kubelet-observed restart count.
	RestartCount int32 `json:"restartCount"`
	// ImageID is the kubelet-observed immutable image identity when available.
	// +optional
	ImageID string `json:"imageID,omitempty"`
}

// AppliedProfileStatus identifies the exact NodeProfile revision applied to a Node.
type AppliedProfileStatus struct {
	// Name is the NodeProfile name.
	Name string `json:"name"`
	// UID distinguishes replacement profiles that reuse a name.
	UID apimachinerytypes.UID `json:"uid"`
	// Generation is the applied NodeProfile generation.
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
	// ExposePort is the allocated Service port targeting the direct device Pod.
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
