//nolint:err113,funlen,gocognit,gocyclo // Bounded interactive worker protocol boundary.
package deviceplan

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
)

const (
	// SessionProtocolVersion identifies the full-duplex planner protocol.
	SessionProtocolVersion  = "v1alpha1"
	sessionFramePrefix      = "C9S_DEVICE_SESSION_V1:"
	sessionCachePrefix      = "C9S_DEVICE_SESSION_CACHE_V1:"
	defaultMaxSessionBytes  = 8 << 20
	defaultMaxSessionRounds = 8
	sessionScannerBuffer    = 64 << 10
	sessionDirectoryMode    = 0o700
	sessionFileMode         = 0o600
)

// SessionFrameType identifies one message in an attached planner conversation.
type SessionFrameType string

// Planner session frame types.
const (
	SessionFrameInitial            SessionFrameType = "initial"
	SessionFrameImageRequest       SessionFrameType = "imageRequest"
	SessionFrameCertificateRequest SessionFrameType = "certificateRequest"
	SessionFrameResponse           SessionFrameType = "response"
	SessionFrameResult             SessionFrameType = "result"
)

// SessionFrame is the only data exchanged over the planner's attached stdin/stdout stream.
// CertificateMaterial is accepted only in controller-to-worker response frames and must never
// appear in worker output or persisted results.
type SessionFrame struct {
	Version             string                   `json:"version"`
	Type                SessionFrameType         `json:"type"`
	SessionDigest       string                   `json:"sessionDigest"`
	Sequence            int                      `json:"sequence"`
	Input               *Input                   `json:"input,omitempty"`
	Images              []ImageRequirement       `json:"images,omitempty"`
	ImageInputs         []ImageInput             `json:"imageInputs,omitempty"`
	Certificates        []CertificateRequirement `json:"certificates,omitempty"`
	CertificateInputs   []CertificateInput       `json:"certificateInputs,omitempty"`
	CertificateMaterial map[string][]byte        `json:"certificateMaterial,omitempty"`
	CertificateSecret   string                   `json:"certificateSecret,omitempty"`
	Result              *SessionResult           `json:"result,omitempty"`
}

// SessionResult is the complete cacheable outcome of one planner conversation.
type SessionResult struct {
	SessionDigest     string                   `json:"-"`
	TerminalDigest    string                   `json:"-"`
	Input             Input                    `json:"input"`
	Plan              Plan                     `json:"plan"`
	Certificates      []CertificateRequirement `json:"certificates,omitempty"`
	CertificateSecret string                   `json:"certificateSecret,omitempty"`
}

// SessionTerminalDigest binds one terminal result to its initial session and sequence.
func SessionTerminalDigest(frame SessionFrame) (string, error) {
	if err := validateSessionFrame(frame); err != nil {
		return "", err
	}
	if frame.Type != SessionFrameResult || frame.Result == nil {
		return "", invalidSessionShape(frame.Type)
	}
	raw, err := json.Marshal(struct {
		SessionDigest string         `json:"sessionDigest"`
		Sequence      int            `json:"sequence"`
		Result        *SessionResult `json:"result"`
	}{
		SessionDigest: frame.SessionDigest, Sequence: frame.Sequence, Result: frame.Result,
	})
	if err != nil {
		return "", err
	}

	return Digest(raw), nil
}

// CachedSessionResult is the compact durable result. Its finalized input remains in the existing
// content-addressed input ConfigMap instead of being duplicated into the output ConfigMap.
type CachedSessionResult struct {
	SessionDigest     string                   `json:"sessionDigest"`
	InputDigest       string                   `json:"inputDigest"`
	Plan              Plan                     `json:"plan"`
	Certificates      []CertificateRequirement `json:"certificates,omitempty"`
	CertificateSecret string                   `json:"certificateSecret,omitempty"`
}

