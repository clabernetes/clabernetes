//nolint:testpackage // dense fixture-driven tests exercise one boundary end to end.
package deviceplan

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	clabruntime "github.com/srl-labs/containerlab/runtime"
	clabtypes "github.com/srl-labs/containerlab/types"
)

func TestRecordingRuntimeBlocksMutationAndImplicitInspection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		operation string
		invoke    func(*recordingRuntime) error
	}{
		{
			name: "image pull", operation: "runtime.PullImage",
			invoke: func(runtime *recordingRuntime) error {
				return runtime.PullImage(context.Background(), "example/image:1", "Always")
			},
		},
		{
			name: "container creation", operation: "runtime.CreateContainer",
			invoke: func(runtime *recordingRuntime) error {
				_, err := runtime.CreateContainer(context.Background(), &clabtypes.NodeConfig{})

				return err
			},
		},
		{
			name: "container start", operation: "runtime.StartContainer",
			invoke: func(runtime *recordingRuntime) error {
				_, err := runtime.StartContainer(
					context.Background(),
					"container",
					clabruntime.NewEndpointlessNode(&clabtypes.NodeConfig{}),
				)

				return err
			},
		},
		{
			name: "container inspection", operation: "runtime.ListContainers",
			invoke: func(runtime *recordingRuntime) error {
				_, err := runtime.ListContainers(context.Background(), nil)

				return err
			},
		},
		{
			name: "namespace inspection", operation: "runtime.GetNSPath",
			invoke: func(runtime *recordingRuntime) error {
				_, err := runtime.GetNSPath(context.Background(), "container")

				return err
			},
		},
		{
			name: "network creation", operation: "runtime.CreateNet",
			invoke: func(runtime *recordingRuntime) error {
				return runtime.CreateNet(context.Background())
			},
		},
		{
			name: "host path inspection", operation: "runtime.GetHostsPath",
			invoke: func(runtime *recordingRuntime) error {
				_, err := runtime.GetHostsPath(context.Background(), "container")

				return err
			},
		},
		{
			name: "container copy", operation: "runtime.CopyToContainer",
			invoke: func(runtime *recordingRuntime) error {
				return runtime.CopyToContainer(context.Background(), "container", "/dst", "/src")
			},
		},
		{
			name: "runtime socket", operation: "runtime.GetRuntimeSocket",
			invoke: func(runtime *recordingRuntime) error {
				_, err := runtime.GetRuntimeSocket()

				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			runtime := newRecordingRuntime(nil, nil, t.TempDir())
			err := tt.invoke(runtime)

			var planningErr *Error
			if !errors.As(err, &planningErr) || planningErr.Code != ErrorSideEffect ||
				planningErr.Behavior != tt.operation {
				t.Fatalf("operation error = %#v, want SideEffect for %q", err, tt.operation)
			}

			if !errors.As(runtime.Failure(), &planningErr) {
				t.Fatal("recorder did not retain forbidden-boundary failure")
			}
		})
	}
}

func TestRecordingRuntimeUsesOnlySuppliedImageMetadata(t *testing.T) {
	t.Parallel()

	runtime := newRecordingRuntime([]ImageInput{{
		NodeID:          "node-a",
		SourceReference: "example/image:1",
		DigestReference: "example/image@sha256:aaaa",
		Config:          ImageConfig{Labels: []KeyValue{{Name: "vendor", Value: "example"}}},
	}}, nil, t.TempDir())

	inspect, err := runtime.InspectImage(context.Background(), "example/image:1")
	if err != nil {
		t.Fatal(err)
	}

	if got, want := inspect.Config.Labels, map[string]string{"vendor": "example"}; !reflect.DeepEqual(
		got,
		want,
	) {
		t.Fatalf("recorded image labels = %#v, want %#v", got, want)
	}

	if failure := runtime.Failure(); failure != nil {
		t.Fatalf("supplied image inspection recorded failure: %v", failure)
	}

	_, err = runtime.InspectImage(context.Background(), "example/missing:1")

	var planningErr *Error
	if !errors.As(err, &planningErr) || planningErr.Code != ErrorMissingInput {
		t.Fatalf("missing image error = %#v, want MissingInput", err)
	}
}

func TestRecordingRuntimeCanInventoryMissingMetadataDuringDiscovery(t *testing.T) {
	t.Parallel()

	runtime := newRecordingRuntime(nil, nil, t.TempDir())
	runtime.AllowMissingImageMetadata()

	inspect, err := runtime.InspectImage(context.Background(), "example/future-component:1")
	if err != nil || inspect == nil {
		t.Fatalf("tolerant InspectImage() = (%#v, %v)", inspect, err)
	}

	if got, want := runtime.MissingImages(), []string{"example/future-component:1"}; !reflect.DeepEqual(
		got,
		want,
	) {
		t.Fatalf("missing image inventory = %#v, want %#v", got, want)
	}

	if failure := runtime.Failure(); failure != nil {
		t.Fatalf("tolerant discovery retained failure: %v", failure)
	}
}

