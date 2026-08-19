//nolint:nlreturn,noinlineerr,wsl_v5 // Planner reconciliation uses compact fail-closed guards.
package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesdeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	k8scorev1 "k8s.io/api/core/v1"
	k8snetworkingv1 "k8s.io/api/networking/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	defaultPlannerDeadlineSeconds int64 = 300
	defaultPlannerMaxPlanBytes          = DefaultMaxPlanBytes
)

var (
	// ErrPlannerObjectConflict means an object at a content-addressed name differs from policy.
	ErrPlannerObjectConflict = errors.New(
		"direct-runtime planner object conflicts with expected content",
	)
	// ErrPlannerFailed means the isolated worker reached a terminal unsuccessful phase.
	ErrPlannerFailed = errors.New("direct-runtime planning worker failed")
)

// PlannerState is the bounded state of one content-addressed planner attempt.
type PlannerState string

const (
	PlannerStatePending   PlannerState = "Pending"
	PlannerStateSucceeded PlannerState = "Succeeded"
)

// PlannerLogReader reads the completed worker's combined log stream.
type PlannerLogReader func(ctx context.Context, namespace, podName, containerName string) ([]byte, error)

// PlannerAttempt contains explicit worker inputs and Kubernetes execution policy.
type PlannerAttempt struct {
	Node                  *clabernetesapisv1alpha1.Node
	Input                 clabernetesdeviceplan.Input
	SensitiveValues       [][]byte
	Image                 string
	PlannerRevision       string
	ImagePullSecrets      []k8scorev1.LocalObjectReference
	CertificateSecretName string
	EntropySecretName     string
	MaxInputBytes         int
	MaxPlanBytes          int
	DeadlineSeconds       int64
}

// PlannerResult reports either pending sandbox setup/execution or a validated canonical plan.
type PlannerResult struct {
	State              PlannerState
	PodName            string
	InputConfigMapName string
	InputDigest        string
	Plan               *clabernetesdeviceplan.Plan
}

// PlannerReconciler owns the isolated worker's input, default-deny policy, and Pod.
type PlannerReconciler struct {
	Client   ctrlruntimeclient.Client
	ReadLogs PlannerLogReader
}

// Reconcile advances one immutable planner attempt. A newly created NetworkPolicy returns Pending
// before a Pod is created, ensuring the deny policy exists in the API before CNI admission.
func (r *PlannerReconciler) Reconcile(
	ctx context.Context,
	attempt PlannerAttempt,
) (*PlannerResult, error) {
	if r.Client == nil {
		return nil, fmt.Errorf("planner reconciler client is required")
	}
	canonicalInput, err := attempt.Input.CanonicalJSON()
	if err != nil {
		return nil, err
	}
	if (len(attempt.Input.Certificates) != 0) !=
		(strings.TrimSpace(attempt.CertificateSecretName) != "") {
		return nil, fmt.Errorf(
			"planner certificate inputs and Secret identity must be supplied together",
		)
	}
	if (attempt.Input.EntropyDigest != "") !=
		(strings.TrimSpace(attempt.EntropySecretName) != "") {
		return nil, fmt.Errorf(
			"planner entropy digest and Secret identity must be supplied together",
		)
	}
	maxInputBytes := attempt.MaxInputBytes
	if maxInputBytes == 0 {
		maxInputBytes = DefaultMaxInputBytes
	}
	maxPlanBytes := attempt.MaxPlanBytes
	if maxPlanBytes == 0 {
		maxPlanBytes = defaultPlannerMaxPlanBytes
	}
	deadlineSeconds := attempt.DeadlineSeconds
	if deadlineSeconds == 0 {
		deadlineSeconds = defaultPlannerDeadlineSeconds
	}
	inputConfigMap, inputDigest, err := (&PlannerInputConfigMapReconciler{
		Client: r.Client, MaxInputBytes: maxInputBytes,
	}).Ensure(ctx, attempt.Node, PlannerInputArtifact{
		CanonicalInput: canonicalInput, SensitiveValues: attempt.SensitiveValues,
	})
	if err != nil {
		return nil, err
	}
	renderInput := PlannerPodInput{
		Node: attempt.Node, Image: attempt.Image,
		InputConfigMapName: inputConfigMap.GetName(), InputDigest: inputDigest,
		PlannerRevision: attempt.PlannerRevision, WorkerCommand: plannerWorkerPlan,
		MaxInputBytes:   int64(maxInputBytes),
		DeadlineSeconds: deadlineSeconds, ImagePullSecrets: attempt.ImagePullSecrets,
		Payloads:              attempt.Input.Payloads,
		CertificateSecretName: attempt.CertificateSecretName,
		EntropySecretName:     attempt.EntropySecretName,
	}
	podName, err := plannerPodName(attempt.Node.GetName(), renderInput)
	if err != nil {
		return nil, err
	}
	renderInput.Name = podName
	result := &PlannerResult{
		State: PlannerStatePending, PodName: podName,
		InputConfigMapName: inputConfigMap.GetName(), InputDigest: inputDigest,
	}
	policy, err := RenderPlannerNetworkPolicy(renderInput)
	if err != nil {
		return nil, err
	}
	policyCreated, err := r.ensurePlannerNetworkPolicy(ctx, policy)
	if err != nil || policyCreated {
		return result, err
	}
	pod, err := RenderPlannerPod(renderInput)
	if err != nil {
		return nil, err
	}
	existingPod, podCreated, err := r.ensurePlannerPod(ctx, pod)
	if err != nil || podCreated {
		return result, err
	}
	switch existingPod.Status.Phase {
	case k8scorev1.PodSucceeded:
		if r.ReadLogs == nil {
			return nil, fmt.Errorf("planner log reader is required for a completed worker")
		}
		logs, readErr := r.ReadLogs(
			ctx,
			existingPod.GetNamespace(),
			existingPod.GetName(),
			plannerContainerName,
		)
		if readErr != nil {
			return nil, fmt.Errorf("reading completed planner output: %w", readErr)
		}
		plan, decodeErr := clabernetesdeviceplan.DecodeWorkerOutput(logs, maxPlanBytes)
		if decodeErr != nil {
			return nil, decodeErr
		}
		if plan.InputDigest != inputDigest ||
			!reflect.DeepEqual(plan.Compatibility, attempt.Input.Compatibility) ||
			plan.Planner.Revision != attempt.PlannerRevision {
			return nil, fmt.Errorf(
				"%w: worker output identity differs from its request",
				ErrPlannerFailed,
			)
		}
		result.State = PlannerStateSucceeded
		result.Plan = &plan

		return result, nil
	case k8scorev1.PodFailed:
		return nil, failedWorkerDiagnostic(
			ctx,
			r.ReadLogs,
			existingPod,
			maxPlanBytes,
			"planning worker Pod reached Failed phase",
		)
	default:
		return result, nil
	}
}

