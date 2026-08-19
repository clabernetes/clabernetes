package node

import (
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesdeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	k8scorev1 "k8s.io/api/core/v1"
	k8snetworkingv1 "k8s.io/api/networking/v1"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	plannerInputKey        = "input.json"
	plannerInputMountPath  = "/var/run/clabernetes/planner/input"
	plannerScratchPath     = "/var/run/clabernetes/planner/scratch"
	plannerContainerName   = "device-planner"
	plannerLabel           = "c9s.run/device-planner"
	plannerInputDigest     = "c9s.run/device-plan-input-digest"
	plannerWorkerPlan      = "device-plan"
	plannerWorkerImages    = "device-images"
	plannerURLFetcherName  = "fetch-url-payloads"
	plannerURLPayloadName  = "planner-url-payloads"
	plannerCertificateName = "planner-certificates"
	plannerCertificateRoot = "/var/run/clabernetes/planner/certificates"
	plannerEntropyName     = "planner-entropy"
	plannerEntropyRoot     = "/var/run/clabernetes/planner/entropy"
)

// PlannerPodInput is the complete Kubernetes policy needed to run one content-addressed planning
// attempt. Canonical input contains normalized metadata only; explicitly referenced payload
// objects may be projected read-only so imported hooks can consume their bytes inside the worker.
type PlannerPodInput struct {
	Node                  *clabernetesapisv1alpha1.Node
	Name                  string
	Image                 string
	InputConfigMapName    string
	InputDigest           string
	PlannerRevision       string
	WorkerCommand         string
	MaxInputBytes         int64
	DeadlineSeconds       int64
	ImagePullSecrets      []k8scorev1.LocalObjectReference
	Payloads              []clabernetesdeviceplan.PayloadInput
	CertificateSecretName string
	EntropySecretName     string
}

// RenderPlannerNetworkPolicy denies all planner ingress and egress, including DNS. Planning inputs
// already contain resolved OCI and topology metadata, so any network request is an undeclared
// dependency rather than valid planning behavior.
func RenderPlannerNetworkPolicy(input PlannerPodInput) (*k8snetworkingv1.NetworkPolicy, error) {
	pod, err := RenderPlannerPod(input)
	if err != nil {
		return nil, err
	}

	policy := &k8snetworkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: input.Name, Namespace: input.Node.GetNamespace(), Labels: pod.GetLabels(),
			Annotations: pod.GetAnnotations(), OwnerReferences: pod.GetOwnerReferences(),
		},
		Spec: k8snetworkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{
				plannerLabel: plannerDigestLabelValue(input.InputDigest),
			}},
			PolicyTypes: []k8snetworkingv1.PolicyType{
				k8snetworkingv1.PolicyTypeIngress,
				k8snetworkingv1.PolicyTypeEgress,
			},
		},
	}
	if plannerHasURLPayloads(input.Payloads) {
		policy.Spec.Egress = plannerURLFetchEgressRules()
	}

	return policy, nil
}

