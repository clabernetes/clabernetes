//nolint:err113,funlen,gocognit,gocyclo,maintidx // Attached fail-closed protocol boundary.
package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	clabernetesinternalocimetadata "github.com/clabernetes/clabernetes/internal/ocimetadata"
	k8scorev1 "k8s.io/api/core/v1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	ctrlruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	plannerSessionErrorBytes  = 4 << 10
	plannerSessionMaxRequests = 8
)

// PlannerSessionAttacher opens one full-duplex stream to a running planner container.
type PlannerSessionAttacher func(
	ctx context.Context,
	namespace, podName, containerName string,
	input io.Reader,
	output, stderr io.Writer,
) error

// PlannerSessionReconciler services requests made by one running isolated planner process.
type PlannerSessionReconciler struct {
	Client              ctrlruntimeclient.Client
	Reader              ctrlruntimeclient.Reader
	Attach              PlannerSessionAttacher
	ImageMetadata       *ImageMetadataResolver
	Certificates        *CertificateReconciler
	ConfigManagerGetter clabernetesconfig.ManagerGetterFunc
	Platform            clabernetesinternalocimetadata.Platform
}

// Reconcile attaches only to running session-labeled Pods. Completion requeues the owning Node
// through its existing Pod watch; that controller validates and persists the terminal frame.
func (r *PlannerSessionReconciler) Reconcile(
	ctx context.Context,
	request ctrlruntime.Request,
) (ctrlruntime.Result, error) {
	if r == nil || r.Client == nil || r.Attach == nil {
		return ctrlruntime.Result{}, errors.New("planner session reconciler is incomplete")
	}
	pod := &k8scorev1.Pod{}
	if err := r.Client.Get(ctx, request.NamespacedName, pod); err != nil {
		if apimachineryerrors.IsNotFound(err) {
			return ctrlruntime.Result{}, nil
		}

		return ctrlruntime.Result{}, err
	}
	if pod.GetLabels()[plannerSessionLabel] != plannerSessionValue ||
		pod.Status.Phase != k8scorev1.PodRunning {
		return ctrlruntime.Result{}, nil
	}
	if pod.GetAnnotations()[plannerSessionResult] != "" {
		return ctrlruntime.Result{}, nil
	}
	node, input, err := r.sessionInput(ctx, pod)
	if err != nil {
		return ctrlruntime.Result{}, r.failAttempt(ctx, pod, err)
	}
	deadlineSeconds, err := strconv.ParseInt(
		pod.GetAnnotations()[plannerSessionDeadline],
		10,
		64,
	)
	if err != nil || deadlineSeconds <= 0 {
		return ctrlruntime.Result{}, r.failAttempt(
			ctx,
			pod,
			errors.New("planner session deadline is invalid"),
		)
	}
	sessionCtx, cancel := context.WithTimeout(ctx, time.Duration(deadlineSeconds)*time.Second)
	defer cancel()
	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	stopPipes := context.AfterFunc(sessionCtx, func() {
		_ = stdinReader.CloseWithError(sessionCtx.Err())
		_ = stdoutWriter.CloseWithError(sessionCtx.Err())
	})
	defer stopPipes()
	errorOutput := &boundedSessionWriter{remaining: plannerSessionErrorBytes}
	streamDone := make(chan error, 1)
	go func() {
		streamErr := r.Attach(
			sessionCtx,
			pod.GetNamespace(),
			pod.GetName(),
			plannerContainerName,
			stdinReader,
			stdoutWriter,
			errorOutput,
		)
		_ = stdinReader.CloseWithError(streamErr)
		_ = stdoutWriter.CloseWithError(streamErr)
		streamDone <- streamErr
	}()
	defer func() {
		_ = stdinWriter.Close()
		_ = stdoutReader.Close()
	}()
	sessionDigest, err := input.Digest()
	if err != nil {
		return ctrlruntime.Result{}, r.failAttempt(ctx, pod, err)
	}
	if err = clabernetesinternaldeviceplan.WriteSessionFrame(
		stdinWriter,
		clabernetesinternaldeviceplan.SessionFrame{
			Version:       clabernetesinternaldeviceplan.SessionProtocolVersion,
			Type:          clabernetesinternaldeviceplan.SessionFrameInitial,
			SessionDigest: sessionDigest,
			Sequence:      0,
			Input:         &input,
		},
	); err != nil {
		return ctrlruntime.Result{}, r.failAttempt(ctx, pod, err)
	}
	decoder := clabernetesinternaldeviceplan.NewSessionFrameDecoder(
		stdoutReader,
		DefaultMaxInputBytes+DefaultMaxPlanBytes,
	)
	expectedSequence := 1
	servicedRequests := map[string]bool{}
	var suppliedImages []clabernetesinternaldeviceplan.ImageInput
	var suppliedCertificates []clabernetesinternaldeviceplan.CertificateInput
	var certificateRequirements []clabernetesinternaldeviceplan.CertificateRequirement
	certificateSecret := ""
	for {
		frame, frameErr := decoder.Next()
		if frameErr != nil {
			if errors.Is(frameErr, io.EOF) {
				// The worker emits its durable structured diagnostic to the Pod log before
				// exiting. Leave the terminal Pod for the Node controller to classify and
				// persist instead of deleting the only copy of that diagnostic here.
				<-streamDone

				return ctrlruntime.Result{}, nil
			}

			return ctrlruntime.Result{}, r.failAttempt(ctx, pod, frameErr)
		}
		if frame.SessionDigest != sessionDigest {
			return ctrlruntime.Result{}, r.failAttempt(
				ctx,
				pod,
				errors.New("planner session request differs from its initial input"),
			)
		}
		if frame.Sequence != expectedSequence {
			return ctrlruntime.Result{}, r.failAttempt(
				ctx,
				pod,
				errors.New("planner session sequence is not monotonic"),
			)
		}
		if err = validateSessionRequestNodes(frame, input); err != nil {
			return ctrlruntime.Result{}, r.failAttempt(ctx, pod, err)
		}
		switch frame.Type {
		case clabernetesinternaldeviceplan.SessionFrameImageRequest:
			if expectedSequence > plannerSessionMaxRequests ||
				!sessionImagesAddFacts(frame.Images, input.Images, suppliedImages) {
				return ctrlruntime.Result{}, r.failAttempt(
					ctx,
					pod,
					errors.New("planner image request adds no new metadata fact"),
				)
			}
			if err = guardControllerSessionProgress(frame, servicedRequests); err != nil {
				return ctrlruntime.Result{}, r.failAttempt(ctx, pod, err)
			}
			response, responseErr := r.resolveImages(ctx, pod, input, frame)
			if responseErr != nil {
				return ctrlruntime.Result{}, r.failAttempt(ctx, pod, responseErr)
			}
			suppliedImages = append(suppliedImages, response.ImageInputs...)
			if err = clabernetesinternaldeviceplan.WriteSessionFrame(
				stdinWriter,
				response,
			); err != nil {
				return ctrlruntime.Result{}, r.failAttempt(ctx, pod, err)
			}
			expectedSequence++
		case clabernetesinternaldeviceplan.SessionFrameCertificateRequest:
			if expectedSequence > plannerSessionMaxRequests || len(certificateRequirements) != 0 {
				return ctrlruntime.Result{}, r.failAttempt(
					ctx,
					pod,
					errors.New("planner certificate request adds no new requirement set"),
				)
			}
			if err = guardControllerSessionProgress(frame, servicedRequests); err != nil {
				return ctrlruntime.Result{}, r.failAttempt(ctx, pod, err)
			}
			response, responseErr := r.resolveCertificates(ctx, node, input, frame)
			if responseErr != nil {
				return ctrlruntime.Result{}, r.failAttempt(ctx, pod, responseErr)
			}
			suppliedCertificates = slices.Clone(response.CertificateInputs)
			certificateRequirements = slices.Clone(frame.Certificates)
			certificateSecret = response.CertificateSecret
			if err = clabernetesinternaldeviceplan.WriteSessionFrame(
				stdinWriter,
				response,
			); err != nil {
				return ctrlruntime.Result{}, r.failAttempt(ctx, pod, err)
			}
			expectedSequence++
		case clabernetesinternaldeviceplan.SessionFrameResult:
			if err = validateControllerSessionResult(
				frame,
				input,
				suppliedImages,
				suppliedCertificates,
				certificateRequirements,
				certificateSecret,
			); err != nil {
				return ctrlruntime.Result{}, r.failAttempt(ctx, pod, err)
			}
			resultDigest, digestErr := clabernetesinternaldeviceplan.SessionTerminalDigest(frame)
			if digestErr != nil {
				return ctrlruntime.Result{}, r.failAttempt(ctx, pod, digestErr)
			}
			before := pod.DeepCopy()
			if pod.Annotations == nil {
				pod.Annotations = map[string]string{}
			}
			pod.Annotations[plannerSessionResult] = resultDigest
			if err = r.Client.Patch(ctx, pod, ctrlruntimeclient.MergeFrom(before)); err != nil {
				return ctrlruntime.Result{}, r.failAttempt(ctx, pod, err)
			}
			_ = stdinWriter.Close()
			if streamErr := <-streamDone; streamErr != nil {
				return ctrlruntime.Result{}, r.failAttempt(
					ctx,
					pod,
					fmt.Errorf("planner session stream failed: %w", streamErr),
				)
			}

			return ctrlruntime.Result{}, nil
		default:
			return ctrlruntime.Result{}, r.failAttempt(
				ctx,
				pod,
				fmt.Errorf("planner emitted unexpected session frame %q", frame.Type),
			)
		}
	}
}

