// Package directpod renders runtime-neutral device plans into Kubernetes-native workloads.
// It deliberately has no dependency on containerlab registries or concrete node implementations.
//
//nolint:err113,funlen,gocognit,gocyclo,maintidx,mnd // single-pass boundary logic with structured one-off diagnostics and protocol literals.
//nolint:err113,funlen,gocognit,gocyclo,maintidx,mnd // single-pass boundary logic with structured one-off diagnostics and protocol literals.
//nolint:noinlineerr // Rendering guards are clearer as compact fail-closed checks.
package directpod

import (
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"math"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	clabernetesinternaldirectruntime "github.com/clabernetes/clabernetes/internal/directruntime"
	k8sappsv1 "k8s.io/api/apps/v1"
	k8scorev1 "k8s.io/api/core/v1"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
)

const (
	planVolumeName                   = "node-plan"
	planMountPath                    = "/var/run/clabernetes/plan"
	inputVolumeName                  = "node-plan-input"
	inputMountPath                   = "/var/run/clabernetes/input"
	artifactRootPath                 = "/var/run/clabernetes/artifacts"
	payloadRootPath                  = "/var/run/clabernetes/payloads"
	certificateVolumeName            = "node-certificates"
	certificateRootPath              = "/var/run/clabernetes/certificates"
	entropyVolumeName                = "node-entropy"
	entropyRootPath                  = "/var/run/clabernetes/entropy"
	endpointCertificateName          = "node-endpoint-certificates"
	preparationScratchName           = "node-preparation-scratch"
	preparationScratchPath           = "/tmp"
	connectivityStateName            = "clabwire-state"
	connectivityStatePath            = "/var/run/clabernetes/connectivity"
	connectivityRevisionVolumeName   = "clabwire-revision"
	connectivityRevisionMountPath    = "/var/run/clabernetes/connectivity-revision"
	hostNetworkNamespaceName         = "worker-host-network-namespace"
	hostNetworkNamespaceSourcePath   = "/proc/1/ns"
	hostNetworkNamespaceMountPath    = "/var/run/clabernetes/host-network-namespaces"
	lifecycleVolumeName              = "node-lifecycle-manager"
	lifecycleBinaryRoot              = "/var/lib/clabernetes/lifecycle-bin"
	lifecycleBinaryPath              = lifecycleBinaryRoot + "/manager"
	runtimeBinaryPath                = "/clabernetes/manager"
	lifecyclePlanRoot                = "/var/lib/clabernetes/lifecycle-plan"
	lifecycleInputRoot               = "/var/lib/clabernetes/lifecycle-input"
	lifecycleArtifactRoot            = "/var/lib/clabernetes/lifecycle-artifacts"
	lifecycleCertificateRoot         = "/var/lib/clabernetes/lifecycle-certificates"
	lifecycleEntropyRoot             = "/var/lib/clabernetes/lifecycle-entropy"
	lifecycleScratchName             = "node-lifecycle-scratch"
	lifecycleScratchRoot             = "/var/lib/clabernetes/lifecycle-scratch"
	applicationRuntimeAPIName        = "node-runtime-api"
	applicationRuntimeAPIRoot        = "/var/lib/clabernetes/runtime-api"
	applicationRuntimeCredentialName = "node-runtime-credentials"                      //nolint:gosec // identifier or path, not a credential.
	applicationRuntimeCredentialRoot = "/var/run/secrets/kubernetes.io/serviceaccount" //nolint:gosec // identifier or path, not a credential.
	probeSecretVolumeName            = "node-probe-secrets"                            //nolint:gosec // identifier or path, not a credential.
	probePasswordPath                = "/var/lib/clabernetes/probe-secret/password"    //nolint:gosec // identifier or path, not a credential.
	preparationName                  = "planner"
	connectivityName                 = "clabwire"
	directWorkloadLabel              = clabernetesconstants.LabelDirectWorkload
	directMeshMemberLabel            = clabernetesconstants.LabelDirectMeshMember
	planDigestAnnotation             = "c9s.run/node-plan-digest"
)

// Stable direct workload identities consumed by the status reconciler.
const (
	PlanDigestAnnotation              = planDigestAnnotation
	NodeUIDAnnotation                 = "c9s.run/direct-node-uid"
	LinkLifecycleModeAnnotation       = "c9s.run/link-lifecycle-mode"
	LinkLifecyclePlanDigestAnnotation = "c9s.run/link-lifecycle-plan-digest"
	PreparationContainerName          = preparationName
	ConnectivityContainerName         = connectivityName
	// KubectlDefaultContainerAnnotation makes unqualified kubectl exec/logs target the logical
	// primary application container rather than a c9s helper or imported component.
	KubectlDefaultContainerAnnotation = "kubectl.kubernetes.io/default-container"
)

// Options supplies c9s/Kubernetes realization policy that does not belong to kind planning.
type Options struct {
	Name                              string
	Namespace                         string
	PlanConfigMapName                 string
	InputConfigMapName                string
	ConnectivityRevisionConfigMapName string
	PreparationImage                  string
	ConnectivityImage                 string
	ServiceAccountName                string
	ImagePullSecrets                  []k8scorev1.LocalObjectReference
	Labels                            map[string]string
	Annotations                       map[string]string
	OwnerReferences                   []metav1.OwnerReference
	NodeSelector                      map[string]string
	Tolerations                       []k8scorev1.Toleration
	Affinity                          *k8scorev1.Affinity
	PrimaryContainerResources         *k8scorev1.ResourceRequirements
	ApplicationImagePullPolicy        string
	Payloads                          []clabernetesinternaldeviceplan.PayloadInput
	CertificateSecretName             string
	CertificateInputs                 []clabernetesinternaldeviceplan.CertificateInput
	EntropySecretName                 string
	ProbeSecretName                   string
	ProbePolicies                     map[string]ProbePolicy
	PersistentVolumeClaims            map[string]string
	EnableContainerStopSignals        bool
	EnableApplicationLogBroker        bool
	LinkLifecycleMode                 clabernetesinternaldeviceplan.LinkApplyMode
	LinkLifecyclePlanDigest           string
}

// ProbePolicy is explicit, kind-neutral NodeProfile policy for one logical Node. Password
// bytes remain in ProbeSecretName; SSHPasswordKey names only the projected Secret entry.
type ProbePolicy struct {
	StartupSeconds int
	TCPPort        int
	SSHUsername    string
	SSHPort        int
	SSHPasswordKey string
}

// PlanReferences identifies the immutable cold artifacts mounted by one rendered Deployment.
type PlanReferences struct {
	PlanConfigMapName                 string
	InputConfigMapName                string
	ConnectivityRevisionConfigMapName string
	PlanDigest                        string
}

// ErrInvalidPlanReferences classifies a Deployment without the renderer-owned cold artifacts.
var ErrInvalidPlanReferences = errors.New("invalid direct Deployment plan references")

// DeploymentPlanReferences resolves the renderer-owned cold artifact references without making
// the Node controller duplicate volume-name knowledge.
func DeploymentPlanReferences(deployment *k8sappsv1.Deployment) (PlanReferences, error) {
	if deployment == nil {
		return PlanReferences{}, fmt.Errorf("%w: Deployment is nil", ErrInvalidPlanReferences)
	}

	references := PlanReferences{
		PlanDigest: deployment.Spec.Template.Annotations[planDigestAnnotation],
	}
	for _, volume := range deployment.Spec.Template.Spec.Volumes {
		if volume.ConfigMap == nil {
			continue
		}

		switch volume.Name {
		case planVolumeName:
			references.PlanConfigMapName = volume.ConfigMap.Name
		case inputVolumeName:
			references.InputConfigMapName = volume.ConfigMap.Name
		case connectivityRevisionVolumeName:
			references.ConnectivityRevisionConfigMapName = volume.ConfigMap.Name
		}
	}

	if references.PlanConfigMapName == "" || references.InputConfigMapName == "" ||
		references.ConnectivityRevisionConfigMapName == "" || references.PlanDigest == "" {
		return PlanReferences{}, fmt.Errorf(
			"%w: cold plan references are incomplete",
			ErrInvalidPlanReferences,
		)
	}

	return references, nil
}

// ValidatePlan performs every plan-only Kubernetes portability check before the controller creates
// or mutates a device workload. It contains no kind or vendor knowledge.
func ValidatePlan(plan clabernetesinternaldeviceplan.Plan) error {
	normalized, err := clabernetesinternaldeviceplan.NormalizePlan(plan)
	if err != nil {
		return err
	}

	return validateNormalizedPlan(normalized)
}

func validateNormalizedPlan(plan clabernetesinternaldeviceplan.Plan) error {
	if err := clabernetesinternaldirectruntime.ValidatePlanCapabilities(plan); err != nil {
		var planningErr *clabernetesinternaldeviceplan.Error
		if errors.As(err, &planningErr) {
			return err
		}

		return &clabernetesinternaldeviceplan.Error{
			Code: clabernetesinternaldeviceplan.ErrorUnsupported, Field: "nodePlan",
			Behavior: "direct-runtime-capability", Message: err.Error(),
		}
	}

	deviceTargets := map[string]string{}

	for containerIndex, container := range plan.Containers {
		securityField := fmt.Sprintf("containers[%d].security", containerIndex)
		if !portableSeccompProfile(container.Security.SeccompProfile) {
			return directPlanPreflightError(
				container.NodeID,
				securityField+".seccompProfile",
				"seccomp profile has no portable Kubernetes representation",
			)
		}

		if !portableAppArmorProfile(container.Security.AppArmorProfile) {
			return directPlanPreflightError(
				container.NodeID,
				securityField+".appArmorProfile",
				"AppArmor profile has no portable Kubernetes representation",
			)
		}

		for deviceIndex, device := range container.Security.Devices {
			field := fmt.Sprintf("%s.devices[%d]", securityField, deviceIndex)
			if !container.Security.Privileged {
				return directPlanPreflightError(
					container.NodeID,
					field,
					"host device requires privileged container cgroup access",
				)
			}

			if !portableHostDevicePath(device.HostPath) ||
				!portableContainerDevicePath(device.ContainerPath) ||
				!portableDevicePermissions(device.Permissions) {
				return directPlanPreflightError(
					container.NodeID,
					field,
					"host device path, target, or permissions are not portable",
				)
			}

			if existing := deviceTargets[device.ContainerPath]; existing != "" &&
				existing != device.HostPath {
				return directPlanPreflightError(
					container.NodeID,
					field+".containerPath",
					"multiple host devices request the same target-worker path",
				)
			}

			deviceTargets[device.ContainerPath] = device.HostPath
		}
	}

	for volumeIndex, volume := range plan.Volumes {
		if volume.Kind == clabernetesinternaldeviceplan.VolumeDevice &&
			!portableHostDevicePath(volume.Reference) {
			return directPlanPreflightError(
				volume.NodeID,
				fmt.Sprintf("volumes[%d].reference", volumeIndex),
				"device volume is not a canonical character-device path under /dev",
			)
		}
	}

	mountDestinations := map[string]string{}

	for mountIndex, mount := range plan.Mounts {
		key := mount.ContainerID + "\x00" + mount.Destination
		if existing := mountDestinations[key]; existing != "" {
			return &clabernetesinternaldeviceplan.Error{
				Code:     clabernetesinternaldeviceplan.ErrorInvariant,
				Field:    fmt.Sprintf("mounts[%d].destination", mountIndex),
				NodeID:   containerNodeID(plan, mount.ContainerID),
				Behavior: "kubernetes-workload-preflight",
				Message: fmt.Sprintf(
					"mounts %q and %q both land on container path %q; "+
						"a Kubernetes container cannot mount one path twice",
					existing,
					mount.ID,
					mount.Destination,
				),
			}
		}

		mountDestinations[key] = mount.ID
	}

	return nil
}

func containerNodeID(plan clabernetesinternaldeviceplan.Plan, containerID string) string {
	for _, container := range plan.Containers {
		if container.ID == containerID {
			return container.NodeID
		}
	}

	return ""
}

