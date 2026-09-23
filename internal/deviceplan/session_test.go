package deviceplan_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
)

func TestSessionWorkerPlansKnownImageWithoutRequest(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	stream, digest := initialSessionStream(t, input)
	output := &bytes.Buffer{}
	worker := clabernetesinternaldeviceplan.SessionWorker{
		Adapter: clabernetesinternaldeviceplan.Adapter{
			Registry: newSyntheticRegistry(t), Revision: "session-v1",
		},
		Input: stream, Output: output,
	}

	if err := worker.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	decoder := clabernetesinternaldeviceplan.NewSessionFrameDecoder(output, 1<<20)
	frame, err := decoder.Next()
	if err != nil {
		t.Fatal(err)
	}
	if frame.Type != clabernetesinternaldeviceplan.SessionFrameResult ||
		frame.SessionDigest != digest || frame.Sequence != 1 || frame.Result == nil ||
		frame.Result.Plan.InputDigest == "" {
		t.Fatalf("session result = %#v", frame)
	}
}

func TestSessionWorkerRequestsOnlyActuallyMissingImageMetadata(t *testing.T) {
	t.Parallel()

	complete := singleNodeInput(syntheticKind, "example/future:1")
	input := complete
	input.Images = nil
	stream, digest := initialSessionStream(t, input)
	if err := clabernetesinternaldeviceplan.WriteSessionFrame(stream,
		clabernetesinternaldeviceplan.SessionFrame{
			Version:       clabernetesinternaldeviceplan.SessionProtocolVersion,
			Type:          clabernetesinternaldeviceplan.SessionFrameResponse,
			SessionDigest: digest,
			Sequence:      1,
			ImageInputs:   complete.Images,
		}); err != nil {
		t.Fatal(err)
	}

	output := &bytes.Buffer{}
	worker := clabernetesinternaldeviceplan.SessionWorker{
		Adapter: clabernetesinternaldeviceplan.Adapter{
			Registry: newSyntheticRegistry(t), Revision: "session-v1",
		},
		Input: stream, Output: output,
	}
	if err := worker.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	decoder := clabernetesinternaldeviceplan.NewSessionFrameDecoder(output, 1<<20)
	request, err := decoder.Next()
	if err != nil {
		t.Fatal(err)
	}
	if request.Type != clabernetesinternaldeviceplan.SessionFrameImageRequest ||
		request.Sequence != 1 || len(request.Images) != 1 ||
		request.Images[0].SourceReference != "example/future:1" {
		t.Fatalf("image request = %#v", request)
	}
	result, err := decoder.Next()
	if err != nil {
		t.Fatal(err)
	}
	if result.Type != clabernetesinternaldeviceplan.SessionFrameResult ||
		result.Sequence != 2 || result.Result == nil || len(result.Result.Input.Images) != 1 {
		t.Fatalf("session result = %#v", result)
	}
}

func TestSessionWorkerRejectsImageResponseThatMakesNoProgress(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Images = nil
	stream, digest := initialSessionStream(t, input)
	wrong := singleNodeInput(syntheticKind, "example/other:1").Images[0]
	if err := clabernetesinternaldeviceplan.WriteSessionFrame(
		stream,
		clabernetesinternaldeviceplan.SessionFrame{
			Version:       clabernetesinternaldeviceplan.SessionProtocolVersion,
			Type:          clabernetesinternaldeviceplan.SessionFrameResponse,
			SessionDigest: digest,
			Sequence:      1,
			ImageInputs:   []clabernetesinternaldeviceplan.ImageInput{wrong},
		},
	); err != nil {
		t.Fatal(err)
	}
	output := &bytes.Buffer{}
	err := (clabernetesinternaldeviceplan.SessionWorker{
		Adapter: clabernetesinternaldeviceplan.Adapter{
			Registry: newSyntheticRegistry(t), Revision: "session-v1",
		},
		Input: stream, Output: output,
	}).Run(context.Background())
	var planningErr *clabernetesinternaldeviceplan.Error
	if !errors.As(err, &planningErr) ||
		planningErr.Code != clabernetesinternaldeviceplan.ErrorMissingInput {
		t.Fatalf("non-progressing response error = %#v", err)
	}
}