func guardControllerSessionProgress(
	frame clabernetesinternaldeviceplan.SessionFrame,
	seen map[string]bool,
) error {
	frame.Sequence = 0
	raw, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	fingerprint := clabernetesinternaldeviceplan.Digest(raw)
	if seen[fingerprint] {
		return errors.New("planner repeated a request without adding new facts")
	}
	seen[fingerprint] = true

	return nil
}

func sessionImagesAddFacts(
	requests []clabernetesinternaldeviceplan.ImageRequirement,
	initial, supplied []clabernetesinternaldeviceplan.ImageInput,
) bool {
	known := append(slices.Clone(initial), supplied...)
	for _, request := range requests {
		for _, image := range known {
			if request.NodeID == image.NodeID &&
				(request.SourceReference == image.SourceReference ||
					request.SourceReference == image.DigestReference) {
				return false
			}
		}
	}

	return len(requests) != 0
}

func validateControllerSessionResult(
	frame clabernetesinternaldeviceplan.SessionFrame,
	initial clabernetesinternaldeviceplan.Input,
	images []clabernetesinternaldeviceplan.ImageInput,
	certificates []clabernetesinternaldeviceplan.CertificateInput,
	requirements []clabernetesinternaldeviceplan.CertificateRequirement,
	certificateSecret string,
) error {
	if frame.Result == nil {
		return errors.New("planner terminal result is absent")
	}
	expected := initial
	expected.Images = append(slices.Clone(initial.Images), images...)
	expected.Certificates = slices.Clone(certificates)
	expected, err := clabernetesinternaldeviceplan.NormalizeInput(expected)
	if err != nil {
		return err
	}
	actual, err := clabernetesinternaldeviceplan.NormalizeInput(frame.Result.Input)
	if err != nil {
		return err
	}
	normalizedRequirements, err := clabernetesinternaldeviceplan.NormalizeCertificateRequirements(
		requirements,
	)
	if err != nil {
		return err
	}
	actualRequirements, err := clabernetesinternaldeviceplan.NormalizeCertificateRequirements(
		frame.Result.Certificates,
	)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(expected, actual) ||
		!reflect.DeepEqual(normalizedRequirements, actualRequirements) ||
		frame.Result.CertificateSecret != certificateSecret {
		return errors.New("planner terminal result differs from controller-supplied facts")
	}
	encoded := &bytes.Buffer{}
	if err = clabernetesinternaldeviceplan.WriteSessionFrame(encoded, frame); err != nil {
		return err
	}
	if _, err = clabernetesinternaldeviceplan.DecodeSessionResult(
		encoded.Bytes(),
		DefaultMaxInputBytes+DefaultMaxPlanBytes,
	); err != nil {
		return err
	}

	return nil
}