func directPlanPreflightError(nodeID, field, message string) error {
	return &clabernetesinternaldeviceplan.Error{
		Code: clabernetesinternaldeviceplan.ErrorUnsupported, NodeID: nodeID, Field: field,
		Behavior: "kubernetes-workload-preflight", Message: message,
	}
}

func portableHostDevicePath(value string) bool {
	return value != "" && !strings.ContainsRune(value, 0) && path.Clean(value) == value &&
		strings.HasPrefix(value, "/dev/")
}

func portableContainerDevicePath(value string) bool {
	return value != "" && !strings.ContainsRune(value, 0) && path.IsAbs(value) &&
		path.Clean(value) == value
}

func portableDevicePermissions(value string) bool {
	if value == "" || len(value) > len("rwm") {
		return false
	}

	seen := map[rune]bool{}
	for _, permission := range value {
		if (permission != 'r' && permission != 'w' && permission != 'm') || seen[permission] {
			return false
		}

		seen[permission] = true
	}

	return true
}

func portableSeccompProfile(value string) bool {
	if value == "" {
		return true
	}

	profile := mapSeccomp(value)
	if profile == nil {
		return false
	}

	return portableLocalhostProfile(profile.LocalhostProfile)
}

func portableAppArmorProfile(value string) bool {
	if value == "" {
		return true
	}

	profile := mapAppArmor(value)
	if profile == nil {
		return false
	}

	return portableLocalhostProfile(profile.LocalhostProfile)
}

func portableLocalhostProfile(localhost *string) bool {
	if localhost == nil {
		return true
	}

	local := *localhost

	return local != "" && !strings.ContainsRune(local, 0) && !path.IsAbs(local) &&
		path.Clean(local) == local && local != "." && !strings.HasPrefix(local, "../")
}

// Render produces a Recreate Deployment whose regular containers are the planned device and
// component images. The two c9s helpers do not launch or own device processes.
func Render(plan clabernetesinternaldeviceplan.Plan,
	options Options,
) (*k8sappsv1.Deployment, error) {
	normalized, err := clabernetesinternaldeviceplan.NormalizePlan(plan)
	if err != nil {
		return nil, err
	}

	if err = validateOptions(options); err != nil {
		return nil, err
	}

	if err = validateNormalizedPlan(normalized); err != nil {
		return nil, err
	}

	planDigest, err := normalized.Digest()
	if err != nil {
		return nil, err
	}

	labels := maps.Clone(options.Labels)
	if labels == nil {
		labels = map[string]string{}
	}

	labels[directWorkloadLabel] = options.Name
	labels[directMeshMemberLabel] = clabernetesconstants.DirectMeshMemberEnabled

	annotations := maps.Clone(options.Annotations)
	if annotations == nil {
		annotations = map[string]string{}
	}

	delete(annotations, LinkLifecycleModeAnnotation)
	delete(annotations, LinkLifecyclePlanDigestAnnotation)

	if options.LinkLifecycleMode != "" {
		annotations[LinkLifecycleModeAnnotation] = string(options.LinkLifecycleMode)
		annotations[LinkLifecyclePlanDigestAnnotation] = options.LinkLifecyclePlanDigest
	}

	annotations[planDigestAnnotation] = planDigest

	defaultContainer, err := defaultApplicationContainer(normalized, options.Name)
	if err != nil {
		return nil, err
	}

	annotations[KubectlDefaultContainerAnnotation] = defaultContainer

	volumes, volumeNames, err := renderVolumes(
		normalized.Volumes,
		options.PersistentVolumeClaims,
	)
	if err != nil {
		return nil, err
	}
	//nolint:gocritic // one append per planned element reads clearest.
	volumes = append(
		volumes,
		k8scorev1.Volume{ //nolint:gocritic // one append per rendered volume reads clearest.
			Name: planVolumeName,
			VolumeSource: k8scorev1.VolumeSource{ConfigMap: &k8scorev1.ConfigMapVolumeSource{
				LocalObjectReference: k8scorev1.LocalObjectReference{
					Name: options.PlanConfigMapName,
				},
				Items: []k8scorev1.KeyToPath{{Key: "plan.json", Path: "plan.json"}},
			}},
		},
	)

	volumes = append(volumes, k8scorev1.Volume{
		Name:         connectivityStateName,
		VolumeSource: k8scorev1.VolumeSource{EmptyDir: &k8scorev1.EmptyDirVolumeSource{}},
	})
	if options.ConnectivityRevisionConfigMapName != "" {
		volumes = append(volumes, k8scorev1.Volume{
			Name: connectivityRevisionVolumeName,
			VolumeSource: k8scorev1.VolumeSource{ConfigMap: &k8scorev1.ConfigMapVolumeSource{
				LocalObjectReference: k8scorev1.LocalObjectReference{
					Name: options.ConnectivityRevisionConfigMapName,
				},
				Items: []k8scorev1.KeyToPath{{Key: "revision.json", Path: "revision.json"}},
			}},
		})
	}

	if hasImportedEndpointLifecycle(normalized) {
		hostPathType := k8scorev1.HostPathDirectory
		volumes = append(volumes, k8scorev1.Volume{
			Name: hostNetworkNamespaceName,
			VolumeSource: k8scorev1.VolumeSource{HostPath: &k8scorev1.HostPathVolumeSource{
				Path: hostNetworkNamespaceSourcePath, Type: &hostPathType,
			}},
		})
	}
	//nolint:gocritic // one append per planned element reads clearest.
	volumes = append(
		volumes,
		k8scorev1.Volume{ //nolint:gocritic // one append per rendered volume reads clearest.
			Name:         preparationScratchName,
			VolumeSource: k8scorev1.VolumeSource{EmptyDir: &k8scorev1.EmptyDirVolumeSource{}},
		},
	)
	volumes = append(volumes, k8scorev1.Volume{
		Name: inputVolumeName,
		VolumeSource: k8scorev1.VolumeSource{ConfigMap: &k8scorev1.ConfigMapVolumeSource{
			LocalObjectReference: k8scorev1.LocalObjectReference{Name: options.InputConfigMapName},
			Items:                []k8scorev1.KeyToPath{{Key: "input.json", Path: "input.json"}},
		}},
	})

	payloadVolumes, payloadMounts, err := renderPayloadSources(
		normalized,
		options.Namespace,
		options.Payloads,
	)
	if err != nil {
		return nil, err
	}

	volumes = append(volumes, payloadVolumes...)
	if options.CertificateSecretName != "" {
		volumes = append(volumes, k8scorev1.Volume{
			Name: certificateVolumeName,
			VolumeSource: k8scorev1.VolumeSource{Secret: &k8scorev1.SecretVolumeSource{
				SecretName: options.CertificateSecretName,
			}},
		})
	}

	if options.EntropySecretName != "" {
		volumes = append(volumes, k8scorev1.Volume{
			Name: entropyVolumeName,
			VolumeSource: k8scorev1.VolumeSource{Secret: &k8scorev1.SecretVolumeSource{
				SecretName: options.EntropySecretName,
				Items: []k8scorev1.KeyToPath{{
					Key:  clabernetesinternaldeviceplan.EntropySeedKey,
					Path: clabernetesinternaldeviceplan.EntropySeedKey,
				}},
			}},
		})
	}

	certificateVolumes, certificateNames, err := renderLifecycleCertificateVolumes(
		normalized,
		options,
	)
	if err != nil {
		return nil, err
	}

	volumes = append(volumes, certificateVolumes...)

	endpointCertificateVolume, err := renderEndpointCertificateVolume(normalized, options)
	if err != nil {
		return nil, err
	}

	hasEndpointCertificates := endpointCertificateVolume != nil
	if hasEndpointCertificates {
		volumes = append(volumes, *endpointCertificateVolume)
	}

	if options.ProbeSecretName != "" {
		volumes = append(volumes, k8scorev1.Volume{
			Name: probeSecretVolumeName,
			VolumeSource: k8scorev1.VolumeSource{Secret: &k8scorev1.SecretVolumeSource{
				SecretName: options.ProbeSecretName,
			}},
		})
	}

	mountsByContainer, err := indexMounts(normalized, volumeNames)
	if err != nil {
		return nil, err
	}

	containers, deviceVolumes, dns, err := renderContainers(
		normalized,
		mountsByContainer,
		options.EnableContainerStopSignals,
	)
	if err != nil {
		return nil, err
	}

	enableApplicationLogBroker := options.EnableApplicationLogBroker &&
		hasImportedPostDeployLifecycle(normalized)

	hasLifecycle, hasRuntimeScratch, err := renderApplicationLifecycle(
		normalized,
		containers,
		volumeNames,
		options.ProbePolicies,
		options.ProbeSecretName,
		certificateNames,
		enableApplicationLogBroker,
		options.EntropySecretName != "",
		options.ConnectivityRevisionConfigMapName != "",
	)
	if err != nil {
		return nil, err
	}

	if hasLifecycle {
		volumes = append(volumes, k8scorev1.Volume{
			Name: lifecycleVolumeName,
			VolumeSource: k8scorev1.VolumeSource{
				EmptyDir: &k8scorev1.EmptyDirVolumeSource{},
			},
		})
	}

	if hasRuntimeScratch {
		volumes = append(volumes, k8scorev1.Volume{
			Name: lifecycleScratchName,
			VolumeSource: k8scorev1.VolumeSource{
				EmptyDir: &k8scorev1.EmptyDirVolumeSource{},
			},
		})
	}

	if enableApplicationLogBroker {
		if options.ServiceAccountName == "" {
			return nil, errors.New("application log broker has no service account")
		}

		expirationSeconds := int64(3600)
		volumes = append(
			volumes,
			k8scorev1.Volume{
				Name: applicationRuntimeAPIName,
				VolumeSource: k8scorev1.VolumeSource{
					EmptyDir: &k8scorev1.EmptyDirVolumeSource{},
				},
			},
			k8scorev1.Volume{
				Name: applicationRuntimeCredentialName,
				VolumeSource: k8scorev1.VolumeSource{
					Projected: &k8scorev1.ProjectedVolumeSource{
						Sources: []k8scorev1.VolumeProjection{
							{ServiceAccountToken: &k8scorev1.ServiceAccountTokenProjection{
								Path: "token", ExpirationSeconds: &expirationSeconds,
							}},
							{ConfigMap: &k8scorev1.ConfigMapProjection{
								LocalObjectReference: k8scorev1.LocalObjectReference{
									Name: "kube-root-ca.crt",
								},
								Items: []k8scorev1.KeyToPath{{Key: "ca.crt", Path: "ca.crt"}},
							}},
						},
					},
				},
			},
		)
	}

	if err = applyApplicationImagePullPolicy(
		normalized,
		containers,
		options.ApplicationImagePullPolicy,
	); err != nil {
		return nil, err
	}

	applyPrimaryContainerResources(normalized, containers, options.PrimaryContainerResources)

	volumes = append(volumes, deviceVolumes...)
	if err = validateUniqueVolumeNames(volumes); err != nil {
		return nil, err
	}

	initContainers, err := renderHelpers(
		normalized,
		options,
		volumeNames,
		payloadMounts,
		hasLifecycle,
		hasEndpointCertificates,
		enableApplicationLogBroker,
	)
	if err != nil {
		return nil, err
	}

	one := int32(1)
	zero := int32(0)
	falseValue := false
	linux := k8scorev1.Linux

	return &k8sappsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: options.Name, Namespace: options.Namespace,
			Labels: labels, Annotations: annotations, OwnerReferences: options.OwnerReferences,
		},
		Spec: k8sappsv1.DeploymentSpec{
			Replicas:             &one,
			RevisionHistoryLimit: &zero,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{
				directWorkloadLabel: options.Name,
			}},
			Strategy: k8sappsv1.DeploymentStrategy{Type: k8sappsv1.RecreateDeploymentStrategyType},
			Template: k8scorev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels, Annotations: annotations},
				Spec: k8scorev1.PodSpec{
					OS:                           &k8scorev1.PodOS{Name: linux},
					ServiceAccountName:           options.ServiceAccountName,
					AutomountServiceAccountToken: &falseValue,
					// Containers under a container runtime receive no Kubernetes service-link
					// variables; injected *_PORT values break applications that bind
					// environment configuration (and every expose Service would inject them).
					EnableServiceLinks: &falseValue,
					ImagePullSecrets:   slices.Clone(options.ImagePullSecrets),
					NodeSelector:       maps.Clone(options.NodeSelector),
					Tolerations:        slices.Clone(options.Tolerations),
					Affinity:           options.Affinity.DeepCopy(),
					RestartPolicy:      k8scorev1.RestartPolicyAlways,
					Hostname:           options.Name,
					DNSPolicy:          dns.policy,
					DNSConfig:          dns.config,
					HostAliases:        renderHostAliases(normalized, options.Name),
					InitContainers:     initContainers,
					Containers:         containers,
					Volumes:            volumes,
				},
			},
		},
	}, nil
}

