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

// FileFromSecret represents one file projected from a same-namespace Kubernetes Secret.
type FileFromSecret struct {
	// FilePath is the absolute destination path, or the destination directory when SecretPath is
	// omitted and every Secret key is projected.
	FilePath string `json:"filePath"`
	// SecretName is the name of the same-namespace Secret.
	SecretName string `json:"secretName"`
	// SecretPath is the Secret data key to project. When omitted, every key is projected beneath
	// FilePath in deterministic key order.
	// +optional
	SecretPath string `json:"secretPath,omitempty"`
	// Mode selects read-only or read-and-execute permissions for the staged file.
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
	// Digest is the required SHA-256 identity of the downloaded bytes in direct-runtime mode. It
	// prevents a mutable URL from changing a device payload without changing the accepted plan.
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	// +optional
	Digest string `json:"digest,omitempty"`
}

// Persistence holds direct device artifact persistence policy for each Node in a Topology.
type Persistence struct {
	// Enabled indicates whether package-planned persistent artifacts are placed in a mounted PVC.
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

// Deployment holds portable policy compiled into direct Node workloads.
type Deployment struct {
	// Resources is a mapping of nodeName (or "default") to kubernetes resource requirements -- any
	// value set here overrides the "global" config resource definitions. If a key "default" is set,
	// those resource values will be preferred over *all global settings* for this topology --
	// meaning, the "global" resource settings will never be looked up for this topology, and any
	// kind/type that is *not* in this resources map will have the "default" resources from this
	// mapping applied.
	// +optional
	Resources map[string]k8scorev1.ResourceRequirements `json:"resources"`
	// Scheduling holds direct Pod scheduling policy.
	// +optional
	Scheduling Scheduling `json:"scheduling"`
	// FilesFromConfigMap is a slice of FileFromConfigMap that define the configmap/path and node
	// and path on a launcher node that the file should be mounted to. If the path is not provided
	// the configmap is mounted in its entirety (like normal k8s things), so you *probably* want
	// to specify the sub path unless you are sure what you're doing!
	// +optional
	FilesFromConfigMap map[string][]FileFromConfigMap `json:"filesFromConfigMap"`
	// FilesFromSecret maps logical Node names to same-namespace Secret-backed payloads.
	// +optional
	FilesFromSecret map[string][]FileFromSecret `json:"filesFromSecret,omitempty"`
	// FilesFromURL is a mapping of FileFromURL that define a URL at which to fetch a file, and path
	// on a launcher node that the file should be downloaded to. This is useful for configs that are
	// larger than the ConfigMap (etcd) 1Mb size limit.
	// +optional
	FilesFromURL map[string][]FileFromURL `json:"filesFromURL"`
	// Persistence holds direct device artifact persistence policy.
	// +optional
	Persistence Persistence `json:"persistence"`
}

// Scheduling holds direct Pod node selection and toleration policy.
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
// handled. Enabled probes always require the nested container to be running and not paused,
// restarting, or dead. If Docker exposes an image-defined healthcheck, it must also be healthy.
// Configured TCP and SSH probes are additional requirements.
type StatusProbes struct {
	// Enabled sets the status probes to enabled (or obviously disabled). A Node that has previously
	// started but later fails its readiness check remains running and is reported not ready. A Node
	// that never passes its startup check is restarted after its startup allowance expires.
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

// ProbeConfiguration holds optional application-specific probes for a (containerlab) node in a
// Topology. If both styles are configured, both and the generic nested-container probe must succeed
// in order to report healthy.
type ProbeConfiguration struct {
	// StartupSeconds is the total amount of seconds to allow for the node to start. This defaults
	// to roughly 15 minutes to account for slow-to-boot nodes. The allowance must include time for
	// c9s to pull the image, load it into Docker on the launcher, and boot the node. A larger value
	// does not delay fast nodes because the readiness probe takes over as soon as startup succeeds.
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

// ImagePull holds Kubernetes-native image pull defaults compiled into LauncherProfile.
type ImagePull struct {
	// Policy is the default Kubernetes pull policy for application containers whose flattened Node
	// definition does not explicitly declare one.
	// +kubebuilder:validation:Enum=IfNotPresent;Always;Never
	// +optional
	Policy string `json:"policy,omitempty"`
	// PullSecrets lists same-namespace Docker-config Secrets placed on direct device Pods through
	// Pod.spec.imagePullSecrets. Credentials are not mounted into application containers.
	// +listType=set
	// +optional
	PullSecrets []string `json:"pullSecrets"`
}
