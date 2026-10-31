package deviceplan_test

import (
	"bytes"
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

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
)

// planProjectedArtifacts plans one input and projects its generated artifact bytes exactly where the
// controller mounts the digest-named artifact objects, returning the plan and the payload root.
func planProjectedArtifacts(
	t *testing.T,
	adapter clabernetesinternaldeviceplan.Adapter,
	input clabernetesinternaldeviceplan.Input,
) (clabernetesinternaldeviceplan.Plan, string) {
	t.Helper()

	plan, artifacts, err := adapter.PlanArtifacts(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	payloadRoot := t.TempDir()
	projectPlanArtifacts(t, *plan, artifacts, payloadRoot)

	return *plan, payloadRoot
}

// projectPlanArtifacts writes artifact bytes under the preparation payload root.
func projectPlanArtifacts(
	t *testing.T,
	plan clabernetesinternaldeviceplan.Plan,
	artifacts []clabernetesinternaldeviceplan.ArtifactContent,
	payloadRoot string,
) {
	t.Helper()

	references := map[string]string{}
	for _, file := range plan.Files {
		if file.SourceKind == clabernetesinternaldeviceplan.FileSourcePlanArtifact {
			references[file.NodeID+"\x00"+file.ArtifactPath] = file.SourceReference
		}
	}
	for _, artifact := range artifacts {
		reference := references[artifact.NodeID+"\x00"+artifact.ArtifactPath]
		if reference == "" {
			t.Fatalf("generated artifact %q has no plan reference", artifact.ArtifactPath)
		}
		directory := filepath.Join(
			payloadRoot,
			clabernetesinternaldeviceplan.ArtifactNodeDirectory(reference),
		)
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(directory, "source"),
			artifact.Data,
			0o600,
		); err != nil {
			t.Fatal(err)
		}
		for _, attribute := range artifact.ExtendedAttributes {
			if err := os.WriteFile(
				filepath.Join(
					directory,
					clabernetesinternaldeviceplan.PlanArtifactAttributeKey(attribute.Name),
				),
				attribute.Value,
				0o600,
			); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// testPreparer builds a preparation boundary whose revision matches the planned one.
func testPreparer(
	adapter clabernetesinternaldeviceplan.Adapter,
	payloadRoot string,
) clabernetesinternaldeviceplan.Preparer {
	return clabernetesinternaldeviceplan.Preparer{
		Revision:    adapter.Revision,
		PayloadRoot: payloadRoot,
	}
}

func TestPreparerStagesOnlyAcceptedPlanArtifacts(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	adapter := clabernetesinternaldeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "preparer-v1",
	}

	plan, payloadRoot := planProjectedArtifacts(t, adapter, input)
	uid, gid := int64(os.Getuid()), int64(os.Getgid())
	root := t.TempDir()

	preparer := testPreparer(adapter, payloadRoot)
	if err := preparer.Prepare(context.Background(), input, plan, root); err != nil {
		t.Fatal(err)
	}
	// A second run must safely replace the same staged file.
	if err := preparer.Prepare(context.Background(), input, plan, root); err != nil {
		t.Fatal(err)
	}

	nodeDirectory := clabernetesinternaldeviceplan.ArtifactNodeDirectory("node-a")
	content, err := os.ReadFile( //nolint:gosec // test-controlled path.
		filepath.Join(root, nodeDirectory, "generated", "imported.conf"),
	)
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
	if _, err = os.Stat(
		filepath.Join(root, nodeDirectory, ".clabernetes-runtime-hosts"),
	); !os.IsNotExist(err) {
		t.Fatalf("runtime post-deploy artifact was created during preparation: %v", err)
	}
}

// TestPreparerStagesPlanBytesWithoutImportedEvaluation proves preparation publishes exactly the
// accepted bytes: a projected source that differs from the plan digest fails closed instead of
// being regenerated locally.
func TestPreparerStagesPlanBytesWithoutImportedEvaluation(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	adapter := clabernetesinternaldeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "preparer-v1",
	}

	plan, payloadRoot := planProjectedArtifacts(t, adapter, input)
	for _, file := range plan.Files {
		if file.SourceKind != clabernetesinternaldeviceplan.FileSourcePlanArtifact ||
			file.ArtifactKind != clabernetesinternaldeviceplan.ArtifactRegular {
			continue
		}
		source := filepath.Join(
			payloadRoot,
			clabernetesinternaldeviceplan.ArtifactNodeDirectory(file.SourceReference),
			"source",
		)
		if err := os.Chmod(source, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(source, []byte("tampered\n"), 0o600); err != nil {
			t.Fatal(err)
		}

		break
	}

	err := testPreparer(adapter, payloadRoot).Prepare(
		context.Background(), input, plan, t.TempDir(),
	)

	var planningErr *clabernetesinternaldeviceplan.Error
	if !errors.As(err, &planningErr) ||
		planningErr.Code != clabernetesinternaldeviceplan.ErrorInvariant ||
		planningErr.Behavior != "artifact-generation" {
		t.Fatalf("Prepare() error = %#v, want artifact-generation Invariant", err)
	}
}

// TestPlanArtifactBytesAreDeterministic proves repeated planning of identical input produces
// byte-identical generated artifacts, which is what lets preparation stage plan content.
func TestPlanArtifactBytesAreDeterministic(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	adapter := clabernetesinternaldeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "preparer-v1",
	}

	first, firstArtifacts, err := adapter.PlanArtifacts(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	second, secondArtifacts, err := adapter.PlanArtifacts(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstArtifacts) == 0 {
		t.Fatal("synthetic kind generated no artifact content")
	}
	if len(firstArtifacts) != len(secondArtifacts) {
		t.Fatalf("artifact count = %d, want %d", len(secondArtifacts), len(firstArtifacts))
	}
	for index := range firstArtifacts {
		if firstArtifacts[index].ArtifactPath != secondArtifacts[index].ArtifactPath ||
			firstArtifacts[index].Digest != secondArtifacts[index].Digest ||
			!bytes.Equal(firstArtifacts[index].Data, secondArtifacts[index].Data) {
			t.Fatalf("artifact %d is not deterministic: %#v", index, secondArtifacts[index])
		}
	}
	firstCanonical, err := first.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	secondCanonical, err := second.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstCanonical, secondCanonical) {
		t.Fatal("repeated planning produced different plan documents")
	}
}

// TestPlanArtifactSizeBoundFailsClosed proves a plan whose generated content exceeds the declared
// artifact bound fails planning instead of producing a partial plan.
func TestPlanArtifactSizeBoundFailsClosed(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Nodes[0].Type = "oversized-artifact-test"
	input.Nodes[0].Definition = mustJSON(t, map[string]any{
		"kind": syntheticKind, "type": "oversized-artifact-test", "image": "example/future:1",
	})
	adapter := clabernetesinternaldeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "artifact-bound-v1",
	}

	_, _, err := adapter.PlanArtifacts(context.Background(), input)

	var planningErr *clabernetesinternaldeviceplan.Error
	if !errors.As(err, &planningErr) ||
		planningErr.Code != clabernetesinternaldeviceplan.ErrorUnsupported ||
		planningErr.Behavior != "generated-artifact-size" {
		t.Fatalf("PlanArtifacts() error = %#v, want generated-artifact-size Unsupported", err)
	}
}