func failedWorkerDiagnostic(
	ctx context.Context,
	readLogs PlannerLogReader,
	pod *k8scorev1.Pod,
	maxBytes int,
	fallback string,
) error {
	if readLogs != nil && pod != nil {
		logs, err := readLogs(
			ctx,
			pod.GetNamespace(),
			pod.GetName(),
			plannerContainerName,
		)
		if err == nil {
			diagnostic, decodeErr := clabernetesdeviceplan.DecodeWorkerError(logs, maxBytes)
			if decodeErr == nil {
				return errors.Join(ErrPlannerFailed, diagnostic)
			}
		}
	}

	return fmt.Errorf("%w: %s", ErrPlannerFailed, fallback)
}

func (r *PlannerReconciler) ensurePlannerNetworkPolicy(
	ctx context.Context,
	rendered *k8snetworkingv1.NetworkPolicy,
) (bool, error) {
	existing := &k8snetworkingv1.NetworkPolicy{}
	err := r.Client.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(rendered), existing)
	if apimachineryerrors.IsNotFound(err) {
		if err = r.Client.Create(ctx, rendered); err != nil {
			return false, fmt.Errorf("creating planner default-deny NetworkPolicy: %w", err)
		}

		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("reading planner default-deny NetworkPolicy: %w", err)
	}
	if !reflect.DeepEqual(existing.Spec, rendered.Spec) ||
		!containsExpectedMetadata(existing.Labels, rendered.Labels) ||
		!containsExpectedMetadata(existing.Annotations, rendered.Annotations) ||
		!reflect.DeepEqual(existing.OwnerReferences, rendered.OwnerReferences) {
		return false, fmt.Errorf(
			"%w: NetworkPolicy %s/%s",
			ErrPlannerObjectConflict,
			existing.GetNamespace(),
			existing.GetName(),
		)
	}

	return false, nil
}

func (r *PlannerReconciler) ensurePlannerPod(
	ctx context.Context,
	rendered *k8scorev1.Pod,
) (*k8scorev1.Pod, bool, error) {
	existing := &k8scorev1.Pod{}
	err := r.Client.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(rendered), existing)
	if apimachineryerrors.IsNotFound(err) {
		if err = r.Client.Create(ctx, rendered); err != nil {
			return nil, false, fmt.Errorf("creating planner Pod: %w", err)
		}

		return rendered, true, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("reading planner Pod: %w", err)
	}
	if !plannerPodSpecMatches(rendered, existing) ||
		!containsExpectedMetadata(existing.Labels, rendered.Labels) ||
		!containsExpectedMetadata(existing.Annotations, rendered.Annotations) ||
		!reflect.DeepEqual(existing.OwnerReferences, rendered.OwnerReferences) {
		return nil, false, fmt.Errorf(
			"%w: Pod %s/%s",
			ErrPlannerObjectConflict,
			existing.GetNamespace(),
			existing.GetName(),
		)
	}

	return existing, false, nil
}

