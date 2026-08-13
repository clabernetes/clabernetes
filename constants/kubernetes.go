package constants

const (
	// KubernetesConfigMap is a const to use for "configmap".
	KubernetesConfigMap = "configmap"

	// KubernetesService is a const to use for "service".
	KubernetesService = "service"

	// KubernetesPVC is a const to use for "persistentvolumeclaim".
	KubernetesPVC = "persistentvolumeclaim"

	// KubernetesDeployment is a const to use for "deployment".
	KubernetesDeployment = "deployment"
)

const (
	// KubernetesDefaultInClusterDNSSuffix is the default in cluster dns suffix (duh).
	KubernetesDefaultInClusterDNSSuffix = "svc.cluster.local"
)

const (
	// KubernetesImagePullIfNotPresent holds the constant for "IfNotPresent" image pull policy.
	KubernetesImagePullIfNotPresent = "IfNotPresent"
)

const (
	// KubernetesCRIUnknown is a const for when we dont know what the CRI type is in a cluster.
	KubernetesCRIUnknown = "unknown"
	// KubernetesCRIContainerd is a const for the "containerd" type of CRI in a cluster.
	KubernetesCRIContainerd = "containerd"
	// KubernetesCRICrio is a const for the "cri-o" type of CRI in a cluster.
	KubernetesCRICrio = "crio"
)

const (
	// KubernetesCRISockContainerdPath is the path where the containerd sock lives.
	KubernetesCRISockContainerdPath = "/run/containerd"
	// KubernetesCRISockContainerd is the containerd sock filename.
	KubernetesCRISockContainerd = "containerd.sock"
	// ContainerdCertsDir is the conventional registry hosts directory used by containerd clients.
	ContainerdCertsDir = "/etc/containerd/certs.d"
)

const (
	// LauncherCRISockPath is the path where, if configured, the CRI sock is mounted in launcher
	// pods.
	LauncherCRISockPath = "/clabernetes/.node"
)

const (
	// TopologyReadyStatus a const for the ready status, for consistency.
	TopologyReadyStatus = "TopologyReady"
)