// renderHostAliases resolves the imported runtime identities that Docker DNS would resolve in
// local containerlab: chassis component runtime names (e.g. "sros-a") and grouped logical Node
// names other than the Pod hostname map to their Node's management address. Imported package
// hooks (save, post-deploy) dial devices by these names.
func renderHostAliases(
	plan clabernetesinternaldeviceplan.Plan,
	workloadName string,
) []k8scorev1.HostAlias {
	addressByNode := make(map[string]string, len(plan.Management))
	for _, management := range plan.Management {
		address := bareManagementAddress(management.IPv4)
		if address == "" {
			address = bareManagementAddress(management.IPv6)
		}

		if address == "" {
			continue
		}

		addressByNode[management.NodeID] = address
	}

	hostnamesByAddress := map[string][]string{}
	seen := map[string]bool{workloadName: true}
	addHost := func(name, nodeID string) {
		if name == "" || seen[name] {
			return
		}

		address, resolved := addressByNode[nodeID]
		if !resolved {
			return
		}

		seen[name] = true
		hostnamesByAddress[address] = append(hostnamesByAddress[address], name)
	}

	for _, node := range plan.Nodes {
		addHost(node.Name, node.ID)
	}

	for _, container := range plan.Containers {
		addHost(container.RuntimeID, container.NodeID)
	}

	if len(hostnamesByAddress) == 0 {
		return nil
	}

	addresses := make([]string, 0, len(hostnamesByAddress))
	for address := range hostnamesByAddress {
		addresses = append(addresses, address)
	}

	sort.Strings(addresses)

	aliases := make([]k8scorev1.HostAlias, 0, len(addresses))
	for _, address := range addresses {
		hostnames := hostnamesByAddress[address]
		sort.Strings(hostnames)
		aliases = append(aliases, k8scorev1.HostAlias{IP: address, Hostnames: hostnames})
	}

	return aliases
}

func bareManagementAddress(cidr string) string {
	if cidr == "" {
		return ""
	}

	address, _, found := strings.Cut(cidr, "/")
	if !found {
		return cidr
	}

	return address
}

func defaultApplicationContainer(
	plan clabernetesinternaldeviceplan.Plan,
	workloadName string,
) (string, error) {
	for _, node := range plan.Nodes {
		if node.Name != workloadName {
			continue
		}

		if len(node.ContainerIDs) == 0 {
			return "", errors.New("direct workload primary Node has no application container")
		}

		return ApplicationContainerName(node.ContainerIDs[0]), nil
	}

	return "", fmt.Errorf("direct workload has no logical primary named %q", workloadName)
}

func applyPrimaryContainerResources(
	plan clabernetesinternaldeviceplan.Plan,
	containers []k8scorev1.Container,
	policy *k8scorev1.ResourceRequirements,
) {
	if policy == nil {
		return
	}

	primaryIDs := make(map[string]bool, len(plan.Nodes))
	for _, node := range plan.Nodes {
		if len(node.ContainerIDs) != 0 {
			primaryIDs[node.ContainerIDs[0]] = true
		}
	}

	for index, planned := range plan.Containers {
		if !primaryIDs[planned.ID] {
			continue
		}

		if containers[index].Resources.Requests == nil {
			containers[index].Resources.Requests = k8scorev1.ResourceList{}
		}

		if containers[index].Resources.Limits == nil {
			containers[index].Resources.Limits = k8scorev1.ResourceList{}
		}

		for name, quantity := range policy.Requests {
			containers[index].Resources.Requests[name] = quantity.DeepCopy()
		}

		for name, quantity := range policy.Limits {
			containers[index].Resources.Limits[name] = quantity.DeepCopy()
		}
	}
}

func applyApplicationImagePullPolicy(
	plan clabernetesinternaldeviceplan.Plan,
	containers []k8scorev1.Container,
	policy string,
) error {
	if policy == "" {
		return nil
	}

	resolved, err := pullPolicy(policy)
	if err != nil {
		return fmt.Errorf("direct Pod application image pull policy: %w", err)
	}

	for index, planned := range plan.Containers {
		if !planned.ImagePullPolicyExplicit {
			containers[index].ImagePullPolicy = resolved
		}
	}

	return nil
}

func validateOptions(options Options) error {
	for field, value := range map[string]string{
		"name": options.Name, "namespace": options.Namespace,
		"plan ConfigMap":     options.PlanConfigMapName,
		"input ConfigMap":    options.InputConfigMapName,
		"preparation image":  options.PreparationImage,
		"connectivity image": options.ConnectivityImage,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("direct Pod %s is required", field)
		}
	}

	if problems := validation.IsDNS1123Subdomain(options.Name); len(problems) != 0 {
		return fmt.Errorf("direct Pod name is invalid: %s", strings.Join(problems, "; "))
	}

	if problems := validation.IsDNS1123Label(options.Namespace); len(problems) != 0 {
		return fmt.Errorf("direct Pod namespace is invalid: %s", strings.Join(problems, "; "))
	}

	usesProbeSecret := false

	for nodeID, policy := range options.ProbePolicies {
		if nodeID == "" || policy.StartupSeconds < 0 || !validProbePort(policy.TCPPort) ||
			!validProbePort(policy.SSHPort) {
			return errors.New("direct Pod probe policy is invalid")
		}

		sshFields := 0

		for _, present := range []bool{
			policy.SSHUsername != "", policy.SSHPort != 0, policy.SSHPasswordKey != "",
		} {
			if present {
				sshFields++
			}
		}

		if sshFields != 0 && sshFields != 3 {
			return errors.New("direct Pod SSH probe policy is incomplete")
		}

		usesProbeSecret = usesProbeSecret || policy.SSHPasswordKey != ""
	}

	if usesProbeSecret != (options.ProbeSecretName != "") {
		return errors.New("direct Pod probe Secret identity is inconsistent")
	}

	hasLifecycleMode := options.LinkLifecycleMode != ""

	hasLifecycleDigest := options.LinkLifecyclePlanDigest != ""
	if hasLifecycleMode != hasLifecycleDigest {
		return errors.New("direct Pod Link lifecycle identity is incomplete")
	}

	if hasLifecycleMode {
		if options.LinkLifecycleMode != clabernetesinternaldeviceplan.LinkApplyRecreate {
			return fmt.Errorf(
				"direct Pod Link lifecycle mode %q does not roll a Pod",
				options.LinkLifecycleMode,
			)
		}

		encoded := strings.TrimPrefix(options.LinkLifecyclePlanDigest, "sha256:")
		if len(encoded) != 64 {
			return errors.New("direct Pod Link lifecycle plan digest is invalid")
		}

		if _, err := hex.DecodeString(encoded); err != nil {
			return errors.New("direct Pod Link lifecycle plan digest is invalid")
		}
	}

	return nil
}

func validProbePort(port int) bool {
	return port >= 0 && port <= 65535
}

type renderedDNS struct {
	policy k8scorev1.DNSPolicy
	config *k8scorev1.PodDNSConfig
}

func renderContainers(
	plan clabernetesinternaldeviceplan.Plan,
	mountsByContainer map[string][]k8scorev1.VolumeMount,
	enableStopSignals bool,
) ([]k8scorev1.Container, []k8scorev1.Volume, renderedDNS, error) {
	containers := make([]k8scorev1.Container, 0, len(plan.Containers))
	deviceVolumes := []k8scorev1.Volume{}
	containerNames := map[string]string{}
	usedNames := map[string]string{}
	sysctlValues := map[string]string{}

	var commonDNS *clabernetesinternaldeviceplan.DNSConfig

	for _, planned := range plan.Containers {
		name := ApplicationContainerName(planned.ID)
		if previous := usedNames[name]; previous != "" && previous != planned.ID {
			return nil, nil, renderedDNS{}, fmt.Errorf(
				"planned container names %q and %q collide as %q",
				previous,
				planned.ID,
				name,
			)
		}

		usedNames[name] = planned.ID
		containerNames[planned.ID] = name
	}

	for _, planned := range plan.Containers {
		if _, exists := containerNames[planned.NamespaceOwnerID]; !exists {
			return nil, nil, renderedDNS{}, fmt.Errorf(
				"container %q has unknown namespace owner %q",
				planned.ID,
				planned.NamespaceOwnerID,
			)
		}

		container, extraVolumes, err := renderContainer(
			planned,
			containerNames[planned.ID],
			mountsByContainer[planned.ID],
			enableStopSignals,
		)
		if err != nil {
			return nil, nil, renderedDNS{}, err
		}

		containers = append(containers, container)
		deviceVolumes = append(deviceVolumes, extraVolumes...)

		for _, sysctl := range planned.Security.Sysctls {
			if existing, exists := sysctlValues[sysctl.Name]; exists && existing != sysctl.Value {
				return nil, nil, renderedDNS{}, fmt.Errorf(
					"containers request conflicting network-namespace sysctl %q",
					sysctl.Name,
				)
			}

			sysctlValues[sysctl.Name] = sysctl.Value
		}

		if !emptyDNS(planned.DNS) {
			if commonDNS != nil && !reflectDNS(*commonDNS, planned.DNS) {
				return nil, nil, renderedDNS{}, errors.New("containers request conflicting Pod DNS")
			}

			duplicate := planned.DNS
			commonDNS = &duplicate
		}
	}

	dns := renderedDNS{policy: k8scorev1.DNSClusterFirst}
	if commonDNS != nil {
		dns.policy = k8scorev1.DNSNone

		dns.config = &k8scorev1.PodDNSConfig{
			Nameservers: slices.Clone(commonDNS.Servers),
			Searches:    slices.Clone(commonDNS.Search),
		}
		for _, option := range commonDNS.Options {
			name, value, _ := strings.Cut(option, ":")
			dns.config.Options = append(dns.config.Options, k8scorev1.PodDNSConfigOption{
				Name: name, Value: optionalString(value),
			})
		}
	}

	return containers, deviceVolumes, dns, nil
}

func renderLifecycleCertificateVolumes(
	plan clabernetesinternaldeviceplan.Plan,
	options Options,
) ([]k8scorev1.Volume, map[string]string, error) {
	result := []k8scorev1.Volume{}
	byNode := map[string]string{}

	itemsByNode, err := certificateProjectionItemsByNode(plan, options)
	if err != nil {
		return nil, nil, err
	}

	for _, node := range plan.Nodes {
		items := itemsByNode[node.ID]
		if len(items) == 0 {
			continue
		}

		name := dnsName("node-lifecycle-cert", node.ID)
		byNode[node.ID] = name
		result = append(result, k8scorev1.Volume{
			Name: name,
			VolumeSource: k8scorev1.VolumeSource{Secret: &k8scorev1.SecretVolumeSource{
				SecretName: options.CertificateSecretName,
				Items:      items,
			}},
		})
	}

	return result, byNode, nil
}