func plannerPodSpecMatches(rendered, existing *k8scorev1.Pod) bool {
	expected := rendered.DeepCopy()
	observed := existing.DeepCopy()

	normalizePlannerPodSchedulingState(&expected.Spec)
	normalizePlannerPodSchedulingState(&observed.Spec)

	return apiequality.Semantic.DeepEqual(expected.Spec, observed.Spec)
}

// normalizePlannerPodSchedulingState removes fields assigned by scheduling and admission after
// creation. The planner never supplies these fields; all of its security and execution policy
// remains part of the exact semantic comparison.
func normalizePlannerPodSchedulingState(spec *k8scorev1.PodSpec) {
	// API defaulting fills these fixed fields even though the planner does not own them.
	spec.DNSPolicy = ""
	spec.ServiceAccountName = ""
	spec.DeprecatedServiceAccount = ""
	spec.SchedulerName = ""
	spec.NodeName = ""
	spec.Priority = nil
	spec.PreemptionPolicy = nil
	spec.Tolerations = nil
	for index := range spec.InitContainers {
		spec.InitContainers[index].TerminationMessagePath = ""
		spec.InitContainers[index].TerminationMessagePolicy = ""
	}
	for index := range spec.Containers {
		spec.Containers[index].TerminationMessagePath = ""
		spec.Containers[index].TerminationMessagePolicy = ""
	}
	for index := range spec.Volumes {
		volume := &spec.Volumes[index]
		if volume.ConfigMap != nil {
			volume.ConfigMap.DefaultMode = nil
		}
		if volume.Secret != nil {
			volume.Secret.DefaultMode = nil
		}
		if volume.Projected != nil {
			volume.Projected.DefaultMode = nil
		}
		if volume.DownwardAPI != nil {
			volume.DownwardAPI.DefaultMode = nil
		}
	}
}

func plannerPodName(nodeName string, input PlannerPodInput) (string, error) {
	return contentAddressedPlannerObjectName(nodeName, "-planner-", input)
}

// contentAddressedPlannerObjectName covers the complete immutable Pod and NetworkPolicy policy,
// not only the normalized planning input. Runtime image, planner revision, pull-secret, deadline,
// and worker-policy changes must select a fresh object name instead of conflicting with a prior
// completed attempt at the same input digest.
func contentAddressedPlannerObjectName(
	nodeName, separator string,
	input PlannerPodInput,
) (string, error) {
	identityInput := input
	identityInput.Name = "planner-object-identity"
	pod, err := RenderPlannerPod(identityInput)
	if err != nil {
		return "", err
	}
	policy, err := RenderPlannerNetworkPolicy(identityInput)
	if err != nil {
		return "", err
	}
	identity, err := json.Marshal(struct {
		PodSpec               k8scorev1.PodSpec                 `json:"podSpec"`
		PodLabels             map[string]string                 `json:"podLabels"`
		PodAnnotations        map[string]string                 `json:"podAnnotations"`
		PodOwnerReferences    []metav1.OwnerReference           `json:"podOwnerReferences"`
		NetworkPolicySpec     k8snetworkingv1.NetworkPolicySpec `json:"networkPolicySpec"`
		PolicyLabels          map[string]string                 `json:"policyLabels"`
		PolicyAnnotations     map[string]string                 `json:"policyAnnotations"`
		PolicyOwnerReferences []metav1.OwnerReference           `json:"policyOwnerReferences"`
	}{
		PodSpec: pod.Spec, PodLabels: pod.GetLabels(),
		PodAnnotations: pod.GetAnnotations(), PodOwnerReferences: pod.GetOwnerReferences(),
		NetworkPolicySpec: policy.Spec, PolicyLabels: policy.GetLabels(),
		PolicyAnnotations:     policy.GetAnnotations(),
		PolicyOwnerReferences: policy.GetOwnerReferences(),
	})
	if err != nil {
		return "", fmt.Errorf("encoding planner object identity: %w", err)
	}
	digestSuffix := strings.TrimPrefix(digestArtifact(identity), "sha256:")[:12]
	maxNodeLength := kubernetesNameLimit - len(separator) - len(digestSuffix)
	if len(nodeName) > maxNodeLength {
		nodeName = strings.TrimRight(nodeName[:maxNodeLength], "-")
	}

	return nodeName + separator + digestSuffix, nil
}

func plannerObjectKey(namespace, name string) apimachinerytypes.NamespacedName {
	return apimachinerytypes.NamespacedName{Namespace: namespace, Name: name}
}
