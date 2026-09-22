//nolint:err113 // Boundary errors intentionally describe individual rejected request invariants.
package node

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	plannerPoolLabel      = "c9s.run/planner-pool"
	plannerPoolRetryDelay = 2 * time.Second
)

// ErrPlannerPoolBusy means the bounded pool has no ready, unoccupied worker yet.
var ErrPlannerPoolBusy = errors.New("waiting for an available planner pool worker")

// PlannerPool reuses Kubernetes Pods, never imported Go state. Only the elected Node controller
// submits work. A second exclusion lock in each worker protects controller handovers.
type PlannerPool struct {
	Client    ctrlruntimeclient.Client
	Reader    ctrlruntimeclient.Reader
	Namespace string
	AppName   string
	Image     string
	Execute   PlannerSessionAttacher
	Sessions  *PlannerSessionReconciler
	mutex     sync.Mutex
	busy      map[string]bool
	releases  uint64
}

func (p *PlannerPool) acquire(ctx context.Context) (*k8scorev1.Pod, error) {
	pods := &k8scorev1.PodList{}
	if err := p.Client.List(ctx, pods, ctrlruntimeclient.InNamespace(p.Namespace),
		ctrlruntimeclient.MatchingLabels{plannerPoolLabel: p.AppName}); err != nil {
		return nil, err
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if p.busy == nil {
		p.busy = map[string]bool{}
	}
	for index := range pods.Items {
		pod := &pods.Items[index]
		if pod.DeletionTimestamp != nil || pod.Status.Phase != k8scorev1.PodRunning ||
			len(pod.Spec.Containers) != 1 || pod.Spec.Containers[0].Image != p.Image ||
			pod.GetUID() == "" || p.busy[string(pod.GetUID())] {
			continue
		}
		ready := slices.ContainsFunc(pod.Status.Conditions, func(c k8scorev1.PodCondition) bool {
			return c.Type == k8scorev1.PodReady && c.Status == k8scorev1.ConditionTrue
		})
		if ready {
			p.busy[string(pod.GetUID())] = true

			return pod.DeepCopy(), nil
		}
	}

	return nil, ErrPlannerPoolBusy
}

func (p *PlannerPool) release(pod *k8scorev1.Pod) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	delete(p.busy, string(pod.GetUID()))
	p.releases++
}

// releaseGeneration lets pending Nodes distinguish healthy saturation from a stalled pool.
func (p *PlannerPool) releaseGeneration() uint64 {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	return p.releases
}

//nolint:gocyclo // Validate and persist one complete request at the stream boundary.
func (p *PlannerPool) run(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	policy PlannerPodInput,
	artifact PlannerInputArtifact,
) ([]byte, error) {
	pod, err := p.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer p.release(pod)
	ctx, cancel := context.WithTimeout(ctx, time.Duration(policy.DeadlineSeconds)*time.Second)
	defer cancel()
	input, err := clabernetesinternaldeviceplan.DecodeInput(artifact.CanonicalInput)
	if err != nil {
		return nil, err
	}
	bootstrap, sensitive, err := p.material(ctx, node, input, policy.EntropySecretName)
	if err != nil {
		return nil, err
	}
	sensitive = append(sensitive, artifact.SensitiveValues...)
	result, err := p.converse(ctx, pod, node, policy, bootstrap)
	if err != nil {
		var diagnostic *clabernetesinternaldeviceplan.Error
		if !errors.As(err, &diagnostic) {
			return nil, err
		}
		frame, encodeErr := clabernetesinternaldeviceplan.EncodeWorkerError(*diagnostic)
		if encodeErr != nil {
			return nil, encodeErr
		}

		return frame, (workerOutputStore{Client: p.Client}).Persist(ctx, node, policy.Name,
			plannerWorkerPlan, frame, sensitive)
	}
	if result.CertificateSecret != "" {
		secret := &k8scorev1.Secret{}
		key := ctrlruntimeclient.ObjectKey{
			Namespace: node.Namespace, Name: result.CertificateSecret,
		}
		if err = p.Reader.Get(ctx, key, secret); err != nil {
			return nil, err
		}
		if !metav1.IsControlledBy(secret, node) {
			return nil, errors.New("planner certificate owner differs from request")
		}
		for _, value := range secret.Data {
			sensitive = append(sensitive, value)
		}
	}
	current := &clabernetesapisv1alpha1.Node{}
	if err = p.Reader.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(node), current); err != nil {
		return nil, err
	}
	if current.UID != node.UID || current.DeletionTimestamp != nil {
		return nil, errors.New("planner owner no longer exists")
	}
	canonical, err := result.Input.CanonicalJSON()
	if err != nil {
		return nil, err
	}
	if _, _, err = (&PlannerInputConfigMapReconciler{Client: p.Client}).Ensure(ctx, node,
		PlannerInputArtifact{CanonicalInput: canonical, SensitiveValues: sensitive}); err != nil {
		return nil, err
	}
	frame, err := clabernetesinternaldeviceplan.EncodeCachedSessionResult(*result)
	if err != nil {
		return nil, err
	}

	return frame, (workerOutputStore{Client: p.Client}).Persist(ctx, node, policy.Name,
		plannerWorkerPlan, frame, sensitive)
}

