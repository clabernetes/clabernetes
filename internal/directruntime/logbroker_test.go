//nolint:testpackage // dense fixture-driven tests exercise one boundary end to end.
package directruntime

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
)

type fakeApplicationLogStreamer struct {
	mu      sync.Mutex
	targets []string
}

func (f *fakeApplicationLogStreamer) StreamLogs(
	_ context.Context,
	containerName string,
) (io.ReadCloser, error) {
	f.mu.Lock()
	f.targets = append(f.targets, containerName)
	f.mu.Unlock()

	return io.NopCloser(strings.NewReader("package boot log\n")), nil
}

func (f *fakeApplicationLogStreamer) Targets() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), f.targets...)
}

func TestApplicationLogBrokerStreamsOnlyAcceptedRuntimeTarget(t *testing.T) {
	t.Parallel()

	streamer := &fakeApplicationLogStreamer{}

	broker, err := StartApplicationLogBroker(
		context.Background(),
		t.TempDir()+"/runtime.sock",
		map[string]string{"package-runtime-a": "device-a"},
		streamer,
	)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = broker.Close() })

	stream, err := openApplicationLogStream(
		context.Background(),
		broker.SocketPath(),
		"package-runtime-a",
	)
	if err != nil {
		t.Fatal(err)
	}

	raw, err := io.ReadAll(stream)
	if closeErr := stream.Close(); err == nil {
		err = closeErr
	}

	if err != nil {
		t.Fatal(err)
	}

	if string(raw) != "package boot log\n" {
		t.Fatalf("streamed logs = %q", raw)
	}

	if got := streamer.Targets(); len(got) != 1 || got[0] != "device-a" {
		t.Fatalf("Kubernetes log targets = %#v", got)
	}
}

func TestApplicationLogBrokerRejectsUnplannedRuntimeTarget(t *testing.T) {
	t.Parallel()

	streamer := &fakeApplicationLogStreamer{}

	broker, err := StartApplicationLogBroker(
		context.Background(),
		t.TempDir()+"/runtime.sock",
		map[string]string{"package-runtime-a": "device-a"},
		streamer,
	)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = broker.Close() })

	stream, err := openApplicationLogStream(
		context.Background(),
		broker.SocketPath(),
		"package-runtime-b",
	)
	if stream != nil {
		_ = stream.Close()
	}

	if err == nil || !strings.Contains(err.Error(), "not present in the accepted plan") {
		t.Fatalf("unplanned target error = %v", err)
	}

	if got := streamer.Targets(); len(got) != 0 {
		t.Fatalf("unplanned target reached Kubernetes log source: %#v", got)
	}
}

func TestApplicationLogTargetsAreBoundToPodUIDAndNormalizedPlanOrder(t *testing.T) {
	t.Parallel()

	plan := applicationLogTestPlan(t)
	plan.Containers = append(plan.Containers, clabernetesinternaldeviceplan.ContainerPlan{
		ID: "container-b", NodeID: "node-a", RuntimeID: "runtime-b",
		NamespaceOwnerID: "container-a", Image: "example/device:1",
		ImageDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Required:    true,
	})
	plan.Nodes[0].ContainerIDs = append(plan.Nodes[0].ContainerIDs, "container-b")
	plan.Nodes[0].ReadinessContainerIDs = append(
		plan.Nodes[0].ReadinessContainerIDs,
		"container-b",
	)
	pod := &k8scorev1.Pod{
		ObjectMeta: metav1.ObjectMeta{UID: apimachinerytypes.UID("pod-uid")},
		Spec: k8scorev1.PodSpec{Containers: []k8scorev1.Container{
			{Name: "device-a"},
			{Name: "device-b"},
		}},
	}

	targets, err := applicationLogTargets(plan, pod, "pod-uid")
	if err != nil {
		t.Fatal(err)
	}

	if targets["runtime-a"] != "device-a" || targets["runtime-b"] != "device-b" {
		t.Fatalf("application log targets = %#v", targets)
	}

	if _, err = applicationLogTargets(plan, pod, "replacement-uid"); err == nil ||
		!strings.Contains(err.Error(), "Pod UID differs") {
		t.Fatalf("stale Pod target error = %v", err)
	}
}

func applicationLogTestPlan(t *testing.T) clabernetesinternaldeviceplan.Plan {
	t.Helper()

	compatibility := clabernetesinternaldeviceplan.Compatibility{
		ContainerlabModule:  clabernetesinternaldeviceplan.ContainerlabModulePath,
		ContainerlabVersion: "v-test", PlanSchemaVersion: clabernetesinternaldeviceplan.SchemaVersion,
		RegistryDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	input := clabernetesinternaldeviceplan.Input{
		SchemaVersion: clabernetesinternaldeviceplan.SchemaVersion,
		TopologyName:  "lab",
		Compatibility: compatibility,
		Nodes: []clabernetesinternaldeviceplan.NodeInput{{
			ID: "node-a", Name: "router", Kind: "package-kind",
			Definition: []byte(`{"kind":"package-kind","image":"example/device:1"}`),
		}},
		Images: []clabernetesinternaldeviceplan.ImageInput{{
			NodeID: "node-a", Role: "device", SourceReference: "example/device:1",
			DigestReference: "example/device@sha256:aaaaaaaa",
			Platform: clabernetesinternaldeviceplan.Platform{
				OS:           "linux",
				Architecture: "amd64",
			},
		}},
	}

	digest, err := input.Digest()
	if err != nil {
		t.Fatal(err)
	}

	return clabernetesinternaldeviceplan.Plan{
		SchemaVersion: clabernetesinternaldeviceplan.SchemaVersion,
		Compatibility: compatibility,
		InputDigest:   digest,
		Planner: clabernetesinternaldeviceplan.PlannerIdentity{
			Name: "clabernetes", Revision: "test",
		},
		Nodes: []clabernetesinternaldeviceplan.NodePlan{{
			ID: "node-a", Name: "router", Kind: "package-kind",
			ContainerIDs:          []string{"container-a"},
			ReadinessContainerIDs: []string{"container-a"},
		}},
		Containers: []clabernetesinternaldeviceplan.ContainerPlan{{
			ID: "container-a", NodeID: "node-a", RuntimeID: "runtime-a",
			NamespaceOwnerID: "container-a", Image: "example/device:1",
			ImageDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Required:    true,
		}},
	}
}
