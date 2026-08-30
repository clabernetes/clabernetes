package deviceplan

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	defaultMaxWorkerInputBytes int64 = 1 << 20
	workerOutputPrefix               = "C9S_DEVICE_PLAN_V1:"
	imageWorkerOutputPrefix          = "C9S_DEVICE_IMAGES_V1:"
	workerErrorPrefix                = "C9S_DEVICE_ERROR_V1:"
)

// Worker is the strict stream boundary used by a disposable planning process. Sandboxing is
// supplied by the Pod that runs this worker; Worker itself accepts no implicit runtime inputs.
type Worker struct {
	Adapter       Adapter
	Input         io.Reader
	Output        io.Writer
	MaxInputBytes int64
}

// Run decodes one input document and writes one canonical plan document. It never writes a
// partial plan when validation or imported evaluation fails.
func (w Worker) Run(ctx context.Context) (runErr error) {
	defer func() {
		if runErr != nil {
			_ = writeWorkerError(w.Output, runErr)
		}
	}()

	if ctx == nil {
		return planningError(ErrorInvalidInput, "context", "context is nil", nil)
	}

	if w.Input == nil || w.Output == nil {
		return planningError(
			ErrorMissingInput,
			"worker.stream",
			"input and output streams are required",
			nil,
		)
	}

	input, err := decodeWorkerInput(w.Input, w.MaxInputBytes)
	if err != nil {
		return err
	}

	plan, err := w.Adapter.Plan(ctx, input)
	if err != nil {
		return err
	}

	framed, err := EncodeWorkerOutput(*plan)
	if err != nil {
		return err
	}

	if _, err = w.Output.Write(framed); err != nil {
		return planningError(ErrorSerialization, "worker.output", "cannot write device plan", err)
	}

	return nil
}

// ImageWorker is the strict stream boundary for imported image-role discovery.
type ImageWorker struct {
	Adapter       Adapter
	Input         io.Reader
	Output        io.Writer
	MaxInputBytes int64
}

// Run discovers package-owned image roles without running deployment or lifecycle hooks.
func (w ImageWorker) Run(ctx context.Context) (runErr error) {
	defer func() {
		if runErr != nil {
			_ = writeWorkerError(w.Output, runErr)
		}
	}()

	if ctx == nil {
		return planningError(ErrorInvalidInput, "context", "context is nil", nil)
	}

	if w.Input == nil || w.Output == nil {
		return planningError(
			ErrorMissingInput,
			"worker.stream",
			"input and output streams are required",
			nil,
		)
	}

	input, err := decodeWorkerInput(w.Input, w.MaxInputBytes)
	if err != nil {
		return err
	}

	discovery, err := w.Adapter.DiscoverImages(ctx, input)
	if err != nil {
		return err
	}

	framed, err := EncodeImageWorkerOutput(*discovery)
	if err != nil {
		return err
	}

	if _, err = w.Output.Write(framed); err != nil {
		return planningError(
			ErrorSerialization,
			"worker.output",
			"cannot write image discovery",
			err,
		)
	}

	return nil
}

func decodeWorkerInput(input io.Reader, configuredMaxBytes int64) (Input, error) {
	maxInputBytes := configuredMaxBytes
	if maxInputBytes == 0 {
		maxInputBytes = defaultMaxWorkerInputBytes
	}

	if maxInputBytes < 0 {
		return Input{}, planningError(
			ErrorInvalidInput,
			"worker.maxInputBytes",
			"maximum input size must not be negative",
			nil,
		)
	}

	limited := io.LimitReader(input, maxInputBytes+1)

	raw, err := io.ReadAll(limited)
	if err != nil {
		return Input{}, planningError(
			ErrorSerialization,
			"worker.input",
			"cannot read planning input",
			err,
		)
	}

	if int64(len(raw)) > maxInputBytes {
		return Input{}, planningError(
			ErrorInvalidInput,
			"worker.input",
			fmt.Sprintf("planning input exceeds %d-byte limit", maxInputBytes),
			nil,
		)
	}

	return DecodeInput(raw)
}

