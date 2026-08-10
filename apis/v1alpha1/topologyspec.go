package v1alpha1

import k8scorev1 "k8s.io/api/core/v1"

// FileFromConfigMap represents a file that you would like to mount (from a configmap) in the
// launcher pod for a given node.
type FileFromConfigMap struct {
	// FilePath is the path to mount the file.
	FilePath string `json:"filePath"`
	// ConfigMapName is the name of the configmap to mount.
	ConfigMapName string `json:"configMapName"`
	// ConfigMapPath is the path/key in the configmap to mount, if not specified the configmap will
	// be mounted without a sub-path.
	// +optional
	ConfigMapPath string `json:"configMapPath"`
	// Mode sets the file permissions when mounting the configmap. Since the configmap will be read
	// only filesystem anyway, we basically just want to expose if the file should be mounted as
	// executable or not. So, default permissions would be 0o444 (read) and execute would be 0o555.
	// +kubebuilder:validation:Enum=read;execute
	// +kubebuilder:default=read
	// +optional
	Mode string `json:"mode,omitempty"`
}

// FileFromURL represents a file that you would like to mount from a URL in the launcher pod for
// a given node.
type FileFromURL struct {
	// FilePath is the path to mount the file.
	FilePath string `json:"filePath"`
	// URL is the url to fetch and mount at the provided FilePath. This URL must be a url that can
	// be simply downloaded and dumped to disk -- meaning a normal file server type endpoint or if
	// using GitHub or similar a "raw" path.
	URL string `json:"url"`
}

// Persistence holds information about how to persist the containlerab lab directory for each node
// in a topology.
type Persistence struct {
	// Enabled indicates if persistence of hte containerlab lab/working directory will be placed in
	// a mounted PVC.
	Enabled bool `json:"enabled"`
	// ClaimSize is the size of the PVC for this topology -- if not provided this defaults to 5Gi.
	// If provided, the string value must be a valid kubernetes storage requests style string. Note
	// the claim size *cannot be made smaller* once created, but it *can* be expanded. If you need
	// to make the claim smaller you must delete the topology (or the node from the topology) and
	// re-add it.
	// +optional
	ClaimSize string `json:"claimSize,omitempty"`
	// StorageClassName is the storage class to set in the PVC -- if not provided this will be left
	// empty which will end up using your default storage class. Note that currently we assume you
	// have (as default) or provide a dynamically provisionable storage class, hence no selector.
	// +optional
	StorageClassName string `json:"storageClassName,omitempty"`
}

// InsecureRegistries is a slice of strings of insecure registries to configure in the launcher
// pods.
type InsecureRegistries []string

// Definition holds the underlying topology definition for the Topology CR. A Topology *must* have
// one -- and only one -- definition type defined.
type Definition struct {
	// Containerlab holds a valid containerlab topology.
	Containerlab string `json:"containerlab"`
}