func renderEndpointCertificateVolume(
	plan clabernetesinternaldeviceplan.Plan,
	options Options,
) (*k8scorev1.Volume, error) {
	itemsByNode, err := certificateProjectionItemsByNode(plan, options)
	if err != nil {
		return nil, err
	}

	keys := map[string]bool{}

	for _, action := range plan.Actions {
		if action.Phase != clabernetesinternaldeviceplan.PhaseInterfaceFixup ||
			action.Kind != clabernetesinternaldeviceplan.ActionImportedDeployEndpoints ||
			action.ImportedDeployEndpoints == nil {
			continue
		}

		for _, item := range itemsByNode[action.Target.NodeID] {
			keys[item.Key] = true
		}
	}

	if len(keys) == 0 {
		return nil, nil //nolint:nilnil // no keys means no projection to render.
	}

	items := make([]k8scorev1.KeyToPath, 0, len(keys))
	for key := range keys {
		items = append(items, k8scorev1.KeyToPath{Key: key, Path: key})
	}

	slices.SortFunc(items, func(left, right k8scorev1.KeyToPath) int {
		return strings.Compare(left.Key, right.Key)
	})

	return &k8scorev1.Volume{
		Name: endpointCertificateName,
		VolumeSource: k8scorev1.VolumeSource{Secret: &k8scorev1.SecretVolumeSource{
			SecretName: options.CertificateSecretName,
			Items:      items,
		}},
	}, nil
}

func certificateProjectionItemsByNode(
	plan clabernetesinternaldeviceplan.Plan,
	options Options,
) (map[string][]k8scorev1.KeyToPath, error) {
	result := map[string][]k8scorev1.KeyToPath{}
	if len(options.CertificateInputs) == 0 {
		return result, nil
	}

	if options.CertificateSecretName == "" {
		return nil, errors.New("lifecycle certificates have no backing Secret")
	}

	plannedNodes := make(map[string]bool, len(plan.Nodes))
	for _, node := range plan.Nodes {
		plannedNodes[node.ID] = true
	}

	keysByNode := map[string]map[string]bool{}

	for _, input := range options.CertificateInputs {
		if !plannedNodes[input.NodeID] || input.StorageName == "" {
			return nil, errors.New("lifecycle certificate input has no planned Node identity")
		}

		if keysByNode[input.NodeID] == nil {
			keysByNode[input.NodeID] = map[string]bool{
				clabernetesinternaldeviceplan.CertificateCACertKey: true,
			}
		}

		certificateKey, privateKeyKey := clabernetesinternaldeviceplan.CertificateMaterialKeys(
			input.NodeID,
			input.StorageName,
		)
		keysByNode[input.NodeID][certificateKey] = true
		keysByNode[input.NodeID][privateKeyKey] = true
	}

	for nodeID, keys := range keysByNode {
		items := make([]k8scorev1.KeyToPath, 0, len(keys))
		for key := range keys {
			items = append(items, k8scorev1.KeyToPath{Key: key, Path: key})
		}

		slices.SortFunc(items, func(left, right k8scorev1.KeyToPath) int {
			return strings.Compare(left.Key, right.Key)
		})
		result[nodeID] = items
	}

	return result, nil
}

// renderApplicationLifecycle maps generic pre-start mount operations and recorded post-start
// operations into the actual application containers. Each target receives only its logical
// Node's artifact volume; no grouped Node can observe another Node's generated or secret-derived
// files.
func renderApplicationLifecycle(
	plan clabernetesinternaldeviceplan.Plan,
	containers []k8scorev1.Container,
	volumeNames map[string]string,
	probePolicies map[string]ProbePolicy,
	probeSecretName string,
	certificateNames map[string]string,
	enableApplicationLogBroker bool,
	hasEntropy bool,
	hasConnectivityRevision bool,
) (bool, bool, error) {
	containerIndexes := make(map[string]int, len(plan.Containers))

	containerNodes := make(map[string]string, len(plan.Containers))
	for index, container := range plan.Containers {
		containerIndexes[container.ID] = index
		containerNodes[container.ID] = container.NodeID
	}

	files := make(map[string]clabernetesinternaldeviceplan.FilePlan, len(plan.Files))
	for _, file := range plan.Files {
		files[file.ID] = file
	}

	artifactVolumes := map[string]string{}

	for _, volume := range plan.Volumes {
		if volume.Kind != clabernetesinternaldeviceplan.VolumeArtifacts {
			continue
		}

		if artifactVolumes[volume.NodeID] != "" {
			return false, false, errors.New("logical Node has multiple artifact volumes")
		}

		artifactVolumes[volume.NodeID] = volumeNames[volume.ID]
	}

	targets := map[string]bool{}
	artifactTargets := map[string]bool{}
	preStartTargets := map[string]bool{}
	postStartTargets := map[string]bool{}
	importedPostDeployTargets := map[string]bool{}
	importedSaveTargets := map[string]bool{}
	readinessTargets := map[string]bool{}

	for _, planned := range plan.Containers {
		// Every direct application container receives the fixed c9s lifecycle binary and a
		// Pod-scoped state directory. This is the generic, shell-independent boundary used when
		// the imported plan declares Restart for a future Link change.
		targets[planned.ID] = true
		if planned.StartupDelay > 0 {
			preStartTargets[planned.ID] = true
			targets[planned.ID] = true
		}
	}

	for _, action := range plan.Actions {
		preStartMount := action.Phase == clabernetesinternaldeviceplan.PhasePreStart &&
			action.Kind == clabernetesinternaldeviceplan.ActionMount
		importedReadiness := action.Phase == clabernetesinternaldeviceplan.PhaseReadiness &&
			action.Kind == clabernetesinternaldeviceplan.ActionImportedReadiness

		importedSave := action.Phase == clabernetesinternaldeviceplan.PhaseSave &&
			action.Kind == clabernetesinternaldeviceplan.ActionSave
		if action.Phase != clabernetesinternaldeviceplan.PhasePostStart && !preStartMount &&
			!importedReadiness && !importedSave {
			continue
		}

		containerIndex, exists := containerIndexes[action.Target.ContainerID]
		if !exists || action.Target.ContainerID == "" {
			return false, false, fmt.Errorf(
				"application lifecycle action %q has no application-container target",
				action.ID,
			)
		}

		nodeID := containerNodes[action.Target.ContainerID]
		if nodeID == "" || action.Target.NodeID != nodeID {
			return false, false, fmt.Errorf(
				"application lifecycle action %q crosses logical Node ownership",
				action.ID,
			)
		}

		if importedReadiness {
			if action.ImportedReadiness == nil {
				return false, false, fmt.Errorf(
					"readiness action %q has no imported readiness payload",
					action.ID,
				)
			}

			readinessTargets[action.Target.ContainerID] = true
			targets[action.Target.ContainerID] = true

			continue
		}

		if importedSave {
			if action.Save == nil ||
				action.Save.Method != clabernetesinternaldeviceplan.SaveMethodImported {
				return false, false, fmt.Errorf(
					"save action %q has no imported save payload",
					action.ID,
				)
			}

			artifactTargets[action.Target.ContainerID] = true
			importedSaveTargets[action.Target.ContainerID] = true
			targets[action.Target.ContainerID] = true

			continue
		}

		if preStartMount {
			planned := plan.Containers[containerIndex]

			if action.Mount == nil || action.Mount.Filesystem != "tmpfs" ||
				action.Mount.Source != "tmpfs" {
				return false, false, fmt.Errorf(
					"pre-start action %q has unsupported scoped filesystem operation",
					action.ID,
				)
			}

			if !planned.Security.Privileged &&
				!containsLinuxCapability(planned.Security.CapabilitiesAdd, "SYS_ADMIN") {
				return false, false, fmt.Errorf(
					"pre-start mount action %q requires privileged execution or SYS_ADMIN",
					action.ID,
				)
			}

			preStartTargets[action.Target.ContainerID] = true
			targets[action.Target.ContainerID] = true

			continue
		}

		switch action.Kind {
		case clabernetesinternaldeviceplan.ActionImportedPostDeploy:
			if action.ImportedPostDeploy == nil {
				return false, false, fmt.Errorf(
					"post-start action %q has no imported post-deploy payload",
					action.ID,
				)
			}

			artifactTargets[action.Target.ContainerID] = true
			importedPostDeployTargets[action.Target.ContainerID] = true
		case clabernetesinternaldeviceplan.ActionExec:
		case clabernetesinternaldeviceplan.ActionFile:
			if action.File == nil || files[action.File.FileID].NodeID != nodeID {
				return false, false, fmt.Errorf(
					"post-start action %q has an unscoped file",
					action.ID,
				)
			}

			artifactTargets[action.Target.ContainerID] = true
		case clabernetesinternaldeviceplan.ActionWriteStdin:
			if action.WriteStdin == nil || files[action.WriteStdin.FileID].NodeID != nodeID {
				return false, false, fmt.Errorf(
					"post-start action %q has unscoped stdin data",
					action.ID,
				)
			}

			artifactTargets[action.Target.ContainerID] = true
		default:
			return false, false, fmt.Errorf(
				"post-start action %q uses unsupported application-container operation %q",
				action.ID,
				action.Kind,
			)
		}

		if containerIndex >= len(containers) {
			return false, false, errors.New("post-start target is absent from rendered containers")
		}

		targets[action.Target.ContainerID] = true
		postStartTargets[action.Target.ContainerID] = true
	}

	for containerID := range targets {
		index := containerIndexes[containerID]
		container := &containers[index]

		mounts := []k8scorev1.VolumeMount{
			{Name: lifecycleVolumeName, MountPath: lifecycleBinaryRoot, ReadOnly: true},
			{Name: planVolumeName, MountPath: lifecyclePlanRoot, ReadOnly: true},
			{Name: lifecycleScratchName, MountPath: lifecycleScratchRoot},
		}
		if readinessTargets[containerID] || postStartTargets[containerID] ||
			importedSaveTargets[containerID] {
			mounts = append(mounts,
				k8scorev1.VolumeMount{
					Name: inputVolumeName, MountPath: lifecycleInputRoot, ReadOnly: true,
				},
			)
		}

		// A retained Pod recreated after a live or restart Link revision must replay its plan
		// actions against the revised interface set, so post-start lifecycle boundaries read
		// the projected connectivity revision the sidecar already consumes.
		if hasConnectivityRevision && postStartTargets[containerID] {
			mounts = append(mounts, k8scorev1.VolumeMount{
				Name:      connectivityRevisionVolumeName,
				MountPath: connectivityRevisionMountPath,
				ReadOnly:  true,
			})
		}

		if hasEntropy && (readinessTargets[containerID] ||
			importedPostDeployTargets[containerID] || importedSaveTargets[containerID]) {
			mounts = append(mounts, k8scorev1.VolumeMount{
				Name: entropyVolumeName, MountPath: lifecycleEntropyRoot, ReadOnly: true,
			})
		}

		if readinessTargets[containerID] {
			policy := probePolicies[containerNodes[containerID]]
			if policy.SSHPasswordKey != "" {
				if probeSecretName == "" {
					return false, false, errors.New("readiness target has no probe Secret")
				}

				mounts = append(mounts, k8scorev1.VolumeMount{
					Name: probeSecretVolumeName, MountPath: probePasswordPath,
					SubPath: policy.SSHPasswordKey, ReadOnly: true,
				})
			}
		}

		if artifactTargets[containerID] {
			nodeID := containerNodes[containerID]

			volumeName := artifactVolumes[nodeID]
			if volumeName == "" {
				return false,
					false,
					errors.New("post-start target has no plan-owned artifact volume")
			}

			mounts = append(mounts, k8scorev1.VolumeMount{
				Name: volumeName,
				MountPath: path.Join(
					lifecycleArtifactRoot,
					clabernetesinternaldeviceplan.ArtifactNodeDirectory(nodeID),
				),
				ReadOnly: !importedPostDeployTargets[containerID] &&
					!importedSaveTargets[containerID],
			})
		}

		certificateVolume := certificateNames[containerNodes[containerID]]
		if importedPostDeployTargets[containerID] && certificateVolume != "" {
			mounts = append(mounts, k8scorev1.VolumeMount{
				Name: certificateVolume, MountPath: lifecycleCertificateRoot, ReadOnly: true,
			})
		}

		if importedPostDeployTargets[containerID] && enableApplicationLogBroker {
			mounts = append(mounts, k8scorev1.VolumeMount{
				Name:      applicationRuntimeAPIName,
				MountPath: applicationRuntimeAPIRoot,
				ReadOnly:  true,
			})
		}

		for _, mount := range mounts {
			for _, existing := range container.VolumeMounts {
				if existing.MountPath == mount.MountPath {
					return false, false, fmt.Errorf(
						"container %q already uses lifecycle mount path %q",
						containerID,
						mount.MountPath,
					)
				}
			}

			container.VolumeMounts = append(container.VolumeMounts, mount)
		}

		slices.SortFunc(container.VolumeMounts, func(left, right k8scorev1.VolumeMount) int {
			return strings.Compare(left.MountPath, right.MountPath)
		})
		// Every application container starts through the launch boundary: it applies pre-start
		// operations and restores the container-runtime-conventional process limits imported
		// packages assume before exec-ing the image's real process.
		container.Command = []string{
			lifecycleBinaryPath,
			"node-runtime",
			"launch",
			"--plan",
			lifecyclePlanRoot + "/plan.json",
			"--containerID",
			containerID,
		}

		container.Args = nil
		if postStartTargets[containerID] {
			if container.Lifecycle == nil {
				container.Lifecycle = &k8scorev1.Lifecycle{}
			}

			container.Lifecycle.PostStart = &k8scorev1.LifecycleHandler{
				Exec: &k8scorev1.ExecAction{Command: []string{
					lifecycleBinaryPath,
					"node-runtime",
					"lifecycle",
					"--plan",
					lifecyclePlanRoot + "/plan.json",
					"--input",
					lifecycleInputRoot + "/input.json",
					"--phase",
					string(clabernetesinternaldeviceplan.PhasePostStart),
					"--containerID",
					containerID,
					"--artifacts",
					lifecycleArtifactRoot,
					"--scratch",
					lifecycleScratchRoot,
					"--revision",
					plan.Planner.Revision,
				}},
			}
			if importedPostDeployTargets[containerID] &&
				certificateNames[containerNodes[containerID]] != "" {
				container.Lifecycle.PostStart.Exec.Command = append(
					container.Lifecycle.PostStart.Exec.Command,
					"--certificates",
					lifecycleCertificateRoot,
				)
			}

			if hasEntropy && importedPostDeployTargets[containerID] {
				container.Lifecycle.PostStart.Exec.Command = append(
					container.Lifecycle.PostStart.Exec.Command,
					"--entropy",
					lifecycleEntropyRoot,
				)
			}

			if hasConnectivityRevision {
				container.Lifecycle.PostStart.Exec.Command = append(
					container.Lifecycle.PostStart.Exec.Command,
					"--connectivityRevision",
					connectivityRevisionMountPath+"/revision.json",
				)
			}
		}

		if readinessTargets[containerID] {
			policy := probePolicies[containerNodes[containerID]]

			command := []string{
				lifecycleBinaryPath,
				"node-runtime",
				"readiness",
				"--plan",
				lifecyclePlanRoot + "/plan.json",
				"--input",
				lifecycleInputRoot + "/input.json",
				"--containerID",
				containerID,
				"--scratch",
				lifecycleScratchRoot,
				"--revision",
				plan.Planner.Revision,
			}
			if hasEntropy {
				command = append(command, "--entropy", lifecycleEntropyRoot)
			}

			if policy.TCPPort != 0 {
				command = append(command, "--tcpPort", strconv.Itoa(policy.TCPPort))
			}

			if policy.SSHPasswordKey != "" {
				command = append(
					command,
					"--sshUsername",
					policy.SSHUsername,
					"--sshPort",
					strconv.Itoa(policy.SSHPort),
					"--sshPasswordFile",
					probePasswordPath,
				)
			}

			container.StartupProbe, container.ReadinessProbe = composeImportedReadinessProbes(
				container.StartupProbe,
				container.ReadinessProbe,
				command,
				policy.StartupSeconds,
			)
		}
	}

	return len(targets) != 0, len(targets) != 0, nil
}