func TestRecordingRuntimeRecordsGenericContainerLifecycle(t *testing.T) {
	t.Parallel()

	runtime := newRecordingRuntime(nil, nil, t.TempDir())
	runtime.BeginMutationRecording()

	config := &clabtypes.NodeConfig{LongName: "recorded-container"}

	runtimeID, err := runtime.CreateContainer(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}

	if _, err = runtime.StartContainer(
		context.Background(),
		runtimeID,
		clabruntime.NewEndpointlessNode(config),
	); err != nil {
		t.Fatal(err)
	}

	containers := runtime.Containers()
	if got, want := len(containers), 1; got != want ||
		containers[0].RuntimeID != runtimeID || !containers[0].Started ||
		containers[0].Config != config {
		t.Fatalf("recorded containers = %#v, want one complete lifecycle", containers)
	}

	if failure := runtime.Failure(); failure != nil {
		t.Fatalf("generic recording retained failure: %v", failure)
	}

	listed, err := runtime.ListContainers(context.Background(), []*clabtypes.GenericFilter{{
		FilterType: "name", Match: runtimeID,
	}})
	if err != nil || len(listed) != 1 || listed[0].ID != runtimeID ||
		listed[0].State != "running" {
		t.Fatalf("recorded runtime observation = %#v, err=%v", listed, err)
	}

	if status := runtime.GetContainerStatus(context.Background(), runtimeID); status != clabruntime.Running {
		t.Fatalf("recorded container status = %q", status)
	}

	if healthy, healthErr := runtime.IsHealthy(context.Background(), runtimeID); healthErr != nil ||
		!healthy {
		t.Fatalf("recorded container health = %v, err=%v", healthy, healthErr)
	}
}

func TestRecordingRuntimeRecordsGenericContainerCopy(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	source := filepath.Join(root, "source.cfg")
	if err := os.WriteFile(source, []byte("package-derived\n"), 0o640); err != nil { //nolint:gosec // test fixture permissions.
		t.Fatal(err)
	}

	runtime := newRecordingRuntime(nil, nil, filepath.Join(root, "artifacts"))
	runtime.BeginMutationRecording()

	config := &clabtypes.NodeConfig{LongName: "recorded-container"}

	runtimeID, err := runtime.CreateContainer(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}

	if err = runtime.CopyToContainer(context.Background(), runtimeID, "/etc/device.cfg", source); err != nil {
		t.Fatal(err)
	}

	copies := runtime.Copies()
	if got, want := len(copies), 1; got != want || copies[0].RuntimeID != runtimeID ||
		copies[0].Destination != "/etc/device.cfg" {
		t.Fatalf("recorded copies = %#v, want one generic copy", copies)
	}

	snapshot := filepath.Join(root, "artifacts", filepath.FromSlash(copies[0].ArtifactPath))

	content, err := os.ReadFile( //nolint:gosec // test-controlled path.
		snapshot,
	)
	if err != nil {
		t.Fatal(err)
	}

	if got, want := string(content), "package-derived\n"; got != want {
		t.Fatalf("snapshot content = %q, want %q", got, want)
	}
}

func TestRecordingRuntimeRecordsControlledHostsAndStdinArtifacts(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runtime := newRecordingRuntime(nil, nil, root)
	runtime.BeginMutationRecording()

	config := &clabtypes.NodeConfig{LongName: "recorded-container"}

	runtimeID, err := runtime.CreateContainer(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}

	hostsPath, err := runtime.GetHostsPath(context.Background(), runtimeID)
	if err != nil {
		t.Fatal(err)
	}

	if err = os.WriteFile(hostsPath, []byte("192.0.2.10 device-a\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err = runtime.WriteToStdinNoWait(
		context.Background(), runtimeID, []byte("configure terminal\n"),
	); err != nil {
		t.Fatal(err)
	}

	if got, want := len(runtime.Copies()), 1; got != want ||
		runtime.Copies()[0].Destination != "/etc/hosts" ||
		runtime.Copies()[0].WriteMode != FileWriteAppend {
		t.Fatalf("hosts copies = %#v, want one /etc/hosts action", runtime.Copies())
	}

	if got, want := len(runtime.Stdins()), 1; got != want ||
		runtime.Stdins()[0].Order <= runtime.Copies()[0].Order {
		t.Fatalf("stdin records = %#v, want one action ordered after hosts", runtime.Stdins())
	}

	content, readErr := os.ReadFile(filepath.Join( //nolint:gosec // test-controlled path.
		root,
		filepath.FromSlash(runtime.Stdins()[0].ArtifactPath),
	))
	if readErr != nil || string(content) != "configure terminal\n" {
		t.Fatalf("stdin artifact = %q, err = %v", content, readErr)
	}
}

func TestRecordingRuntimeFlagsNoErrorRuntimeMethods(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		operation string
		invoke    func(*recordingRuntime)
	}{
		{
			name: "container status", operation: "runtime.GetContainerStatus",
			invoke: func(runtime *recordingRuntime) {
				runtime.GetContainerStatus(context.Background(), "container")
			},
		},
		{
			name: "runtime bind mounts", operation: "runtime.GetCooCBindMounts",
			invoke: func(runtime *recordingRuntime) { runtime.GetCooCBindMounts() },
		},
		{
			name: "non-running output", operation: "runtime.LogNonRunningContainerOutput",
			invoke: func(runtime *recordingRuntime) {
				runtime.LogNonRunningContainerOutput(context.Background(), "container")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			runtime := newRecordingRuntime(nil, nil, t.TempDir())
			tt.invoke(runtime)

			var planningErr *Error
			if !errors.As(runtime.Failure(), &planningErr) ||
				planningErr.Code != ErrorSideEffect || planningErr.Behavior != tt.operation {
				t.Fatalf(
					"retained failure = %#v, want SideEffect for %q",
					planningErr,
					tt.operation,
				)
			}
		})
	}
}
