//nolint:nlreturn,noinlineerr,wsl_v5 // Worker reconciliation uses compact fail-closed guards.
package node

import (
	"context"
	"fmt"
	"reflect"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesdeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	k8scorev1 "k8s.io/api/core/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// ImageDiscoveryAttempt contains explicit input and execution policy for imported image hooks.
type ImageDiscoveryAttempt struct {
	Node              *clabernetesapisv1alpha1.Node
	Input             clabernetesdeviceplan.Input
	SensitiveValues   [][]byte
	Image             string
	PlannerRevision   string
	ImagePullSecrets  []k8scorev1.LocalObjectReference
	EntropySecretName string
	MaxInputBytes     int
	MaxOutputBytes    int
	DeadlineSeconds   int64
}

// ImageDiscoveryResult reports sandbox progress or validated imported image requirements.
type ImageDiscoveryResult struct {
	State              PlannerState
	PodName            string
	InputConfigMapName string
	InputDigest        string
	Discovery          *clabernetesdeviceplan.ImageDiscovery
}

// ImageDiscoveryReconciler runs image hooks in the same locked-down worker boundary as planning.
type ImageDiscoveryReconciler struct {
	Client   ctrlruntimeclient.Client
	ReadLogs PlannerLogReader
}

// Reconcile advances one immutable image-discovery worker attempt.
func (r *ImageDiscoveryReconciler) Reconcile(
	ctx context.Context,
	attempt ImageDiscoveryAttempt,
) (*ImageDiscoveryResult, error) {
	if r.Client == nil {
		return nil, fmt.Errorf("image discovery reconciler client is required")
	}
	if (attempt.Input.EntropyDigest != "") != (attempt.EntropySecretName != "") {
		return nil, fmt.Errorf(
			"image discovery entropy digest and Secret identity must be supplied together",
		)
	}
	canonicalInput, err := attempt.Input.CanonicalJSON()
	if err != nil {
		return nil, err
	}
	maxInputBytes := attempt.MaxInputBytes
	if maxInputBytes == 0 {
		maxInputBytes = DefaultMaxInputBytes
	}
	maxOutputBytes := attempt.MaxOutputBytes
	if maxOutputBytes == 0 {
		maxOutputBytes = defaultPlannerMaxPlanBytes
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
		PlannerRevision: attempt.PlannerRevision, WorkerCommand: plannerWorkerImages,
		MaxInputBytes: int64(maxInputBytes), DeadlineSeconds: deadlineSeconds,
		ImagePullSecrets:  attempt.ImagePullSecrets,
		Payloads:          attempt.Input.Payloads,
		EntropySecretName: attempt.EntropySecretName,
	}
	podName, err := imageDiscoveryPodName(attempt.Node.GetName(), renderInput)
	if err != nil {
		return nil, err
	}
	renderInput.Name = podName
	result := &ImageDiscoveryResult{
		State: PlannerStatePending, PodName: podName,
		InputConfigMapName: inputConfigMap.GetName(), InputDigest: inputDigest,
	}
	objects := &PlannerReconciler{Client: r.Client}
	policy, err := RenderPlannerNetworkPolicy(renderInput)
	if err != nil {
		return nil, err
	}
	policyCreated, err := objects.ensurePlannerNetworkPolicy(ctx, policy)
	if err != nil || policyCreated {
		return result, err
	}
	pod, err := RenderPlannerPod(renderInput)
	if err != nil {
		return nil, err
	}
	existingPod, podCreated, err := objects.ensurePlannerPod(ctx, pod)
	if err != nil || podCreated {
		return result, err
	}
	switch existingPod.Status.Phase {
	case k8scorev1.PodSucceeded:
		if r.ReadLogs == nil {
			return nil, fmt.Errorf("image discovery log reader is required for a completed worker")
		}
		logs, readErr := r.ReadLogs(
			ctx,
			existingPod.GetNamespace(),
			existingPod.GetName(),
			plannerContainerName,
		)
		if readErr != nil {
			return nil, fmt.Errorf("reading completed image discovery output: %w", readErr)
		}
		discovery, decodeErr := clabernetesdeviceplan.DecodeImageWorkerOutput(
			logs,
			maxOutputBytes,
		)
		if decodeErr != nil {
			return nil, decodeErr
		}
		if discovery.InputDigest != inputDigest ||
			!reflect.DeepEqual(discovery.Compatibility, attempt.Input.Compatibility) ||
			discovery.Planner.Revision != attempt.PlannerRevision ||
			!discoveryReferencesInputNodes(discovery, attempt.Input) {
			return nil, fmt.Errorf(
				"%w: image discovery identity differs from its request",
				ErrPlannerFailed,
			)
		}
		result.State = PlannerStateSucceeded
		result.Discovery = &discovery

		return result, nil
	case k8scorev1.PodFailed:
		return nil, failedWorkerDiagnostic(
			ctx,
			r.ReadLogs,
			existingPod,
			maxOutputBytes,
			"image discovery Pod reached Failed phase",
		)
	default:
		return result, nil
	}
}

func discoveryReferencesInputNodes(
	discovery clabernetesdeviceplan.ImageDiscovery,
	input clabernetesdeviceplan.Input,
) bool {
	nodes := make(map[string]bool, len(input.Nodes))
	for _, node := range input.Nodes {
		nodes[node.ID] = true
	}
	for _, image := range discovery.Images {
		if !nodes[image.NodeID] {
			return false
		}
	}
	for _, certificate := range discovery.Certificates {
		if !nodes[certificate.NodeID] {
			return false
		}
	}

	return true
}

func imageDiscoveryPodName(nodeName string, input PlannerPodInput) (string, error) {
	return contentAddressedPlannerObjectName(nodeName, "-images-", input)
}