// ApplicationRestartCommand returns the shell-independent command used by the manager to signal
// one direct application container after a planner-declared Restart connectivity transition.
func ApplicationRestartCommand(
	requestDigest string,
	container clabernetesinternaldeviceplan.ContainerPlan,
) ([]string, error) {
	encoded := strings.TrimPrefix(requestDigest, "sha256:")
	if len(encoded) != 64 || encoded == requestDigest {
		return nil, errors.New("application restart plan digest is invalid")
	}

	if _, err := hex.DecodeString(encoded); err != nil {
		return nil, errors.New("application restart plan digest is invalid")
	}

	if container.ID == "" {
		return nil, errors.New("application restart container identity is empty")
	}

	command := []string{
		lifecycleBinaryPath,
		"node-runtime",
		"restart",
		"--request",
		requestDigest,
		"--state",
		path.Join(lifecycleScratchRoot, "link-restarts", dnsName("container", container.ID)),
	}
	if container.StopSignal != "" {
		command = append(command, "--signal", container.StopSignal)
	}

	return command, nil
}

// ApplicationSaveCommand returns the shell-independent command for one package-owned SaveConfig
// action in the primary direct application container of a logical Node.
func ApplicationSaveCommand(
	plan clabernetesinternaldeviceplan.Plan,
	containerID string,
) ([]string, error) {
	normalized, err := clabernetesinternaldeviceplan.NormalizePlan(plan)
	if err != nil {
		return nil, err
	}

	var container *clabernetesinternaldeviceplan.ContainerPlan

	for index := range normalized.Containers {
		if normalized.Containers[index].ID == containerID {
			container = &normalized.Containers[index]

			break
		}
	}

	if container == nil {
		return nil, errors.New("application save container is absent from the accepted plan")
	}

	owned := false

	for _, node := range normalized.Nodes {
		if node.ID != container.NodeID {
			continue
		}

		if slices.Contains(node.ContainerIDs, containerID) {
			owned = true
		}

		break
	}

	if !owned {
		return nil, errors.New("application save target is not a logical Node container")
	}

	actionCount := 0

	for _, action := range normalized.Actions {
		if action.Phase == clabernetesinternaldeviceplan.PhaseSave &&
			action.Kind == clabernetesinternaldeviceplan.ActionSave &&
			action.Target.NodeID == container.NodeID &&
			action.Target.ContainerID == containerID && action.Save != nil &&
			action.Save.Method == clabernetesinternaldeviceplan.SaveMethodImported {
			actionCount++
		}
	}

	if actionCount != 1 {
		return nil, errors.New("application save target requires exactly one imported save action")
	}

	return []string{
		lifecycleBinaryPath,
		"node-runtime",
		"lifecycle",
		"--plan",
		lifecyclePlanRoot + "/plan.json",
		"--input",
		lifecycleInputRoot + "/input.json",
		"--phase",
		string(clabernetesinternaldeviceplan.PhaseSave),
		"--containerID",
		containerID,
		"--artifacts",
		lifecycleArtifactRoot,
		"--scratch",
		lifecycleScratchRoot,
		"--revision",
		normalized.Planner.Revision,
		"--entropy",
		lifecycleEntropyRoot,
	}, nil
}

// PacketCaptureCommand returns the fixed connectivity-helper command for a finite capture of one
// interface owned by the requested opaque logical Node in the accepted plan.
func PacketCaptureCommand(
	plan clabernetesinternaldeviceplan.Plan,
	options clabernetesinternaldirectruntime.PacketCaptureOptions,
) ([]string, error) {
	normalizedOptions, err := clabernetesinternaldirectruntime.NormalizePacketCaptureOptions(
		options,
	)
	if err != nil {
		return nil, err
	}

	if _, err = clabernetesinternaldirectruntime.PacketCaptureTarget(
		plan,
		normalizedOptions.NodeID,
		normalizedOptions.InterfaceName,
	); err != nil {
		return nil, err
	}

	command := []string{
		runtimeBinaryPath,
		"node-runtime",
		"packet-capture",
		"--plan",
		planMountPath + "/plan.json",
		"--input",
		inputMountPath + "/input.json",
		"--connectivityRevision",
		connectivityRevisionMountPath + "/revision.json",
		"--nodeID",
		normalizedOptions.NodeID,
		"--interface",
		normalizedOptions.InterfaceName,
		"--snapLength",
		strconv.Itoa(normalizedOptions.SnapLength),
	}
	if normalizedOptions.PacketLimit != 0 {
		command = append(command, "--packetLimit", strconv.Itoa(normalizedOptions.PacketLimit))
	}

	if normalizedOptions.Duration != 0 {
		command = append(command, "--duration", normalizedOptions.Duration.String())
	}

	return command, nil
}

func composeImportedReadinessProbes(
	startup,
	readiness *k8scorev1.Probe,
	command []string,
	startupSeconds int,
) (*k8scorev1.Probe, *k8scorev1.Probe) {
	if readiness == nil {
		readiness = &k8scorev1.Probe{
			PeriodSeconds: 10, TimeoutSeconds: 10,
			SuccessThreshold: 1, FailureThreshold: 3,
		}
	} else {
		readiness = readiness.DeepCopy()
	}

	readiness.ProbeHandler = k8scorev1.ProbeHandler{
		Exec: &k8scorev1.ExecAction{Command: slices.Clone(command)},
	}
	if startup == nil {
		startup = readiness.DeepCopy()
		startup.InitialDelaySeconds = 10
	} else {
		startup = startup.DeepCopy()
	}

	if startupSeconds == 0 {
		startupSeconds = 15 * 60
	}

	startup.FailureThreshold = max(
		startup.FailureThreshold,
		max(
			1,
			//nolint:gosec // the value is bounded by validated plan input or a kernel interface width.
			int32((startupSeconds+int(startup.PeriodSeconds)-1)/int(startup.PeriodSeconds)),
		),
	)
	startup.ProbeHandler = k8scorev1.ProbeHandler{
		Exec: &k8scorev1.ExecAction{Command: slices.Clone(command)},
	}

	return startup, readiness
}

func containsLinuxCapability(values []string, expected string) bool {
	for _, value := range values {
		value = strings.ToUpper(strings.TrimSpace(value))

		value = strings.TrimPrefix(value, "CAP_")
		if value == expected {
			return true
		}
	}

	return false
}