func (r *PlannerSessionReconciler) sessionInput(
	ctx context.Context,
	pod *k8scorev1.Pod,
) (*clabernetesapisv1alpha1.Node, clabernetesinternaldeviceplan.Input, error) {
	inputName := pod.GetAnnotations()[plannerInputConfigMap]
	inputDigest := pod.GetAnnotations()[plannerInputDigest]
	if inputName == "" || inputDigest == "" || len(pod.OwnerReferences) != 1 ||
		pod.OwnerReferences[0].APIVersion !=
			clabernetesapisv1alpha1.SchemeGroupVersion.String() ||
		pod.OwnerReferences[0].Kind != nodeCRKind ||
		pod.OwnerReferences[0].Controller == nil || !*pod.OwnerReferences[0].Controller {
		return nil, clabernetesinternaldeviceplan.Input{},
			errors.New("planner session identity is incomplete")
	}
	node := &clabernetesapisv1alpha1.Node{}
	if err := r.reader().Get(ctx, apimachinerytypes.NamespacedName{
		Namespace: pod.GetNamespace(), Name: pod.OwnerReferences[0].Name,
	}, node); err != nil {
		return nil, clabernetesinternaldeviceplan.Input{}, err
	}
	if node.GetUID() != pod.OwnerReferences[0].UID {
		return nil, clabernetesinternaldeviceplan.Input{},
			errors.New("planner session owner identity changed")
	}
	inputConfigMap := &k8scorev1.ConfigMap{}
	if err := r.reader().Get(ctx, apimachinerytypes.NamespacedName{
		Namespace: pod.GetNamespace(), Name: inputName,
	}, inputConfigMap); err != nil {
		return nil, clabernetesinternaldeviceplan.Input{}, err
	}
	if inputConfigMap.Immutable == nil || !*inputConfigMap.Immutable ||
		inputConfigMap.GetLabels()[planOwnerUIDLabel] != string(node.GetUID()) ||
		!metav1.IsControlledBy(inputConfigMap, node) {
		return nil, clabernetesinternaldeviceplan.Input{},
			errors.New("planner session input ConfigMap ownership is invalid")
	}
	input, err := clabernetesinternaldeviceplan.DecodeInput(
		[]byte(inputConfigMap.Data[plannerInputKey]),
	)
	if err != nil {
		return nil, clabernetesinternaldeviceplan.Input{}, err
	}
	digest, err := input.Digest()
	if err != nil || digest != inputDigest {
		return nil, clabernetesinternaldeviceplan.Input{},
			errors.New("planner session input differs from its Pod identity")
	}

	return node, input, nil
}