// RenderPlannerPod renders a locked-down disposable worker for imported hook evaluation.
func RenderPlannerPod(input PlannerPodInput) (*k8scorev1.Pod, error) {
	if input.Node == nil || input.Node.GetName() == "" || input.Node.GetNamespace() == "" ||
		input.Node.GetUID() == "" {
		return nil, fmt.Errorf("planner Pod requires an identified owning Node")
	}
	for field, value := range map[string]string{
		"name": input.Name, "image": input.Image, "input ConfigMap": input.InputConfigMapName,
		"input digest": input.InputDigest, "planner revision": input.PlannerRevision,
	} {
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("planner Pod %s is required", field)
		}
	}
	if input.MaxInputBytes <= 0 {
		return nil, fmt.Errorf("planner Pod maximum input size must be positive")
	}
	if input.DeadlineSeconds <= 0 {
		return nil, fmt.Errorf("planner Pod deadline must be positive")
	}
	workerCommand := input.WorkerCommand
	if workerCommand == "" {
		workerCommand = plannerWorkerPlan
	}
	if workerCommand != plannerWorkerPlan && workerCommand != plannerWorkerImages {
		return nil, fmt.Errorf("planner Pod worker command %q is unsupported", workerCommand)
	}
	payloadVolumes, payloadMounts, err := renderPlannerPayloadSources(
		input.Node.GetNamespace(),
		input.Payloads,
	)
	if err != nil {
		return nil, err
	}

	falseValue := false
	rootUser := int64(0)
	gracePeriod := int64(1)
	readOnly := true
	ownerController := true
	blockOwnerDeletion := true
	labels := map[string]string{
		clabernetesconstants.LabelApp:            clabernetesconstants.Clabernetes,
		clabernetesconstants.LabelKubernetesName: "clabernetes-device-planner",
		clabernetesconstants.LabelTopologyNode:   input.Node.GetName(),
		plannerLabel:                             plannerDigestLabelValue(input.InputDigest),
	}
	securityContext := &k8scorev1.SecurityContext{
		AllowPrivilegeEscalation: &falseValue,
		ReadOnlyRootFilesystem:   &readOnly,
		RunAsNonRoot:             &falseValue,
		RunAsUser:                &rootUser,
		Capabilities: &k8scorev1.Capabilities{
			Add:  []k8scorev1.Capability{"CHOWN", "FOWNER"},
			Drop: []k8scorev1.Capability{"ALL"},
		},
		SeccompProfile: &k8scorev1.SeccompProfile{Type: k8scorev1.SeccompProfileTypeRuntimeDefault},
	}
	var initContainers []k8scorev1.Container
	if plannerHasURLPayloads(input.Payloads) {
		initContainers = append(initContainers, k8scorev1.Container{
			Name: plannerURLFetcherName, Image: input.Image,
			ImagePullPolicy: k8scorev1.PullIfNotPresent,
			Command:         []string{"/clabernetes/manager"},
			Args: []string{
				"device-payloads",
				"--input", plannerInputMountPath + "/" + plannerInputKey,
				"--maxInputBytes", strconv.FormatInt(input.MaxInputBytes, 10),
				"--payloads", plannerPayloadRootPath,
			},
			SecurityContext: &k8scorev1.SecurityContext{
				AllowPrivilegeEscalation: &falseValue,
				ReadOnlyRootFilesystem:   &readOnly,
				RunAsNonRoot:             &falseValue,
				RunAsUser:                &rootUser,
				Capabilities: &k8scorev1.Capabilities{
					Add:  []k8scorev1.Capability{"NET_ADMIN"},
					Drop: []k8scorev1.Capability{"ALL"},
				},
				SeccompProfile: &k8scorev1.SeccompProfile{
					Type: k8scorev1.SeccompProfileTypeRuntimeDefault,
				},
			},
			Resources: k8scorev1.ResourceRequirements{
				Requests: k8scorev1.ResourceList{
					k8scorev1.ResourceCPU:    apiresource.MustParse("10m"),
					k8scorev1.ResourceMemory: apiresource.MustParse("16Mi"),
				},
				Limits: k8scorev1.ResourceList{
					k8scorev1.ResourceCPU:    apiresource.MustParse("500m"),
					k8scorev1.ResourceMemory: apiresource.MustParse("128Mi"),
				},
			},
			VolumeMounts: []k8scorev1.VolumeMount{
				{Name: "planner-input", MountPath: plannerInputMountPath, ReadOnly: true},
				{Name: plannerURLPayloadName, MountPath: plannerPayloadRootPath},
			},
		})
	}

	workerArgs := []string{
		workerCommand,
		"--input", plannerInputMountPath + "/" + plannerInputKey,
		"--revision", input.PlannerRevision,
		"--maxInputBytes", strconv.FormatInt(input.MaxInputBytes, 10),
	}
	if len(payloadMounts) != 0 {
		workerArgs = append(workerArgs, "--payloads", plannerPayloadRootPath)
	}
	workerMounts := []k8scorev1.VolumeMount{
		{Name: "planner-input", MountPath: plannerInputMountPath, ReadOnly: true},
		{Name: "planner-scratch", MountPath: plannerScratchPath},
	}
	workerMounts = append(workerMounts, payloadMounts...)
	if input.CertificateSecretName != "" {
		workerArgs = append(workerArgs, "--certificates", plannerCertificateRoot)
		workerMounts = append(workerMounts, k8scorev1.VolumeMount{
			Name: plannerCertificateName, MountPath: plannerCertificateRoot, ReadOnly: true,
		})
	}
	if input.EntropySecretName != "" {
		workerArgs = append(workerArgs, "--entropy", plannerEntropyRoot)
		workerMounts = append(workerMounts, k8scorev1.VolumeMount{
			Name: plannerEntropyName, MountPath: plannerEntropyRoot, ReadOnly: true,
		})
	}
	slices.SortFunc(workerMounts, func(left, right k8scorev1.VolumeMount) int {
		return strings.Compare(left.MountPath, right.MountPath)
	})
	volumes := []k8scorev1.Volume{
		{
			Name: "planner-input",
			VolumeSource: k8scorev1.VolumeSource{
				ConfigMap: &k8scorev1.ConfigMapVolumeSource{
					LocalObjectReference: k8scorev1.LocalObjectReference{
						Name: input.InputConfigMapName,
					},
					Items: []k8scorev1.KeyToPath{{Key: plannerInputKey, Path: plannerInputKey}},
				},
			},
		},
		{
			Name: "planner-scratch",
			VolumeSource: k8scorev1.VolumeSource{
				EmptyDir: &k8scorev1.EmptyDirVolumeSource{},
			},
		},
	}
	volumes = append(volumes, payloadVolumes...)
	if input.CertificateSecretName != "" {
		volumes = append(volumes, k8scorev1.Volume{
			Name: plannerCertificateName,
			VolumeSource: k8scorev1.VolumeSource{Secret: &k8scorev1.SecretVolumeSource{
				SecretName: input.CertificateSecretName,
			}},
		})
	}
	if input.EntropySecretName != "" {
		volumes = append(volumes, k8scorev1.Volume{
			Name: plannerEntropyName,
			VolumeSource: k8scorev1.VolumeSource{Secret: &k8scorev1.SecretVolumeSource{
				SecretName: input.EntropySecretName,
				Items: []k8scorev1.KeyToPath{{
					Key:  clabernetesdeviceplan.EntropySeedKey,
					Path: clabernetesdeviceplan.EntropySeedKey,
				}},
			}},
		})
	}

	return &k8scorev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: input.Name, Namespace: input.Node.GetNamespace(), Labels: labels,
			Annotations: map[string]string{plannerInputDigest: input.InputDigest},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: clabernetesapisv1alpha1.SchemeGroupVersion.String(), Kind: "Node",
				Name: input.Node.GetName(), UID: input.Node.GetUID(),
				Controller: &ownerController, BlockOwnerDeletion: &blockOwnerDeletion,
			}},
		},
		Spec: k8scorev1.PodSpec{
			AutomountServiceAccountToken:  &falseValue,
			EnableServiceLinks:            &falseValue,
			RestartPolicy:                 k8scorev1.RestartPolicyNever,
			ActiveDeadlineSeconds:         &input.DeadlineSeconds,
			TerminationGracePeriodSeconds: &gracePeriod,
			ImagePullSecrets:              input.ImagePullSecrets,
			SecurityContext: &k8scorev1.PodSecurityContext{
				SeccompProfile: &k8scorev1.SeccompProfile{
					Type: k8scorev1.SeccompProfileTypeRuntimeDefault,
				},
			},
			InitContainers: initContainers,
			Containers: []k8scorev1.Container{{
				Name:            plannerContainerName,
				Image:           input.Image,
				ImagePullPolicy: k8scorev1.PullIfNotPresent,
				Command:         []string{"/clabernetes/manager"},
				Args:            workerArgs,
				Env:             []k8scorev1.EnvVar{{Name: "TMPDIR", Value: plannerScratchPath}},
				SecurityContext: securityContext,
				Resources: k8scorev1.ResourceRequirements{
					Requests: k8scorev1.ResourceList{
						k8scorev1.ResourceCPU:    apiresource.MustParse("10m"),
						k8scorev1.ResourceMemory: apiresource.MustParse("32Mi"),
					},
					Limits: k8scorev1.ResourceList{
						k8scorev1.ResourceCPU:    apiresource.MustParse("1"),
						k8scorev1.ResourceMemory: apiresource.MustParse("512Mi"),
					},
				},
				VolumeMounts: workerMounts,
			}},
			Volumes: volumes,
		},
	}, nil
}

