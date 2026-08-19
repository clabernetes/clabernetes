// Package ocimetadata resolves the execution metadata needed to plan a direct
// device container without downloading its filesystem layers.
//
//nolint:nlreturn,noinlineerr,wsl_v5 // Keep resolver protocol guards compact.
package ocimetadata

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

// Canonical metadata schema and resolver defaults.
const (
	// SchemaVersion identifies the canonical metadata document schema.
	SchemaVersion         = "v1alpha1"
	defaultMaxConfigBytes = 4 << 20
	defaultUserAgent      = "clabernetes-oci-metadata"
)

// ErrorCode is a stable machine-readable class for resolver failures.
type ErrorCode string

// Resolver error codes form the public failure-classification contract.
const (
	// ErrorInvalidRequest classifies invalid references, platforms, and limits.
	ErrorInvalidRequest        ErrorCode = "InvalidRequest"
	ErrorInvalidAuthentication ErrorCode = "InvalidAuthentication"
	ErrorInvalidTrust          ErrorCode = "InvalidTrust"
	ErrorResolveManifest       ErrorCode = "ResolveManifest"
	ErrorUnsupportedMedia      ErrorCode = "UnsupportedMediaType"
	ErrorSelectPlatform        ErrorCode = "SelectPlatform"
	ErrorInspectManifest       ErrorCode = "InspectManifest"
	ErrorConfigTooLarge        ErrorCode = "ConfigTooLarge"
	ErrorFetchConfig           ErrorCode = "FetchConfig"
	ErrorInvalidConfig         ErrorCode = "InvalidConfig"
	ErrorPlatformMismatch      ErrorCode = "PlatformMismatch"
	ErrorCanonicalMetadata     ErrorCode = "CanonicalMetadata"
)

// Error is a stable, machine-classifiable resolver failure. Err contains only a
// redacted diagnostic; the original transport error is deliberately not retained
// because concrete registry errors can carry authentication details.
type Error struct {
	Code      ErrorCode
	Reference string
	Platform  Platform
	Err       error
}

func (e *Error) Error() string {
	message := fmt.Sprintf("OCI metadata %s for %q", e.Code, e.Reference)
	if e.Platform.OS != "" || e.Platform.Architecture != "" {
		message += " (" + e.Platform.String() + ")"
	}
	if e.Err != nil {
		message += ": " + e.Err.Error()
	}
	return message
}

func (e *Error) Unwrap() error {
	return e.Err
}

// Platform identifies one OCI operating-system and architecture variant.
type Platform struct {
	OS           string   `json:"os"`
	Architecture string   `json:"architecture"`
	Variant      string   `json:"variant,omitempty"`
	OSVersion    string   `json:"osVersion,omitempty"`
	OSFeatures   []string `json:"osFeatures,omitempty"`
}

// String returns a normalized OCI-style platform string.
func (p Platform) String() string {
	parts := []string{p.OS, p.Architecture}
	if p.Variant != "" {
		parts = append(parts, p.Variant)
	}
	result := strings.Join(parts, "/")
	if p.OSVersion != "" {
		result += ":" + p.OSVersion
	}
	return result
}

// Request defines the image reference, platform, credentials, and trust policy to resolve.
type Request struct {
	Reference      string
	Platform       Platform
	Authentication *Authentication
	Trust          *RegistryTrust
}

// Resolver fetches only image manifests and configuration metadata.
type Resolver struct {
	Transport      http.RoundTripper
	MaxConfigBytes int64
	UserAgent      string
}

// Metadata is the canonical, integrity-checked result used by direct-runtime planning.
type Metadata struct {
	SchemaVersion     string        `json:"schemaVersion"`
	SourceReference   string        `json:"sourceReference"`
	DigestReference   string        `json:"digestReference"`
	RootDigest        string        `json:"rootDigest"`
	RootMediaType     string        `json:"rootMediaType"`
	ManifestDigest    string        `json:"manifestDigest"`
	ManifestMediaType string        `json:"manifestMediaType"`
	ConfigDigest      string        `json:"configDigest"`
	ConfigMediaType   string        `json:"configMediaType"`
	Platform          Platform      `json:"platform"`
	Config            RuntimeConfig `json:"config"`
}

