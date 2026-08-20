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
	inputArtifact := PlannerInputArtifact{
		CanonicalInput: canonicalInput, SensitiveValues: attempt.SensitiveValues,
	}
	inputRendered, inputDigest, err := (&PlannerInputConfigMapReconciler{
		Client: r.Client, MaxInputBytes: maxInputBytes,
	}).Render(attempt.Node, inputArtifact)
	if err != nil {
		return nil, err
	}
	renderInput := PlannerPodInput{
		Node: attempt.Node, Image: attempt.Image,
		InputConfigMapName: inputRendered.GetName(), InputDigest: inputDigest,
		PlannerRevision: attempt.PlannerRevision, WorkerCommand: plannerWorkerPlan,
		MaxInputBytes:   int64(maxInputBytes),
		DeadlineSeconds: deadlineSeconds, ImagePullSecrets: attempt.ImagePullSecrets,
		Payloads:              attempt.Input.Payloads,
		CertificateSecretName: attempt.CertificateSecretName,
		EntropySecretName:     attempt.EntropySecretName,
	}
	frame, podName, pending, err := r.executeWorkerAttempt(
		ctx,
		attempt.Node,
		renderInput,
		"-planner-",
		inputArtifact,
		maxInputBytes,
	)
	result := &PlannerResult{
		State: PlannerStatePending, PodName: podName,
		InputConfigMapName: inputRendered.GetName(), InputDigest: inputDigest,
	}
	if err != nil {
		return nil, err
	}
	if pending {
		return result, nil
	}
	if clabernetesdeviceplan.FrameKind(frame) == clabernetesdeviceplan.WorkerFrameError {
		diagnostic, decodeErr := clabernetesdeviceplan.DecodeWorkerError(frame, maxPlanBytes)
		if decodeErr != nil {
			return nil, decodeErr
		}

		return nil, errors.Join(ErrPlannerFailed, diagnostic)
	}
	plan, decodeErr := clabernetesdeviceplan.DecodeWorkerOutput(frame, maxPlanBytes)
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
}

// executeWorkerAttempt advances one content-addressed worker attempt to a persisted framed
// record. A persisted record short-circuits all Pod work; a completed Pod has its record stored
// and its Pod, NetworkPolicy, and input ConfigMap removed; a Pod that terminated without any
// framed record is deleted so controller backoff retries it instead of caching the failure.
func (r *PlannerReconciler) executeWorkerAttempt(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	renderInput PlannerPodInput,
	separator string,
	inputArtifact PlannerInputArtifact,
	maxInputBytes int,
) (frame []byte, podName string, pending bool, err error) {
	podName, err = contentAddressedPlannerObjectName(node.GetName(), separator, renderInput)
	if err != nil {
		return nil, "", false, err
	}
	renderInput.Name = podName
	store := workerOutputStore{Client: r.Client}
	frame, found, err := store.Lookup(ctx, node, podName)
	if err != nil {
		return nil, podName, false, err
	}
	if found {
		return frame, podName, false, nil
	}
	if _, _, err = (&PlannerInputConfigMapReconciler{
		Client: r.Client, MaxInputBytes: maxInputBytes,
	}).Ensure(ctx, node, inputArtifact); err != nil {
		return nil, podName, false, err
	}
	policy, err := RenderPlannerNetworkPolicy(renderInput)
	if err != nil {
		return nil, podName, false, err
	}
	policyCreated, err := r.ensurePlannerNetworkPolicy(ctx, policy)
	if err != nil || policyCreated {
		return nil, podName, true, err
	}
	pod, err := RenderPlannerPod(renderInput)
	if err != nil {
		return nil, podName, false, err
	}
	existingPod, podCreated, err := r.ensurePlannerPod(ctx, pod)
	if err != nil || podCreated {
		return nil, podName, true, err
	}
	switch existingPod.Status.Phase {
	case k8scorev1.PodSucceeded, k8scorev1.PodFailed:
		if r.ReadLogs == nil {
			return nil, podName, false, fmt.Errorf(
				"planner log reader is required for a completed worker",
			)
		}
		logs, readErr := r.ReadLogs(
			ctx,
			existingPod.GetNamespace(),
			existingPod.GetName(),
			plannerContainerName,
		)
		extracted, ok := clabernetesdeviceplan.ExtractWorkerFrame(logs)
		if readErr != nil || !ok {
			// Unreadable logs or a terminal Pod without any framed record (deadline, eviction,
			// OOM, log rotation) is indistinguishable from a transient failure: remove the Pod
			// so the next reconcile runs a fresh attempt instead of caching the condition.
			if deleteErr := r.Client.Delete(
				ctx, existingPod,
			); deleteErr != nil && !apimachineryerrors.IsNotFound(deleteErr) {
				return nil, podName, false, fmt.Errorf(
					"deleting worker Pod without a usable record: %w",
					deleteErr,
				)
			}

			return nil, podName, false, fmt.Errorf(
				"%w: worker Pod %s terminated without a usable framed record; retrying",
				ErrPlannerFailed,
				podName,
			)
		}
		if err = store.Persist(
			ctx,
			node,
			podName,
			renderInput.WorkerCommand,
			extracted,
			inputArtifact.SensitiveValues,
		); err != nil {
			return nil, podName, false, err
		}
		if err = deleteWorkerAttemptArtifacts(
			ctx,
			r.Client,
			node.GetNamespace(),
			podName,
		); err != nil {
			return nil, podName, false, err
		}

		return extracted, podName, false, nil
	default:
		return nil, podName, true, nil
	}
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