// EncodeWorkerOutput returns the canonical framed record written as the last worker log line.
// The leading newline guarantees the frame starts a line even after a partial hook write on the
// same stream.
func EncodeWorkerOutput(plan Plan) ([]byte, error) {
	canonical, err := plan.CanonicalJSON()
	if err != nil {
		return nil, err
	}

	encoded := base64.RawStdEncoding.EncodeToString(canonical)

	return []byte("\n" + workerOutputPrefix + encoded + "\n"), nil
}

// EncodeImageWorkerOutput returns the canonical framed image-discovery record.
func EncodeImageWorkerOutput(discovery ImageDiscovery) ([]byte, error) {
	canonical, err := discovery.CanonicalJSON()
	if err != nil {
		return nil, err
	}

	encoded := base64.RawStdEncoding.EncodeToString(canonical)

	return []byte("\n" + imageWorkerOutputPrefix + encoded + "\n"), nil
}

// WorkerFrameKind identifies which framed record type a worker emitted.
type WorkerFrameKind string

// The three framed record types a worker can emit.
const (
	WorkerFramePlan    WorkerFrameKind = "plan"
	WorkerFrameImages  WorkerFrameKind = "images"
	WorkerFrameError   WorkerFrameKind = "error"
	WorkerFrameUnknown WorkerFrameKind = ""
)

// FrameKind returns the record type of the last framed line in raw worker output.
func FrameKind(raw []byte) WorkerFrameKind {
	kind := WorkerFrameUnknown

	for line := range strings.SplitSeq(string(raw), "\n") {
		switch {
		case strings.HasPrefix(line, workerOutputPrefix):
			kind = WorkerFramePlan
		case strings.HasPrefix(line, imageWorkerOutputPrefix):
			kind = WorkerFrameImages
		case strings.HasPrefix(line, workerErrorPrefix):
			kind = WorkerFrameError
		}
	}

	return kind
}

// ExtractWorkerFrame returns the last framed worker record in raw logs, including its prefix
// and trailing newline, so the record can be persisted and later decoded exactly like the
// original stream. Surrounding hook output is discarded.
func ExtractWorkerFrame(raw []byte) ([]byte, bool) {
	frame := ""

	for line := range strings.SplitSeq(string(raw), "\n") {
		for _, prefix := range []string{
			workerOutputPrefix, imageWorkerOutputPrefix, workerErrorPrefix,
		} {
			if strings.HasPrefix(line, prefix) {
				frame = line
			}
		}
	}

	if frame == "" {
		return nil, false
	}

	return []byte(frame + "\n"), true
}

// DecodeWorkerFramePayload returns the decoded payload bytes of the last framed record so a
// caller can screen its content without caring which record type it is.
func DecodeWorkerFramePayload(raw []byte) ([]byte, bool) {
	encoded := ""

	for line := range strings.SplitSeq(string(raw), "\n") {
		for _, prefix := range []string{
			workerOutputPrefix, imageWorkerOutputPrefix, workerErrorPrefix,
		} {
			if value, found := strings.CutPrefix(line, prefix); found {
				encoded = value
			}
		}
	}

	if encoded == "" {
		return nil, false
	}

	decoded, err := base64.RawStdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(decoded) == 0 {
		return nil, false
	}

	return decoded, true
}

// DecodeWorkerError extracts the last bounded, structured preflight diagnostic from failed worker
// logs. Arbitrary hook output and CLI error text are ignored.
func DecodeWorkerError(raw []byte, maxBytes int) (*Error, error) {
	decoded, err := decodeFramedWorkerOutput(raw, workerErrorPrefix, maxBytes, "diagnostic")
	if err != nil {
		return nil, err
	}

	diagnostic, err := decodeStrict[Error](decoded, "worker diagnostic")
	if err != nil {
		return nil, err
	}

	if !validWorkerErrorCode(diagnostic.Code) || diagnostic.Message == "" {
		return nil, planningError(
			ErrorSerialization,
			"worker.output",
			"worker emitted an invalid structured diagnostic",
			nil,
		)
	}

	return &diagnostic, nil
}