// KeyValue is a deterministically ordered map entry.
type KeyValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// RuntimeConfig is the normalized, execution-relevant portion of an OCI image
// configuration. Ordered OCI fields remain ordered; map-backed fields become
// sorted slices so serialization is deterministic.
type RuntimeConfig struct {
	AttachStderr    bool         `json:"attachStderr,omitempty"`
	AttachStdin     bool         `json:"attachStdin,omitempty"`
	AttachStdout    bool         `json:"attachStdout,omitempty"`
	ArgsEscaped     bool         `json:"argsEscaped,omitempty"`
	Cmd             []string     `json:"cmd,omitempty"`
	Domainname      string       `json:"domainname,omitempty"`
	Entrypoint      []string     `json:"entrypoint,omitempty"`
	Env             []string     `json:"env,omitempty"`
	ExposedPorts    []string     `json:"exposedPorts,omitempty"`
	Healthcheck     *Healthcheck `json:"healthcheck,omitempty"`
	Hostname        string       `json:"hostname,omitempty"`
	Labels          []KeyValue   `json:"labels,omitempty"`
	MacAddress      string       `json:"macAddress,omitempty"`
	NetworkDisabled bool         `json:"networkDisabled,omitempty"`
	OnBuild         []string     `json:"onBuild,omitempty"`
	OpenStdin       bool         `json:"openStdin,omitempty"`
	Shell           []string     `json:"shell,omitempty"`
	StdinOnce       bool         `json:"stdinOnce,omitempty"`
	StopSignal      string       `json:"stopSignal,omitempty"`
	TTY             bool         `json:"tty,omitempty"`
	User            string       `json:"user,omitempty"`
	Volumes         []string     `json:"volumes,omitempty"`
	WorkingDir      string       `json:"workingDir,omitempty"`
}

// Healthcheck is the normalized OCI healthcheck configuration.
type Healthcheck struct {
	Test        []string      `json:"test,omitempty"`
	Interval    time.Duration `json:"interval,omitempty"`
	Timeout     time.Duration `json:"timeout,omitempty"`
	StartPeriod time.Duration `json:"startPeriod,omitempty"`
	Retries     int           `json:"retries,omitempty"`
}

// CanonicalJSON serializes metadata deterministically for hashing and persistence.
func (m *Metadata) CanonicalJSON() ([]byte, error) {
	encoded, err := json.Marshal(m)
	if err != nil {
		return nil, &Error{
			Code:      ErrorCanonicalMetadata,
			Reference: m.SourceReference,
			Platform:  m.Platform,
			Err:       err,
		}
	}
	return encoded, nil
}

