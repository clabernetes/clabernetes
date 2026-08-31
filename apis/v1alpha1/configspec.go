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

// ConfigDeployment holds generic global defaults for direct device workloads.
type ConfigDeployment struct {
	// ResourcesDefault is merged onto each logical Node's primary application container. Imported
	// plans remain the source of kind-owned component requirements.
	// +optional
	ResourcesDefault *k8scorev1.ResourceRequirements `json:"resourcesDefault"`
	// NodeSelectorsByImage is a mapping of image glob pattern as key and node selectors (value)
	// to apply to direct device workloads. The longest matching pattern wins; conflicts between
	// images in one grouped/component workload fail preflight. A config example:
	// {
	//   "internal.io/nokia_sros*": {"node-flavour": "baremetal"},
	//   "ghcr.io/nokia/srlinux*":  {"node-flavour": "amd64"},
	//   "default":                 {"node-flavour": "cheap"},
	// }.
	// +optional
	NodeSelectorsByImage map[string]map[string]string `json:"nodeSelectorsByImage"`
	// ContainerStopSignals, when true, has the direct-pod renderer map an image's OCI stop signal
	// to the Kubernetes lifecycle.stopSignal field; this requires the cluster (apiserver and
	// kubelets) to enable the ContainerStopSignals feature gate. When false, images that declare a
	// stop signal fail planning rather than silently dropping the signal.
	// +kubebuilder:default=false
	// +optional
	ContainerStopSignals bool `json:"containerStopSignals,omitempty"`
}

// RegistryMetadataTrustEntry is one exact registry transport policy used only by the c9s
// controller while resolving OCI manifests and configuration blobs. Kubelet registry mirrors,
// credentials, and transport trust remain cluster-runtime configuration.
type RegistryMetadataTrustEntry struct {
	// Registry is the exact registry host, optionally including a port. URL schemes and paths are
	// not accepted.
	// +kubebuilder:validation:MinLength=1
	Registry string `json:"registry"`
	// CABundle is a PEM-encoded CA bundle that extends the controller's system trust roots for
	// this registry.
	// +optional
	CABundle string `json:"caBundle,omitempty"`
	// PlainHTTP explicitly allows unencrypted HTTP metadata access to this registry. It does not
	// disable TLS verification and cannot be combined with CABundle.
	// +optional
	PlainHTTP bool `json:"plainHTTP,omitempty"`
}

// RegistryMetadataMirrorEntry redirects the c9s controller's OCI metadata requests for one exact
// source registry to a mirror endpoint, mirroring a CRI pull-through configuration (containerd
// hosts.toml). Only the controller's HTTP hop is rewritten: image references, resolved digest
// identities, and Pod image strings keep the original registry, so kubelets keep using their own
// runtime mirror configuration. There is no origin fallback.
type RegistryMetadataMirrorEntry struct {
	// Registry is the exact source registry host, optionally including a port. URL schemes and
	// paths are not accepted. Docker Hub aliases (docker.io, index.docker.io,
	// registry-1.docker.io) select one shared entry.
	// +kubebuilder:validation:MinLength=1
	Registry string `json:"registry"`
	// Endpoint is the mirror URL: an https or http scheme, a host, and an optional path prefix.
	// An endpoint path requires overridePath. The endpoint scheme selects the connection
	// transport; add a RegistryMetadataTrust entry for the endpoint host when it needs a private
	// CA.
	// +kubebuilder:validation:MinLength=1
	Endpoint string `json:"endpoint"`
	// OverridePath treats the endpoint path as the mirror's registry API root for this source
	// registry, replacing the standard /v2 prefix on rewritten request paths (containerd
	// hosts.toml override_path semantics, for example a Harbor proxy project at /v2/<project>).
	// +optional
	OverridePath bool `json:"overridePath,omitempty"`
}

// ConfigImagePull holds global image-pull and controller metadata-access configuration.
type ConfigImagePull struct {
	// Policy is the default Kubernetes pull policy for application containers whose flattened Node
	// definition does not explicitly declare one.
	// +kubebuilder:validation:Enum=IfNotPresent;Always;Never
	// +kubebuilder:default=IfNotPresent
	// +optional
	Policy string `json:"policy,omitempty"`
	// PullSecrets lists same-namespace Docker-config Secrets placed on direct device Pods. Every
	// listed Secret name must exist in each workload namespace that inherits this Config default.
	// +listType=set
	// +optional
	PullSecrets []string `json:"pullSecrets,omitempty"`
	// RegistryMetadataTrust contains exact, controller-only trust exceptions for OCI metadata
	// resolution. It does not configure kubelets: administrators must configure the corresponding
	// registry mirror, CA, or HTTP endpoint independently in every eligible node runtime.
	// +listType=map
	// +listMapKey=registry
	// +optional
	RegistryMetadataTrust []RegistryMetadataTrustEntry `json:"registryMetadataTrust,omitempty"`
	// RegistryMetadataMirrors contains exact, controller-only registry mirrors for OCI metadata
	// resolution, for clusters whose registry access flows through a CRI pull-through mirror the
	// controller cannot see. It does not configure kubelets, and trust for a mirror connection
	// comes from the RegistryMetadataTrust entry matching the mirror endpoint host.
	// +listType=map
	// +listMapKey=registry
	// +optional
	RegistryMetadataMirrors []RegistryMetadataMirrorEntry `json:"registryMetadataMirrors,omitempty"`
}
