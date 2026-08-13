package v1alpha1

import k8scorev1 "k8s.io/api/core/v1"

// ConfigMetadata holds "global" configuration data that will be applied to all objects created by
// the clabernetes controller.
type ConfigMetadata struct {
	// Annotations holds key/value pairs that should be set as annotations on clabernetes created
	// resources. Note that (currently?) there is no input validation here, but this data must be
	// valid kubernetes annotation data.
	// +optional
	Annotations map[string]string `json:"annotations"`
	// Labels holds key/value pairs that should be set as labels on clabernetes created resources.
	// Note that (currently?) there is no input validation here, but this data must be valid
	// kubernetes label data.
	// +optional
	Labels map[string]string `json:"labels"`
}

// ResourceMap defined type alias to be used below.
type ResourceMap map[string]map[string]*k8scorev1.ResourceRequirements

// ConfigDeployment holds "global" or "default" configurations related to clabernetes spawned
// deployments. In the future this will likely include more of the "normal" (topology-level)
// deployment configs (ex: persistence, or maybe files from url).
type ConfigDeployment struct {
	// ResourcesDefault is the default set of resources for clabernetes launcher pods. This is used
	// only as a last option if a Topology does not have resources, and there are no resources for
	// the given containerlab kind/type
	// +optional
	ResourcesDefault *k8scorev1.ResourceRequirements `json:"resourcesDefault"`
	// ResourcesByContainerlabKind is a mapping of container lab kind -> type -> default resource
	// settings. Note that a key value of "default" in the inner map will apply the given resources
	// for any pod of that containerlab *kind*. For example:
	// {
	//   "srl": {
	//     "default": DEFAULT RESOURCES FOR KIND "srl",
	//     "ixr10": RESOURCES FOR KIND "srl", TYPE "ixr10"
	// }
	// Given resources as above, a containerlab node of kind "srl" and "type" ixr10" would get the
	// specific resources as allocated in the ixr10 key, whereas a containerlab kind of "srl" and
	// "type" unset or "ixr6" would get the "default" resource settings. To apply global default
	// resources, regardless of containerlab kind/type, use the `resourcesDefault` field.
	// +optional
	ResourcesByContainerlabKind ResourceMap `json:"resourcesByContainerlabKind"`
	// NodeSelectorsByImage is a mapping of image glob pattern as key and node selectors (value)
	// to apply to each deployment. Note that in case of multiple matches, the longest (with
	// most characters) will take precedence. A config example:
	// {
	//   "internal.io/nokia_sros*": {"node-flavour": "baremetal"},
	//   "ghcr.io/nokia/srlinux*":  {"node-flavour": "amd64"},
	//   "default":                 {"node-flavour": "cheap"},
	// }.
	// +optional
	NodeSelectorsByImage map[string]map[string]string `json:"nodeSelectorsByImage"`
	// PrivilegedLauncher, when true, sets the launcher containers to privileged. By default, we do
	// our best to *not* need this/set this, and instead set only the capabilities we need, however
	// its possible that some containers launched by the launcher may need/want more capabilities,
	// so this flag exists for users to bypass the default settings and enable fully privileged
	// launcher pods.
	// +optional
	PrivilegedLauncher bool `json:"privilegedLauncher"`
	// ContainerlabDebug sets the `--debug` flag when invoking containerlab in the launcher pods.
	// This is disabled by default.
	// +optional
	ContainerlabDebug bool `json:"containerlabDebug"`
	// ContainerlabTimeout sets the `--timeout` flag when invoking containerlab in the launcher
	// pods.
	// +optional
	ContainerlabTimeout string `json:"containerlabTimeout"`
	// ContainerlabVersion sets a custom version to use for containerlab -- when set this will cause
	// the launcher pods to download and use this specific version of containerlab. Setting a bad
	// version (version that doesn't exist/typo/etc.) will cause pods to fail to launch, so be
	// careful! You never "need" to this as the publicly available launcher image will always be
	// built with a (reasonably) up to date containerlab version, this setting exists in case you
	// want to pin back to an older version for some reason or you want to be bleeding edge with
	// some new feature (but do note that just because it exists in containerlab doesn't
	// *necessarily* mean it will be auto-working in clabernetes!
	// 0.78.0 is the floor -- the Node vocabulary includes fields older releases reject outright.
	// +optional
	ContainerlabVersion string `json:"containerlabVersion,omitempty"`
	// LauncherImage sets the default launcher image to use when spawning launcher deployments.
	// +kubebuilder:default="ghcr.io/clabernetes/clabernetes/clabernetes-launcher:latest"
	LauncherImage string `json:"launcherImage"`
	// LauncherImagePullPolicy sets the default launcher image pull policy to use when spawning
	// launcher deployments.
	// +kubebuilder:validation:Enum=IfNotPresent;Always;Never
	// +kubebuilder:default=IfNotPresent
	LauncherImagePullPolicy string `json:"launcherImagePullPolicy"`
	// LauncherLogLevel sets the launcher clabernetes worker log level -- this overrides whatever
	// is set on the controllers env vars for this topology. Note: omitempty because empty str does
	// not satisfy enum of course.
	// +kubebuilder:validation:Enum=disabled;critical;warn;info;debug
	// +optional
	LauncherLogLevel string `json:"launcherLogLevel,omitempty"`
	// ExtraEnv is a list of additional environment variables to set on the launcher container. The
	// values here are applied to *all* launchers since this is the global config after all!
	// +optional
	// +listType=atomic
	ExtraEnv []k8scorev1.EnvVar `json:"extraEnv"`
}

