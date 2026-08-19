package directruntime_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	clabernetesdeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	clabernetesdirectruntime "github.com/clabernetes/clabernetes/internal/directruntime"
)

type recordingRestartOperations struct {
	signals []syscall.Signal
	err     error
}

func (o *recordingRestartOperations) SignalPID(pid int, signal syscall.Signal) error {
	if pid != 1 {
		return errors.New("unexpected restart PID")
	}
	o.signals = append(o.signals, signal)

	return o.err
}

func TestApplicationRestartSignalsPIDOneOncePerPlanRequest(t *testing.T) {
	t.Parallel()

	request := "sha256:" + strings.Repeat("a", 64)
	state := filepath.Join(t.TempDir(), "container-a")
	operations := &recordingRestartOperations{}
	for attempt := 0; attempt < 2; attempt++ {
		if err := clabernetesdirectruntime.RunApplicationRestartWithOperations(
			request,
			state,
			"SIGUSR1",
			operations,
		); err != nil {
			t.Fatal(err)
		}
	}
	if len(operations.signals) != 1 || operations.signals[0] != syscall.SIGUSR1 {
		t.Fatalf("restart signals = %#v", operations.signals)
	}
	if err := clabernetesdirectruntime.RunApplicationRestartWithOperations(
		"sha256:"+strings.Repeat("b", 64),
		state,
		"",
		operations,
	); err != nil {
		t.Fatal(err)
	}
	if len(operations.signals) != 2 || operations.signals[1] != syscall.SIGTERM {
		t.Fatalf("restart signals after new request = %#v", operations.signals)
	}
}

func TestApplicationRestartFailsClosedBeforePublishingMarker(t *testing.T) {
	t.Parallel()

	request := "sha256:" + strings.Repeat("c", 64)
	state := filepath.Join(t.TempDir(), "container-a")
	operations := &recordingRestartOperations{err: errors.New("signal denied")}
	if err := clabernetesdirectruntime.RunApplicationRestartWithOperations(
		request,
		state,
		"SIGTERM",
		operations,
	); err == nil || !strings.Contains(err.Error(), "signal denied") {
		t.Fatalf("restart error = %v", err)
	}
	operations.err = nil
	if err := clabernetesdirectruntime.RunApplicationRestartWithOperations(
		request,
		state,
		"SIGTERM",
		operations,
	); err != nil {
		t.Fatal(err)
	}
	if len(operations.signals) != 2 {
		t.Fatalf("restart signal attempts = %#v", operations.signals)
	}
}