// plannerDigestLabelValue keeps the full canonical digest in an annotation while deriving a
// label-safe selector identity. A raw sha256 digest is 71 bytes including its algorithm prefix,
// so it cannot be stored as a Kubernetes label value.
func plannerDigestLabelValue(digest string) string {
	labelDigest := strings.TrimPrefix(digestArtifact([]byte(digest)), "sha256:")

	return labelDigest[:kubernetesNameLimit]
}

const plannerPayloadRootPath = "/var/run/clabernetes/planner/payloads"

func renderPlannerPayloadSources(
	namespace string,
	payloads []clabernetesdeviceplan.PayloadInput,
) ([]k8scorev1.Volume, []k8scorev1.VolumeMount, error) {
	volumes := []k8scorev1.Volume{}
	mounts := []k8scorev1.VolumeMount{}
	seen := map[string]bool{}
	urlPayloads := false
	for _, payload := range payloads {
		if seen[payload.ID] {
			return nil, nil, fmt.Errorf("planner payload input %q is duplicated", payload.ID)
		}
		seen[payload.ID] = true
		if payload.Kind == clabernetesdeviceplan.PayloadURL {
			urlPayloads = true
			continue
		}
		if payload.Kind != clabernetesdeviceplan.PayloadConfigMap &&
			payload.Kind != clabernetesdeviceplan.PayloadSecret {
			return nil, nil, fmt.Errorf("planner payload input %q has no typed source", payload.ID)
		}
		objectNamespace, objectName, key, err := parsePlannerPayloadObjectReference(
			payload.Reference,
		)
		if err != nil || objectNamespace != namespace {
			return nil, nil, fmt.Errorf(
				"planner payload input %q has an invalid same-namespace object reference",
				payload.ID,
			)
		}
		// Projection permissions belong to the non-root worker boundary. The requested payload
		// mode is applied only at the final artifact destination by the preparation helper.
		mode := int32(0o444)
		item := k8scorev1.KeyToPath{Key: key, Path: "source", Mode: &mode}
		volume := k8scorev1.Volume{Name: plannerPayloadVolumeName(payload.ID)}
		if payload.Kind == clabernetesdeviceplan.PayloadConfigMap {
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
				plannerPayloadRootPath,
				clabernetesdeviceplan.ArtifactNodeDirectory(payload.ID),
			),
			ReadOnly: true,
		})
	}
	if urlPayloads {
		volumes = append(volumes, k8scorev1.Volume{
			Name: plannerURLPayloadName,
			VolumeSource: k8scorev1.VolumeSource{
				EmptyDir: &k8scorev1.EmptyDirVolumeSource{},
			},
		})
		mounts = append(mounts, k8scorev1.VolumeMount{
			Name: plannerURLPayloadName, MountPath: plannerPayloadRootPath, ReadOnly: true,
		})
	}

	return volumes, mounts, nil
}