func TestPreparerTreatsNodeIdentityAsOpaque(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Nodes[0].ID = "../../outside"
	input.Images[0].NodeID = input.Nodes[0].ID
	adapter := clabernetesinternaldeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "preparer-v1",
	}

	plan, payloadRoot := planProjectedArtifacts(t, adapter, input)
	if err := testPreparer(adapter, payloadRoot).Prepare(
		context.Background(), input, plan, t.TempDir(),
	); err != nil {
		t.Fatal(err)
	}

	directory := clabernetesinternaldeviceplan.ArtifactNodeDirectory(input.Nodes[0].ID)
	if strings.Contains(directory, "/") || strings.Contains(directory, "..") {
		t.Fatalf("artifact Node directory is path-like: %q", directory)
	}
}

func TestPreparerPreservesGenericGeneratedSymlink(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Nodes[0].Type = "symlink-artifact-test"
	input.Nodes[0].Definition = mustJSON(t, map[string]string{
		"kind": syntheticKind, "type": input.Nodes[0].Type, "image": "example/future:1",
	})
	adapter := clabernetesinternaldeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "symlink-artifact-v1",
	}

	plan, payloadRoot := planProjectedArtifacts(t, adapter, input)
	foundLink := false
	for _, file := range plan.Files {
		if file.ArtifactKind == clabernetesinternaldeviceplan.ArtifactSymlink &&
			file.LinkTarget == "target" {
			foundLink = true
		}
	}
	if !foundLink {
		t.Fatalf("package symbolic link is absent from the generic plan: %#v", plan.Files)
	}

	preparer := testPreparer(adapter, payloadRoot)
	root := t.TempDir()
	if err := preparer.Prepare(context.Background(), input, plan, root); err != nil {
		t.Fatal(err)
	}
	if err := preparer.Prepare(context.Background(), input, plan, root); err != nil {
		t.Fatal(err)
	}

	generated := filepath.Join(
		root,
		clabernetesinternaldeviceplan.ArtifactNodeDirectory(input.Nodes[0].ID),
		"generated",
	)
	target, err := os.Readlink(filepath.Join(generated, "alias"))
	if err != nil || target != "target" {
		t.Fatalf("staged package symlink = %q, %v", target, err)
	}
	content, err := os.ReadFile( //nolint:gosec // test-controlled path.
		filepath.Join(generated, "alias", "value"),
	)
	if err != nil || string(content) != "package-target\n" {
		t.Fatalf("staged package symlink target = %q, %v", content, err)
	}
}

