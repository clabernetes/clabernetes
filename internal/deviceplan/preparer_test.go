package deviceplan_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	clabernetesdeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
)

func TestPreparerRegeneratesOnlyAcceptedPreparationArtifacts(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	adapter := clabernetesdeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "preparer-v1",
	}
	plan, err := adapter.Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	uid, gid := int64(os.Getuid()), int64(os.Getgid())
	root := t.TempDir()
	preparer := clabernetesdeviceplan.Preparer{Adapter: adapter}
	if err = preparer.Prepare(context.Background(), input, *plan, root); err != nil {
		t.Fatal(err)
	}
	// A second run must safely replace the same generated file.
	if err = preparer.Prepare(context.Background(), input, *plan, root); err != nil {
		t.Fatal(err)
	}
	nodeDirectory := clabernetesdeviceplan.ArtifactNodeDirectory("node-a")
	content, err := os.ReadFile(filepath.Join(root, nodeDirectory, "generated", "imported.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(content), "generated\n"; got != want {
		t.Fatalf("prepared content = %q, want %q", got, want)
	}
	info, err := os.Stat(filepath.Join(root, nodeDirectory, "generated", "imported.conf"))
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int64(stat.Uid) != uid || int64(stat.Gid) != gid {
		t.Fatalf("prepared ownership = %#v, want %d:%d", info.Sys(), uid, gid)
	}
	if _, err = os.Stat(filepath.Join(root, nodeDirectory, ".clabernetes-runtime-hosts")); !os.IsNotExist(
		err,
	) {
		t.Fatalf("runtime post-deploy artifact was created during preparation: %v", err)
	}
}

func TestPreparerTreatsNodeIdentityAsOpaque(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Nodes[0].ID = "../../outside"
	input.Images[0].NodeID = input.Nodes[0].ID
	adapter := clabernetesdeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "preparer-v1",
	}
	plan, err := adapter.Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err = (clabernetesdeviceplan.Preparer{Adapter: adapter}).Prepare(
		context.Background(), input, *plan, root,
	); err != nil {
		t.Fatal(err)
	}
	directory := clabernetesdeviceplan.ArtifactNodeDirectory(input.Nodes[0].ID)
	if strings.Contains(directory, "/") || strings.Contains(directory, "..") {
		t.Fatalf("artifact Node directory is path-like: %q", directory)
	}
	if _, err = os.Stat(filepath.Join(root, directory, "generated", "imported.conf")); err != nil {
		t.Fatal(err)
	}
}

func TestPreparerPreservesGenericGeneratedSymlink(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Nodes[0].Type = "symlink-artifact-test"
	input.Nodes[0].Definition = mustJSON(t, map[string]string{
		"kind": syntheticKind, "type": input.Nodes[0].Type, "image": "example/future:1",
	})
	adapter := clabernetesdeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "symlink-artifact-v1",
	}
	plan, err := adapter.Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	foundLink := false
	for _, file := range plan.Files {
		if file.ArtifactKind == clabernetesdeviceplan.ArtifactSymlink &&
			file.LinkTarget == "target" {
			foundLink = true
		}
	}
	if !foundLink {
		t.Fatalf("package symbolic link is absent from the generic plan: %#v", plan.Files)
	}
	root := t.TempDir()
	preparer := clabernetesdeviceplan.Preparer{Adapter: adapter}
	if err = preparer.Prepare(context.Background(), input, *plan, root); err != nil {
		t.Fatal(err)
	}
	if err = preparer.Prepare(context.Background(), input, *plan, root); err != nil {
		t.Fatal(err)
	}
	generated := filepath.Join(
		root,
		clabernetesdeviceplan.ArtifactNodeDirectory(input.Nodes[0].ID),
		"generated",
	)
	target, err := os.Readlink(filepath.Join(generated, "alias"))
	if err != nil || target != "target" {
		t.Fatalf("staged package symlink = %q, %v", target, err)
	}
	content, err := os.ReadFile(filepath.Join(generated, "alias", "value"))
	if err != nil || string(content) != "package-target\n" {
		t.Fatalf("staged package symlink target = %q, %v", content, err)
	}
}

func TestPreparerRunsImportedDeploymentConditionsOnTargetWorker(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Nodes[0].Type = "condition-failure-test"
	input.Nodes[0].Definition = mustJSON(t, map[string]any{
		"kind": syntheticKind, "type": "condition-failure-test", "image": "example/future:1",
	})
	adapter := clabernetesdeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "target-worker-condition-v1",
	}
	plan, err := adapter.Plan(context.Background(), input)
	if err != nil {
		t.Fatalf("controller-side planning unexpectedly ran target-worker condition: %v", err)
	}
	err = (clabernetesdeviceplan.Preparer{Adapter: adapter}).Prepare(
		context.Background(),
		input,
		*plan,
		t.TempDir(),
	)
	var planningErr *clabernetesdeviceplan.Error
	if !errors.As(err, &planningErr) ||
		planningErr.Behavior != "imported-deployment-conditions" ||
		planningErr.Field != "deployment.conditions" {
		t.Fatalf("Prepare() condition error = %#v", err)
	}
}

