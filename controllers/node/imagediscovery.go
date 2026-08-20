//nolint:err113,gocyclo,noinlineerr,wsl_v5 // Worker reconciliation uses compact fail-closed guards.
package node

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	k8scorev1 "k8s.io/api/core/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// ImageDiscoveryAttempt contains explicit input and execution policy for imported image hooks.
type ImageDiscoveryAttempt struct {
	Node              *clabernetesapisv1alpha1.Node
	Input             clabernetesinternaldeviceplan.Input
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
	Discovery          *clabernetesinternaldeviceplan.ImageDiscovery
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
		return nil, errors.New("image discovery reconciler client is required")
	}
	if (attempt.Input.EntropyDigest != "") != (attempt.EntropySecretName != "") {
		return nil, errors.New(
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
		PlannerRevision: attempt.PlannerRevision, WorkerCommand: plannerWorkerImages,
		MaxInputBytes: int64(maxInputBytes), DeadlineSeconds: deadlineSeconds,
		ImagePullSecrets:  attempt.ImagePullSecrets,
		Payloads:          attempt.Input.Payloads,
		EntropySecretName: attempt.EntropySecretName,
	}
	objects := &PlannerReconciler{Client: r.Client, ReadLogs: r.ReadLogs}
	frame, podName, pending, err := objects.executeWorkerAttempt(
		ctx,
		attempt.Node,
		renderInput,
		"-images-",
		inputArtifact,
		maxInputBytes,
	)
	result := &ImageDiscoveryResult{
		State: PlannerStatePending, PodName: podName,
		InputConfigMapName: inputRendered.GetName(), InputDigest: inputDigest,
	}
	if err != nil {
		return nil, err
	}
	if pending {
		return result, nil
	}
	if clabernetesinternaldeviceplan.FrameKind(
		frame,
	) == clabernetesinternaldeviceplan.WorkerFrameError {
		diagnostic, decodeErr := clabernetesinternaldeviceplan.DecodeWorkerError(frame,
			maxOutputBytes)
		if decodeErr != nil {
			return nil, decodeErr
		}

		return nil, errors.Join(ErrPlannerFailed, diagnostic)
	}
	discovery, decodeErr := clabernetesinternaldeviceplan.DecodeImageWorkerOutput(frame,
		maxOutputBytes)
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
}

func discoveryReferencesInputNodes(
	discovery clabernetesinternaldeviceplan.ImageDiscovery,
	input clabernetesinternaldeviceplan.Input,
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