func plannerHasURLPayloads(payloads []clabernetesdeviceplan.PayloadInput) bool {
	return slices.ContainsFunc(payloads, func(payload clabernetesdeviceplan.PayloadInput) bool {
		return payload.Kind == clabernetesdeviceplan.PayloadURL
	})
}

func plannerURLFetchEgressRules() []k8snetworkingv1.NetworkPolicyEgressRule {
	protocolTCP := k8scorev1.ProtocolTCP
	protocolUDP := k8scorev1.ProtocolUDP
	dnsPort := intstr.FromInt32(53)
	return []k8snetworkingv1.NetworkPolicyEgressRule{
		{
			Ports: []k8snetworkingv1.NetworkPolicyPort{
				{Protocol: &protocolUDP, Port: &dnsPort},
				{Protocol: &protocolTCP, Port: &dnsPort},
			},
		},
		{
			To: []k8snetworkingv1.NetworkPolicyPeer{
				{IPBlock: &k8snetworkingv1.IPBlock{
					CIDR: "0.0.0.0/0",
					Except: []string{
						"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8",
						"169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/24",
						"192.168.0.0/16", "198.18.0.0/15", "224.0.0.0/4", "240.0.0.0/4",
					},
				}},
				{IPBlock: &k8snetworkingv1.IPBlock{
					CIDR:   "::/0",
					Except: []string{"::/128", "::1/128", "fc00::/7", "fe80::/10", "ff00::/8"},
				}},
			},
		},
	}
}

func parsePlannerPayloadObjectReference(reference string) (string, string, string, error) {
	object, key, found := strings.Cut(reference, ":")
	namespace, name, separated := strings.Cut(object, "/")
	if !found || !separated || namespace == "" || name == "" || key == "" ||
		strings.Contains(key, "/") {
		return "", "", "", fmt.Errorf("expected namespace/name:key")
	}

	return namespace, name, key, nil
}

func plannerPayloadVolumeName(id string) string {
	return "planner-payload-" + strings.TrimPrefix(
		clabernetesdeviceplan.Digest([]byte(id)),
		"sha256:",
	)[:16]
}