// TestPreparerAcceptsSingleCharacterNodeNames keeps containerlab's single-character node-name
// regression covered at the boundary that still runs deployment conditions: planning. Preparation
// stages plan content and never evaluates imported code, so names are proved here through the
// accepted plan's container identities.
func TestPreparerAcceptsSingleCharacterNodeNames(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"a", "z", "r1"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			input := singleNodeInput("linux", "alpine:3")
			input.Nodes[0].Name = name
			adapter := clabernetesinternaldeviceplan.Adapter{
				Registry: clabernetesinternaldeviceplan.NewContainerlabRegistry(),
				Revision: "single-character-name-v1",
			}

			plan, payloadRoot := planProjectedArtifacts(t, adapter, input)
			if err := testPreparer(adapter, payloadRoot).Prepare(
				context.Background(), input, plan, t.TempDir(),
			); err != nil {
				t.Fatalf("Prepare() for node %q: %v", name, err)
			}

			for _, container := range plan.Containers {
				if container.RuntimeID != name {
					t.Fatalf("container runtime ID = %q, want the Node name %q",
						container.RuntimeID, name)
				}
			}
		})
	}
}

func TestPreparerRejectsSymlinkNodeRoot(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	adapter := clabernetesinternaldeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "preparer-v1",
	}

	plan, payloadRoot := planProjectedArtifacts(t, adapter, input)
	root := t.TempDir()
	escape := t.TempDir()
	if err := os.Symlink(
		escape,
		filepath.Join(root, clabernetesinternaldeviceplan.ArtifactNodeDirectory(input.Nodes[0].ID)),
	); err != nil {
		t.Fatal(err)
	}

	err := testPreparer(adapter, payloadRoot).Prepare(context.Background(), input, plan, root)

	var planningErr *clabernetesinternaldeviceplan.Error
	if !errors.As(err, &planningErr) ||
		planningErr.Code != clabernetesinternaldeviceplan.ErrorUnsupported {
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
	adapter := clabernetesinternaldeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "preparer-v1",
	}

	plan, payloadRoot := planProjectedArtifacts(t, adapter, input)
	root := t.TempDir()
	nodeRoot := filepath.Join(
		root,
		clabernetesinternaldeviceplan.ArtifactNodeDirectory(input.Nodes[0].ID),
	)
	if err := os.MkdirAll(nodeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	escape := t.TempDir()
	if err := os.Symlink(escape, filepath.Join(nodeRoot, "generated")); err != nil {
		t.Fatal(err)
	}

	err := testPreparer(adapter, payloadRoot).Prepare(context.Background(), input, plan, root)

	var planningErr *clabernetesinternaldeviceplan.Error
	if !errors.As(err, &planningErr) ||
		planningErr.Code != clabernetesinternaldeviceplan.ErrorUnsupported {
		t.Fatalf("Prepare() error = %#v, want Unsupported", err)
	}
	if _, statErr := os.Stat(filepath.Join(escape, "imported.conf")); !os.IsNotExist(statErr) {
		t.Fatalf("preparation escaped through artifact-parent symlink: %v", statErr)
	}
}