// Expose holds configurations relevant to how clabernetes exposes a topology.
type Expose struct {
	// DisableExpose indicates if exposing nodes via LoadBalancer service should be disabled, by
	// default any mapped ports in a containerlab topology will be exposed.
	// +optional
	DisableExpose bool `json:"disableExpose"`
	// DisableAutoExpose disables the automagic exposing of ports for a given topology. When this
	// setting is disabled clabernetes will not auto add ports so if you want to expose (via a
	// load balancer service) you will need to have ports outlined in your containerlab config.
	// When this is `false` (default), clabernetes will add and expose the
	// following list of ports to whatever ports you have already defined:
	//
	// 21    - tcp - ftp
	// 22    - tcp - ssh
	// 23    - tcp - telnet
	// 80    - tcp - http
	// 161   - udp - snmp
	// 443   - tcp - https
	// 830   - tcp - netconf (over ssh)
	// 5000  - tcp - telnet for vrnetlab qemu host
	// 5900  - tcp - vnc
	// 6030  - tcp - gnmi (arista default)
	// 9339  - tcp - gnmi/gnoi
	// 9340  - tcp - gribi
	// 9559  - tcp - p4rt
	// 57400 - tcp - gnmi (nokia srl/sros default)
	//
	// This setting is *ignored completely* if `DisableExpose` is true!
	//
	// +optional
	DisableAutoExpose bool `json:"disableAutoExpose"`
	// ExposeType configures the service type(s) related to exposing the topology. This is an enum
	// that has the following valid values:
	// - None: expose is *not* disabled, but we just don't create any services related to the pods,
	//         you may want to do this if you want to tickle the pods by pod name directly for some
	//         reason while not having extra services floating around.
	// - ClusterIP: a clusterip service is created so you can hit that service name for the pods.
	// - Headless: a headless service (clusterIP: None) is created. This is useful when you don't
	//         need load-balancing or a single service IP but want to directly connect to pods via
	//         DNS records that return pod IPs.
	// - LoadBalancer: (default) creates a load balancer service so you can access your pods from
	//         outside the cluster. this is/was the only behavior up to v0.2.4.
	// +kubebuilder:validation:Enum=None;ClusterIP;Headless;LoadBalancer
	// +kubebuilder:default=LoadBalancer
	// +optional
	ExposeType string `json:"exposeType,omitempty"`
	// UseNodeMgmtIpv4Address, when set to true, the controller will look up each node’s management
	// IPv4 address (from the `mgmt-ipv4` field in your containerlab topology) and assign
	// that address to `Service.spec.loadBalancerIP` on the corresponding LoadBalancer
	// Service.
	// - Only applies if `spec.expose.exposeType` is `LoadBalancer`.
	// - If the IP is missing or fails validation, a warning is emitted and Kubernetes
	//   will allocate an IP automatically.
	UseNodeMgmtIpv4Address bool `json:"useNodeMgmtIpv4Address,omitempty"`
	// UseNodeMgmtIpv6Address, when set to true, the controller will look up each node’s management
	// IPv6 address (from the `mgmt-ipv6` field in your containerlab topology) and assign
	// that address to `Service.spec.loadBalancerIP` on the corresponding LoadBalancer
	// Service.
	// - Only applies if `spec.expose.exposeType` is `LoadBalancer`.
	// - If the IP is missing or fails validation, a warning is emitted and Kubernetes
	// will allocate an IP automatically.
	UseNodeMgmtIpv6Address bool `json:"useNodeMgmtIpv6Address,omitempty"`
}