func renderContainer(
	planned clabernetesinternaldeviceplan.ContainerPlan,
	name string,
	mounts []k8scorev1.VolumeMount,
	enableStopSignals bool,
) (k8scorev1.Container, []k8scorev1.Volume, error) {
	if planned.Resources.CPUSet != "" || planned.Resources.HugePages != "" {
		return k8scorev1.Container{}, nil, fmt.Errorf(
			"container %q requests CPU pinning or unsized huge pages with no portable Pod mapping",
			planned.ID,
		)
	}

	if planned.StopSignal != "" && !enableStopSignals {
		return k8scorev1.Container{}, nil, fmt.Errorf(
			"container %q requires the Kubernetes ContainerStopSignals feature",
			planned.ID,
		)
	}

	if !portableRestartPolicy(planned.RestartPolicy) {
		return k8scorev1.Container{}, nil, fmt.Errorf(
			"container %q restart policy %q has no shared-Pod mapping",
			planned.ID,
			planned.RestartPolicy,
		)
	}

	security, err := renderSecurity(planned.Security, planned.User)
	if err != nil {
		return k8scorev1.Container{}, nil, fmt.Errorf("container %q: %w", planned.ID, err)
	}

	resources, err := renderResources(planned.Resources)
	if err != nil {
		return k8scorev1.Container{}, nil, fmt.Errorf("container %q: %w", planned.ID, err)
	}

	container := k8scorev1.Container{
		Name: name, Image: immutableImage(planned.Image, planned.ImageDigest),
		Command: slices.Clone(planned.Entrypoint), Args: slices.Clone(planned.Command),
		WorkingDir: planned.WorkingDir, TTY: planned.TTY, Stdin: planned.Stdin,
		SecurityContext: security, Resources: resources, VolumeMounts: slices.Clone(mounts),
	}

	container.ImagePullPolicy, err = pullPolicy(planned.ImagePullPolicy)
	if err != nil {
		return k8scorev1.Container{}, nil, fmt.Errorf("container %q: %w", planned.ID, err)
	}
	// Every application container learns the Pod's own cluster address through the downward API:
	// it is the runtime management identity of every logical Node the controller left
	// unaddressed. A planned variable of the same name wins because Kubernetes resolves the last
	// occurrence.
	container.Env = append(container.Env, k8scorev1.EnvVar{
		Name: clabernetesinternaldirectruntime.PodAddressEnvironmentVariable,
		ValueFrom: &k8scorev1.EnvVarSource{FieldRef: &k8scorev1.ObjectFieldSelector{
			FieldPath: "status.podIP",
		}},
	})
	for _, variable := range planned.Environment {
		if problems := validation.IsEnvVarName(variable.Name); len(problems) != 0 {
			return k8scorev1.Container{}, nil, errors.New("invalid environment variable name")
		}

		container.Env = append(
			container.Env,
			k8scorev1.EnvVar{Name: variable.Name, Value: variable.Value},
		)
	}

	for _, port := range planned.Ports {
		container.Ports = append(container.Ports, k8scorev1.ContainerPort{
			ContainerPort: int32( //nolint:gosec // the value is bounded by validated plan input or a kernel interface width.
				port.Number,
			), Protocol: k8scorev1.Protocol(strings.ToUpper(port.Protocol)),
		})
	}

	container.StartupProbe, container.ReadinessProbe, err = renderHealthcheck(planned.Healthcheck)
	if err != nil {
		return k8scorev1.Container{}, nil, fmt.Errorf("container %q: %w", planned.ID, err)
	}

	if planned.StopSignal != "" {
		signal := k8scorev1.Signal(planned.StopSignal)
		container.Lifecycle = &k8scorev1.Lifecycle{StopSignal: &signal}
	}

	extraVolumes := make([]k8scorev1.Volume, 0, len(planned.Security.Devices))
	for index, device := range planned.Security.Devices {
		if !planned.Security.Privileged {
			return k8scorev1.Container{}, nil, fmt.Errorf(
				"device %q requires privileged container cgroup access",
				device.HostPath,
			)
		}

		volumeName := dnsName("device", planned.ID+"/"+strconv.Itoa(index)+"/"+device.HostPath)
		hostPathType := k8scorev1.HostPathCharDev
		extraVolumes = append(extraVolumes, k8scorev1.Volume{
			Name: volumeName,
			VolumeSource: k8scorev1.VolumeSource{HostPath: &k8scorev1.HostPathVolumeSource{
				Path: device.HostPath, Type: &hostPathType,
			}},
		})
		container.VolumeMounts = append(container.VolumeMounts, k8scorev1.VolumeMount{
			Name: volumeName, MountPath: device.ContainerPath,
			ReadOnly: !strings.Contains(device.Permissions, "w"),
		})
	}

	return container, extraVolumes, nil
}

func renderSecurity(
	planned clabernetesinternaldeviceplan.SecurityPlan,
	user string,
) (*k8scorev1.SecurityContext, error) {
	security := &k8scorev1.SecurityContext{
		Privileged:             optionalBool(planned.Privileged),
		ReadOnlyRootFilesystem: optionalBool(planned.ReadOnlyRootFS),
		Capabilities: &k8scorev1.Capabilities{
			Add:  mapCapabilities(planned.CapabilitiesAdd),
			Drop: mapCapabilities(planned.CapabilitiesDrop),
		},
	}

	if user != "" {
		uid, gid, err := parseUser(user)
		if err != nil {
			return nil, err
		}

		security.RunAsUser = uid
		security.RunAsGroup = gid
	}

	security.SeccompProfile = mapSeccomp(planned.SeccompProfile)
	if planned.SeccompProfile != "" && security.SeccompProfile == nil {
		return nil, fmt.Errorf("seccomp profile %q is not portable", planned.SeccompProfile)
	}

	security.AppArmorProfile = mapAppArmor(planned.AppArmorProfile)
	if planned.AppArmorProfile != "" && security.AppArmorProfile == nil {
		return nil, fmt.Errorf("AppArmor profile %q is not portable", planned.AppArmorProfile)
	}

	return security, nil
}

func renderResources(
	planned clabernetesinternaldeviceplan.ResourcePlan,
) (k8scorev1.ResourceRequirements, error) {
	result := k8scorev1.ResourceRequirements{}

	for _, item := range []struct {
		value    string
		name     k8scorev1.ResourceName
		requests bool
	}{
		{planned.CPURequest, k8scorev1.ResourceCPU, true},
		{planned.MemoryRequest, k8scorev1.ResourceMemory, true},
		{planned.CPULimit, k8scorev1.ResourceCPU, false},
		{planned.MemoryLimit, k8scorev1.ResourceMemory, false},
	} {
		if item.value == "" {
			continue
		}

		quantity, err := apiresource.ParseQuantity(item.value)
		if err != nil {
			return k8scorev1.ResourceRequirements{}, errors.New("invalid resource quantity")
		}

		if item.requests {
			if result.Requests == nil {
				result.Requests = k8scorev1.ResourceList{}
			}

			result.Requests[item.name] = quantity
		} else {
			if result.Limits == nil {
				result.Limits = k8scorev1.ResourceList{}
			}

			result.Limits[item.name] = quantity
		}
	}

	return result, nil
}

func renderHealthcheck(
	value *clabernetesinternaldeviceplan.Healthcheck,
) (*k8scorev1.Probe, *k8scorev1.Probe, error) {
	if value == nil || len(value.Test) == 0 || strings.EqualFold(value.Test[0], "NONE") {
		return nil, nil, nil
	}

	command := slices.Clone(value.Test)
	switch strings.ToUpper(command[0]) {
	case "CMD":
		command = command[1:]
	case "CMD-SHELL":
		if len(command) < 2 {
			return nil, nil, errors.New("healthcheck shell command is empty")
		}

		command = []string{"/bin/sh", "-c", strings.Join(command[1:], " ")}
	}

	if len(command) == 0 {
		return nil, nil, errors.New("healthcheck command is empty")
	}

	periodSeconds, err := healthcheckSeconds(value.Interval, 30)
	if err != nil {
		return nil, nil, fmt.Errorf("healthcheck interval: %w", err)
	}

	timeoutSeconds, err := healthcheckSeconds(value.Timeout, 30)
	if err != nil {
		return nil, nil, fmt.Errorf("healthcheck timeout: %w", err)
	}

	failureThreshold := value.Retries
	if failureThreshold == 0 {
		failureThreshold = 3
	}

	if failureThreshold < 1 || failureThreshold > math.MaxInt32 {
		return nil, nil, errors.New("healthcheck retries are outside Kubernetes probe limits")
	}

	readiness := &k8scorev1.Probe{
		ProbeHandler:     k8scorev1.ProbeHandler{Exec: &k8scorev1.ExecAction{Command: command}},
		PeriodSeconds:    periodSeconds,
		TimeoutSeconds:   timeoutSeconds,
		SuccessThreshold: 1,
		FailureThreshold: int32(failureThreshold),
	}
	if value.StartPeriod == 0 {
		return nil, readiness, nil
	}

	startSeconds, err := healthcheckSeconds(value.StartPeriod, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("healthcheck start period: %w", err)
	}
	// OCI/Docker ignores failures during StartPeriod but ends that allowance on the first
	// success. A Kubernetes startup probe has the same early-success handoff. Including the
	// ordinary retry budget after the rounded-up start window preserves the earliest failure
	// point while making an application that never becomes healthy restart instead of run
	// indefinitely as an unready Pod.
	startIntervals := (int64(startSeconds) + int64(periodSeconds) - 1) / int64(periodSeconds)

	startupFailures := startIntervals + int64(failureThreshold)
	if startupFailures > math.MaxInt32 {
		return nil, nil, errors.New("healthcheck startup allowance exceeds Kubernetes probe limits")
	}

	startup := readiness.DeepCopy()
	//nolint:gosec // the value is bounded by validated plan input or a kernel interface width.
	startup.FailureThreshold = int32(
		startupFailures,
	) //nolint:gosec // the value is bounded by validated plan input or a kernel interface width.

	return startup, readiness, nil
}

func healthcheckSeconds(value int64, defaultSeconds int32) (int32, error) {
	if value == 0 {
		return defaultSeconds, nil
	}

	if value < 0 {
		return 0, errors.New("duration is negative")
	}

	seconds := value / int64(time.Second)
	if value%int64(time.Second) != 0 {
		seconds++
	}

	if seconds < 1 || seconds > math.MaxInt32 {
		return 0, errors.New("duration is outside Kubernetes probe limits")
	}

	return int32(seconds), nil
}

func renderVolumes(
	plans []clabernetesinternaldeviceplan.VolumePlan,
	persistentVolumeClaims map[string]string,
) ([]k8scorev1.Volume, map[string]string, error) {
	volumes := make([]k8scorev1.Volume, 0, len(plans))
	names := make(map[string]string, len(plans))
	used := map[string]string{}

	for _, planned := range plans {
		name := dnsName("volume", planned.ID)
		if previous := used[name]; previous != "" && previous != planned.ID {
			return nil, nil, fmt.Errorf(
				"planned volume names %q and %q collide",
				previous,
				planned.ID,
			)
		}

		used[name] = planned.ID
		names[planned.ID] = name
		volume := k8scorev1.Volume{Name: name}

		switch planned.Kind {
		case clabernetesinternaldeviceplan.VolumeArtifacts:
			if claimName := persistentVolumeClaims[planned.NodeID]; claimName != "" {
				if problems := validation.IsDNS1123Subdomain(claimName); len(problems) != 0 {
					return nil, nil, fmt.Errorf("volume %q has an invalid claim name", planned.ID)
				}

				volume.PersistentVolumeClaim = &k8scorev1.PersistentVolumeClaimVolumeSource{
					ClaimName: claimName,
				}
			} else {
				volume.EmptyDir = &k8scorev1.EmptyDirVolumeSource{}
			}
		case clabernetesinternaldeviceplan.VolumeEmptyDir:
			empty := &k8scorev1.EmptyDirVolumeSource{}
			if strings.EqualFold(planned.Medium, "Memory") {
				empty.Medium = k8scorev1.StorageMediumMemory
			} else if planned.Medium != "" {
				return nil, nil, fmt.Errorf("volume %q has unsupported medium", planned.ID)
			}

			if planned.Size != "" {
				quantity, err := apiresource.ParseQuantity(planned.Size)
				if err != nil {
					return nil, nil, fmt.Errorf("volume %q has invalid size", planned.ID)
				}

				empty.SizeLimit = &quantity
			}

			volume.EmptyDir = empty
		case clabernetesinternaldeviceplan.VolumeConfigMap:
			if planned.Reference == "" {
				return nil, nil, fmt.Errorf("volume %q has no ConfigMap reference", planned.ID)
			}

			volume.ConfigMap = &k8scorev1.ConfigMapVolumeSource{
				LocalObjectReference: k8scorev1.LocalObjectReference{Name: planned.Reference},
			}
		case clabernetesinternaldeviceplan.VolumeSecret:
			if planned.Reference == "" {
				return nil, nil, fmt.Errorf("volume %q has no Secret reference", planned.ID)
			}

			volume.Secret = &k8scorev1.SecretVolumeSource{SecretName: planned.Reference}
		case clabernetesinternaldeviceplan.VolumePersistent:
			if planned.Reference == "" {
				return nil, nil, fmt.Errorf("volume %q has no claim reference", planned.ID)
			}

			volume.PersistentVolumeClaim = &k8scorev1.PersistentVolumeClaimVolumeSource{
				ClaimName: planned.Reference,
			}
		case clabernetesinternaldeviceplan.VolumeDevice:
			cleanReference := path.Clean(planned.Reference)
			if cleanReference != planned.Reference || !strings.HasPrefix(cleanReference, "/dev/") {
				return nil, nil, fmt.Errorf("volume %q is not a portable host device", planned.ID)
			}

			hostPathType := k8scorev1.HostPathCharDev
			volume.HostPath = &k8scorev1.HostPathVolumeSource{
				Path: planned.Reference, Type: &hostPathType,
			}
		default:
			return nil, nil, fmt.Errorf(
				"volume %q has unsupported kind %q",
				planned.ID,
				planned.Kind,
			)
		}

		volumes = append(volumes, volume)
	}

	return volumes, names, nil
}