func TestRunLifecycleExecutesTypedActionsInsideTargetFilesystem(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	nodeRoot := filepath.Join(
		root,
		clabernetesdeviceplan.ArtifactNodeDirectory("node-a"),
	)
	if err := os.MkdirAll(filepath.Join(nodeRoot, "generated"), 0o755); err != nil {
		t.Fatal(err)
	}
	content := []byte("generated package behavior\n")
	if err := os.WriteFile(filepath.Join(nodeRoot, "generated", "startup.cfg"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	destinationDirectory := t.TempDir()
	destination := filepath.Join(destinationDirectory, "startup.cfg")
	marker := filepath.Join(destinationDirectory, "post-start-ran")
	plan := lifecycleTestPlan()
	plan.Files = []clabernetesdeviceplan.FilePlan{{
		ID: "generated/startup", NodeID: "node-a",
		SourceKind:      clabernetesdeviceplan.FileSourceGenerator,
		SourceReference: "imported-hook", ArtifactPath: "generated/startup.cfg",
		Digest: clabernetesdeviceplan.Digest(content), Mode: 0o600,
	}}
	plan.Actions = []clabernetesdeviceplan.Action{
		{
			ID: "copy", Phase: clabernetesdeviceplan.PhasePostStart, Order: 1,
			Target: clabernetesdeviceplan.ActionTarget{
				NodeID: "node-a", ContainerID: "container-a",
			},
			Kind: clabernetesdeviceplan.ActionFile,
			File: &clabernetesdeviceplan.FileAction{
				FileID: "generated/startup", Destination: destination,
			},
		},
		{
			ID: "exec", Phase: clabernetesdeviceplan.PhasePostStart, Order: 2,
			Target: clabernetesdeviceplan.ActionTarget{
				NodeID: "node-a", ContainerID: "container-a",
			},
			Kind: clabernetesdeviceplan.ActionExec,
			Exec: &clabernetesdeviceplan.ExecAction{
				Command: []string{"/bin/sh", "-c", "printf complete > " + marker}, Wait: true,
			},
		},
	}
	if err := clabernetesdirectruntime.RunLifecycle(
		context.Background(),
		plan,
		clabernetesdeviceplan.PhasePostStart,
		"container-a",
		root,
	); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("copied lifecycle content = %q", got)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("copied lifecycle mode = %o", info.Mode().Perm())
	}
	if markerContent, err := os.ReadFile(marker); err != nil ||
		string(markerContent) != "complete" {
		t.Fatalf("post-start marker = %q, %v", markerContent, err)
	}
}

func TestRunLifecycleRejectsCrossNodeArtifactAccess(t *testing.T) {
	t.Parallel()

	plan := lifecycleTestPlan()
	plan.Nodes = append(plan.Nodes, clabernetesdeviceplan.NodePlan{
		ID: "node-b", Name: "node-b", Kind: "package-kind",
		ContainerIDs: []string{"container-b"}, ReadinessContainerIDs: []string{"container-b"},
	})
	plan.Containers = append(plan.Containers, clabernetesdeviceplan.ContainerPlan{
		ID: "container-b", NodeID: "node-b", NamespaceOwnerID: "container-b",
		Image: "example/device:1", Required: true,
	})
	plan.Actions = []clabernetesdeviceplan.Action{{
		ID: "cross-node", Phase: clabernetesdeviceplan.PhasePostStart,
		Target: clabernetesdeviceplan.ActionTarget{
			NodeID: "node-b", ContainerID: "container-a",
		},
		Kind: clabernetesdeviceplan.ActionExec,
		Exec: &clabernetesdeviceplan.ExecAction{Command: []string{"true"}, Wait: true},
	}}
	err := clabernetesdirectruntime.RunLifecycle(
		context.Background(),
		plan,
		clabernetesdeviceplan.PhasePostStart,
		"container-a",
		t.TempDir(),
	)
	if err == nil || !strings.Contains(err.Error(), "crosses logical Node ownership") {
		t.Fatalf("RunLifecycle() error = %v", err)
	}
}

func TestRunLifecycleAppendsWithoutReplacingExistingContent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	nodeRoot := filepath.Join(
		root,
		clabernetesdeviceplan.ArtifactNodeDirectory("node-a"),
	)
	if err := os.MkdirAll(nodeRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	addition := []byte("package-derived\n")
	if err := os.WriteFile(filepath.Join(nodeRoot, "addition"), addition, 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "mounted-file")
	if err := os.WriteFile(destination, []byte("runtime-owned\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan := lifecycleTestPlan()
	plan.Files = []clabernetesdeviceplan.FilePlan{{
		ID: "generated/addition", NodeID: "node-a",
		SourceKind:      clabernetesdeviceplan.FileSourceGenerator,
		SourceReference: "imported-hook", ArtifactPath: "addition",
		Digest: clabernetesdeviceplan.Digest(addition), Mode: 0o600,
	}}
	plan.Actions = []clabernetesdeviceplan.Action{{
		ID: "append", Phase: clabernetesdeviceplan.PhasePostStart,
		Target: clabernetesdeviceplan.ActionTarget{
			NodeID: "node-a", ContainerID: "container-a",
		},
		Kind: clabernetesdeviceplan.ActionFile,
		File: &clabernetesdeviceplan.FileAction{
			FileID: "generated/addition", Destination: destination,
			WriteMode: clabernetesdeviceplan.FileWriteAppend,
		},
	}}
	if err := clabernetesdirectruntime.RunLifecycle(
		context.Background(),
		plan,
		clabernetesdeviceplan.PhasePostStart,
		"container-a",
		root,
	); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(content), "runtime-owned\npackage-derived\n"; got != want {
		t.Fatalf("appended lifecycle content = %q, want %q", got, want)
	}
}

func TestInstallLifecycleBinaryPublishesExecutableAtomically(t *testing.T) {
	t.Parallel()

	destination := filepath.Join(t.TempDir(), "manager")
	if err := clabernetesdirectruntime.InstallLifecycleBinary(destination); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o555 || info.Size() == 0 {
		t.Fatalf("installed lifecycle binary = %#v", info)
	}
}

func lifecycleTestPlan() clabernetesdeviceplan.Plan {
	return clabernetesdeviceplan.Plan{
		SchemaVersion: clabernetesdeviceplan.SchemaVersion,
		Compatibility: clabernetesdeviceplan.Compatibility{
			ContainerlabModule:  clabernetesdeviceplan.ContainerlabModulePath,
			ContainerlabVersion: "v-test",
			RegistryDigest:      "sha256:" + strings.Repeat("a", 64),
			PlanSchemaVersion:   clabernetesdeviceplan.SchemaVersion,
		},
		InputDigest: "sha256:" + strings.Repeat("b", 64),
		Planner:     clabernetesdeviceplan.PlannerIdentity{Name: "clabernetes", Revision: "test"},
		Nodes: []clabernetesdeviceplan.NodePlan{{
			ID: "node-a", Name: "node-a", Kind: "package-kind",
			ContainerIDs: []string{"container-a"}, ReadinessContainerIDs: []string{"container-a"},
		}},
		Containers: []clabernetesdeviceplan.ContainerPlan{{
			ID: "container-a", NodeID: "node-a", NamespaceOwnerID: "container-a",
			Image: "example/device:1", Required: true,
		}},
	}
}