// EncodeCachedSessionResult returns a compact framed result suitable for a ConfigMap.
func EncodeCachedSessionResult(result SessionResult) ([]byte, error) {
	inputDigest, err := result.Input.Digest()
	if err != nil {
		return nil, err
	}
	cached := CachedSessionResult{
		SessionDigest: result.SessionDigest, InputDigest: inputDigest, Plan: result.Plan,
		Certificates: result.Certificates, CertificateSecret: result.CertificateSecret,
	}
	if _, err = normalizeCachedSessionResult(cached); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(cached)
	if err != nil {
		return nil, err
	}

	return []byte("\n" + sessionCachePrefix +
		base64.RawStdEncoding.EncodeToString(raw) + "\n"), nil
}

// DecodeCachedSessionResult validates a persisted compact session result.
func DecodeCachedSessionResult(raw []byte, maxBytes int) (CachedSessionResult, error) {
	decoded, err := decodeFramedWorkerOutput(
		raw,
		sessionCachePrefix,
		maxBytes,
		"cached session",
	)
	if err != nil {
		return CachedSessionResult{}, err
	}
	cached, err := decodeStrict[CachedSessionResult](decoded, "cached session")
	if err != nil {
		return CachedSessionResult{}, err
	}

	return normalizeCachedSessionResult(cached)
}

func normalizeCachedSessionResult(
	result CachedSessionResult,
) (CachedSessionResult, error) {
	if !validDigest(result.SessionDigest) || !validDigest(result.InputDigest) {
		return CachedSessionResult{}, planningError(
			ErrorInvalidInput,
			"session.cache",
			"cached session input identities are invalid",
			nil,
		)
	}
	plan, err := NormalizePlan(result.Plan)
	if err != nil {
		return CachedSessionResult{}, err
	}
	if plan.InputDigest != result.InputDigest {
		return CachedSessionResult{}, planningError(
			ErrorInvariant,
			"session.cache",
			"cached plan differs from its finalized input",
			nil,
		)
	}
	certificates, err := NormalizeCertificateRequirements(result.Certificates)
	if err != nil {
		return CachedSessionResult{}, err
	}
	if (len(certificates) != 0) != (result.CertificateSecret != "") {
		return CachedSessionResult{}, planningError(
			ErrorInvariant,
			"session.cache.certificates",
			"cached certificate requirements and Secret identity differ",
			nil,
		)
	}
	result.Plan = plan
	result.Certificates = certificates

	return result, nil
}

// ValidateSessionExtension verifies that negotiation only added image or certificate facts.
func ValidateSessionExtension(initial, final Input) error {
	initial, err := NormalizeInput(initial)
	if err != nil {
		return err
	}
	final, err = NormalizeInput(final)
	if err != nil {
		return err
	}
	initialBase := initial
	initialBase.Images = nil
	initialBase.Certificates = nil
	finalBase := final
	finalBase.Images = nil
	finalBase.Certificates = nil
	if !reflect.DeepEqual(initialBase, finalBase) {
		return planningError(
			ErrorInvariant,
			"session.result.input",
			"planner changed immutable initial input",
			nil,
		)
	}
	for _, initialImage := range initial.Images {
		found := false
		for _, finalImage := range final.Images {
			if reflect.DeepEqual(initialImage, finalImage) {
				found = true

				break
			}
		}
		if !found {
			return planningError(
				ErrorInvariant,
				"session.result.images",
				"planner changed or removed initial image metadata",
				nil,
			)
		}
	}
	for _, initialCertificate := range initial.Certificates {
		if !slices.Contains(final.Certificates, initialCertificate) {
			return planningError(
				ErrorInvariant,
				"session.result.certificates",
				"planner changed or removed initial certificate metadata",
				nil,
			)
		}
	}

	return nil
}