// Resolve obtains canonical execution metadata without fetching filesystem layers.
//
//nolint:err113,funlen,gocritic,gocyclo // ErrorCode is stable; Request is copied.
func (r Resolver) Resolve(ctx context.Context, request Request) (*Metadata, error) {
	request.Reference = strings.TrimSpace(request.Reference)
	request.Platform = normalizePlatform(request.Platform)
	if ctx == nil {
		return nil, resolverError(ErrorInvalidRequest, request, errors.New("context is nil"))
	}
	if request.Reference == "" {
		return nil, resolverError(ErrorInvalidRequest, request, errors.New("reference is empty"))
	}
	if request.Platform.OS == "" || request.Platform.Architecture == "" {
		return nil, resolverError(
			ErrorInvalidRequest,
			request,
			errors.New("platform OS and architecture are required"),
		)
	}
	maxConfigBytes := r.MaxConfigBytes
	if maxConfigBytes == 0 {
		maxConfigBytes = defaultMaxConfigBytes
	}
	if maxConfigBytes < 0 {
		return nil, resolverError(
			ErrorInvalidRequest,
			request,
			errors.New("maximum config size must not be negative"),
		)
	}

	// References parse with Docker semantics: a missing tag selects "latest", matching how
	// containerlab and the kubelet interpret the same value. Only malformed references fail.
	nameOptions := []name.Option{}
	if request.Trust != nil && request.Trust.PlainHTTP {
		nameOptions = append(nameOptions, name.Insecure)
	}
	reference, err := name.ParseReference(request.Reference, nameOptions...)
	if err != nil {
		return nil, resolverError(ErrorInvalidRequest, request, errors.New("reference is invalid"))
	}
	securityCode, securityErr := validateRequestSecurity(
		&request,
		reference.Context().RegistryStr(),
	)
	if securityErr != nil {
		return nil, resolverError(securityCode, request, securityErr)
	}
	transport, err := r.transportForTrust(request.Trust)
	if err != nil {
		return nil, resolverError(ErrorInvalidTrust, request, err)
	}

	platform := v1.Platform{
		OS:           request.Platform.OS,
		Architecture: request.Platform.Architecture,
		Variant:      request.Platform.Variant,
		OSVersion:    request.Platform.OSVersion,
		OSFeatures:   slices.Clone(request.Platform.OSFeatures),
	}
	const remoteOptionCapacity = 5
	options := make([]remote.Option, 0, remoteOptionCapacity)
	options = append(options,
		remote.WithAuth(request.Authentication.authenticator()),
		remote.WithContext(ctx),
		remote.WithPlatform(platform),
		remote.WithUserAgent(defaultIfEmpty(r.UserAgent, defaultUserAgent)),
	)
	options = append(options, remote.WithTransport(transport))

	descriptor, err := remote.Get(reference, options...)
	if err != nil {
		return nil, resolverError(ErrorResolveManifest, request, err)
	}
	if !supportedRootMediaType(descriptor.MediaType) {
		return nil, resolverError(
			ErrorUnsupportedMedia,
			request,
			fmt.Errorf(
				"media type %q is not an OCI/Docker v2 image or index",
				descriptor.MediaType,
			),
		)
	}

	image, err := descriptor.Image()
	if err != nil {
		return nil, resolverError(ErrorSelectPlatform, request, err)
	}
	manifestDigest, err := image.Digest()
	if err != nil {
		return nil, resolverError(
			ErrorInspectManifest,
			request,
			fmt.Errorf("reading manifest digest: %w", err),
		)
	}
	manifestMediaType, err := image.MediaType()
	if err != nil {
		return nil, resolverError(
			ErrorInspectManifest,
			request,
			fmt.Errorf("reading manifest media type: %w", err),
		)
	}
	rawManifest, err := image.RawManifest()
	if err != nil {
		return nil, resolverError(
			ErrorInspectManifest,
			request,
			fmt.Errorf("reading manifest: %w", err),
		)
	}
	manifest, err := v1.ParseManifest(bytes.NewReader(rawManifest))
	if err != nil {
		return nil, resolverError(
			ErrorInspectManifest,
			request,
			fmt.Errorf("decoding manifest: %w", err),
		)
	}

	if manifest.Config.Size < 0 || manifest.Config.Size > maxConfigBytes {
		return nil, resolverError(
			ErrorConfigTooLarge,
			request,
			fmt.Errorf(
				"config descriptor size %d exceeds limit %d",
				manifest.Config.Size,
				maxConfigBytes,
			),
		)
	}

	rawConfig, err := image.RawConfigFile()
	if err != nil {
		return nil, resolverError(ErrorFetchConfig, request, err)
	}
	if int64(len(rawConfig)) != manifest.Config.Size {
		return nil, resolverError(
			ErrorInvalidConfig,
			request,
			fmt.Errorf(
				"config size is %d, descriptor declares %d",
				len(rawConfig),
				manifest.Config.Size,
			),
		)
	}
	configHash, _, err := v1.SHA256(bytes.NewReader(rawConfig))
	if err != nil {
		return nil, resolverError(
			ErrorInvalidConfig,
			request,
			fmt.Errorf("hashing config: %w", err),
		)
	}
	if configHash != manifest.Config.Digest {
		return nil, resolverError(
			ErrorInvalidConfig,
			request,
			fmt.Errorf(
				"config digest is %s, descriptor declares %s",
				configHash,
				manifest.Config.Digest,
			),
		)
	}

	configFile, err := decodeConfig(rawConfig)
	if err != nil {
		return nil, resolverError(ErrorInvalidConfig, request, err)
	}
	actualPlatform := normalizePlatform(Platform{
		OS:           configFile.OS,
		Architecture: configFile.Architecture,
		Variant:      configFile.Variant,
		OSVersion:    configFile.OSVersion,
		OSFeatures:   slices.Clone(configFile.OSFeatures),
	})
	if !platformSatisfies(&actualPlatform, &request.Platform) {
		return nil, resolverError(
			ErrorPlatformMismatch,
			request,
			fmt.Errorf("selected image config declares %s", actualPlatform.String()),
		)
	}

	digestReference := reference.Context().Digest(manifestDigest.String()).Name()
	return &Metadata{
		SchemaVersion: SchemaVersion,
		// The source reference echoes the request verbatim: planning matches resolved metadata
		// back to declared and discovered references by this exact string, so normalization
		// (a defaulted tag, a canonicalized registry host) must not rewrite it.
		SourceReference: strings.TrimSpace(request.Reference),
		DigestReference: digestReference,
		RootDigest:        descriptor.Digest.String(),
		RootMediaType:     string(descriptor.MediaType),
		ManifestDigest:    manifestDigest.String(),
		ManifestMediaType: string(manifestMediaType),
		ConfigDigest:      manifest.Config.Digest.String(),
		ConfigMediaType:   string(manifest.Config.MediaType),
		Platform:          actualPlatform,
		Config:            normalizeConfig(&configFile.Config),
	}, nil
}

