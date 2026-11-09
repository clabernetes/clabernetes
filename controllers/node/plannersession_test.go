//nolint:testpackage // Exercises the unexported controller stream boundary.
package node

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	clabernetesinternalocimetadata "github.com/clabernetes/clabernetes/internal/ocimetadata"
	k8scorev1 "k8s.io/api/core/v1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

//nolint:gocyclo,gocognit // Exercises the complete private stream lifecycle.
func TestPlannerSessionReconcilerAttachesAndSuppliesInitialInput(t *testing.T) {
	t.Parallel()

	node := planTestNode("router-session")
	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(plannerTestScheme(t)).
		WithObjects(node).
		Build()
	entropy, err := (&EntropyReconciler{Client: client, Reader: client}).Resolve(t.Context(), node)
	if err != nil {
		t.Fatal(err)
	}
	input := validInput()
	input.EntropyDigest = entropy.Digest
	// The disposable worker mounts public payloads already; exec must not fetch them again.
	input.Payloads = []clabernetesinternaldeviceplan.PayloadInput{{
		ID: "mounted-public", NodeID: input.Nodes[0].ID,
		Kind:        clabernetesinternaldeviceplan.PayloadURL,
		Reference:   "https://example.invalid/already-mounted",
		Digest:      clabernetesinternaldeviceplan.Digest([]byte("mounted")),
		Destination: "/config/public", Mode: 0o444,
	}}
	canonical, err := input.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := input.Digest()
	if err != nil {
		t.Fatal(err)
	}
	inputConfigMap, _, err := (&PlannerInputConfigMapReconciler{}).Render(
		node,
		PlannerInputArtifact{CanonicalInput: canonical},
	)
	if err != nil {
		t.Fatal(err)
	}
	pod, err := RenderPlannerPod(PlannerPodInput{
		Node: node, Name: "router-session-planner", Image: "example/manager:1",
		InputConfigMapName: inputConfigMap.GetName(), InputDigest: digest,
		PlannerRevision: "session-v1", MaxInputBytes: 1 << 20,
		DeadlineSeconds: 60, Session: true, EntropySecretName: entropy.SecretName,
	})
	if err != nil {
		t.Fatal(err)
	}
	pod.Status.Phase = k8scorev1.PodRunning
	if err = client.Create(t.Context(), inputConfigMap); err != nil {
		t.Fatal(err)
	}
	if err = client.Create(t.Context(), pod); err != nil {
		t.Fatal(err)
	}
	attached := false
	reconciler := &PlannerSessionReconciler{
		Client: client, Reader: client,
		Attach: func(
			_ context.Context,
			namespace, podName, containerName string,
			stream io.Reader,
			output, _ io.Writer,
		) error {
			attached = true
			if namespace != pod.GetNamespace() || podName != pod.GetName() ||
				containerName != plannerContainerName {
				t.Fatalf("attach target = %s/%s:%s", namespace, podName, containerName)
			}
			var size uint64
			if err := binary.Read(stream, binary.BigEndian, &size); err != nil {
				return err
			}
			var bootstrap clabernetesinternaldeviceplan.PoolBootstrap
			if err := json.NewDecoder(io.LimitReader(stream, int64(size))).Decode(&bootstrap); err != nil {
				return err
			}
			if !bootstrap.ProjectedPayloads || len(bootstrap.Payloads) != 0 {
				t.Fatal("disposable worker should consume its mounted payloads")
			}
			initial, decodeErr := clabernetesinternaldeviceplan.NewSessionFrameDecoder(
				stream,
				1<<20,
			).Next()
			if decodeErr != nil {
				return decodeErr
			}
			if initial.Type != clabernetesinternaldeviceplan.SessionFrameInitial ||
				initial.SessionDigest != digest || initial.Input == nil {
				t.Fatalf("initial frame = %#v", initial)
			}
			plan := validPlannerResult(t, *initial.Input, "session-v1")
			content := []byte("generated-private-key-material")
			plan.Files = []clabernetesinternaldeviceplan.FilePlan{{
				ID: "key", NodeID: "node-a", SourceReference: "key", ArtifactPath: "key",
				SourceKind: clabernetesinternaldeviceplan.FileSourcePlanArtifact,
				Digest:     clabernetesinternaldeviceplan.Digest(content), Sensitive: true,
			}}

			return clabernetesinternaldeviceplan.WriteSessionFrame(
				output,
				clabernetesinternaldeviceplan.SessionFrame{
					Version:       clabernetesinternaldeviceplan.SessionProtocolVersion,
					Type:          clabernetesinternaldeviceplan.SessionFrameResult,
					SessionDigest: digest,
					Sequence:      1,
					Result: &clabernetesinternaldeviceplan.SessionResult{
						Input: *initial.Input, Plan: plan,
						Artifacts: []clabernetesinternaldeviceplan.ArtifactContent{{
							NodeID: "node-a", ArtifactPath: "key", Digest: plan.Files[0].Digest,
							Data: content, Sensitive: true,
						}},
					},
				},
			)
		},
	}

	if _, err = reconciler.Reconcile(context.Background(), ctrlruntime.Request{
		NamespacedName: plannerObjectKey(pod.GetNamespace(), pod.GetName()),
	}); err != nil {
		t.Fatal(err)
	}
	if !attached {
		t.Fatal("planner session was not attached")
	}
	accepted := &k8scorev1.Pod{}
	if err = client.Get(
		context.Background(),
		plannerObjectKey(pod.GetNamespace(), pod.GetName()),
		accepted,
	); !apimachineryerrors.IsNotFound(err) {
		t.Fatalf("completed disposable worker still exists: %v", err)
	}
	cached, exists, err := (workerOutputStore{Client: client}).Lookup(t.Context(), node, pod.Name)
	if err != nil || !exists {
		t.Fatalf("private session result was not persisted: %v", err)
	}
	payload, _ := clabernetesinternaldeviceplan.DecodeWorkerFramePayload(cached)
	if bytes.Contains(payload, []byte("generated-private-key-material")) {
		t.Fatal("generated private material escaped into the cache ConfigMap")
	}
	result, err := clabernetesinternaldeviceplan.DecodeCachedSessionResult(cached, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err = (&PlanArtifactReconciler{Client: client}).Verify(t.Context(), node, result.Plan); err != nil {
		t.Fatal(err)
	}
	// Model a retry after result persistence but before successful worker deletion.
	pod.ResourceVersion = ""
	if err = client.Create(t.Context(), pod); err != nil {
		t.Fatal(err)
	}
	attached = false
	if _, err = reconciler.Reconcile(context.Background(), ctrlruntime.Request{
		NamespacedName: plannerObjectKey(pod.GetNamespace(), pod.GetName()),
	}); err != nil {
		t.Fatal(err)
	}
	if attached {
		t.Fatal("accepted running planner Pod was attached a second time")
	}
	// A deletion event must not start another session, even without a cached result.
	pod.Name = "deleting-planner"
	pod.ResourceVersion = ""
	pod.Finalizers = []string{"test-finalizer"}
	if err = client.Create(t.Context(), pod); err != nil {
		t.Fatal(err)
	}
	if err = client.Delete(t.Context(), pod); err != nil {
		t.Fatal(err)
	}
	if _, err = reconciler.Reconcile(t.Context(), ctrlruntime.Request{
		NamespacedName: plannerObjectKey(pod.GetNamespace(), pod.GetName()),
	}); err != nil {
		t.Fatal(err)
	}
	if attached {
		t.Fatal("deleting planner Pod was attached")
	}
}

func TestPlannerSessionReconcilerResolvesRequestedImageMetadata(t *testing.T) {
	t.Parallel()

	input := validInput()
	nodeID := input.Nodes[0].ID
	pod := &k8scorev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "router-session-planner", Namespace: "lab",
			Annotations: map[string]string{plannerRevision: "session-v1"},
		},
	}
	client := ctrlruntimefake.NewClientBuilder().WithScheme(plannerTestScheme(t)).Build()
	reconciler := &PlannerSessionReconciler{
		Client: client, Reader: client,
		ConfigManagerGetter: clabernetesconfig.GetFakeManager,
		Platform: clabernetesinternalocimetadata.Platform{
			OS: "linux", Architecture: "amd64",
		},
		ImageMetadata: &ImageMetadataResolver{
			Client: client,
			Resolver: &fakeOCIMetadataResolver{
				result: &clabernetesinternalocimetadata.Metadata{
					SchemaVersion: clabernetesinternalocimetadata.SchemaVersion,
					DigestReference: "registry.example/extra@sha256:" +
						strings.Repeat("a", 64),
				},
			},
		},
	}
	response, err := reconciler.resolveImages(
		context.Background(),
		pod,
		input,
		clabernetesinternaldeviceplan.SessionFrame{
			Version:       clabernetesinternaldeviceplan.SessionProtocolVersion,
			Type:          clabernetesinternaldeviceplan.SessionFrameImageRequest,
			SessionDigest: "sha256:" + strings.Repeat("b", 64),
			Sequence:      1,
			Images: []clabernetesinternaldeviceplan.ImageRequirement{{
				NodeID: nodeID, Role: "extra", SourceReference: "registry.example/extra:1",
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if response.Type != clabernetesinternaldeviceplan.SessionFrameResponse ||
		len(response.ImageInputs) != 1 ||
		response.ImageInputs[0].SourceReference != "registry.example/extra:1" {
		t.Fatalf("image response = %#v", response)
	}
}

func TestPlannerSessionAcceptsFullArtifactBudget(t *testing.T) {
	t.Parallel()
	input := validInput()
	plan := validPlannerResult(t, input, "artifact-budget")
	content := bytes.Repeat([]byte("x"), clabernetesinternaldeviceplan.MaxPlanArtifactBytes)
	count := clabernetesinternaldeviceplan.MaxPlanArtifactsBytes / len(content)
	artifacts := make([]clabernetesinternaldeviceplan.ArtifactContent, 0, count)
	for index := range count {
		path := fmt.Sprintf("generated/%d", index)
		plan.Files = append(plan.Files, clabernetesinternaldeviceplan.FilePlan{
			ID: path, NodeID: "node-a", SourceReference: path, ArtifactPath: path,
			SourceKind: clabernetesinternaldeviceplan.FileSourcePlanArtifact,
			Digest:     clabernetesinternaldeviceplan.Digest(content), Sensitive: true,
		})
		artifacts = append(artifacts, clabernetesinternaldeviceplan.ArtifactContent{
			NodeID: "node-a", ArtifactPath: path, Data: content,
			Digest: clabernetesinternaldeviceplan.Digest(content), Sensitive: true,
		})
	}
	frame := clabernetesinternaldeviceplan.SessionFrame{
		Version: clabernetesinternaldeviceplan.SessionProtocolVersion,
		Type:    clabernetesinternaldeviceplan.SessionFrameResult, Sequence: 1,
		SessionDigest: plan.InputDigest,
		Result: &clabernetesinternaldeviceplan.SessionResult{
			Input:     input,
			Plan:      plan,
			Artifacts: artifacts,
		},
	}
	encoded := &bytes.Buffer{}
	if err := clabernetesinternaldeviceplan.WriteSessionFrame(encoded, frame); err != nil {
		t.Fatal(err)
	}
	decoded, err := clabernetesinternaldeviceplan.NewSessionFrameDecoder(encoded, clabernetesinternaldeviceplan.MaxSessionFrameBytes).
		Next()
	if err != nil {
		t.Fatal(err)
	}
	if err = validateControllerSessionResult(decoded, input, nil, nil, nil, ""); err != nil {
		t.Fatal(err)
	}
}