// DecodeSessionResult extracts and validates the terminal session frame from noisy worker logs.
func DecodeSessionResult(raw []byte, maxBytes int) (SessionResult, error) {
	decoded, err := decodeFramedWorkerOutput(raw, sessionFramePrefix, maxBytes, "session")
	if err != nil {
		return SessionResult{}, err
	}
	frame, err := decodeStrict[SessionFrame](decoded, "session result")
	if err != nil {
		return SessionResult{}, err
	}
	if err = validateSessionFrame(frame); err != nil {
		return SessionResult{}, err
	}
	if frame.Type != SessionFrameResult || frame.Result == nil ||
		len(frame.CertificateMaterial) != 0 {
		return SessionResult{}, planningError(
			ErrorSerialization,
			"session.result",
			"worker emitted no valid terminal session result",
			nil,
		)
	}
	normalizedInput, err := NormalizeInput(frame.Result.Input)
	if err != nil {
		return SessionResult{}, err
	}
	normalizedPlan, err := NormalizePlan(frame.Result.Plan)
	if err != nil {
		return SessionResult{}, err
	}
	inputDigest, err := normalizedInput.Digest()
	if err != nil {
		return SessionResult{}, err
	}
	if normalizedPlan.InputDigest != inputDigest {
		return SessionResult{}, planningError(
			ErrorInvariant,
			"session.result",
			"terminal plan differs from its finalized input",
			nil,
		)
	}
	certificates, err := NormalizeCertificateRequirements(frame.Result.Certificates)
	if err != nil {
		return SessionResult{}, err
	}
	if (len(certificates) != 0) != (frame.Result.CertificateSecret != "") {
		return SessionResult{}, planningError(
			ErrorInvariant,
			"session.result.certificates",
			"certificate requirements and Secret identity must be supplied together",
			nil,
		)
	}
	terminalDigest, err := SessionTerminalDigest(frame)
	if err != nil {
		return SessionResult{}, err
	}

	return SessionResult{
		SessionDigest:  frame.SessionDigest,
		TerminalDigest: terminalDigest,
		Input:          normalizedInput, Plan: normalizedPlan, Certificates: certificates,
		CertificateSecret: frame.Result.CertificateSecret,
	}, nil
}

// SessionFrameDecoder incrementally extracts protocol frames while ignoring imported stdout.
type SessionFrameDecoder struct {
	scanner    *bufio.Scanner
	diagnostic *Error
}

// NewSessionFrameDecoder returns a bounded decoder for an attached worker stream.
func NewSessionFrameDecoder(input io.Reader, maxBytes int) *SessionFrameDecoder {
	if maxBytes == 0 {
		maxBytes = defaultMaxSessionBytes
	}
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, sessionScannerBuffer), maxBytes)

	return &SessionFrameDecoder{scanner: scanner}
}

// Diagnostic returns a structured worker failure encountered before the stream ended.
func (d *SessionFrameDecoder) Diagnostic() *Error { return d.diagnostic }

// Next returns the next valid prefixed protocol frame.
func (d *SessionFrameDecoder) Next() (SessionFrame, error) {
	if d == nil || d.scanner == nil {
		return SessionFrame{}, errors.New("session frame decoder is required")
	}
	for d.scanner.Scan() {
		line := d.scanner.Text()
		if FrameKind([]byte(line)) == WorkerFrameError {
			diagnostic, err := DecodeWorkerError([]byte(line), defaultMaxSessionBytes)
			if err != nil {
				return SessionFrame{}, err
			}
			d.diagnostic = diagnostic

			continue
		}
		if !strings.HasPrefix(line, sessionFramePrefix) {
			continue
		}
		raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(line, sessionFramePrefix))
		if err != nil {
			return SessionFrame{}, planningError(
				ErrorSerialization, "session.frame", "cannot decode planner session frame", err,
			)
		}
		frame := SessionFrame{}
		if err = json.Unmarshal(raw, &frame); err != nil {
			return SessionFrame{}, planningError(
				ErrorSerialization, "session.frame", "cannot parse planner session frame", err,
			)
		}
		if err = validateSessionFrame(frame); err != nil {
			return SessionFrame{}, err
		}

		return frame, nil
	}
	if err := d.scanner.Err(); err != nil {
		return SessionFrame{}, planningError(
			ErrorSerialization, "session.frame", "cannot read planner session frame", err,
		)
	}

	return SessionFrame{}, io.EOF
}

// WriteSessionFrame writes one canonical, line-delimited protocol frame.
func WriteSessionFrame(output io.Writer, frame SessionFrame) error {
	if output == nil {
		return errors.New("session frame output is required")
	}
	if err := validateSessionFrame(frame); err != nil {
		return err
	}
	raw, err := json.Marshal(frame)
	if err != nil {
		return planningError(
			ErrorSerialization, "session.frame", "cannot serialize planner session frame", err,
		)
	}
	_, err = fmt.Fprintf(output, "\n%s%s\n", sessionFramePrefix,
		base64.RawStdEncoding.EncodeToString(raw))
	if err != nil {
		return planningError(
			ErrorSerialization, "session.frame", "cannot write planner session frame", err,
		)
	}

	return nil
}

