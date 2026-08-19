package deviceplan_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	clabernetesdeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
)

func TestPayloadFetcherStagesVerifiedURLForImportedPlanningAndSealsNetwork(t *testing.T) {
	t.Parallel()

	content := []byte("package startup input\n")
	server := httptest.NewServer(
		http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = writer.Write(content)
		}),
	)
	defer server.Close()
	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Nodes[0].Type = "payload-workspace-test"
	input.Nodes[0].Definition = mustJSON(t, map[string]any{
		"kind": syntheticKind, "type": "payload-workspace-test",
		"image": "example/future:1", "startup-config": "/inputs/startup.cfg",
	})
	input.Payloads = []clabernetesdeviceplan.PayloadInput{{
		ID: "url-startup", NodeID: "node-a", Kind: clabernetesdeviceplan.PayloadURL,
		Reference: server.URL, Digest: clabernetesdeviceplan.Digest(content),
		Destination: "/inputs/startup.cfg", Mode: 0o400,
	}}
	payloadRoot := filepath.Join(t.TempDir(), "payloads")
	sealed := false
	if err := (clabernetesdeviceplan.PayloadFetcher{
		HTTPClient: server.Client(),
		SealNetwork: func() error {
			sealed = true

			return nil
		},
	}).FetchURLPayloads(context.Background(), input, payloadRoot); err != nil {
		t.Fatal(err)
	}
	if !sealed {
		t.Fatal("payload fetcher did not seal the planner network")
	}
	source := filepath.Join(
		payloadRoot,
		clabernetesdeviceplan.ArtifactNodeDirectory("url-startup"),
		"source",
	)
	got, err := os.ReadFile(source)
	if err != nil || string(got) != string(content) {
		t.Fatalf("fetched payload = %q, %v", got, err)
	}
	info, err := os.Stat(source)
	if err != nil || info.Mode().Perm() != 0o444 {
		t.Fatalf("planner source mode = %#v, %v", info, err)
	}
	evaluation, err := (clabernetesdeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "url-payload-workspace-v1",
		PayloadRoot: payloadRoot,
	}).Evaluate(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !hasGeneratedArtifact(
		evaluation.Nodes[0].GeneratedArtifacts,
		"generated/payload-derived.conf",
	) {
		t.Fatalf(
			"URL-derived package artifact is absent: %#v",
			evaluation.Nodes[0].GeneratedArtifacts,
		)
	}
}

func TestPayloadFetcherDefaultClientRejectsPrivateEndpointWithoutLeakingURL(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Payloads = []clabernetesdeviceplan.PayloadInput{{
		ID: "url-private", NodeID: "node-a", Kind: clabernetesdeviceplan.PayloadURL,
		Reference:   "http://127.0.0.1/private?token=must-not-leak",
		Digest:      "sha256:" + strings.Repeat("a", 64),
		Destination: "/inputs/private", Mode: 0o400,
	}}
	sealed := false
	err := (clabernetesdeviceplan.PayloadFetcher{SealNetwork: func() error {
		sealed = true

		return nil
	}}).FetchURLPayloads(context.Background(), input, filepath.Join(t.TempDir(), "payloads"))
	if err == nil || sealed {
		t.Fatalf("private URL fetch = %v, sealed %v", err, sealed)
	}
	if strings.Contains(err.Error(), "must-not-leak") || strings.Contains(err.Error(), "/private") {
		t.Fatalf("URL fetch diagnostic leaks URL data: %v", err)
	}
}
