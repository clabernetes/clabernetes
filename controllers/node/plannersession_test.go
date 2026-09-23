//nolint:testpackage // Exercises the unexported controller stream boundary.
package node

import (
	"context"
	"io"
	"strings"
	"testing"

	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	clabernetesinternalocimetadata "github.com/clabernetes/clabernetes/internal/ocimetadata"
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

//nolint:gocyclo // Exercises the complete attached stream lifecycle.
func TestPlannerSessionReconcilerAttachesAndSuppliesInitialInput(t *testing.T) {
	t.Parallel()

	input := validInput()
	canonical, err := input.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := input.Digest()
	if err != nil {
		t.Fatal(err)
	}
	node := planTestNode("router-session")
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
		DeadlineSeconds: 60, Session: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	pod.Status.Phase = k8scorev1.PodRunning
	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(plannerTestScheme(t)).
		WithObjects(node, inputConfigMap, pod).
		Build()
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

			return clabernetesinternaldeviceplan.WriteSessionFrame(
				output,
				clabernetesinternaldeviceplan.SessionFrame{
					Version:       clabernetesinternaldeviceplan.SessionProtocolVersion,
					Type:          clabernetesinternaldeviceplan.SessionFrameResult,
					SessionDigest: digest,
					Sequence:      1,
					Result: &clabernetesinternaldeviceplan.SessionResult{
						Input: *initial.Input, Plan: plan,
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
	); err != nil {
		t.Fatal(err)
	}
	if accepted.GetAnnotations()[plannerSessionResult] == "" {
		t.Fatal("accepted terminal result was not bound to the planner Pod")
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