// Deployment holds configurations relevant to how clabernetes configures deployments that make
// up a given topology.
type Deployment struct {
	// Resources is a mapping of nodeName (or "default") to kubernetes resource requirements -- any
	// value set here overrides the "global" config resource definitions. If a key "default" is set,
	// those resource values will be preferred over *all global settings* for this topology --
	// meaning, the "global" resource settings will never be looked up for this topology, and any
	// kind/type that is *not* in this resources map will have the "default" resources from this
	// mapping applied.
	// +optional
	Resources map[string]k8scorev1.ResourceRequirements `json:"resources"`
	// Scheduling holds information about how the launcher pod(s) should be configured with respect
	// to "scheduling" things (affinity/node selector/tolerations).
	// +optional
	Scheduling Scheduling `json:"scheduling"`
	// PrivilegedLauncher, when true, sets the launcher containers to privileged. Historically we
	// tried very hard to *not* need to set privileged mode on pods, however the reality is it is
	// much, much easier to get various network operating system images booting with this enabled,
	// so, the default mode is to set the privileged flag on pods. Disabling this option causes
	// clabernetes to try to run the pods for this topology in the "not so privileged" mode -- this
	// basically means we mount all capabilities we think should be available, set apparmor to
	// "unconfined", and mount paths like /dev/kvm and dev/net/tun. With this "not so privileged"
	// mode, Nokia SRL devices and Arista cEOS devices have been able to boot on some clusters, but
	// your mileage may vary. In short: if you don't care about having some privileged pods, just
	// leave this alone.
	// +optional
	PrivilegedLauncher *bool `json:"privilegedLauncher"`
	// FilesFromConfigMap is a slice of FileFromConfigMap that define the configmap/path and node
	// and path on a launcher node that the file should be mounted to. If the path is not provided
	// the configmap is mounted in its entirety (like normal k8s things), so you *probably* want
	// to specify the sub path unless you are sure what you're doing!
	// +optional
	FilesFromConfigMap map[string][]FileFromConfigMap `json:"filesFromConfigMap"`
	// FilesFromURL is a mapping of FileFromURL that define a URL at which to fetch a file, and path
	// on a launcher node that the file should be downloaded to. This is useful for configs that are
	// larger than the ConfigMap (etcd) 1Mb size limit.
	// +optional
	FilesFromURL map[string][]FileFromURL `json:"filesFromURL"`
	// Persistence holds configurations relating to persisting each nodes working containerlab
	// directory.
	// +optional
	Persistence Persistence `json:"persistence"`
	// ContainerlabDebug sets the `--debug` flag when invoking containerlab in the launcher pods.
	// This is disabled by default. If this value is unset, the global config value (default of
	// "false") will be used.
	// +optional
	ContainerlabDebug *bool `json:"containerlabDebug"`
	// ContainerlabTimeout sets the `--timeout` flag when invoking containerlab in the launcher
	// pods.
	// +optional
	ContainerlabTimeout string `json:"containerlabTimeout"`
	// ContainerlabVersion sets a custom version to use for containerlab -- when set this will cause
	// the launcher pods to download and use this specific version of containerlab. Setting a bad
	// version (version that doesnt exist/typo/etc.) will cause pods to fail to launch, so be
	// careful! You never "need" to this as the publicly available launcher image will always be
	// built with a (reasonably) up to date containerlab version, this setting exists in case you
	// want to pin back to an older version for some reason or you want to be bleeding edge with
	// some new feature (but do note that just because it exists in containerlab doesnt
	// *necessarily* mean it will be auto-working in clabernetes!
	// 0.78.0 is the floor -- the Node vocabulary includes fields older releases reject outright.
	// +optional
	ContainerlabVersion string `json:"containerlabVersion,omitempty"`
	// LauncherImage sets the default launcher image to use when spawning launcher deployments for
	// this Topology. This is optional, the launcher image will default to whatever is set in the
	// global config CR.
	// +optional
	LauncherImage string `json:"launcherImage,omitempty"`
	// LauncherImagePullPolicy sets the default launcher image pull policy to use when spawning
	// launcher deployments for this Topology. This is also optional and defaults to whatever is set
	// in the global config CR (typically "IfNotPresent"). Note: omitempty because empty str does
	// not satisfy enum of course.
	// +kubebuilder:validation:Enum=IfNotPresent;Always;Never
	// +optional
	LauncherImagePullPolicy string `json:"launcherImagePullPolicy,omitempty"`
	// LauncherLogLevel sets the launcher clabernetes worker log level -- this overrides whatever
	// is set on the controllers env vars for this topology. Note: omitempty because empty str does
	// not satisfy enum of course.
	// +kubebuilder:validation:Enum=disabled;critical;warn;info;debug
	// +optional
	LauncherLogLevel string `json:"launcherLogLevel,omitempty"`
	// ExtraEnv is a list of additional environment variables to set on the launcher container. The
	// values here override any configured global config extra envs!
	// +optional
	// +listType=atomic
	ExtraEnv []k8scorev1.EnvVar `json:"extraEnv"`
}

// Scheduling holds information about how the launcher pod(s) should be configured with respect
// to "scheduling" things (affinity/node selector/tolerations).
type Scheduling struct {
	// NodeSelector sets the node selector that will be configured on all launcher pods for this
	// Topology.
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
	// Tolerations is a list of Tolerations that will be set on the launcher pod spec.
	// +listType=atomic
	// +optional
	Tolerations []k8scorev1.Toleration `json:"tolerations"`
}

// StatusProbes holds details about if the status probes are enabled and if so how they should be
// handled.
type StatusProbes struct {
	// Enabled sets the status probes to enabled (or obviously disabled). Note that if the probes
	// are enabled and the health condition fails due to configuring the node the cluster will
	// restart the node. So, if you plan on being destructive with the node config (probably because
	// you will have exec'd onto the node) then you may want to disable this!
	// +kubebuilder:default=true
	// +optional
	Enabled bool `json:"enabled"`
	// ExcludedNodes is a set of nodes to be excluded from status probe checking. It may be
	// desirable to exclude some node(s) from status checking due to them not having an easy way
	// for clabernetes to check the state of the node. The node names here should match the name of
	// the nodes in the containerlab sub-topology.
	// +listType=atomic
	// +optional
	ExcludedNodes []string `json:"excludedNodes"`
	// NodeProbeConfigurations is a map of node specific probe configurations -- if you only need
	// a simple ssh or tcp connect style setup that works on all node types in the topology you can
	// ignore this and just configure ProbeConfiguration.
	// +optional
	NodeProbeConfigurations map[string]ProbeConfiguration `json:"nodeProbeConfigurations"`
	// ProbeConfiguration is the default probe configuration for the Topology.
	// +optional
	ProbeConfiguration ProbeConfiguration `json:"probeConfiguration"`
}