// EncodeWorkerError returns the framed structured diagnostic a worker emits on failure.
func EncodeWorkerError(diagnostic Error) ([]byte, error) {
	raw, err := json.Marshal(&diagnostic)
	if err != nil {
		return nil, err
	}

	return []byte("\n" + workerErrorPrefix + base64.RawStdEncoding.EncodeToString(raw) + "\n"), nil
}

func writeWorkerError(output io.Writer, err error) error {
	if output == nil {
		return nil
	}

	diagnostic := &Error{
		Code: ErrorSideEffect, Field: "worker", Behavior: "isolated-worker",
		Message: "isolated worker failed without a structured diagnostic",
	}

	if structured, ok := errors.AsType[*Error](err); ok {
		diagnostic = &Error{
			Code: structured.Code, NodeID: structured.NodeID, Field: structured.Field,
			Behavior: structured.Behavior, Message: structured.Message,
		}
	}

	framed, marshalErr := EncodeWorkerError(*diagnostic)
	if marshalErr != nil {
		return marshalErr
	}

	_, writeErr := output.Write(framed)

	return writeErr
}

func validWorkerErrorCode(code ErrorCode) bool {
	switch code {
	case ErrorInvalidInput, ErrorMissingInput, ErrorUnsupported, ErrorInvariant, ErrorSideEffect,
		ErrorSerialization:
		return true
	default:
		return false
	}
}

// DecodeWorkerOutput extracts and validates the final framed plan from potentially noisy worker
// logs. Hook output cannot be mistaken for a plan merely because it happens to be valid JSON.
func DecodeWorkerOutput(raw []byte, maxPlanBytes int) (Plan, error) {
	decoded, err := decodeFramedWorkerOutput(
		raw,
		workerOutputPrefix,
		maxPlanBytes,
		"plan",
	)
	if err != nil {
		return Plan{}, err
	}

	return DecodePlan(decoded)
}

// DecodeImageWorkerOutput extracts and validates framed image discovery from noisy logs.
func DecodeImageWorkerOutput(raw []byte, maxBytes int) (ImageDiscovery, error) {
	decoded, err := decodeFramedWorkerOutput(
		raw,
		imageWorkerOutputPrefix,
		maxBytes,
		"image discovery",
	)
	if err != nil {
		return ImageDiscovery{}, err
	}

	return DecodeImageDiscovery(decoded)
}

func decodeFramedWorkerOutput(
	raw []byte,
	prefix string,
	maxBytes int,
	document string,
) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, planningError(
			ErrorInvalidInput,
			"worker.maxOutputBytes",
			"maximum output size must be positive",
			nil,
		)
	}

	var encoded string

	maxEncodedBytes := base64.RawStdEncoding.EncodedLen(maxBytes)
	oversizedFrame := false
	// Split rather than bufio.Scan: an unrelated oversized log line from an imported hook must
	// not abort framed-record extraction, and only a matching frame is size-limited.
	for line := range strings.SplitSeq(string(raw), "\n") {
		value, found := strings.CutPrefix(line, prefix)
		if !found {
			continue
		}

		if len(value) > maxEncodedBytes {
			oversizedFrame = true

			continue
		}

		encoded = value
		oversizedFrame = false
	}

	if oversizedFrame {
		return nil, planningError(
			ErrorSerialization,
			"worker.output",
			document+" worker output exceeds its framing limit",
			nil,
		)
	}

	if encoded == "" {
		return nil, planningError(
			ErrorSerialization,
			"worker.output",
			document+" worker emitted no framed output",
			nil,
		)
	}

	decoded, err := base64.RawStdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(decoded) == 0 || len(decoded) > maxBytes {
		return nil, planningError(
			ErrorSerialization,
			"worker.output",
			document+" worker emitted invalid framed output",
			nil,
		)
	}

	return decoded, nil
}