func validateSessionRequestNodes(
	frame clabernetesinternaldeviceplan.SessionFrame,
	input clabernetesinternaldeviceplan.Input,
) error {
	nodes := make(map[string]bool, len(input.Nodes))
	for _, node := range input.Nodes {
		nodes[node.ID] = true
	}
	for _, image := range frame.Images {
		if !nodes[image.NodeID] {
			return errors.New("planner image request references an unknown Node")
		}
	}
	for _, certificate := range frame.Certificates {
		if !nodes[certificate.NodeID] {
			return errors.New("planner certificate request references an unknown Node")
		}
	}

	return nil
}

func (r *PlannerSessionReconciler) resolveImages(
	ctx context.Context,
	pod *k8scorev1.Pod,
	input clabernetesinternaldeviceplan.Input,
	request clabernetesinternaldeviceplan.SessionFrame,
) (clabernetesinternaldeviceplan.SessionFrame, error) {
	if r.ImageMetadata == nil || r.ConfigManagerGetter == nil {
		return clabernetesinternaldeviceplan.SessionFrame{},
			errors.New("planner image metadata resolver is unavailable")
	}
	resolver := *r.ImageMetadata
	resolver.Platform = r.Platform
	trust, err := compileRegistryMetadataTrust(
		r.ConfigManagerGetter().GetRegistryMetadataTrust(),
	)
	if err != nil {
		return clabernetesinternaldeviceplan.SessionFrame{}, err
	}
	mirrors, err := compileRegistryMetadataMirrors(
		r.ConfigManagerGetter().GetRegistryMetadataMirrors(),
	)
	if err != nil {
		return clabernetesinternaldeviceplan.SessionFrame{}, err
	}
	resolver.TrustFor = trust.ForReference
	resolver.TrustForRegistry = trust.ForRegistry
	resolver.MirrorFor = mirrors.ForReference
	inputDigest, err := input.Digest()
	if err != nil {
		return clabernetesinternaldeviceplan.SessionFrame{}, err
	}
	discovery := clabernetesinternaldeviceplan.ImageDiscovery{
		SchemaVersion: input.SchemaVersion,
		Compatibility: input.Compatibility,
		InputDigest:   inputDigest,
		Planner: clabernetesinternaldeviceplan.PlannerIdentity{
			Name: "clabernetes-session", Revision: pod.GetAnnotations()[plannerRevision],
		},
		Images: request.Images,
	}
	pullSecrets := make([]string, 0, len(pod.Spec.ImagePullSecrets))
	for _, secret := range pod.Spec.ImagePullSecrets {
		pullSecrets = append(pullSecrets, secret.Name)
	}
	resolution, err := resolver.Resolve(ctx, pod.GetNamespace(), discovery, pullSecrets)
	if err != nil {
		return clabernetesinternaldeviceplan.SessionFrame{}, err
	}

	return clabernetesinternaldeviceplan.SessionFrame{
		Version:       clabernetesinternaldeviceplan.SessionProtocolVersion,
		Type:          clabernetesinternaldeviceplan.SessionFrameResponse,
		SessionDigest: request.SessionDigest,
		Sequence:      request.Sequence,
		ImageInputs:   resolution.Images,
	}, nil
}

