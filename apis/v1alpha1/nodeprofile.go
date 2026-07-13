package v1alpha1

import (
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// NodeProfile holds *deployment policy* for a set of Nodes -- everything that is fleet policy
// rather than per-node payload: expose behavior, image pull configuration, launcher pod
// resources, scheduling, privileges/persistence, status probes, management network and
// connectivity flavor. Profiles select Nodes with a standard label selector, which keeps
// emitters of Nodes (users, the Topology compiler, containerlab tooling) completely decoupled
// from deployment policy -- an emitter never needs to know profiles exist to emit a valid Node.
// Policy resolution for a Node is deliberately boring: the helm-managed global Config is the
// base (lowest precedence), then all matching NodeProfiles merge over it *per field* in
// ascending priority order (name breaks ties). There are no per-Node overrides -- if a single
// node needs special treatment, give it a label and a profile. The resolved chain is recorded in
// each Node's status.appliedProfiles.
// +k8s:openapi-gen=true
// +kubebuilder:resource:path="nodeprofiles",shortName="c9sprofile"
// +kubebuilder:printcolumn:JSONPath=".spec.priority",name=Priority,type=integer
// +kubebuilder:printcolumn:JSONPath=".metadata.creationTimestamp",name=Age,type=date
type NodeProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NodeProfileSpec   `json:"spec,omitempty"`
	Status NodeProfileStatus `json:"status,omitempty"`
}

// NodeProfileSpec is the spec for a NodeProfile resource.
type NodeProfileSpec struct {
	// NodeSelector selects the Nodes (in the profile's namespace) this profile applies to. An
	// empty (or omitted) selector selects *every* Node in the namespace.
	// +optional
	NodeSelector metav1.LabelSelector `json:"nodeSelector,omitempty"`
	// Priority orders profiles when more than one selects a Node -- higher priority wins per
	// field on overlap, profile name breaks ties.
	// +optional
	Priority int `json:"priority,omitempty"`
	// Expose holds configurations relevant to how the nodes selected by this profile are exposed.
	// +optional
	Expose *NodeProfileExpose `json:"expose,omitempty"`
	// ImagePull holds configurations relevant to how the launcher pods for the selected nodes
	// handle pulling images.
	// +optional
	ImagePull *NodeProfileImagePull `json:"imagePull,omitempty"`
	// Resources holds the kubernetes resource requirements for the launcher pods of the selected
	// nodes.
	// +optional
	Resources *k8scorev1.ResourceRequirements `json:"resources,omitempty"`
	// Scheduling holds information about how the launcher pods of the selected nodes should be
	// configured with respect to "scheduling" things (node selector/tolerations).
	// +optional
	Scheduling *Scheduling `json:"scheduling,omitempty"`
	// Deployment holds launcher deployment settings (privileges, persistence, launcher
	// image/log level, containerlab flags, extra env/files) for the selected nodes.
	// +optional
	Deployment *NodeProfileDeployment `json:"deployment,omitempty"`
	// StatusProbes holds the configurations relevant to how clabernetes and the launcher check
	// and report the (containerlab) node status for the selected nodes.
	// +optional
	StatusProbes *StatusProbes `json:"statusProbes,omitempty"`
	// Mgmt holds the containerlab management network settings the launchers of the selected
	// nodes run their (pod local) management network with.
	// +optional
	Mgmt *MgmtNet `json:"mgmt,omitempty"`
	// Connectivity defines the type of connectivity to use between nodes -- "vxlan" (default) or
	// the experimental "slurpeeth". Both sides of a link must resolve the same flavor for the
	// tunnel to come up, so this is typically set by an all-nodes profile (or the Topology
	// compiler).
	// +kubebuilder:validation:Enum=vxlan;slurpeeth
	// +optional
	Connectivity string `json:"connectivity,omitempty"`
}