func indexMounts(
	plan clabernetesinternaldeviceplan.Plan,
	volumeNames map[string]string,
) (map[string][]k8scorev1.VolumeMount, error) {
	result := map[string][]k8scorev1.VolumeMount{}

	for _, mount := range plan.Mounts {
		volumeName := volumeNames[mount.VolumeID]
		if volumeName == "" {
			return nil, fmt.Errorf("mount %q references unknown volume", mount.ID)
		}

		propagation, err := mountPropagation(mount.Propagation)
		if err != nil {
			return nil, fmt.Errorf("mount %q: %w", mount.ID, err)
		}

		result[mount.ContainerID] = append(result[mount.ContainerID], k8scorev1.VolumeMount{
			Name: volumeName, MountPath: mount.Destination, SubPath: mount.SourcePath,
			ReadOnly: mount.ReadOnly, MountPropagation: propagation,
		})
	}

	return result, nil
}

func renderPayloadSources(
	plan clabernetesinternaldeviceplan.Plan,
	namespace string,
	payloads []clabernetesinternaldeviceplan.PayloadInput,
) ([]k8scorev1.Volume, []k8scorev1.VolumeMount, error) {
	planned := map[string]clabernetesinternaldeviceplan.FilePlan{}

	for _, file := range plan.Files {
		if file.SourceKind != clabernetesinternaldeviceplan.FileSourcePayload {
			continue
		}

		if _, exists := planned[file.SourceReference]; exists {
			return nil, nil, fmt.Errorf(
				"payload %q is referenced by multiple file plans",
				file.SourceReference,
			)
		}

		planned[file.SourceReference] = file
	}

	volumes := []k8scorev1.Volume{}
	mounts := []k8scorev1.VolumeMount{}

	seen := map[string]bool{}
	for _, payload := range payloads {
		if seen[payload.ID] {
			return nil, nil, fmt.Errorf("payload input %q is duplicated", payload.ID)
		}

		seen[payload.ID] = true

		file, exists := planned[payload.ID]
		if !exists || file.NodeID != payload.NodeID || file.Destination != payload.Destination ||
			file.Mode != payload.Mode || file.Digest != payload.Digest {
			return nil, nil, fmt.Errorf(
				"payload input %q differs from the accepted file plan",
				payload.ID,
			)
		}

		delete(planned, payload.ID)

		if payload.Kind == clabernetesinternaldeviceplan.PayloadURL {
			continue
		}

		if payload.Kind != clabernetesinternaldeviceplan.PayloadConfigMap &&
			payload.Kind != clabernetesinternaldeviceplan.PayloadSecret {
			return nil, nil, fmt.Errorf("payload input %q has no typed Pod source", payload.ID)
		}

		objectNamespace, objectName, key, err := parsePayloadObjectReference(payload.Reference)
		if err != nil || objectNamespace != namespace {
			return nil, nil, fmt.Errorf(
				"payload input %q has an invalid same-namespace object reference",
				payload.ID,
			)
		}
		// The non-root preparation helper must be able to read the source. It applies the
		// requested destination mode when staging the verified artifact.
		mode := int32(0o444)
		item := k8scorev1.KeyToPath{Key: key, Path: "source", Mode: &mode}

		volume := k8scorev1.Volume{Name: dnsName("payload", payload.ID)}
		if payload.Kind == clabernetesinternaldeviceplan.PayloadConfigMap {
			volume.ConfigMap = &k8scorev1.ConfigMapVolumeSource{
				LocalObjectReference: k8scorev1.LocalObjectReference{Name: objectName},
				Items:                []k8scorev1.KeyToPath{item},
			}
		} else {
			volume.Secret = &k8scorev1.SecretVolumeSource{
				SecretName: objectName,
				Items:      []k8scorev1.KeyToPath{item},
			}
		}

		volumes = append(volumes, volume)
		mounts = append(mounts, k8scorev1.VolumeMount{
			Name: volume.Name,
			MountPath: path.Join(
				payloadRootPath,
				clabernetesinternaldeviceplan.ArtifactNodeDirectory(payload.ID),
			),
			ReadOnly: true,
		})
	}

	if len(planned) != 0 {
		return nil,
			nil,
			errors.New("accepted file plan references an unavailable typed payload source")
	}

	return volumes, mounts, nil
}

func parsePayloadObjectReference(reference string) (string, string, string, error) {
	object, key, found := strings.Cut(reference, ":")

	namespace, name, separated := strings.Cut(object, "/")
	if !found || !separated || namespace == "" || name == "" || key == "" ||
		strings.Contains(key, "/") {
		return "", "", "", errors.New("expected namespace/name:key")
	}

	return namespace, name, key, nil
}

func renderHelpers(
	plan clabernetesinternaldeviceplan.Plan,
	options Options,
	volumeNames map[string]string,
	payloadMounts []k8scorev1.VolumeMount,
	hasLifecycle bool,
	hasEndpointCertificates bool,
	enableApplicationLogBroker bool,
) ([]k8scorev1.Container, error) {
	planMount := k8scorev1.VolumeMount{
		Name:      planVolumeName,
		MountPath: planMountPath,
		ReadOnly:  true,
	}
	inputMount := k8scorev1.VolumeMount{
		Name:      inputVolumeName,
		MountPath: inputMountPath,
		ReadOnly:  true,
	}
	stateMount := k8scorev1.VolumeMount{
		Name:      connectivityStateName,
		MountPath: connectivityStatePath,
	}
	endpointLifecycle := hasImportedEndpointLifecycle(plan)
	preparationMounts := []k8scorev1.VolumeMount{
		planMount,
		inputMount,
		{Name: preparationScratchName, MountPath: preparationScratchPath},
	}
	preparationArgs := []string{
		"--plan", planMountPath + "/plan.json",
		"--input", inputMountPath + "/input.json",
		"--artifacts", artifactRootPath,
		"--payloads", payloadRootPath,
		"--revision", plan.Planner.Revision,
	}

	if options.CertificateSecretName != "" {
		preparationMounts = append(preparationMounts, k8scorev1.VolumeMount{
			Name: certificateVolumeName, MountPath: certificateRootPath, ReadOnly: true,
		})
		preparationArgs = append(preparationArgs, "--certificates", certificateRootPath)
	}

	if options.EntropySecretName != "" {
		preparationMounts = append(preparationMounts, k8scorev1.VolumeMount{
			Name: entropyVolumeName, MountPath: entropyRootPath, ReadOnly: true,
		})
		preparationArgs = append(preparationArgs, "--entropy", entropyRootPath)
	}

	if hasLifecycle {
		preparationMounts = append(preparationMounts, k8scorev1.VolumeMount{
			Name: lifecycleVolumeName, MountPath: lifecycleBinaryRoot,
		})
		preparationArgs = append(
			preparationArgs,
			"--lifecycleBinary",
			lifecycleBinaryPath,
		)
	}

	deviceSources := map[string]string{}

	for _, container := range plan.Containers {
		for index, device := range container.Security.Devices {
			if existing := deviceSources[device.ContainerPath]; existing != "" {
				if existing != device.HostPath {
					return nil, fmt.Errorf(
						"target-worker preflight has conflicting host devices at %q",
						device.ContainerPath,
					)
				}

				continue
			}

			deviceSources[device.ContainerPath] = device.HostPath
			preparationMounts = append(preparationMounts, k8scorev1.VolumeMount{
				Name: dnsName(
					"device",
					container.ID+"/"+strconv.Itoa(index)+"/"+device.HostPath,
				),
				MountPath: device.ContainerPath,
				ReadOnly:  true,
			})
		}
	}

	preparationMounts = append(preparationMounts, payloadMounts...)

	for _, volume := range plan.Volumes {
		if volume.Kind == clabernetesinternaldeviceplan.VolumeArtifacts {
			preparationMounts = append(preparationMounts, k8scorev1.VolumeMount{
				Name: volumeNames[volume.ID], MountPath: path.Join(
					artifactRootPath,
					clabernetesinternaldeviceplan.ArtifactNodeDirectory(volume.NodeID),
				),
			})
		}
	}

	preparationPaths := map[string]string{}
	for _, mount := range preparationMounts {
		if previous := preparationPaths[mount.MountPath]; previous != "" {
			return nil, fmt.Errorf(
				"target-worker preflight mount path %q is requested by %q and %q",
				mount.MountPath,
				previous,
				mount.Name,
			)
		}

		preparationPaths[mount.MountPath] = mount.Name
	}

	slices.SortFunc(preparationMounts, func(left, right k8scorev1.VolumeMount) int {
		return strings.Compare(left.MountPath, right.MountPath)
	})

	falseValue := false
	trueValue := true
	rootUser := int64(0)
	always := k8scorev1.ContainerRestartPolicyAlways
	connectivityArgs := []string{
		"--plan", planMountPath + "/plan.json",
		"--input", inputMountPath + "/input.json",
		"--state", connectivityStatePath,
		"--podNamespace", "$(C9S_POD_NAMESPACE)",
		"--podName", "$(C9S_POD_NAME)",
		"--podUID", "$(C9S_POD_UID)",
		"--podAddress", "$(C9S_POD_ADDRESS)",
	}
	connectivityReadyCommand := []string{
		"/clabernetes/manager", "node-runtime", "connectivity-ready",
		"--plan", planMountPath + "/plan.json",
		"--state", connectivityStatePath,
	}
	connectivityMounts := []k8scorev1.VolumeMount{
		planMount,
		inputMount,
		stateMount,
	}

	if options.ConnectivityRevisionConfigMapName != "" {
		connectivityArgs = append(
			connectivityArgs,
			"--connectivityRevision",
			connectivityRevisionMountPath+"/revision.json",
		)
		connectivityReadyCommand = append(
			connectivityReadyCommand,
			"--connectivityRevision",
			connectivityRevisionMountPath+"/revision.json",
		)
		connectivityMounts = append(connectivityMounts, k8scorev1.VolumeMount{
			Name: connectivityRevisionVolumeName, MountPath: connectivityRevisionMountPath,
			ReadOnly: true,
		})
	}

	connectivityEnvironment := []k8scorev1.EnvVar{}
	for _, item := range []struct {
		name      string
		fieldPath string
	}{
		{name: "C9S_POD_NAMESPACE", fieldPath: "metadata.namespace"},
		{name: "C9S_POD_NAME", fieldPath: "metadata.name"},
		{name: "C9S_POD_UID", fieldPath: "metadata.uid"},
		{name: "C9S_POD_ADDRESS", fieldPath: "status.podIP"},
	} {
		connectivityEnvironment = append(connectivityEnvironment, k8scorev1.EnvVar{
			Name: item.name,
			ValueFrom: &k8scorev1.EnvVarSource{FieldRef: &k8scorev1.ObjectFieldSelector{
				FieldPath: item.fieldPath,
			}},
		})
	}

	if enableApplicationLogBroker {
		connectivityArgs = append(
			connectivityArgs,
			"--applicationRuntimeSocket",
			clabernetesinternaldirectruntime.ApplicationRuntimeSocketPath,
		)
		connectivityMounts = append(
			connectivityMounts,
			k8scorev1.VolumeMount{
				Name: applicationRuntimeAPIName, MountPath: applicationRuntimeAPIRoot,
			},
			k8scorev1.VolumeMount{
				Name:      applicationRuntimeCredentialName,
				MountPath: applicationRuntimeCredentialRoot,
				ReadOnly:  true,
			},
		)
	}

	if endpointLifecycle {
		connectivityArgs = append(
			connectivityArgs,
			"--artifacts", artifactRootPath,
			"--revision", plan.Planner.Revision,
			"--hostNetworkNamespace", clabernetesinternaldirectruntime.HostNetworkNamespacePath,
		)
		connectivityMounts = append(
			connectivityMounts,
			k8scorev1.VolumeMount{
				Name:      hostNetworkNamespaceName,
				MountPath: hostNetworkNamespaceMountPath,
				ReadOnly:  true,
			},
		)

		connectivityMounts = append(connectivityMounts, payloadMounts...)
		if options.EntropySecretName != "" {
			connectivityArgs = append(connectivityArgs, "--entropy", entropyRootPath)
			connectivityMounts = append(connectivityMounts, k8scorev1.VolumeMount{
				Name: entropyVolumeName, MountPath: entropyRootPath, ReadOnly: true,
			})
		}

		for _, volume := range plan.Volumes {
			if volume.Kind != clabernetesinternaldeviceplan.VolumeArtifacts {
				continue
			}

			connectivityMounts = append(connectivityMounts, k8scorev1.VolumeMount{
				Name: volumeNames[volume.ID], MountPath: path.Join(
					artifactRootPath,
					clabernetesinternaldeviceplan.ArtifactNodeDirectory(volume.NodeID),
				),
			})
		}

		if hasEndpointCertificates {
			connectivityArgs = append(
				connectivityArgs,
				"--certificates", certificateRootPath,
			)
			connectivityMounts = append(connectivityMounts, k8scorev1.VolumeMount{
				Name: endpointCertificateName, MountPath: certificateRootPath, ReadOnly: true,
			})
		}
	}

	slices.SortFunc(connectivityMounts, func(left, right k8scorev1.VolumeMount) int {
		return strings.Compare(left.MountPath, right.MountPath)
	})

	return []k8scorev1.Container{
		{
			Name: preparationName, Image: options.PreparationImage,
			Command: []string{"/clabernetes/manager", "node-runtime", "prepare"},
			Args:    preparationArgs,
			// Preparation records the Pod's prefixed management identity while the primary
			// interface is still pristine; devices may strip it at boot.
			Env: []k8scorev1.EnvVar{{
				Name: clabernetesinternaldirectruntime.PodAddressEnvironmentVariable,
				ValueFrom: &k8scorev1.EnvVarSource{FieldRef: &k8scorev1.ObjectFieldSelector{
					FieldPath: "status.podIP",
				}},
			}},
			SecurityContext: &k8scorev1.SecurityContext{
				AllowPrivilegeEscalation: &falseValue, ReadOnlyRootFilesystem: &trueValue,
				RunAsUser: &rootUser,
				Capabilities: &k8scorev1.Capabilities{
					Add:  []k8scorev1.Capability{"CHOWN", "FOWNER"},
					Drop: []k8scorev1.Capability{"ALL"},
				},
				SeccompProfile: &k8scorev1.SeccompProfile{
					Type: k8scorev1.SeccompProfileTypeRuntimeDefault,
				},
			},
			VolumeMounts: preparationMounts,
		},
		{
			Name: connectivityName, Image: options.ConnectivityImage,
			Command:       []string{"/clabernetes/manager", "node-runtime", "connectivity"},
			Args:          connectivityArgs,
			Env:           connectivityEnvironment,
			RestartPolicy: &always,
			SecurityContext: &k8scorev1.SecurityContext{
				Privileged: &trueValue, RunAsUser: &rootUser,
			},
			StartupProbe: &k8scorev1.Probe{
				ProbeHandler: k8scorev1.ProbeHandler{Exec: &k8scorev1.ExecAction{
					Command: connectivityReadyCommand,
				}},
				PeriodSeconds: 1, TimeoutSeconds: 1,
				SuccessThreshold: 1, FailureThreshold: 300,
			},
			ReadinessProbe: &k8scorev1.Probe{
				ProbeHandler: k8scorev1.ProbeHandler{Exec: &k8scorev1.ExecAction{
					Command: connectivityReadyCommand,
				}},
				PeriodSeconds: 1, TimeoutSeconds: 1,
				SuccessThreshold: 1, FailureThreshold: 1,
			},
			VolumeMounts: connectivityMounts,
		},
	}, nil
}