// ConfigImagePull holds configurations relevant to how clabernetes launcher pods handle pulling
// images.
type ConfigImagePull struct {
	// PullThroughOverride allows for overriding the image pull through mode for this
	// particular topology.
	// +kubebuilder:validation:Enum=auto;always;never
	// +optional
	PullThroughOverride string `json:"pullThroughOverride,omitempty"`
	// CRISockOverride allows for overriding the path of the CRI sock that is mounted in the
	// launcher pods (if/when image pull through mode is auto or always). This can be useful if,
	// for example, the CRI sock is in a "non-standard" location like K3s which puts the containerd
	// sock at `/run/k3s/containerd/containerd.sock` rather than the "normal" (whatever that means)
	// location of `/run/containerd/containerd.sock`. The value must end with "containerd.sock" for
	// now, in the future maybe crio support will be added.
	// +kubebuilder:validation:Pattern=(.*containerd\.sock)
	// +optional
	CRISockOverride string `json:"criSockOverride,omitempty"`
	// CRIKindOverride allows for overriding the auto discovered cri flavor of the cluster -- this
	// may be useful if we fail to parse the cri kind for some reason, or in mixed cri flavor
	// clusters -- however in the latter case, make sure that if you are using image pull through
	// that clabernetes workloads are only run on the nodes of the cri kind specified here!
	// +kubebuilder:validation:Enum=containerd
	// +optional
	CRIKindOverride string `json:"criKindOverride,omitempty"`
	// CRIHostsDir is an optional host directory containing containerd registry hosts
	// configuration. When set and the effective CRI is containerd, the directory is mounted
	// read-only into pull-through launchers both at its original path (so certificate paths rooted
	// in this directory remain valid) and at containerd's conventional certs.d path used by
	// nerdctl.
	// +kubebuilder:validation:Pattern=^/.+
	// +kubebuilder:validation:XValidation:rule="self.matches('^/.*[^/].*$') && !self.matches('(^|/)[.][.]?(/|$)')",message="criHostsDir must be an absolute non-root path without '.' or '..' segments"
	// +optional
	CRIHostsDir string `json:"criHostsDir,omitempty"`
	// DockerDaemonConfig allows for setting a default docker daemon config for launcher pods
	// with the specified secret. The secret *must be present in the namespace of any given
	// topology* -- so if you are configuring this at the "global config" level, ensure that you are
	// deploying topologies into a specific namespace, or have ensured there is a secret of the
	// given name in every namespace you wish to deploy a topology to. When set, insecure registries
	// config option is ignored as it is assumed you are handling that in the given docker config.
	// Note that the secret *must* contain a key "daemon.json" -- as this secret will be mounted to
	// /etc/docker and docker will be expecting the config at /etc/docker/daemon.json.
	// +optional
	DockerDaemonConfig string `json:"dockerDaemonConfig,omitempty"`
	// DockerConfig allows for setting the docker user (for root) config for all launchers in this
	// topology. The secret *must be present in the namespace of this topology*. The secret *must*
	// contain a key "config.json" -- as this secret will be mounted to /root/.docker/config.json
	// and as such wil be utilized when doing docker-y things -- this means you can put auth things
	// in here in the event your cluster doesn't support the preferred image pull through option.
	// +optional
	DockerConfig string `json:"dockerConfig,omitempty"`
}