func validateSessionFrame(frame SessionFrame) error {
	if frame.Version != SessionProtocolVersion || !validDigest(frame.SessionDigest) ||
		frame.Sequence < 0 {
		return planningError(
			ErrorInvalidInput,
			"session.frame",
			"session version, digest, and sequence are required",
			nil,
		)
	}
	switch frame.Type {
	case SessionFrameInitial:
		if frame.Sequence != 0 || frame.Input == nil || len(frame.Images) != 0 ||
			len(frame.ImageInputs) != 0 || len(frame.Certificates) != 0 ||
			len(frame.CertificateInputs) != 0 || len(frame.CertificateMaterial) != 0 ||
			frame.CertificateSecret != "" || frame.Result != nil {
			return invalidSessionShape(frame.Type)
		}
	case SessionFrameImageRequest:
		if frame.Sequence == 0 || len(frame.Images) == 0 || frame.Input != nil ||
			len(frame.ImageInputs) != 0 || len(frame.Certificates) != 0 ||
			len(frame.CertificateInputs) != 0 || len(frame.CertificateMaterial) != 0 ||
			frame.CertificateSecret != "" || frame.Result != nil {
			return invalidSessionShape(frame.Type)
		}
	case SessionFrameCertificateRequest:
		if frame.Sequence == 0 || len(frame.Certificates) == 0 || frame.Input != nil ||
			len(frame.Images) != 0 || len(frame.ImageInputs) != 0 ||
			len(frame.CertificateInputs) != 0 || len(frame.CertificateMaterial) != 0 ||
			frame.CertificateSecret != "" || frame.Result != nil {
			return invalidSessionShape(frame.Type)
		}
	case SessionFrameResponse:
		if frame.Sequence == 0 ||
			(len(frame.ImageInputs) == 0 && len(frame.CertificateInputs) == 0) ||
			(len(frame.ImageInputs) != 0 && len(frame.CertificateInputs) != 0) ||
			frame.Input != nil || len(frame.Images) != 0 || len(frame.Certificates) != 0 ||
			(len(frame.ImageInputs) != 0 && len(frame.CertificateMaterial) != 0) ||
			(len(frame.ImageInputs) != 0 && frame.CertificateSecret != "") ||
			(len(frame.CertificateInputs) != 0 && frame.CertificateSecret == "") ||
			frame.Result != nil {
			return invalidSessionShape(frame.Type)
		}
	case SessionFrameResult:
		if frame.Sequence == 0 || frame.Result == nil || frame.Input != nil ||
			len(frame.Images) != 0 || len(frame.ImageInputs) != 0 ||
			len(frame.Certificates) != 0 || len(frame.CertificateInputs) != 0 ||
			len(frame.CertificateMaterial) != 0 || frame.CertificateSecret != "" {
			return invalidSessionShape(frame.Type)
		}
	default:
		return planningError(
			ErrorInvalidInput, "session.frame.type", "session frame type is unsupported", nil,
		)
	}

	return nil
}

func invalidSessionShape(frameType SessionFrameType) error {
	return planningError(
		ErrorInvalidInput,
		"session.frame",
		fmt.Sprintf("session %s frame has an invalid payload", frameType),
		nil,
	)
}

// SessionWorker runs discovery and planning as one bounded conversation in one process.
type SessionWorker struct {
	Adapter       Adapter
	Input         io.Reader
	Output        io.Writer
	MaxFrameBytes int
	MaxRounds     int
}