// NodeProfileExpose holds the expose policy fields of a NodeProfile. The fields mirror the
// Topology expose block -- pointers distinguish "unset" (defer down the precedence chain) from
// an explicit false/empty value.
type NodeProfileExpose struct {
	// DisableExpose indicates if exposing selected nodes via a service should be disabled -- by
	// default any ports in a node definition (plus the auto-expose defaults) are exposed.
	// +optional
	DisableExpose *bool `json:"disableExpose,omitempty"`
	// DisableAutoExpose disables the automagic exposing of the default port list -- see the
	// Topology CRD (or docs) for that list; when disabled only ports explicitly listed in the
	// node definition are exposed.
	// +optional
	DisableAutoExpose *bool `json:"disableAutoExpose,omitempty"`
	// ExposeType configures the service type used for exposing the selected nodes -- one of
	// "None", "ClusterIP", "Headless" or "LoadBalancer" (default).
	// +kubebuilder:validation:Enum=None;ClusterIP;Headless;LoadBalancer
	// +optional
	ExposeType string `json:"exposeType,omitempty"`
	// UseNodeMgmtIpv4Address assigns each selected node's `mgmt-ipv4` address as the
	// LoadBalancerIP of its expose service (LoadBalancer expose type only).
	// +optional
	UseNodeMgmtIpv4Address *bool `json:"useNodeMgmtIpv4Address,omitempty"`
	// UseNodeMgmtIpv6Address assigns each selected node's `mgmt-ipv6` address as the
	// LoadBalancerIP of its expose service (LoadBalancer expose type only).
	// +optional
	UseNodeMgmtIpv6Address *bool `json:"useNodeMgmtIpv6Address,omitempty"`
}

// NodeProfileImagePull holds the image pull policy fields of a NodeProfile -- the fields mirror
// the Topology imagePull block.
type NodeProfileImagePull struct {
	// InsecureRegistries is a slice of strings of insecure registries to configure in the
	// launcher pods.
	// +optional
	InsecureRegistries InsecureRegistries `json:"insecureRegistries,omitempty"`
	// PullThroughOverride allows for overriding the image pull through mode for the launcher
	// pods of the selected nodes.
	// +kubebuilder:validation:Enum=auto;always;never
	// +optional
	PullThroughOverride string `json:"pullThroughOverride,omitempty"`
	// PullSecrets allows for providing secret(s) to use when pulling the image. This is only
	// applicable *if* image pull through mode is auto or always.
	// +listType=set
	// +optional
	PullSecrets []string `json:"pullSecrets,omitempty"`
	// DockerDaemonConfig sets the secret (in the node's namespace, with a "daemon.json" key)
	// holding the docker daemon config to mount in the launchers.
	// +optional
	DockerDaemonConfig string `json:"dockerDaemonConfig,omitempty"`
	// DockerConfig sets the secret (in the node's namespace, with a "config.json" key) holding
	// the docker (root user) config to mount in the launchers.
	// +optional
	DockerConfig string `json:"dockerConfig,omitempty"`
}

// NodeProfileDeployment holds the launcher deployment policy fields of a NodeProfile -- the
// fields mirror the Topology deployment block (minus the per node maps, which are covered by
// profile selectors instead).
type NodeProfileDeployment struct {
	// PrivilegedLauncher, when true, sets the launcher containers to privileged -- see the
	// Topology CRD (or docs) for the "not so privileged" mode details.
	// +optional
	PrivilegedLauncher *bool `json:"privilegedLauncher,omitempty"`
	// FilesFromConfigMap is a slice of FileFromConfigMap that define the configmap/path and
	// path on the launcher pod that the file should be mounted to.
	// +listType=atomic
	// +optional
	FilesFromConfigMap []FileFromConfigMap `json:"filesFromConfigMap,omitempty"`
	// Persistence enables persisting the containerlab working directory of the selected nodes'
	// launchers in a PVC.
	// +optional
	Persistence *Persistence `json:"persistence,omitempty"`
	// ContainerlabDebug sets the `--debug` flag when invoking containerlab in the launcher pods.
	// +optional
	ContainerlabDebug *bool `json:"containerlabDebug,omitempty"`
	// ContainerlabTimeout sets the `--timeout` flag when invoking containerlab in the launcher
	// pods.
	// +optional
	ContainerlabTimeout string `json:"containerlabTimeout,omitempty"`
	// ContainerlabVersion sets a custom version to use for containerlab in the launcher pods.
	// +optional
	ContainerlabVersion string `json:"containerlabVersion,omitempty"`
	// LauncherImage sets the launcher image to use when spawning the launcher deployments.
	// +optional
	LauncherImage string `json:"launcherImage,omitempty"`
	// LauncherImagePullPolicy sets the launcher image pull policy -- one of
	// IfNotPresent/Always/Never.
	// +kubebuilder:validation:Enum=IfNotPresent;Always;Never
	// +optional
	LauncherImagePullPolicy string `json:"launcherImagePullPolicy,omitempty"`
	// LauncherLogLevel sets the launcher log level -- one of
	// disabled/critical/warn/info/debug.
	// +kubebuilder:validation:Enum=disabled;critical;warn;info;debug
	// +optional
	LauncherLogLevel string `json:"launcherLogLevel,omitempty"`
	// ExtraEnv is a list of additional environment variables to set on the launcher container.
	// +listType=atomic
	// +optional
	ExtraEnv []k8scorev1.EnvVar `json:"extraEnv,omitempty"`
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