func (p *PlannerPool) material(ctx context.Context, node *clabernetesapisv1alpha1.Node,
	input clabernetesinternaldeviceplan.Input, entropyName string,
) (clabernetesinternaldeviceplan.PoolBootstrap, [][]byte, error) {
	bootstrap := clabernetesinternaldeviceplan.PoolBootstrap{
		Input:    input,
		Payloads: map[string][]byte{},
	}
	entropy := &k8scorev1.Secret{}
	entropyKey := ctrlruntimeclient.ObjectKey{Namespace: node.Namespace, Name: entropyName}
	if err := p.Reader.Get(ctx, entropyKey, entropy); err != nil {
		return bootstrap, nil, err
	}
	if !metav1.IsControlledBy(entropy, node) {
		return bootstrap, nil, errors.New("planner entropy owner differs from request")
	}
	bootstrap.Entropy = bytes.Clone(entropy.Data[clabernetesinternaldeviceplan.EntropySeedKey])
	sensitive := [][]byte{bootstrap.Entropy}
	total := 0
	for _, payload := range input.Payloads {
		content, err := p.payload(ctx, node.Namespace, payload)
		if err != nil {
			return bootstrap, nil, err
		}
		if clabernetesinternaldeviceplan.Digest(content) != payload.Digest {
			return bootstrap, nil, errors.New("planner payload changed before execution")
		}
		total += len(content)
		if total > clabernetesinternaldeviceplan.MaxPoolPayloadBytes {
			return bootstrap, nil, errors.New("planner payloads exceed the 64 MiB request limit")
		}
		bootstrap.Payloads[payload.ID] = content
		if payload.Sensitive {
			sensitive = append(sensitive, content)
		}
	}

	return bootstrap, sensitive, nil
}

func (p *PlannerPool) payload(ctx context.Context, namespace string,
	payload clabernetesinternaldeviceplan.PayloadInput,
) ([]byte, error) {
	if payload.Kind == clabernetesinternaldeviceplan.PayloadURL {
		return clabernetesinternaldeviceplan.ReadPoolURLPayload(ctx, payload.Reference)
	}
	referenceNamespace, name, key, err := parsePlannerPayloadObjectReference(payload.Reference)
	if err != nil || referenceNamespace != namespace {
		return nil, errors.New("planner payload references another namespace")
	}
	objectKey := ctrlruntimeclient.ObjectKey{Namespace: namespace, Name: name}
	switch payload.Kind {
	case clabernetesinternaldeviceplan.PayloadConfigMap:
		object := &k8scorev1.ConfigMap{}
		if err = p.Reader.Get(ctx, objectKey, object); err != nil {
			return nil, err
		}
		if value, found := object.Data[key]; found {
			return []byte(value), nil
		}
		value, found := object.BinaryData[key]
		if !found {
			return nil, errors.New("planner ConfigMap payload key is missing")
		}

		return bytes.Clone(value), nil
	case clabernetesinternaldeviceplan.PayloadSecret:
		object := &k8scorev1.Secret{}
		if err = p.Reader.Get(ctx, objectKey, object); err != nil {
			return nil, err
		}
		value, found := object.Data[key]
		if !found {
			return nil, errors.New("planner Secret payload key is missing")
		}

		return bytes.Clone(value), nil
	default:
		return nil, errors.New("planner payload has no supported source")
	}
}