// Run requests only facts absent from the current normalized input, then emits one complete plan.
func (w SessionWorker) Run(ctx context.Context) (runErr error) {
	defer func() {
		if runErr != nil {
			_ = writeWorkerError(w.Output, runErr)
		}
	}()
	if ctx == nil || w.Input == nil || w.Output == nil {
		return planningError(
			ErrorMissingInput, "session.stream", "context and session streams are required", nil,
		)
	}
	decoder := NewSessionFrameDecoder(w.Input, w.MaxFrameBytes)
	initial, err := decoder.Next()
	if err != nil {
		return err
	}
	if initial.Type != SessionFrameInitial {
		return invalidSessionShape(initial.Type)
	}
	current, err := NormalizeInput(*initial.Input)
	if err != nil {
		return err
	}
	initialDigest, err := current.Digest()
	if err != nil {
		return err
	}
	if initialDigest != initial.SessionDigest {
		return planningError(
			ErrorInvalidInput, "session.digest", "initial input differs from session identity", nil,
		)
	}
	maxRounds := w.MaxRounds
	if maxRounds == 0 {
		maxRounds = defaultMaxSessionRounds
	}
	if maxRounds < 1 {
		return planningError(
			ErrorInvalidInput, "session.rounds", "session round limit must be positive", nil,
		)
	}
	sequence := 1
	seenRequests := map[string]bool{}
	var certificateRequirements []CertificateRequirement
	certificateSecret := ""
	for range maxRounds {
		discovery, discoverErr := w.Adapter.DiscoverImages(ctx, current)
		if discoverErr != nil {
			return discoverErr
		}
		missingImages := missingImageRequirements(discovery.Images, current.Images)
		if len(missingImages) != 0 {
			request := SessionFrame{
				Version: SessionProtocolVersion, Type: SessionFrameImageRequest,
				SessionDigest: initialDigest, Sequence: sequence, Images: missingImages,
			}
			if err = guardSessionProgress(request, seenRequests); err != nil {
				return err
			}
			response, responseErr := w.exchange(request, decoder)
			if responseErr != nil {
				return responseErr
			}
			current.Images, err = mergeSessionImages(current.Images, missingImages,
				response.ImageInputs)
			if err != nil {
				return err
			}
			current, err = NormalizeInput(current)
			if err != nil {
				return err
			}
			sequence++

			continue
		}
		certificateRequirements = discovery.Certificates
		if len(certificateRequirements) != 0 &&
			!certificateInputsCover(certificateRequirements, current.Certificates) {
			request := SessionFrame{
				Version: SessionProtocolVersion, Type: SessionFrameCertificateRequest,
				SessionDigest: initialDigest, Sequence: sequence,
				Certificates: certificateRequirements,
			}
			if err = guardSessionProgress(request, seenRequests); err != nil {
				return err
			}
			response, responseErr := w.exchange(request, decoder)
			if responseErr != nil {
				return responseErr
			}
			if err = materializeSessionCertificates(
				w.Adapter.CertificateRoot,
				certificateRequirements,
				response.CertificateInputs,
				response.CertificateMaterial,
			); err != nil {
				return err
			}
			current.Certificates = slices.Clone(response.CertificateInputs)
			certificateSecret = response.CertificateSecret
			current, err = NormalizeInput(current)
			if err != nil {
				return err
			}
			sequence++

			continue
		}
		plan, planErr := w.Adapter.Plan(ctx, current)
		if planErr != nil {
			return planErr
		}

		return WriteSessionFrame(w.Output, SessionFrame{
			Version: SessionProtocolVersion, Type: SessionFrameResult,
			SessionDigest: initialDigest, Sequence: sequence,
			Result: &SessionResult{
				Input: current, Plan: *plan, Certificates: certificateRequirements,
				CertificateSecret: certificateSecret,
			},
		})
	}

	return planningError(
		ErrorUnsupported,
		"session.rounds",
		"planner requests did not converge within the bounded session rounds",
		nil,
	)
}

func (w SessionWorker) exchange(
	request SessionFrame,
	decoder *SessionFrameDecoder,
) (SessionFrame, error) {
	if err := WriteSessionFrame(w.Output, request); err != nil {
		return SessionFrame{}, err
	}
	response, err := decoder.Next()
	if err != nil {
		return SessionFrame{}, err
	}
	if response.Type != SessionFrameResponse ||
		response.SessionDigest != request.SessionDigest ||
		response.Sequence != request.Sequence {
		return SessionFrame{}, planningError(
			ErrorInvalidInput,
			"session.response",
			"planner response identity differs from its request",
			nil,
		)
	}

	return response, nil
}