//nolint:err113,gocritic // Copy request for a redacted diagnostic snapshot.
func resolverError(code ErrorCode, request Request, err error) *Error {
	if err != nil {
		message := err.Error()
		for _, sensitive := range request.Authentication.sensitiveValues() {
			message = strings.ReplaceAll(message, sensitive, "<redacted>")
		}
		err = errors.New(message)
	}
	return &Error{Code: code, Reference: request.Reference, Platform: request.Platform, Err: err}
}

func supportedRootMediaType(mediaType types.MediaType) bool {
	// Unknown and non-image media types are rejected by default.
	//nolint:exhaustive
	switch mediaType {
	case types.OCIManifestSchema1,
		types.DockerManifestSchema2,
		types.OCIImageIndex,
		types.DockerManifestList:
		return true
	default:
		return false
	}
}

//nolint:err113 // Resolve wraps decoder details in the stable InvalidConfig error code.
func decodeConfig(raw []byte) (*v1.ConfigFile, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	config := &v1.ConfigFile{}
	if err := decoder.Decode(config); err != nil {
		return nil, fmt.Errorf("decoding config: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("config contains trailing JSON values")
		}
		return nil, fmt.Errorf("decoding config trailer: %w", err)
	}
	return config, nil
}

//nolint:gocritic // Normalization intentionally returns a detached platform value.
func normalizePlatform(platform Platform) Platform {
	platform.OS = strings.ToLower(strings.TrimSpace(platform.OS))
	platform.Architecture = strings.ToLower(strings.TrimSpace(platform.Architecture))
	platform.Variant = strings.ToLower(strings.TrimSpace(platform.Variant))
	platform.OSVersion = strings.TrimSpace(platform.OSVersion)
	platform.OSFeatures = sortedUnique(platform.OSFeatures)
	return platform
}

func platformSatisfies(actual, requested *Platform) bool {
	return v1.Platform{
		OS:           actual.OS,
		Architecture: actual.Architecture,
		Variant:      actual.Variant,
		OSVersion:    actual.OSVersion,
		OSFeatures:   slices.Clone(actual.OSFeatures),
	}.Satisfies(v1.Platform{
		OS:           requested.OS,
		Architecture: requested.Architecture,
		Variant:      requested.Variant,
		OSVersion:    requested.OSVersion,
		OSFeatures:   slices.Clone(requested.OSFeatures),
	})
}

func normalizeConfig(config *v1.Config) RuntimeConfig {
	normalized := RuntimeConfig{
		AttachStderr:    config.AttachStderr,
		AttachStdin:     config.AttachStdin,
		AttachStdout:    config.AttachStdout,
		ArgsEscaped:     config.ArgsEscaped,
		Cmd:             slices.Clone(config.Cmd),
		Domainname:      config.Domainname,
		Entrypoint:      slices.Clone(config.Entrypoint),
		Env:             slices.Clone(config.Env),
		ExposedPorts:    sortedKeys(config.ExposedPorts),
		Hostname:        config.Hostname,
		Labels:          sortedLabels(config.Labels),
		MacAddress:      config.MacAddress,
		NetworkDisabled: config.NetworkDisabled,
		OnBuild:         slices.Clone(config.OnBuild),
		OpenStdin:       config.OpenStdin,
		Shell:           slices.Clone(config.Shell),
		StdinOnce:       config.StdinOnce,
		StopSignal:      config.StopSignal,
		TTY:             config.Tty,
		User:            config.User,
		Volumes:         sortedKeys(config.Volumes),
		WorkingDir:      config.WorkingDir,
	}
	if config.Healthcheck != nil {
		normalized.Healthcheck = &Healthcheck{
			Test:        slices.Clone(config.Healthcheck.Test),
			Interval:    config.Healthcheck.Interval,
			Timeout:     config.Healthcheck.Timeout,
			StartPeriod: config.Healthcheck.StartPeriod,
			Retries:     config.Healthcheck.Retries,
		}
	}
	return normalized
}

func sortedLabels(values map[string]string) []KeyValue {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)

	result := make([]KeyValue, 0, len(keys))
	for _, key := range keys {
		result = append(result, KeyValue{Name: key, Value: values[key]})
	}
	return result
}

func sortedKeys[T any](values map[string]T) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	slices.Sort(result)
	return result
}

func sortedUnique(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	slices.Sort(result)
	return result
}

func defaultIfEmpty(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