func TestPreparerStagesDigestVerifiedMountedPayload(t *testing.T) {
	t.Parallel()

	content := []byte("mounted payload\n")
	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Payloads = []clabernetesinternaldeviceplan.PayloadInput{{
		ID: "payload-a", NodeID: input.Nodes[0].ID,
		Kind: clabernetesinternaldeviceplan.PayloadConfigMap, Reference: "lab/config:key",
		Digest: clabernetesinternaldeviceplan.Digest(
			content,
		), Destination: "/etc/payload", Mode: 0o444,
	}}
	adapter := clabernetesinternaldeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "preparer-v1",
	}

	plan, payloadRoot := planProjectedArtifacts(t, adapter, input)
	sourceRoot := filepath.Join(
		payloadRoot,
		clabernetesinternaldeviceplan.ArtifactNodeDirectory(input.Payloads[0].ID),
	)
	if err := os.MkdirAll(sourceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "source"), content, 0o444); err != nil { //nolint:gosec // test fixture.
		t.Fatal(err)
	}

	artifactRoot := t.TempDir()
	if err := testPreparer(adapter, payloadRoot).Prepare(
		context.Background(), input, plan, artifactRoot,
	); err != nil {
		t.Fatal(err)
	}

	staged, err := os.ReadFile(filepath.Join( //nolint:gosec // test-controlled path.
		artifactRoot,
		clabernetesinternaldeviceplan.ArtifactNodeDirectory(input.Nodes[0].ID),
		"payloads",
		input.Payloads[0].ID,
	))
	if err != nil || !bytes.Equal(staged, content) {
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
	input.Payloads = []clabernetesinternaldeviceplan.PayloadInput{{
		ID: "payload-a", NodeID: input.Nodes[0].ID,
		Kind: clabernetesinternaldeviceplan.PayloadURL, Reference: server.URL,
		Digest:      clabernetesinternaldeviceplan.Digest([]byte("accepted payload\n")),
		Destination: "/etc/payload", Mode: 0o444,
	}}
	adapter := clabernetesinternaldeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "preparer-v1",
	}

	plan, payloadRoot := planProjectedArtifacts(t, adapter, input)
	preparer := testPreparer(adapter, payloadRoot)
	preparer.HTTPClient = server.Client()

	err := preparer.Prepare(context.Background(), input, plan, t.TempDir())

	var planningErr *clabernetesinternaldeviceplan.Error
	if !errors.As(err, &planningErr) ||
		planningErr.Code != clabernetesinternaldeviceplan.ErrorInvariant ||
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
	input.Nodes = append(input.Nodes, clabernetesinternaldeviceplan.NodeInput{
		ID: "node-b", Name: "router-b", Kind: syntheticKind, GroupOwner: "node-a",
		Definition: []byte(`{"kind":"future-kind","image":"example/future:1"}`),
	})
	input.Images = append(input.Images, clabernetesinternaldeviceplan.ImageInput{
		NodeID: "node-b", SourceReference: "example/future:1",
		DigestReference: "example/future@sha256:" + strings.Repeat("a", 64),
		Platform:        clabernetesinternaldeviceplan.Platform{OS: "linux", Architecture: "amd64"},
	})
	digest := clabernetesinternaldeviceplan.Digest(content)
	input.Payloads = []clabernetesinternaldeviceplan.PayloadInput{
		{
			ID: "payload-a", NodeID: "node-a", Kind: clabernetesinternaldeviceplan.PayloadURL,
			Reference: server.URL, Digest: digest, Destination: "/licenses/device.key", Mode: 0o444,
		},
		{
			ID: "payload-b", NodeID: "node-b", Kind: clabernetesinternaldeviceplan.PayloadURL,
			Reference: server.URL, Digest: digest, Destination: "/licenses/device.key", Mode: 0o444,
		},
	}
	adapter := clabernetesinternaldeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "preparer-v1",
	}

	plan, payloadRoot := planProjectedArtifacts(t, adapter, input)
	preparer := testPreparer(adapter, payloadRoot)
	preparer.HTTPClient = server.Client()
	if err := preparer.Prepare(
		context.Background(), input, plan, t.TempDir(),
	); err != nil {
		t.Fatal(err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("identical grouped payload source reads = %d, want 1", got)
	}
}