func guardSessionProgress(frame SessionFrame, seen map[string]bool) error {
	duplicate := frame
	duplicate.Sequence = 0
	raw, err := json.Marshal(duplicate)
	if err != nil {
		return err
	}
	fingerprint := Digest(raw)
	if seen[fingerprint] {
		return planningError(
			ErrorUnsupported,
			"session.progress",
			"planner repeated a request without adding new facts",
			nil,
		)
	}
	seen[fingerprint] = true

	return nil
}

func missingImageRequirements(
	requirements []ImageRequirement,
	inputs []ImageInput,
) []ImageRequirement {
	result := []ImageRequirement{}
	for _, requirement := range requirements {
		found := false
		for _, input := range inputs {
			if input.NodeID == requirement.NodeID &&
				(input.SourceReference == requirement.SourceReference ||
					input.DigestReference == requirement.SourceReference) {
				found = true

				break
			}
		}
		if !found {
			result = append(result, requirement)
		}
	}

	return result
}

func mergeSessionImages(
	current []ImageInput,
	requested []ImageRequirement,
	response []ImageInput,
) ([]ImageInput, error) {
	for _, requirement := range requested {
		found := false
		for _, input := range response {
			if input.NodeID == requirement.NodeID &&
				input.SourceReference == requirement.SourceReference {
				found = true

				break
			}
		}
		if !found {
			return nil, planningError(
				ErrorMissingInput,
				"session.response.images",
				"image response does not satisfy every requested reference",
				nil,
			)
		}
	}
	for _, input := range response {
		found := false
		for _, requirement := range requested {
			if input.NodeID == requirement.NodeID &&
				input.SourceReference == requirement.SourceReference {
				found = true

				break
			}
		}
		if !found {
			return nil, planningError(
				ErrorInvalidInput,
				"session.response.images",
				"image response contains an unrequested reference",
				nil,
			)
		}
	}
	merged := append(slices.Clone(current), response...)

	return merged, nil
}

func certificateInputsCover(
	requirements []CertificateRequirement,
	inputs []CertificateInput,
) bool {
	for _, requirement := range requirements {
		found := false
		for _, input := range inputs {
			if input.NodeID == requirement.NodeID && input.StorageName == requirement.StorageName {
				found = true

				break
			}
		}
		if !found {
			return false
		}
	}

	return len(requirements) == len(inputs)
}

func materializeSessionCertificates(
	root string,
	requirements []CertificateRequirement,
	inputs []CertificateInput,
	material map[string][]byte,
) error {
	if !filepath.IsAbs(root) || filepath.Clean(root) == string(filepath.Separator) ||
		!certificateInputsCover(requirements, inputs) {
		return planningError(
			ErrorInvalidInput,
			"session.response.certificates",
			"certificate response identity is incomplete",
			nil,
		)
	}
	expected := map[string]string{}
	for _, input := range inputs {
		certificateKey, privateKeyKey := CertificateMaterialKeys(input.NodeID, input.StorageName)
		expected[certificateKey] = input.CertificateDigest
		expected[privateKeyKey] = input.PrivateKeyDigest
		expected[CertificateCACertKey] = input.CACertificateDigest
		expected[CertificateCAKeyKey] = input.CAPrivateKeyDigest
	}
	if len(material) != len(expected) {
		return planningError(
			ErrorInvalidInput,
			"session.response.certificateMaterial",
			"certificate response contains an unexpected material set",
			nil,
		)
	}
	if err := os.MkdirAll(root, sessionDirectoryMode); err != nil {
		return planningError(
			ErrorSideEffect, "session.certificates", "cannot create certificate workspace", err,
		)
	}
	for name, digest := range expected {
		value := material[name]
		if len(value) == 0 || Digest(value) != digest {
			return planningError(
				ErrorInvalidInput,
				"session.response.certificateMaterial",
				"certificate response bytes differ from their accepted digest",
				nil,
			)
		}
		if err := os.WriteFile(filepath.Join(root, name), value, sessionFileMode); err != nil {
			return planningError(
				ErrorSideEffect, "session.certificates", "cannot write certificate material", err,
			)
		}
	}

	return nil
}
