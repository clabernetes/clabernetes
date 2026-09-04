package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TopologyState represents the high-level lifecycle state of a Topology.
// +kubebuilder:validation:Enum=deploying;running;degraded;deployfailed
type TopologyState string

const (
	// TopologyStateDeploying indicates the topology is being deployed and not all nodes are ready.
	TopologyStateDeploying TopologyState = "deploying"

	// TopologyStateRunning indicates all nodes in the topology are ready.
	TopologyStateRunning TopologyState = "running"

	// TopologyStateDegraded indicates the topology was previously running but one or more nodes
	// are no longer ready.
	TopologyStateDegraded TopologyState = "degraded"

	// TopologyStateDeployFailed indicates one or more nodes have terminally failed before the
	// topology ever reached the running state.
	TopologyStateDeployFailed TopologyState = "deployfailed"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Topology is an object that holds information about a clabernetes Topology -- that is, a valid
// topology file (ex: containerlab topology), and any associated configurations.
// +k8s:openapi-gen=true
// +kubebuilder:resource:path="topologies"
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:JSONPath=".status.kind",name=Kind,type=string
// +kubebuilder:printcolumn:JSONPath=".status.topologyState",name=State,type=string
// +kubebuilder:printcolumn:JSONPath=".metadata.creationTimestamp",name=Age,type=date
// +kubebuilder:printcolumn:JSONPath=".status.topologyReady",name=Ready,type=boolean
type Topology struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TopologySpec   `json:"spec,omitempty"`
	Status TopologyStatus `json:"status,omitempty"`
}

// TopologySpec is the spec for a Topology resource.
type TopologySpec struct {
	// Definition defines the actual set of nodes (network ones, not k8s ones!) that this Topology
	// CR represents -- a containerlab topology file that will be "clabernetsified".
	Definition Definition `json:"definition"`
	// Expose holds configurations relevant to how clabernetes exposes a topology.
	// +optional
	Expose Expose `json:"expose"`
	// Deployment holds portable policy compiled into direct Node workloads.
	// +optional
	Deployment Deployment `json:"deployment"`
	// StatusProbes holds additional direct application readiness policy.
	// +optional
	StatusProbes StatusProbes `json:"statusProbes"`
	// ImagePull holds Kubernetes-native defaults compiled into direct device Pods.
	// +optional
	ImagePull ImagePull `json:"imagePull"`
}

// TopologyStatus is the status for a Topology resource. Note that all *per node* (and per link)
// state lives on the emitted Node and Link custom resources rather than here -- the Topology
// only aggregates, which keeps its size bounded regardless of how big the topology definition
// is.
type TopologyStatus struct {
	// Kind is the topology kind this CR represents -- "containerlab".
	// +kubebuilder:validation:Enum=containerlab
	Kind string `json:"kind"`
	// ObservedGeneration is the metadata.generation of the Topology whose compiled child
	// resources were most recently applied by the controller. Clients compare it against
	// metadata.generation to know whether the reported readiness refers to the current
	// definition.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// NodeCount is the number of (containerlab) nodes this Topology compiled to.
	// +optional
	NodeCount int `json:"nodeCount,omitempty"`
	// ReadyNodeCount is the number of nodes of this Topology that currently report ready.
	// +optional
	ReadyNodeCount int `json:"readyNodeCount,omitempty"`
	// LinkCount is the number of links this Topology compiled to.
	// +optional
	LinkCount int `json:"linkCount,omitempty"`
	// TopologyReady indicates if all nodes in the topology have reported ready. This is duplicated
	// from the conditions so we can easily snag it for print columns!
	TopologyReady bool `json:"topologyReady"`
	// TopologyState is the high-level lifecycle state of the topology.
	// +optional
	TopologyState TopologyState `json:"topologyState,omitempty"`
	// Error holds a bounded controller error that prevents the Topology from realizing its desired
	// child resources. An empty value means no such error is currently reported.
	// +optional
	Error string `json:"error,omitempty"`
	// Conditions is a list of conditions for the topology custom resource.
	// +listType=atomic
	Conditions []metav1.Condition `json:"conditions"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// TopologyList is a list of Topology objects.
type TopologyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []Topology `json:"items"`
}
