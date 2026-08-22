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
	// TopologyReadyStatus a const for the ready status, for consistency.
	TopologyReadyStatus = "TopologyReady"

	// TopologyChildResourceConflictReason identifies a Topology blocked by occupied child names.
	TopologyChildResourceConflictReason = "ChildResourceConflict"
)
