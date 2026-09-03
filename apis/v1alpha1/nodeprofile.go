package v1alpha1

import (
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// NodeProfile holds reusable Kubernetes realization policy for Nodes. A Node applies at most
// one NodeProfile through spec.profileRef; fields omitted from the profile use built-in defaults
// or, where supported, global Config defaults.
// +k8s:openapi-gen=true
// +kubebuilder:resource:path="nodeprofiles",shortName="c9sprofile"
// +kubebuilder:printcolumn:JSONPath=".metadata.creationTimestamp",name=Age,type=date
type NodeProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NodeProfileSpec   `json:"spec,omitempty"`
	Status NodeProfileStatus `json:"status,omitempty"`
}

// NodeProfileSpec is the spec for a NodeProfile resource.
type NodeProfileSpec struct {
	// Expose holds configurations relevant to how Nodes using this profile are exposed.
	// +optional
	Expose *NodeProfileExpose `json:"expose,omitempty"`
	// ImagePull holds configurations relevant to how device Pods handle pulling images.
	// +optional
	ImagePull *NodeProfileImagePull `json:"imagePull,omitempty"`
	// Resources holds the Kubernetes resource requirements for device Pods.
	// +optional
	Resources *k8scorev1.ResourceRequirements `json:"resources,omitempty"`
	// Scheduling holds device Pod scheduling settings.
	// +optional
	Scheduling *Scheduling `json:"scheduling,omitempty"`
	// Deployment holds device workload settings.
	// +optional
	Deployment *NodeProfileDeployment `json:"deployment,omitempty"`
	// StatusProbes holds the configurations used to check and report Node status.
	// +optional
	StatusProbes *StatusProbes `json:"statusProbes,omitempty"`
	// Mgmt holds shared direct management-overlay allocation policy.
	// +optional
	Mgmt *ManagementPolicy `json:"mgmt,omitempty"`
}

// NodeProfileExpose holds the expose policy fields of a NodeProfile. Pointers distinguish
// an omitted value from an explicit false value.
type NodeProfileExpose struct {
	// DisableAutoExpose disables automatic exposure of the default port list.
	// +optional
	DisableAutoExpose *bool `json:"disableAutoExpose,omitempty"`
	// ExposeType configures the Service type used for exposing Nodes.
	// +kubebuilder:validation:Enum=None;ClusterIP;Headless;LoadBalancer
	// +optional
	ExposeType string `json:"exposeType,omitempty"`
	// UseNodeMgmtIpv4Address assigns a Node's management IPv4 address as its LoadBalancerIP.
	// +optional
	UseNodeMgmtIpv4Address *bool `json:"useNodeMgmtIpv4Address,omitempty"`
	// UseNodeMgmtIpv6Address assigns a Node's management IPv6 address as its LoadBalancerIP.
	// +optional
	UseNodeMgmtIpv6Address *bool `json:"useNodeMgmtIpv6Address,omitempty"`
}

// NodeProfileImagePull holds Kubernetes-native image pull policy for direct device Pods.
type NodeProfileImagePull struct {
	// Policy is the default Kubernetes pull policy for application containers whose flattened Node
	// definition does not explicitly declare one.
	// +kubebuilder:validation:Enum=IfNotPresent;Always;Never
	// +optional
	Policy string `json:"policy,omitempty"`
	// PullSecrets provides same-namespace Docker-config Secrets to the kubelet through
	// Pod.spec.imagePullSecrets. Credentials are not mounted into application containers.
	// +listType=set
	// +optional
	PullSecrets []string `json:"pullSecrets,omitempty"`
}

// NodeProfileDeployment holds portable direct workload persistence policy.
type NodeProfileDeployment struct {
	// Persistence enables persistence of the containerlab working directory.
	// +optional
	Persistence *Persistence `json:"persistence,omitempty"`
}

// ManagementPolicy defines direct management-overlay address allocation. Docker network identity,
// MTU, and external-access controls are deliberately absent.
type ManagementPolicy struct {
	// IPv4Subnet is the IPv4 management subnet.
	// +optional
	IPv4Subnet string `json:"ipv4-subnet,omitempty"`
	// IPv4Gw is the IPv4 management gateway.
	// +optional
	IPv4Gw string `json:"ipv4-gw,omitempty"`
	// IPv4Range is the IPv4 allocation range within IPv4Subnet.
	// +optional
	IPv4Range string `json:"ipv4-range,omitempty"`
	// IPv6Subnet is the IPv6 management subnet.
	// +optional
	IPv6Subnet string `json:"ipv6-subnet,omitempty"`
	// IPv6Gw is the IPv6 management gateway.
	// +optional
	IPv6Gw string `json:"ipv6-gw,omitempty"`
	// IPv6Range is the IPv6 allocation range within IPv6Subnet.
	// +optional
	IPv6Range string `json:"ipv6-range,omitempty"`
}

// NodeProfileStatus is the status for a NodeProfile resource.
type NodeProfileStatus struct{}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// NodeProfileList is a list of NodeProfile objects.
type NodeProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []NodeProfile `json:"items"`
}