func TestPreparerRejectsSymlinkNodeRoot(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	adapter := clabernetesdeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "preparer-v1",
	}
	plan, err := adapter.Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	escape := t.TempDir()
	if err = os.Symlink(
		escape,
		filepath.Join(root, clabernetesdeviceplan.ArtifactNodeDirectory(input.Nodes[0].ID)),
	); err != nil {
		t.Fatal(err)
	}
	err = (clabernetesdeviceplan.Preparer{Adapter: adapter}).Prepare(
		context.Background(), input, *plan, root,
	)
	var planningErr *clabernetesdeviceplan.Error
	if !errors.As(err, &planningErr) || planningErr.Code != clabernetesdeviceplan.ErrorUnsupported {
		t.Fatalf("Prepare() error = %#v, want Unsupported", err)
	}
	if _, statErr := os.Stat(filepath.Join(escape, "generated", "imported.conf")); !os.IsNotExist(
		statErr,
	) {
		t.Fatalf("preparation escaped through Node-root symlink: %v", statErr)
	}
}

func TestPreparerRejectsSymlinkArtifactParent(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	adapter := clabernetesdeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "preparer-v1",
	}
	plan, err := adapter.Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	nodeRoot := filepath.Join(
		root,
		clabernetesdeviceplan.ArtifactNodeDirectory(input.Nodes[0].ID),
	)
	if err = os.MkdirAll(nodeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	escape := t.TempDir()
	if err = os.Symlink(escape, filepath.Join(nodeRoot, "generated")); err != nil {
		t.Fatal(err)
	}
	err = (clabernetesdeviceplan.Preparer{Adapter: adapter}).Prepare(
		context.Background(), input, *plan, root,
	)
	var planningErr *clabernetesdeviceplan.Error
	if !errors.As(err, &planningErr) || planningErr.Code != clabernetesdeviceplan.ErrorUnsupported {
		t.Fatalf("Prepare() error = %#v, want Unsupported", err)
	}
	if _, statErr := os.Stat(filepath.Join(escape, "imported.conf")); !os.IsNotExist(statErr) {
		t.Fatalf("preparation escaped through artifact-parent symlink: %v", statErr)
	}
}

func TestPreparerRejectsArtifactDigestDrift(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	adapter := clabernetesdeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "preparer-v1",
	}
	plan, err := adapter.Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	for index := range plan.Files {
		if plan.Files[index].SourceReference == "containerlab/imported-prepare" &&
			plan.Files[index].ArtifactKind == clabernetesdeviceplan.ArtifactRegular {
			plan.Files[index].Digest = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
			break
		}
	}
	err = (clabernetesdeviceplan.Preparer{Adapter: adapter}).Prepare(
		context.Background(), input, *plan, t.TempDir(),
	)
	var planningErr *clabernetesdeviceplan.Error
	if !errors.As(err, &planningErr) || planningErr.Code != clabernetesdeviceplan.ErrorInvariant ||
		planningErr.Behavior != "artifact-generation" {
		t.Fatalf("Prepare() error = %#v, want artifact-generation Invariant", err)
	}
}

func TestPreparerStagesDigestVerifiedMountedPayload(t *testing.T) {
	t.Parallel()

	content := []byte("mounted payload\n")
	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Payloads = []clabernetesdeviceplan.PayloadInput{{
		ID: "payload-a", NodeID: input.Nodes[0].ID,
		Kind: clabernetesdeviceplan.PayloadConfigMap, Reference: "lab/config:key",
		Digest: clabernetesdeviceplan.Digest(content), Destination: "/etc/payload", Mode: 0o444,
	}}
	adapter := clabernetesdeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "preparer-v1",
	}
	plan, err := adapter.Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	payloadRoot := t.TempDir()
	sourceRoot := filepath.Join(
		payloadRoot,
		clabernetesdeviceplan.ArtifactNodeDirectory(input.Payloads[0].ID),
	)
	if err = os.MkdirAll(sourceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(sourceRoot, "source"), content, 0o444); err != nil {
		t.Fatal(err)
	}
	artifactRoot := t.TempDir()
	if err = (clabernetesdeviceplan.Preparer{
		Adapter: adapter, PayloadRoot: payloadRoot,
	}).Prepare(context.Background(), input, *plan, artifactRoot); err != nil {
		t.Fatal(err)
	}
	staged, err := os.ReadFile(filepath.Join(
		artifactRoot,
		clabernetesdeviceplan.ArtifactNodeDirectory(input.Nodes[0].ID),
		"payloads",
		input.Payloads[0].ID,
	))
	if err != nil || string(staged) != string(content) {
		t.Fatalf("staged payload = %q, err=%v", staged, err)
	}
}