// ProbeConfiguration holds information about how to probe a (containerlab) node in a Topology. If
// both style probes are configured, both will be used and both must succeed in order to report
// healthy.
type ProbeConfiguration struct {
	// StartupSeconds is the total amount of seconds to allow for the node to start. This defaults
	// to ~13 minutes to hopefully account for slow to boot nodes. Note that there is also a 60
	// initial delay configured, so technically the default is ~14-15 minutes. Be careful with this
	// delay as there must be time for c9s to (via whatever means) pull the image and load it into
	// docker on the launcher and this can take a bit! Having this be bigger than you think you need
	// is generally better since if the startup probe succeeds ever then the readiness probe takes
	// over anyway.
	// +optional
	StartupSeconds int `json:"startupSeconds"`
	// SSHProbeConfiguration defines an SSH probe.
	// +optional
	SSHProbeConfiguration *SSHProbeConfiguration `json:"sshProbeConfiguration,omitempty"`
	// TCPProbeConfiguration defines a TCP probe.
	// +optional
	TCPProbeConfiguration *TCPProbeConfiguration `json:"tcpProbeConfiguration,omitempty"`
}

// SSHProbeConfiguration defines a "ssh" probe -- the ssh probe just connects using standard go
// crypto ssh setup and reports true if auth is successful, it does no further checking. The probe
// is executed by the launcher and the result is placed into /clabernetes/.nodestatus so the k8s
// probe can pick it up and reflect the status.
type SSHProbeConfiguration struct {
	// Username is the username to use for auth.
	Username string `json:"username"`
	// Password is the password to use for auth.
	Password string `json:"password"`
	// Port is an optional override (of course default is 22).
	// +optional
	Port int `json:"port"`
}

// TCPProbeConfiguration defines a "tcp" probe. The probe is executed by the launcher and the
// result is placed into /clabernetes/.nodestatus so the k8s probe can pick it up and reflect the
// status.
type TCPProbeConfiguration struct {
	// Port defines the port to try to open a TCP connection to. When using TCP probe setup this
	// connection happens inside the launcher rather than the "normal" k8s style probes. This style
	// probe behaves like a k8s style probe though in that it is "successful" whenever a TCP
	// connection to this port can be opened successfully.
	Port int `json:"port"`
}

// ImagePull holds configurations relevant to how clabernetes launcher pods handle pulling
// images.
type ImagePull struct {
	// InsecureRegistries is a slice of strings of insecure registries to configure in the launcher
	// pods.
	// +optional
	InsecureRegistries InsecureRegistries `json:"insecureRegistries"`
	// PullThroughOverride allows for overriding the image pull through mode for this
	// particular topology.
	// +kubebuilder:validation:Enum=auto;always;never
	// +optional
	PullThroughOverride string `json:"pullThroughOverride,omitempty"`
	// PullSecrets allows for providing secret(s) to use when pulling the image. This is only
	// applicable *if* ImagePullThrough mode is auto or always. The secret is used by the launcher
	// pod to pull the image via the cluster CRI. The secret is *not* mounted to the pod, but
	// instead is used in conjunction with a job that spawns a pod using the specified secret. The
	// job will kill the pod as soon as the image has been pulled -- we do this because we don't
	// care if the pod runs, we only care that the image gets pulled on a specific node. Note that
	// just like "normal" pull secrets, the secret needs to be in the namespace that the topology
	// is in.
	// +listType=set
	// +optional
	PullSecrets []string `json:"pullSecrets"`
	// DockerDaemonConfig allows for setting the docker daemon config for all launchers in this
	// topology. The secret *must be present in the namespace of this topology*. The secret *must*
	// contain a key "daemon.json" -- as this secret will be mounted to /etc/docker and docker will
	// be expecting the config at /etc/docker/daemon.json.
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
