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

// NodeProbeStatus represents the status of a single probe type on a node.
// +kubebuilder:validation:Enum=passing;failing;unknown;disabled
type NodeProbeStatus string

const (
	// NodeProbeStatusPassing indicates the probe is succeeding.
	NodeProbeStatusPassing NodeProbeStatus = "passing"

	// NodeProbeStatusFailing indicates the probe is failing.
	NodeProbeStatusFailing NodeProbeStatus = "failing"

	// NodeProbeStatusUnknown indicates the probe status is not yet known.
	NodeProbeStatusUnknown NodeProbeStatus = "unknown"

	// NodeProbeStatusDisabled indicates the probe is not configured.
	NodeProbeStatusDisabled NodeProbeStatus = "disabled"
)

// NodeProbeStatuses holds the individual probe statuses for a single node.
type NodeProbeStatuses struct {
	// StartupProbe is the status of the node's startup probe.
	StartupProbe NodeProbeStatus `json:"startupProbe"`
	// ReadinessProbe is the status of the node's readiness probe.
	ReadinessProbe NodeProbeStatus `json:"readinessProbe"`
	// LivenessProbe is the status of the node's liveness probe.
	LivenessProbe NodeProbeStatus `json:"livenessProbe"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Topology is an object that holds information about a clabernetes Topology -- that is, a valid
// topology file (ex: containerlab topology), and any associated configurations.
// +k8s:openapi-gen=true
// +kubebuilder:resource:path="topologies"
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
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.naming) || has(self.naming)", message="naming is required once set"
type TopologySpec struct {
	// Definition defines the actual set of nodes (network ones, not k8s ones!) that this Topology
	// CR represents. Historically, and probably most often, this means Topology holds a "normal"
	// containerlab topology file that will be "clabernetsified", however this could also be a "kne"
	// config, or perhaps others in the future.
	Definition Definition `json:"definition"`
	// Expose holds configurations relevant to how clabernetes exposes a topology.
	// +optional
	Expose Expose `json:"expose"`
	// Deployment holds configurations relevant to how clabernetes configures deployments that make
	// up a given topology.
	// +optional
	Deployment Deployment `json:"deployment"`
	// StatusProbes holds the configurations relevant to how clabernetes and the launcher handle
	// checking and reporting the containerlab node status
	// +optional
	StatusProbes StatusProbes `json:"statusProbes"`
	// ImagePull holds configurations relevant to how clabernetes launcher pods handle pulling
	// images.
	// +optional
	ImagePull ImagePull `json:"imagePull"`
	// Naming tells the clabernetes controller how it should name resources it creates -- that is
	// whether it should include the containerlab topology name as a prefix on resources spawned
	// from this Topology or not; this includes the actual (containerlab) node Deployment(s), as
	// well as the Service(s) for the Topology. This setting has three modes; "prefixed" -- which of
	// course includes the containerlab topology name as a prefix, "non-prefixed" which does *not*
	// include the containerlab topology name as a prefix, and "global" which defers to the global
	// config setting for this (which defaults to "prefixed").
	// "non-prefixed" mode should only be enabled when/if Topologies are deployed in their own
	// namespace -- the reason for this is simple: if two Topologies exist in the same namespace
	// with a (containerlab) node named "my-router" there will be a conflicting Deployment and
	// Services for the "my-router" (containerlab) node. Note that this field is immutable! If you
	// want to change its value you need to delete the Topology and re-create it.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="naming field is immutable, to change this value delete and re-create the Topology"
	// +kubebuilder:validation:Enum=prefixed;non-prefixed;global
	// +kubebuilder:default=global
	Naming string `json:"naming"`
	// Connectivity defines the type of connectivity to use between nodes in the topology. The
	// default behavior is to use vxlan tunnels, alternatively you can enable a more experimental
	// "slurpeeth" connectivity flavor that stuffs traffic into tcp tunnels to avoid any vxlan mtu
	// and/or fragmentation challenges.
	// +kubebuilder:validation:Enum=vxlan;slurpeeth
	// +kubebuilder:default=vxlan
	Connectivity string `json:"connectivity,omitempty"`
}

// TopologyStatus is the status for a Topology resource.
type TopologyStatus struct {
	// Kind is the topology kind this CR represents -- for example "containerlab".
	// +kubebuilder:validation:Enum=containerlab;kne
	Kind string `json:"kind"`
	// RemoveTopologyPrefix holds the "resolved" value of the RemoveTopologyPrefix field -- that is
	// if it is unset (nil) when a Topology is created, the controller will use the default global
	// config value (false); if the field is non-nil, this status field will hold the non-nil value.
	RemoveTopologyPrefix *bool `json:"removeTopologyPrefix"`
	// ReconcileHashes holds the hashes form the last reconciliation run.
	ReconcileHashes ReconcileHashes `json:"reconcileHashes"`
	// Configs is a map of node name -> containerlab config -- in other words, this is the original
	// Topology.Spec.Definition converted to containerlab "sub-topologies" The actual
	// "sub-topologies"/"sub-configs" are stored as a string -- this is the actual containerlab
	// topology that gets mounted in the launcher pod.
	Configs map[string]string `json:"configs"`
	// ExposedPorts holds a map of (containerlab not k8s!) nodes and their exposed ports
	// (via load balancer).
	ExposedPorts map[string]*ExposedPorts `json:"exposedPorts"`
	// NodeReadiness is a map of nodename to readiness status. The readiness status is as reported
	// by the k8s startup/readiness probe (which is in turn managed by the status probe
	// configuration of the topology). The possible values are "notready" and "ready", "unknown".
	NodeReadiness map[string]string `json:"nodeReadiness"`
	// TopologyReady indicates if all nodes in the topology have reported ready. This is duplicated
	// from the conditions so we can easily snag it for print columns!
	TopologyReady bool `json:"topologyReady"`
	// TopologyState is the high-level lifecycle state of the topology.
	// +optional
	TopologyState TopologyState `json:"topologyState,omitempty"`
	// NodeProbeStatuses is a map of node name to per-probe status information.
	// +optional
	NodeProbeStatuses map[string]NodeProbeStatuses `json:"nodeProbeStatuses,omitempty"`
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
