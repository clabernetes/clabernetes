package deviceplan_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	clabernetesdeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
)

func TestWorkerProducesOnlyCanonicalPlan(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	raw, err := input.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	adapter := clabernetesdeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "worker-v1",
	}
	expected, err := adapter.Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	expectedCanonical, err := expected.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	worker := clabernetesdeviceplan.Worker{
		Adapter: adapter,
		Input:   bytes.NewReader(raw), Output: &output,
	}
	if err = worker.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	plan, err := clabernetesdeviceplan.DecodeWorkerOutput(output.Bytes(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	decodedCanonical, err := plan.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(decodedCanonical), string(expectedCanonical); got != want {
		t.Fatalf("worker output is not canonical:\n%s\nwant:\n%s", got, want)
	}
}

func TestDecodeWorkerOutputIgnoresHookLogsAndRejectsUnframedJSON(t *testing.T) {
	t.Parallel()

	if _, err := clabernetesdeviceplan.DecodeWorkerOutput(
		[]byte("hook log\n{\"schemaVersion\":\"v1alpha1\"}\n"),
		1<<20,
	); err == nil {
		t.Fatal("DecodeWorkerOutput() accepted unframed hook output")
	}
}

func TestImageWorkerDiscoversImportedRolesBeforeMetadataResolution(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Images = nil
	raw, err := input.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err = (clabernetesdeviceplan.ImageWorker{
		Adapter: clabernetesdeviceplan.Adapter{
			Registry: newSyntheticRegistry(t), Revision: "images-v1",
		},
		Input: bytes.NewReader(raw), Output: &output,
	}).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	discovery, err := clabernetesdeviceplan.DecodeImageWorkerOutput(output.Bytes(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(discovery.Images) != 1 || discovery.Images[0].Role != "image" ||
		discovery.Images[0].SourceReference != "example/future:1" {
		t.Fatalf("image discovery = %#v", discovery)
	}
	if discovery.InputDigest == "" || discovery.Planner.Revision != "images-v1" {
		t.Fatalf("image discovery identity = %#v", discovery)
	}
}

func TestWorkerRejectsOversizeInputWithOnlyStructuredDiagnostic(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	err := (clabernetesdeviceplan.Worker{
		Adapter:       clabernetesdeviceplan.Adapter{Revision: "worker-v1"},
		Input:         strings.NewReader(`{"schemaVersion":"v1alpha1"}`),
		Output:        &output,
		MaxInputBytes: 1,
	}).Run(context.Background())
	var planningErr *clabernetesdeviceplan.Error
	if !errors.As(err, &planningErr) ||
		planningErr.Code != clabernetesdeviceplan.ErrorInvalidInput {
		t.Fatalf("worker error = %#v, want InvalidInput", err)
	}
	if _, decodeErr := clabernetesdeviceplan.DecodeWorkerOutput(output.Bytes(), 1<<20); decodeErr == nil {
		t.Fatalf("worker emitted a partial plan: %q", output.String())
	}
	diagnostic, decodeErr := clabernetesdeviceplan.DecodeWorkerError(output.Bytes(), 1<<20)
	if decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if diagnostic.Code != clabernetesdeviceplan.ErrorInvalidInput ||
		diagnostic.Field != planningErr.Field || diagnostic.Message != planningErr.Message {
		t.Fatalf("worker diagnostic = %#v, want %#v", diagnostic, planningErr)
	}
}
