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
	// Mgmt temporarily retains shared containerlab management network settings for Topology
	// compatibility. Its final ownership is intentionally deferred.
	// +optional
	Mgmt *MgmtNet `json:"mgmt,omitempty"`
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

// LauncherProfileImagePull holds image pull policy fields for launcher Pods.
type LauncherProfileImagePull struct {
	// InsecureRegistries is a slice of insecure registries to configure in launcher Pods.
	// +optional
	InsecureRegistries InsecureRegistries `json:"insecureRegistries,omitempty"`
	// PullThroughOverride overrides the image pull-through mode.
	// +kubebuilder:validation:Enum=auto;always;never
	// +optional
	PullThroughOverride string `json:"pullThroughOverride,omitempty"`
	// PullSecrets provides Secrets to use when pulling images.
	// +listType=set
	// +optional
	PullSecrets []string `json:"pullSecrets,omitempty"`
	// DockerDaemonConfig names the Secret containing daemon.json.
	// +optional
	DockerDaemonConfig *string `json:"dockerDaemonConfig,omitempty"`
	// DockerConfig names the Secret containing config.json.
	// +optional
	DockerConfig *string `json:"dockerConfig,omitempty"`
}

// LauncherProfileDeployment holds launcher deployment policy fields.
type LauncherProfileDeployment struct {
	// PrivilegedLauncher configures launcher containers as privileged.
	// +optional
	PrivilegedLauncher *bool `json:"privilegedLauncher,omitempty"`
	// Persistence enables persistence of the containerlab working directory.
	// +optional
	Persistence *Persistence `json:"persistence,omitempty"`
	// ContainerlabDebug sets the containerlab --debug flag.
	// +optional
	ContainerlabDebug *bool `json:"containerlabDebug,omitempty"`
	// ContainerlabTimeout sets the containerlab --timeout flag.
	// +optional
	ContainerlabTimeout *string `json:"containerlabTimeout,omitempty"`
	// ContainerlabVersion selects a custom containerlab version, downloaded by the launcher at
	// startup in place of the one baked into the image. 0.78.0 is the floor: the Node spec
	// vocabulary includes fields (i.e. privileged, tmpfs, security-opts) that older containerlab
	// releases reject outright, so pinning further back makes those nodes fail to deploy.
	// +optional
	ContainerlabVersion *string `json:"containerlabVersion,omitempty"`
	// LauncherImage selects the launcher image.
	// +optional
	LauncherImage string `json:"launcherImage,omitempty"`
	// LauncherImagePullPolicy selects the launcher image pull policy.
	// +kubebuilder:validation:Enum=IfNotPresent;Always;Never
	// +optional
	LauncherImagePullPolicy string `json:"launcherImagePullPolicy,omitempty"`
	// LauncherLogLevel selects the launcher log level.
	// +kubebuilder:validation:Enum=disabled;critical;warn;info;debug
	// +optional
	LauncherLogLevel string `json:"launcherLogLevel,omitempty"`
	// ExtraEnv is a list of additional environment variables for the launcher container.
	// +listType=atomic
	// +optional
	ExtraEnv []k8scorev1.EnvVar `json:"extraEnv,omitempty"`
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