func hasImportedEndpointLifecycle(plan clabernetesinternaldeviceplan.Plan) bool {
	for _, action := range plan.Actions {
		if action.Phase == clabernetesinternaldeviceplan.PhaseInterfaceFixup &&
			action.Kind == clabernetesinternaldeviceplan.ActionImportedDeployEndpoints &&
			action.ImportedDeployEndpoints != nil {
			return true
		}
	}

	// Host Links place their worker-side veth end through the same read-only namespace handle,
	// so they carry the identical mount requirement.
	for _, intf := range plan.Interfaces {
		if intf.Connectivity == clabernetesinternaldeviceplan.ConnectivityHost {
			return true
		}
	}

	return false
}

func hasImportedPostDeployLifecycle(plan clabernetesinternaldeviceplan.Plan) bool {
	for _, action := range plan.Actions {
		if action.Phase == clabernetesinternaldeviceplan.PhasePostStart &&
			action.Kind == clabernetesinternaldeviceplan.ActionImportedPostDeploy &&
			action.ImportedPostDeploy != nil {
			return true
		}
	}

	return false
}

func immutableImage(reference, digest string) string {
	if digest == "" || strings.Contains(reference, "@") {
		return reference
	}

	repository := reference
	if slash, colon := strings.LastIndex(repository,
		"/"), strings.LastIndex(repository, ":"); colon > slash {
		repository = repository[:colon]
	}

	return repository + "@" + digest
}

func pullPolicy(value string) (k8scorev1.PullPolicy, error) {
	switch strings.ToLower(strings.ReplaceAll(value, "-", "")) {
	case "", "ifnotpresent":
		return k8scorev1.PullIfNotPresent, nil
	case "always":
		return k8scorev1.PullAlways, nil
	case "never":
		return k8scorev1.PullNever, nil
	default:
		return "", fmt.Errorf("image pull policy %q is unsupported", value)
	}
}

func portableRestartPolicy(value string) bool {
	switch strings.ToLower(value) {
	case "", "always", "unless-stopped":
		return true
	default:
		return false
	}
}

func parseUser(value string) (*int64, *int64, error) {
	uidValue, gidValue, hasGroup := strings.Cut(value, ":")

	uid, err := strconv.ParseInt(uidValue, 10, 64)
	if err != nil || uid < 0 {
		return nil, nil, fmt.Errorf(
			"named or invalid OCI user %q cannot override Pod security",
			value,
		)
	}

	if !hasGroup || gidValue == "" {
		return &uid, nil, nil
	}

	gid, err := strconv.ParseInt(gidValue, 10, 64)
	if err != nil || gid < 0 {
		return nil, nil, fmt.Errorf("invalid OCI group in user %q", value)
	}

	return &uid, &gid, nil
}

func mapCapabilities(values []string) []k8scorev1.Capability {
	result := make([]k8scorev1.Capability, len(values))
	for index, value := range values {
		result[index] = k8scorev1.Capability(value)
	}

	return result
}

func mapSeccomp(value string) *k8scorev1.SeccompProfile {
	switch {
	case value == "":
		return nil
	case strings.EqualFold(value, "RuntimeDefault") || strings.EqualFold(value, "runtime/default"):
		return &k8scorev1.SeccompProfile{Type: k8scorev1.SeccompProfileTypeRuntimeDefault}
	case strings.EqualFold(value, "Unconfined"):
		return &k8scorev1.SeccompProfile{Type: k8scorev1.SeccompProfileTypeUnconfined}
	case strings.HasPrefix(strings.ToLower(value), "localhost/"):
		return &k8scorev1.SeccompProfile{
			Type:             k8scorev1.SeccompProfileTypeLocalhost,
			LocalhostProfile: optionalString(strings.TrimPrefix(value, "localhost/")),
		}
	default:
		return nil
	}
}

func mapAppArmor(value string) *k8scorev1.AppArmorProfile {
	switch {
	case value == "":
		return nil
	case strings.EqualFold(value, "RuntimeDefault") || strings.EqualFold(value, "runtime/default"):
		return &k8scorev1.AppArmorProfile{Type: k8scorev1.AppArmorProfileTypeRuntimeDefault}
	case strings.EqualFold(value, "Unconfined"):
		return &k8scorev1.AppArmorProfile{Type: k8scorev1.AppArmorProfileTypeUnconfined}
	case strings.HasPrefix(strings.ToLower(value), "localhost/"):
		return &k8scorev1.AppArmorProfile{
			Type:             k8scorev1.AppArmorProfileTypeLocalhost,
			LocalhostProfile: optionalString(strings.TrimPrefix(value, "localhost/")),
		}
	default:
		return nil
	}
}

func mountPropagation(value string) (*k8scorev1.MountPropagationMode, error) {
	var mode k8scorev1.MountPropagationMode

	switch strings.ToLower(value) {
	case "", "private", "rprivate":
		return nil, nil //nolint:nilnil // private propagation renders no explicit mode.
	case "slave", "rslave":
		mode = k8scorev1.MountPropagationHostToContainer
	case "shared", "rshared":
		mode = k8scorev1.MountPropagationBidirectional
	default:
		return nil, fmt.Errorf("mount propagation %q is unsupported", value)
	}

	return &mode, nil
}

func dnsName(prefix, identity string) string {
	var builder strings.Builder
	for _, char := range strings.ToLower(identity) {
		if unicode.IsLetter(char) || unicode.IsDigit(char) {
			builder.WriteRune(char)
		} else if builder.Len() > 0 && !strings.HasSuffix(builder.String(), "-") {
			builder.WriteByte('-')
		}
	}

	base := strings.Trim(builder.String(), "-")
	if base == "" {
		base = prefix
	}

	suffix := strings.TrimPrefix(clabernetesinternaldeviceplan.Digest([]byte(identity)),
		"sha256:")[:10]

	maxBase := 63 - len(prefix) - len(suffix) - 2
	if len(base) > maxBase {
		base = strings.TrimRight(base[:maxBase], "-")
	}

	return prefix + "-" + base + "-" + suffix
}

// ApplicationContainerName maps a runtime-neutral container ID to its Kubernetes container name.
func ApplicationContainerName(identity string) string {
	return dnsName("node", identity)
}

func optionalBool(value bool) *bool {
	if !value {
		return nil
	}

	return &value
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}

	return &value
}

func emptyDNS(value clabernetesinternaldeviceplan.DNSConfig) bool {
	return len(value.Servers) == 0 && len(value.Search) == 0 && len(value.Options) == 0
}

func reflectDNS(left, right clabernetesinternaldeviceplan.DNSConfig) bool {
	return slices.Equal(left.Servers, right.Servers) && slices.Equal(left.Search, right.Search) &&
		slices.Equal(left.Options, right.Options)
}

func validateUniqueVolumeNames(volumes []k8scorev1.Volume) error {
	seen := map[string]bool{}
	for _, volume := range volumes {
		if seen[volume.Name] {
			return fmt.Errorf("rendered volume name %q is duplicated", volume.Name)
		}

		seen[volume.Name] = true
	}

	return nil
}
