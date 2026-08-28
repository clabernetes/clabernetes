//nolint:err113 // dense fixture-driven tests exercise one boundary end to end.
package directruntime_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	clabernetesinternaldirectruntime "github.com/clabernetes/clabernetes/internal/directruntime"
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
	for range 2 {
		if err := clabernetesinternaldirectruntime.RunApplicationRestartWithOperations(
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

	if err := clabernetesinternaldirectruntime.RunApplicationRestartWithOperations(
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
	if err := clabernetesinternaldirectruntime.RunApplicationRestartWithOperations(
		request,
		state,
		"SIGTERM",
		operations,
	); err == nil || !strings.Contains(err.Error(), "signal denied") {
		t.Fatalf("restart error = %v", err)
	}

	operations.err = nil
	if err := clabernetesinternaldirectruntime.RunApplicationRestartWithOperations(
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
		clabernetesinternaldeviceplan.ArtifactNodeDirectory("node-a"),
	)
	if err := os.MkdirAll(filepath.Join(nodeRoot, "generated"), 0o755); err != nil { //nolint:gosec // test fixture permissions.
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
	plan.Files = []clabernetesinternaldeviceplan.FilePlan{{
		ID: "generated/startup", NodeID: "node-a",
		SourceKind:      clabernetesinternaldeviceplan.FileSourceGenerator,
		SourceReference: "imported-hook", ArtifactPath: "generated/startup.cfg",
		Digest: clabernetesinternaldeviceplan.Digest(content), Mode: 0o600,
	}}

	plan.Actions = []clabernetesinternaldeviceplan.Action{
		{
			ID: "copy", Phase: clabernetesinternaldeviceplan.PhasePostStart, Order: 1,
			Target: clabernetesinternaldeviceplan.ActionTarget{
				NodeID: "node-a", ContainerID: "container-a",
			},
			Kind: clabernetesinternaldeviceplan.ActionFile,
			File: &clabernetesinternaldeviceplan.FileAction{
				FileID: "generated/startup", Destination: destination,
			},
		},
		{
			ID: "exec", Phase: clabernetesinternaldeviceplan.PhasePostStart, Order: 2,
			Target: clabernetesinternaldeviceplan.ActionTarget{
				NodeID: "node-a", ContainerID: "container-a",
			},
			Kind: clabernetesinternaldeviceplan.ActionExec,
			Exec: &clabernetesinternaldeviceplan.ExecAction{
				Command: []string{"/bin/sh", "-c", "printf complete > " + marker}, Wait: true,
			},
		},
	}
	if err := clabernetesinternaldirectruntime.RunLifecycle(
		context.Background(),
		plan,
		clabernetesinternaldeviceplan.PhasePostStart,
		"container-a",
		root,
	); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(destination) //nolint:gosec // test-controlled path.
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(got, content) {
		t.Fatalf("copied lifecycle content = %q", got)
	}

	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}

	if info.Mode().Perm() != 0o600 {
		t.Fatalf("copied lifecycle mode = %o", info.Mode().Perm())
	}

	if markerContent, err := os.ReadFile(marker); err != nil || //nolint:gosec // test-controlled path.
		string(markerContent) != "complete" {
		t.Fatalf("post-start marker = %q, %v", markerContent, err)
	}
}

// TestRunLifecycleContinuesPastFailingTopologyExec pins the containerlab behavior of the
// topology's own exec list: a failing command is reported and the rest of the phase still runs.
// A PostStart hook that failed would take the whole application container down instead.
func TestRunLifecycleContinuesPastFailingTopologyExec(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	marker := filepath.Join(t.TempDir(), "post-start-ran")

	plan := lifecycleTestPlan()
	plan.Actions = []clabernetesinternaldeviceplan.Action{
		{
			ID: "post-start/node-a/0", Phase: clabernetesinternaldeviceplan.PhasePostStart,
			Order: 1,
			Target: clabernetesinternaldeviceplan.ActionTarget{
				NodeID: "node-a", ContainerID: "container-a",
			},
			Kind: clabernetesinternaldeviceplan.ActionExec,
			Exec: &clabernetesinternaldeviceplan.ExecAction{
				Command:         []string{"/bin/sh", "-c", "exit 7"},
				Wait:            true,
				ContinueOnError: true,
			},
		},
		{
			ID: "post-start/node-a/1", Phase: clabernetesinternaldeviceplan.PhasePostStart,
			Order: 2,
			Target: clabernetesinternaldeviceplan.ActionTarget{
				NodeID: "node-a", ContainerID: "container-a",
			},
			Kind: clabernetesinternaldeviceplan.ActionExec,
			Exec: &clabernetesinternaldeviceplan.ExecAction{
				Command:         []string{"/bin/sh", "-c", "printf complete > " + marker},
				Wait:            true,
				ContinueOnError: true,
			},
		},
	}

	if err := clabernetesinternaldirectruntime.RunLifecycle(
		context.Background(),
		plan,
		clabernetesinternaldeviceplan.PhasePostStart,
		"container-a",
		root,
	); err != nil {
		t.Fatalf("RunLifecycle() error = %v, want the failing exec command to be skipped", err)
	}

	if markerContent, err := os.ReadFile(marker); err != nil || //nolint:gosec // test-controlled path.
		string(markerContent) != "complete" {
		t.Fatalf("post-start marker = %q, %v", markerContent, err)
	}
}

// TestRunLifecycleFailsOnFailingImportedExec keeps the commands a kind's own deployment recorded
// fail-closed: those are part of bringing the node up, not user-declared best-effort work.
func TestRunLifecycleFailsOnFailingImportedExec(t *testing.T) {
	t.Parallel()

	plan := lifecycleTestPlan()
	plan.Actions = []clabernetesinternaldeviceplan.Action{
		{
			ID:    "imported-deploy-exec/node-a/000001",
			Phase: clabernetesinternaldeviceplan.PhasePostStart,
			Order: 1,
			Target: clabernetesinternaldeviceplan.ActionTarget{
				NodeID: "node-a", ContainerID: "container-a",
			},
			Kind: clabernetesinternaldeviceplan.ActionExec,
			Exec: &clabernetesinternaldeviceplan.ExecAction{
				Command: []string{"/bin/sh", "-c", "exit 7"}, Wait: true,
			},
		},
	}

	err := clabernetesinternaldirectruntime.RunLifecycle(
		context.Background(),
		plan,
		clabernetesinternaldeviceplan.PhasePostStart,
		"container-a",
		t.TempDir(),
	)
	if err == nil {
		t.Fatal("RunLifecycle() error = nil, want the failing imported command to fail the phase")
	}
}

func TestRunLifecycleRejectsCrossNodeArtifactAccess(t *testing.T) {
	t.Parallel()

	plan := lifecycleTestPlan()
	plan.Nodes = append(plan.Nodes, clabernetesinternaldeviceplan.NodePlan{
		ID: "node-b", Name: "node-b", Kind: "package-kind",
		ContainerIDs: []string{"container-b"}, ReadinessContainerIDs: []string{"container-b"},
	})
	plan.Containers = append(plan.Containers, clabernetesinternaldeviceplan.ContainerPlan{
		ID: "container-b", NodeID: "node-b", NamespaceOwnerID: "container-b",
		Image: "example/device:1", Required: true,
	})
	plan.Actions = []clabernetesinternaldeviceplan.Action{{
		ID: "cross-node", Phase: clabernetesinternaldeviceplan.PhasePostStart,
		Target: clabernetesinternaldeviceplan.ActionTarget{
			NodeID: "node-b", ContainerID: "container-a",
		},
		Kind: clabernetesinternaldeviceplan.ActionExec,
		Exec: &clabernetesinternaldeviceplan.ExecAction{Command: []string{"true"}, Wait: true},
	}}

	err := clabernetesinternaldirectruntime.RunLifecycle(
		context.Background(),
		plan,
		clabernetesinternaldeviceplan.PhasePostStart,
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
		clabernetesinternaldeviceplan.ArtifactNodeDirectory("node-a"),
	)
	if err := os.MkdirAll(nodeRoot, 0o755); err != nil { //nolint:gosec // test fixture permissions.
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
	plan.Files = []clabernetesinternaldeviceplan.FilePlan{{
		ID: "generated/addition", NodeID: "node-a",
		SourceKind:      clabernetesinternaldeviceplan.FileSourceGenerator,
		SourceReference: "imported-hook", ArtifactPath: "addition",
		Digest: clabernetesinternaldeviceplan.Digest(addition), Mode: 0o600,
	}}

	plan.Actions = []clabernetesinternaldeviceplan.Action{{
		ID: "append", Phase: clabernetesinternaldeviceplan.PhasePostStart,
		Target: clabernetesinternaldeviceplan.ActionTarget{
			NodeID: "node-a", ContainerID: "container-a",
		},
		Kind: clabernetesinternaldeviceplan.ActionFile,
		File: &clabernetesinternaldeviceplan.FileAction{
			FileID: "generated/addition", Destination: destination,
			WriteMode: clabernetesinternaldeviceplan.FileWriteAppend,
		},
	}}
	if err := clabernetesinternaldirectruntime.RunLifecycle(
		context.Background(),
		plan,
		clabernetesinternaldeviceplan.PhasePostStart,
		"container-a",
		root,
	); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(destination) //nolint:gosec // test-controlled path.
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
	if err := clabernetesinternaldirectruntime.InstallLifecycleBinary(destination); err != nil {
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

func lifecycleTestPlan() clabernetesinternaldeviceplan.Plan {
	return clabernetesinternaldeviceplan.Plan{
		SchemaVersion: clabernetesinternaldeviceplan.SchemaVersion,
		Compatibility: clabernetesinternaldeviceplan.Compatibility{
			ContainerlabModule:  clabernetesinternaldeviceplan.ContainerlabModulePath,
			ContainerlabVersion: "v-test",
			RegistryDigest:      "sha256:" + strings.Repeat("a", 64),
			PlanSchemaVersion:   clabernetesinternaldeviceplan.SchemaVersion,
		},
		InputDigest: "sha256:" + strings.Repeat("b", 64),
		Planner: clabernetesinternaldeviceplan.PlannerIdentity{
			Name:     "clabernetes",
			Revision: "test",
		},
		Nodes: []clabernetesinternaldeviceplan.NodePlan{{
			ID: "node-a", Name: "node-a", Kind: "package-kind",
			ContainerIDs: []string{"container-a"}, ReadinessContainerIDs: []string{"container-a"},
		}},
		Containers: []clabernetesinternaldeviceplan.ContainerPlan{{
			ID: "container-a", NodeID: "node-a", NamespaceOwnerID: "container-a",
			Image: "example/device:1", Required: true,
		}},
	}
}