func (r *PlannerSessionReconciler) resolveCertificates(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	input clabernetesinternaldeviceplan.Input,
	request clabernetesinternaldeviceplan.SessionFrame,
) (clabernetesinternaldeviceplan.SessionFrame, error) {
	if r.Certificates == nil {
		return clabernetesinternaldeviceplan.SessionFrame{},
			errors.New("planner certificate resolver is unavailable")
	}
	resolution, err := r.Certificates.Resolve(
		ctx,
		node,
		input.TopologyName,
		request.Certificates,
	)
	if err != nil {
		return clabernetesinternaldeviceplan.SessionFrame{}, err
	}
	secret := &k8scorev1.Secret{}
	if err = r.reader().Get(ctx, apimachinerytypes.NamespacedName{
		Namespace: node.GetNamespace(), Name: resolution.SecretName,
	}, secret); err != nil {
		return clabernetesinternaldeviceplan.SessionFrame{}, err
	}
	material := make(map[string][]byte, len(secret.Data))
	for name, value := range secret.Data {
		material[name] = slices.Clone(value)
	}

	return clabernetesinternaldeviceplan.SessionFrame{
		Version:             clabernetesinternaldeviceplan.SessionProtocolVersion,
		Type:                clabernetesinternaldeviceplan.SessionFrameResponse,
		SessionDigest:       request.SessionDigest,
		Sequence:            request.Sequence,
		CertificateInputs:   resolution.Inputs,
		CertificateMaterial: material,
		CertificateSecret:   resolution.SecretName,
	}, nil
}

func (r *PlannerSessionReconciler) reader() ctrlruntimeclient.Reader {
	if r.Reader != nil {
		return r.Reader
	}

	return r.Client
}

func (r *PlannerSessionReconciler) failAttempt(
	ctx context.Context,
	pod *k8scorev1.Pod,
	cause error,
) error {
	if pod != nil {
		if err := r.Client.Delete(ctx, pod); err != nil && !apimachineryerrors.IsNotFound(err) {
			return errors.Join(cause, fmt.Errorf("deleting failed planner session Pod: %w", err))
		}
	}

	return cause
}

type boundedSessionWriter struct {
	buffer    bytes.Buffer
	remaining int
}

func (w *boundedSessionWriter) Write(value []byte) (int, error) {
	accepted := min(len(value), w.remaining)
	if accepted > 0 {
		_, _ = w.buffer.Write(value[:accepted])
		w.remaining -= accepted
	}

	return len(value), nil
}

func (w *boundedSessionWriter) String() string {
	return strings.TrimSpace(w.buffer.String())
}