func TestPreparerRejectsURLPayloadDigestDrift(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(
		http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = writer.Write([]byte("changed payload\n"))
		}),
	)
	defer server.Close()
	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Payloads = []clabernetesdeviceplan.PayloadInput{{
		ID: "payload-a", NodeID: input.Nodes[0].ID,
		Kind: clabernetesdeviceplan.PayloadURL, Reference: server.URL,
		Digest:      clabernetesdeviceplan.Digest([]byte("accepted payload\n")),
		Destination: "/etc/payload", Mode: 0o444,
	}}
	adapter := clabernetesdeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "preparer-v1",
	}
	plan, err := adapter.Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	err = (clabernetesdeviceplan.Preparer{
		Adapter: adapter, PayloadRoot: t.TempDir(), HTTPClient: server.Client(),
	}).Prepare(context.Background(), input, *plan, t.TempDir())
	var planningErr *clabernetesdeviceplan.Error
	if !errors.As(err, &planningErr) || planningErr.Code != clabernetesdeviceplan.ErrorInvariant ||
		planningErr.Behavior != "payload-staging" {
		t.Fatalf("Prepare() error = %#v, want payload-staging Invariant", err)
	}
}

func TestPreparerReadsIdenticalGroupedPayloadSourceOnce(t *testing.T) {
	t.Parallel()

	content := []byte("shared license payload\n")
	var requests atomic.Int32
	server := httptest.NewServer(
		http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			requests.Add(1)
			_, _ = writer.Write(content)
		}),
	)
	defer server.Close()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Nodes = append(input.Nodes, clabernetesdeviceplan.NodeInput{
		ID: "node-b", Name: "router-b", Kind: syntheticKind, GroupOwner: "node-a",
		Definition: []byte(`{"kind":"future-kind","image":"example/future:1"}`),
	})
	input.Images = append(input.Images, clabernetesdeviceplan.ImageInput{
		NodeID: "node-b", SourceReference: "example/future:1",
		DigestReference: "example/future@sha256:" + strings.Repeat("a", 64),
		Platform:        clabernetesdeviceplan.Platform{OS: "linux", Architecture: "amd64"},
	})
	digest := clabernetesdeviceplan.Digest(content)
	input.Payloads = []clabernetesdeviceplan.PayloadInput{
		{
			ID: "payload-a", NodeID: "node-a", Kind: clabernetesdeviceplan.PayloadURL,
			Reference: server.URL, Digest: digest, Destination: "/licenses/device.key", Mode: 0o444,
		},
		{
			ID: "payload-b", NodeID: "node-b", Kind: clabernetesdeviceplan.PayloadURL,
			Reference: server.URL, Digest: digest, Destination: "/licenses/device.key", Mode: 0o444,
		},
	}
	adapter := clabernetesdeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "preparer-v1",
	}
	plan, err := adapter.Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if err = (clabernetesdeviceplan.Preparer{
		Adapter: adapter, PayloadRoot: t.TempDir(), HTTPClient: server.Client(),
	}).Prepare(context.Background(), input, *plan, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("identical grouped payload source reads = %d, want 1", got)
	}
}

func TestPreparerRendersGeneratorContentWithRuntimeManagementIdentity(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Nodes[0].Type = "management-render-test"
	input.Nodes[0].Definition = mustJSON(t, map[string]any{
		"kind": syntheticKind, "type": input.Nodes[0].Type, "image": "example/future:1",
	})
	planning := clabernetesdeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "runtime-render-v1",
	}
	plan, err := planning.Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	runtime := planning
	runtime.PodAddress = "10.244.9.9/24"
	runtime.PodGateway = "10.244.9.1"
	if err = (clabernetesdeviceplan.Preparer{Adapter: runtime}).Prepare(
		context.Background(),
		input,
		*plan,
		root,
	); err != nil {
		t.Fatal(err)
	}
	nodeDirectory := clabernetesdeviceplan.ArtifactNodeDirectory("node-a")
	content, err := os.ReadFile(filepath.Join(root, nodeDirectory, "generated", "mgmt.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(content), "mgmt 10.244.9.9/24 gw 10.244.9.1\n"; got != want {
		t.Fatalf("runtime-rendered content = %q, want %q", got, want)
	}
	// Files the identity does not influence keep their plan-verified bytes and no runtime
	// digest record.
	digests := clabernetesdeviceplan.LoadRuntimeArtifactDigests(root, "node-a")
	if len(digests) != 1 || digests["generated/mgmt.conf"] == "" {
		t.Fatalf("runtime artifact record = %#v", digests)
	}
	untouched, err := os.ReadFile(filepath.Join(root, nodeDirectory, "generated", "imported.conf"))
	if err != nil || string(untouched) != "generated\n" {
		t.Fatalf("identity-independent content changed: %q err=%v", untouched, err)
	}

	// Without a runtime identity the plan-verified bytes stage unchanged and the record clears.
	if err = (clabernetesdeviceplan.Preparer{Adapter: planning}).Prepare(
		context.Background(),
		input,
		*plan,
		root,
	); err != nil {
		t.Fatal(err)
	}
	content, err = os.ReadFile(filepath.Join(root, nodeDirectory, "generated", "mgmt.conf"))
	if err != nil || string(content) != "mgmt none\n" {
		t.Fatalf("planning-identical content = %q err=%v", content, err)
	}
	if remaining := clabernetesdeviceplan.LoadRuntimeArtifactDigests(root, "node-a"); len(
		remaining,
	) != 0 {
		t.Fatalf("stale runtime artifact record = %#v", remaining)
	}
}
