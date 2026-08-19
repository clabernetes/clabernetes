package v1alpha1

import (
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// LauncherProfile holds reusable Kubernetes and launcher realization policy for Nodes. A Node
// applies at most one LauncherProfile through spec.launcherProfileRef; fields omitted from the
// profile inherit the global Config defaults.
// +k8s:openapi-gen=true
// +kubebuilder:resource:path="launcherprofiles",shortName="c9sprofile"
// +kubebuilder:printcolumn:JSONPath=".metadata.creationTimestamp",name=Age,type=date
type LauncherProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   LauncherProfileSpec   `json:"spec,omitempty"`
	Status LauncherProfileStatus `json:"status,omitempty"`
}

// LauncherProfileSpec is the spec for a LauncherProfile resource.
type LauncherProfileSpec struct {
	// Expose holds configurations relevant to how Nodes using this profile are exposed.
	// +optional
	Expose *LauncherProfileExpose `json:"expose,omitempty"`
	// ImagePull holds configurations relevant to how launcher Pods handle pulling images.
	// +optional
	ImagePull *LauncherProfileImagePull `json:"imagePull,omitempty"`
	// Resources holds the Kubernetes resource requirements for launcher Pods.
	// +optional
	Resources *k8scorev1.ResourceRequirements `json:"resources,omitempty"`
	// Scheduling holds launcher Pod scheduling settings.
	// +optional
	Scheduling *Scheduling `json:"scheduling,omitempty"`
	// Deployment holds launcher deployment settings.
	// +optional
	Deployment *LauncherProfileDeployment `json:"deployment,omitempty"`
	// StatusProbes holds the configurations used to check and report Node status.
	// +optional
	StatusProbes *StatusProbes `json:"statusProbes,omitempty"`
	// Mgmt holds shared direct management-overlay allocation policy.
	// +optional
	Mgmt *ManagementPolicy `json:"mgmt,omitempty"`
}

// LauncherProfileExpose holds the expose policy fields of a LauncherProfile. Pointers distinguish
// an omitted value from an explicit false value.
type LauncherProfileExpose struct {
	// DisableExpose indicates if exposing Nodes via a Service should be disabled.
	// +optional
	DisableExpose *bool `json:"disableExpose,omitempty"`
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

// LauncherProfileImagePull holds Kubernetes-native image pull policy for direct device Pods.
type LauncherProfileImagePull struct {
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

// LauncherProfileDeployment holds portable direct workload persistence policy.
type LauncherProfileDeployment struct {
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

// LauncherProfileStatus is the status for a LauncherProfile resource.
type LauncherProfileStatus struct{}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// LauncherProfileList is a list of LauncherProfile objects.
type LauncherProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []LauncherProfile `json:"items"`
}
