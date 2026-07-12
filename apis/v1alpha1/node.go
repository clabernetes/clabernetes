package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Node is an object that represents a single (containerlab) node of a clabernetes Topology. Nodes
// are created and managed by the clabernetes controller -- one per launcher pod -- and hold the
// rendered sub-topology (and any related per-node data) for that node, as well as the per-node
// status information. Storing this data per-node avoids topology-wide controller output and its
// amplification. A Node still grows with the size and degree of the launcher group it represents,
// but not with unrelated nodes elsewhere in the topology.
// +k8s:openapi-gen=true
// +kubebuilder:resource:path="nodes",shortName="c9snode"
// +kubebuilder:printcolumn:JSONPath=".spec.topologyName",name=Topology,type=string
// +kubebuilder:printcolumn:JSONPath=".spec.nodeName",name=Node,type=string
// +kubebuilder:printcolumn:JSONPath=".status.readiness",name=Readiness,type=string
// +kubebuilder:printcolumn:JSONPath=".metadata.creationTimestamp",name=Age,type=date
type Node struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NodeSpec   `json:"spec,omitempty"`
	Status NodeStatus `json:"status,omitempty"`
}

// NodeSpec is the spec for a Node resource.
type NodeSpec struct {
	// TopologyName is the name of the Topology this Node belongs to.
	TopologyName string `json:"topologyName"`
	// NodeName is the name of the (containerlab) node this resource represents -- for "grouped"
	// nodes (network-mode: container:<primary>) this is the primary node of the group.
	NodeName string `json:"nodeName"`
	// Config is the rendered containerlab "sub-topology" for this node -- this is the topology
	// that gets mounted/loaded in the launcher pod for this node.
	Config string `json:"config"`
	// FilesFromURL holds any files that the launcher for this node should fetch from a URL prior
	// to launching the node.
	// +optional
	// +listType=atomic
	FilesFromURL []FileFromURL `json:"filesFromURL,omitempty"`
	// ImagePullSecrets holds the secret names the launcher may use when pulling images via the
	// cluster CRI.
	// +optional
	// +listType=set
	ImagePullSecrets []string `json:"imagePullSecrets,omitempty"`
}

// NodeStatus is the status for a Node resource.
type NodeStatus struct {
	// Readiness is the readiness of this node as reported by its launcher deployment -- one of
	// "ready", "notready" or "unknown".
	// +kubebuilder:validation:Enum=ready;notready;unknown
	// +optional
	Readiness string `json:"readiness,omitempty"`
	// ProbeStatuses holds the per-probe status information for this node.
	// +optional
	ProbeStatuses *NodeProbeStatuses `json:"probeStatuses,omitempty"`
	// ExposedPorts holds the ports (and load balancer address if applicable) exposed for this
	// node.
	// +optional
	ExposedPorts *ExposedPorts `json:"exposedPorts,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// NodeList is a list of Node objects.
type NodeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []Node `json:"items"`
}