//nolint:funlen,gocognit,gocyclo // Keep session ordering, fact validation and stream ownership together.
func (p *PlannerPool) converse(ctx context.Context, worker *k8scorev1.Pod,
	node *clabernetesapisv1alpha1.Node, policy PlannerPodInput,
	bootstrap clabernetesinternaldeviceplan.PoolBootstrap,
) (*clabernetesinternaldeviceplan.SessionResult, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	closePipes := func(err error) {
		_ = stdinReader.CloseWithError(err)
		_ = stdinWriter.CloseWithError(err)
		_ = stdoutReader.CloseWithError(err)
		_ = stdoutWriter.CloseWithError(err)
	}
	defer closePipes(io.ErrClosedPipe)
	stop := context.AfterFunc(ctx, func() { closePipes(ctx.Err()) })
	defer stop()
	streamDone := make(chan error, 1)
	diagnostics := &boundedSessionWriter{remaining: plannerSessionErrorBytes}
	go func() {
		err := p.Execute(
			ctx,
			worker.Namespace,
			worker.Name,
			plannerContainerName,
			stdinReader,
			stdoutWriter,
			diagnostics,
		)
		// Close stdin too: a failed exec must unblock the initial material/frame writer.
		_ = stdinReader.CloseWithError(err)
		_ = stdoutWriter.CloseWithError(err)
		streamDone <- err
	}()
	if err := clabernetesinternaldeviceplan.WritePoolBootstrap(stdinWriter, bootstrap); err != nil {
		return nil, err
	}
	input := bootstrap.Input
	digest, err := input.Digest()
	if err != nil {
		return nil, err
	}
	initial := clabernetesinternaldeviceplan.SessionFrame{
		Version:       clabernetesinternaldeviceplan.SessionProtocolVersion,
		Type:          clabernetesinternaldeviceplan.SessionFrameInitial,
		SessionDigest: digest, Input: &input,
	}
	if err = clabernetesinternaldeviceplan.WriteSessionFrame(stdinWriter, initial); err != nil {
		return nil, err
	}
	decoder := clabernetesinternaldeviceplan.NewSessionFrameDecoder(
		stdoutReader,
		DefaultMaxInputBytes+DefaultMaxPlanBytes,
	)
	seen := map[string]bool{}
	var images []clabernetesinternaldeviceplan.ImageInput
	var certificates []clabernetesinternaldeviceplan.CertificateInput
	var requirements []clabernetesinternaldeviceplan.CertificateRequirement
	certificateSecret := ""
	// Registry credentials belong to the requesting namespace, not the worker namespace.
	requestPod := &k8scorev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   node.Namespace,
			Annotations: map[string]string{plannerRevision: policy.PlannerRevision},
		},
		Spec: k8scorev1.PodSpec{ImagePullSecrets: policy.ImagePullSecrets},
	}
	for sequence := 1; ; sequence++ {
		frame, readErr := decoder.Next()
		if readErr != nil {
			if diagnostic := decoder.Diagnostic(); diagnostic != nil {
				return nil, diagnostic
			}

			return nil, fmt.Errorf("planner pool session ended: %w", readErr)
		}
		if frame.SessionDigest != digest || frame.Sequence != sequence {
			return nil, errors.New("planner session response identity differs from request")
		}
		if err = validateSessionRequestNodes(frame, input); err != nil {
			return nil, err
		}
		if frame.Type == clabernetesinternaldeviceplan.SessionFrameResult {
			err = validateControllerSessionResult(
				frame, input, images, certificates, requirements, certificateSecret,
			)
			if err != nil {
				return nil, err
			}
			_ = stdinWriter.Close()
			// Drain harmless hook log tails and let the exec stream close before reusing the Pod.
			if _, err = io.Copy(io.Discard, stdoutReader); err != nil {
				return nil, err
			}
			if err = <-streamDone; err != nil {
				return nil, err
			}
			frame.Result.SessionDigest = digest

			return frame.Result, nil
		}
		if sequence > plannerSessionMaxRequests {
			return nil, errors.New("planner session exceeded its request limit")
		}
		if err = guardControllerSessionProgress(frame, seen); err != nil {
			return nil, err
		}
		var response clabernetesinternaldeviceplan.SessionFrame
		switch frame.Type {
		case clabernetesinternaldeviceplan.SessionFrameImageRequest:
			if !sessionImagesAddFacts(frame.Images, input.Images, images) {
				return nil, errors.New("planner image request adds no facts")
			}
			response, err = p.Sessions.resolveImages(ctx, requestPod, input, frame)
			images = append(images, response.ImageInputs...)
		case clabernetesinternaldeviceplan.SessionFrameCertificateRequest:
			if len(requirements) != 0 {
				return nil, errors.New("planner repeated certificate request")
			}
			response, err = p.Sessions.resolveCertificates(ctx, node, input, frame)
			certificates = response.CertificateInputs
			requirements = frame.Certificates
			certificateSecret = response.CertificateSecret
		default:
			return nil, errors.New("planner emitted an unexpected request")
		}
		if err != nil {
			return nil, err
		}
		err = clabernetesinternaldeviceplan.WriteSessionFrame(stdinWriter, response)
		if err != nil {
			return nil, err
		}
	}
}