//nolint:gocyclo // The test deliberately verifies the complete certificate exchange.
func TestSessionWorkerReceivesCertificatesAndCompletesInSameProcess(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Nodes[0].Type = "certificate-test"
	input.Nodes[0].Definition = []byte(
		`{"kind":"` + syntheticKind +
			`","type":"certificate-test","image":"example/future:1"}`,
	)
	adapter := clabernetesinternaldeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "session-v1",
	}
	discovery, err := adapter.DiscoverImages(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(discovery.Certificates) != 1 {
		t.Fatalf("certificate discovery = %#v", discovery.Certificates)
	}
	certificateInputs, sourceRoot := materializeCertificateRequirements(
		t,
		discovery.Certificates,
	)
	material := map[string][]byte{}
	entries, err := os.ReadDir(sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		material[entry.Name()], err = os.ReadFile( //nolint:gosec // Entries come from t.TempDir.
			filepath.Join(sourceRoot, entry.Name()),
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	stream, digest := initialSessionStream(t, input)
	if err = clabernetesinternaldeviceplan.WriteSessionFrame(
		stream,
		clabernetesinternaldeviceplan.SessionFrame{
			Version:             clabernetesinternaldeviceplan.SessionProtocolVersion,
			Type:                clabernetesinternaldeviceplan.SessionFrameResponse,
			SessionDigest:       digest,
			Sequence:            1,
			CertificateInputs:   certificateInputs,
			CertificateMaterial: material,
			CertificateSecret:   "test-certificates",
		},
	); err != nil {
		t.Fatal(err)
	}
	output := &bytes.Buffer{}
	adapter.CertificateRoot = filepath.Join(t.TempDir(), "certificates")
	if err = (clabernetesinternaldeviceplan.SessionWorker{
		Adapter: adapter, Input: stream, Output: output,
	}).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, secret := range material {
		if bytes.Contains(output.Bytes(), secret) {
			t.Fatal("planner output contains supplied certificate material")
		}
	}
	decoder := clabernetesinternaldeviceplan.NewSessionFrameDecoder(output, 4<<20)
	request, err := decoder.Next()
	if err != nil {
		t.Fatal(err)
	}
	if request.Type != clabernetesinternaldeviceplan.SessionFrameCertificateRequest ||
		len(request.Certificates) != 1 {
		t.Fatalf("certificate request = %#v", request)
	}
	resultFrame, err := decoder.Next()
	if err != nil {
		t.Fatal(err)
	}
	if resultFrame.Type != clabernetesinternaldeviceplan.SessionFrameResult ||
		resultFrame.Result == nil ||
		len(resultFrame.Result.Input.Certificates) != 1 ||
		len(resultFrame.Result.Certificates) != 1 {
		t.Fatalf("certificate session result = %#v", resultFrame)
	}
	if !strings.Contains(resultFrame.Result.Plan.InputDigest, "sha256:") {
		t.Fatalf("plan input digest = %q", resultFrame.Result.Plan.InputDigest)
	}
}

func initialSessionStream(
	t *testing.T,
	input clabernetesinternaldeviceplan.Input,
) (*bytes.Buffer, string) {
	t.Helper()

	normalized, err := clabernetesinternaldeviceplan.NormalizeInput(input)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := normalized.Digest()
	if err != nil {
		t.Fatal(err)
	}
	stream := &bytes.Buffer{}
	if err = clabernetesinternaldeviceplan.WriteSessionFrame(stream,
		clabernetesinternaldeviceplan.SessionFrame{
			Version:       clabernetesinternaldeviceplan.SessionProtocolVersion,
			Type:          clabernetesinternaldeviceplan.SessionFrameInitial,
			SessionDigest: digest,
			Sequence:      0,
			Input:         &normalized,
		}); err != nil {
		t.Fatal(err)
	}

	return stream, digest
}
