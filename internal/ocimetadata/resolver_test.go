//nolint:nlreturn,noinlineerr,testpackage,wsl_v5 // Internal protocol tests inspect transport and normalization details.
package ocimetadata

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

//nolint:gocyclo // One end-to-end assertion set verifies the complete metadata contract.
func TestResolveImageMetadataWithoutFetchingLayers(t *testing.T) {
	t.Parallel()

	testRegistry := newTestRegistry(t)
	image, layerDigest := newTestImage(t, Platform{
		OS: "linux", Architecture: "amd64", OSFeatures: []string{"feature-b", "feature-a"},
	}, "amd64")
	reference := testRegistry.writeImage(t, "devices/router:latest", image)
	configDigest, err := image.ConfigName()
	if err != nil {
		t.Fatal(err)
	}
	testRegistry.recorder.reset()

	metadata, err := (Resolver{Transport: testRegistry.transport}).Resolve(
		context.Background(),
		Request{
			Reference: reference.Name(),
			Platform: Platform{
				OS:           " Linux ",
				Architecture: "AMD64",
				OSFeatures:   []string{"feature-b", "feature-a", "feature-a"},
			},
			Trust: testRegistry.plainHTTPTrust(),
		},
	)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if got, want := metadata.SchemaVersion, SchemaVersion; got != want {
		t.Errorf("schema version = %q, want %q", got, want)
	}
	if !strings.Contains(metadata.DigestReference, "@sha256:") {
		t.Errorf("digest reference = %q, want immutable sha256 reference", metadata.DigestReference)
	}
	if got, want := metadata.Platform, (Platform{OS: "linux", Architecture: "amd64", OSFeatures: []string{"feature-a", "feature-b"}}); !platformEqual(
		got,
		want,
	) {
		t.Errorf("platform = %#v, want %#v", got, want)
	}
	if got, want := metadata.Config.Cmd, []string{"serve", "amd64"}; !slicesEqual(got, want) {
		t.Errorf("command = %#v, want %#v", got, want)
	}
	if got, want := metadata.Config.Labels, []KeyValue{{Name: "a", Value: "first"}, {Name: "z", Value: "last"}}; !keyValuesEqual(
		got,
		want,
	) {
		t.Errorf("labels = %#v, want %#v", got, want)
	}
	if got, want := metadata.Config.ExposedPorts, []string{"22/tcp", "57400/tcp"}; !slicesEqual(
		got,
		want,
	) {
		t.Errorf("exposed ports = %#v, want %#v", got, want)
	}
	if metadata.Config.Healthcheck == nil || metadata.Config.Healthcheck.Interval != 5*time.Second {
		t.Errorf("healthcheck = %#v, want five-second interval", metadata.Config.Healthcheck)
	}

	requestedPaths := strings.Join(testRegistry.recorder.paths(), "\n")
	if strings.Contains(requestedPaths, layerDigest.String()) {
		t.Fatalf("resolver fetched device layer %s; requests:\n%s", layerDigest, requestedPaths)
	}
	if !strings.Contains(requestedPaths, configDigest.String()) {
		t.Fatalf("resolver did not fetch config %s; requests:\n%s", configDigest, requestedPaths)
	}

	first, err := metadata.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	second, err := metadata.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("canonical metadata is unstable:\n%s\n%s", first, second)
	}
}

// A reference without a tag selects "latest", matching containerlab and kubelet semantics.
func TestResolveDefaultsMissingTagToLatest(t *testing.T) {
	t.Parallel()

	testRegistry := newTestRegistry(t)
	image, _ := newTestImage(t, Platform{OS: "linux", Architecture: "amd64"}, "amd64")
	tagged := testRegistry.writeImage(t, "devices/tagless:latest", image)

	metadata, err := (Resolver{Transport: testRegistry.transport}).Resolve(
		context.Background(),
		Request{
			Reference: tagged.Context().Name(),
			Platform:  Platform{OS: "linux", Architecture: "amd64"},
			Trust:     testRegistry.plainHTTPTrust(),
		},
	)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if !strings.Contains(metadata.DigestReference, "@sha256:") {
		t.Errorf("digest reference = %q, want immutable sha256 reference", metadata.DigestReference)
	}
	if metadata.SourceReference != tagged.Context().Name() {
		t.Errorf(
			"source reference = %q, want the requested reference %q echoed verbatim",
			metadata.SourceReference,
			tagged.Context().Name(),
		)
	}

	if _, err = AuthenticationFromPullSecrets(tagged.Context().Name(), nil); err != nil {
		t.Errorf("AuthenticationFromPullSecrets() tagless reference error = %v", err)
	}
}

func TestResolveSelectsRequestedIndexPlatform(t *testing.T) {
	t.Parallel()

	testRegistry := newTestRegistry(t)
	amd64Image, _ := newTestImage(t, Platform{OS: "linux", Architecture: "amd64"}, "amd64")
	arm64Image, arm64Layer := newTestImage(
		t,
		Platform{OS: "linux", Architecture: "arm64", Variant: "v8"},
		"arm64",
	)
	index := mutate.AppendManifests(empty.Index,
		mutate.IndexAddendum{
			Add: amd64Image,
			Descriptor: v1.Descriptor{Platform: &v1.Platform{
				OS: "linux", Architecture: "amd64",
			}},
		},
		mutate.IndexAddendum{
			Add: arm64Image,
			Descriptor: v1.Descriptor{Platform: &v1.Platform{
				OS: "linux", Architecture: "arm64", Variant: "v8",
			}},
		},
	)
	reference := testRegistry.writeIndex(t, "devices/router:multi", index)
	testRegistry.recorder.reset()

	metadata, err := (Resolver{Transport: testRegistry.transport}).Resolve(
		context.Background(),
		Request{
			Reference: reference.Name(),
			Platform:  Platform{OS: "linux", Architecture: "arm64", Variant: "v8"},
			Trust:     testRegistry.plainHTTPTrust(),
		},
	)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if got, want := metadata.Config.Cmd, []string{"serve", "arm64"}; !slicesEqual(got, want) {
		t.Errorf("command = %#v, want %#v", got, want)
	}
	if metadata.RootDigest == metadata.ManifestDigest {
		t.Errorf(
			"index root digest %q unexpectedly equals selected manifest digest",
			metadata.RootDigest,
		)
	}
	if got, want := metadata.RootMediaType, string(types.OCIImageIndex); got != want {
		t.Errorf("root media type = %q, want %q", got, want)
	}
	if requestedPaths := strings.Join(testRegistry.recorder.paths(), "\n"); strings.Contains(
		requestedPaths,
		arm64Layer.String(),
	) {
		t.Fatalf(
			"resolver fetched selected platform layer %s; requests:\n%s",
			arm64Layer,
			requestedPaths,
		)
	}
}

func TestResolveRejectsOversizedConfigBeforeFetchingIt(t *testing.T) {
	t.Parallel()

	testRegistry := newTestRegistry(t)
	image, _ := newTestImage(t, Platform{OS: "linux", Architecture: "amd64"}, "amd64")
	reference := testRegistry.writeImage(t, "devices/router:large", image)
	configDigest, err := image.ConfigName()
	if err != nil {
		t.Fatal(err)
	}
	testRegistry.recorder.reset()

	_, err = (Resolver{Transport: testRegistry.transport, MaxConfigBytes: 1}).Resolve(
		context.Background(),
		Request{
			Reference: reference.Name(),
			Platform:  Platform{OS: "linux", Architecture: "amd64"},
			Trust:     testRegistry.plainHTTPTrust(),
		},
	)
	assertErrorCode(t, err, ErrorConfigTooLarge)

	if requestedPaths := strings.Join(testRegistry.recorder.paths(), "\n"); strings.Contains(
		requestedPaths,
		configDigest.String(),
	) {
		t.Fatalf("oversized config %s was fetched; requests:\n%s", configDigest, requestedPaths)
	}
}

func TestResolveRejectsSingleImagePlatformMismatch(t *testing.T) {
	t.Parallel()

	testRegistry := newTestRegistry(t)
	image, _ := newTestImage(t, Platform{OS: "linux", Architecture: "amd64"}, "amd64")
	reference := testRegistry.writeImage(t, "devices/router:amd64", image)

	_, err := (Resolver{Transport: testRegistry.transport}).Resolve(context.Background(), Request{
		Reference: reference.Name(),
		Platform:  Platform{OS: "linux", Architecture: "arm64"},
		Trust:     testRegistry.plainHTTPTrust(),
	})
	assertErrorCode(t, err, ErrorPlatformMismatch)
}

//nolint:tparallel // Table cases are intentionally serialized around shared test construction.
func TestResolveRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		context context.Context
		request Request
	}{
		{
			name: "nil context",
			request: Request{
				Reference: "example.com/device:latest",
				Platform:  Platform{OS: "linux", Architecture: "amd64"},
			},
		},
		{
			name:    "empty reference",
			context: context.Background(),
			request: Request{Platform: Platform{OS: "linux", Architecture: "amd64"}},
		},
		{
			name:    "missing OS",
			context: context.Background(),
			request: Request{
				Reference: "example.com/device:latest",
				Platform:  Platform{Architecture: "amd64"},
			},
		},
		{
			name:    "bad reference",
			context: context.Background(),
			request: Request{
				Reference: "bad reference",
				Platform:  Platform{OS: "linux", Architecture: "amd64"},
			},
		},
		{
			name:    "negative limit",
			context: context.Background(),
			request: Request{
				Reference: "example.com/device:latest",
				Platform:  Platform{OS: "linux", Architecture: "amd64"},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolver := Resolver{}
			if test.name == "negative limit" {
				resolver.MaxConfigBytes = -1
			}
			_, err := resolver.Resolve(test.context, test.request)
			assertErrorCode(t, err, ErrorInvalidRequest)
		})
	}
}

func TestResolveUsesMatchingKubernetesPullSecret(t *testing.T) {
	t.Parallel()

	testRegistry := newTestRegistry(t)
	image, _ := newTestImage(t, Platform{OS: "linux", Architecture: "amd64"}, "private")
	reference := testRegistry.writeImage(t, "devices/private:latest", image)
	registryHost := reference.Context().RegistryStr()
	username := "device-user"
	password := "super-secret"

	authentication, err := AuthenticationFromPullSecrets(reference.Name(), []k8scorev1.Secret{
		dockerConfigSecret(t, "lab", "unrelated", "registry.example.test", "other", "other-secret"),
		dockerConfigSecret(t, "lab", "device-pull", registryHost, username, password),
	})
	if err != nil {
		t.Fatal(err)
	}
	if authentication == nil {
		t.Fatal("AuthenticationFromPullSecrets() returned anonymous authentication")
	}
	if got, want := authentication.Source(), (SecretSource{Namespace: "lab", Name: "device-pull"}); got != want {
		t.Fatalf("authentication source = %#v, want %#v", got, want)
	}

	testRegistry.recorder.protect(
		"Basic "+base64.StdEncoding.EncodeToString([]byte(username+":"+password)),
		"denied",
	)
	metadata, err := (Resolver{Transport: testRegistry.transport}).Resolve(
		context.Background(),
		Request{
			Reference:      reference.Name(),
			Platform:       Platform{OS: "linux", Architecture: "amd64"},
			Authentication: authentication,
			Trust:          testRegistry.plainHTTPTrust(),
		},
	)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if metadata.Config.Cmd[1] != "private" {
		t.Fatalf("resolved command = %#v", metadata.Config.Cmd)
	}
}

func TestResolveRedactsPrivateRegistryDiagnostics(t *testing.T) {
	t.Parallel()

	testRegistry := newTestRegistry(t)
	image, _ := newTestImage(t, Platform{OS: "linux", Architecture: "amd64"}, "private")
	reference := testRegistry.writeImage(t, "devices/private:redaction", image)
	username := "private-user"
	password := "leak-me-not"
	authentication, err := AuthenticationFromPullSecrets(reference.Name(), []k8scorev1.Secret{
		dockerConfigSecret(
			t,
			"lab",
			"wrong",
			reference.Context().RegistryStr(),
			username,
			password,
		),
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	testRegistry.recorder.protect(
		"Basic expected",
		strings.Join([]string{username, password, encoded}, " "),
	)

	_, err = (Resolver{Transport: testRegistry.transport}).Resolve(context.Background(), Request{
		Reference:      reference.Name(),
		Platform:       Platform{OS: "linux", Architecture: "amd64"},
		Authentication: authentication,
		Trust:          testRegistry.plainHTTPTrust(),
	})
	assertErrorCode(t, err, ErrorResolveManifest)
	for _, secretValue := range []string{username, password, encoded} {
		if strings.Contains(err.Error(), secretValue) {
			t.Errorf("resolver error leaked credential %q: %v", secretValue, err)
		}
	}
	if !strings.Contains(err.Error(), "<redacted>") {
		t.Errorf("resolver error did not show redaction marker: %v", err)
	}
}

func TestAuthenticationFromPullSecretsSupportsLegacyFormatAndRedactsParseErrors(t *testing.T) {
	t.Parallel()

	reference := "private.example.test/device:latest"
	legacyAuth, err := json.Marshal(map[string]any{
		"private.example.test": map[string]string{
			"username": "legacy-user",
			"password": "legacy-password",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	authentication, err := AuthenticationFromPullSecrets(reference, []k8scorev1.Secret{{
		ObjectMeta: metav1.ObjectMeta{Namespace: "lab", Name: "legacy"},
		Type:       k8scorev1.SecretTypeDockercfg,
		Data:       map[string][]byte{k8scorev1.DockerConfigKey: legacyAuth},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if authentication == nil || authentication.config.Username != "legacy-user" {
		t.Fatalf("legacy authentication = %#v", authentication)
	}

	_, err = AuthenticationFromPullSecrets(reference, []k8scorev1.Secret{{
		ObjectMeta: metav1.ObjectMeta{Namespace: "lab", Name: "malformed"},
		Type:       k8scorev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{
			k8scorev1.DockerConfigJsonKey: []byte(`{"auths":"raw-secret-value"`),
		},
	}})
	assertErrorCode(t, err, ErrorInvalidAuthentication)
	if strings.Contains(err.Error(), "raw-secret-value") {
		t.Fatalf("pull Secret parse error leaked data: %v", err)
	}
}

func TestResolveUsesExplicitRegistryCABundle(t *testing.T) {
	t.Parallel()

	testRegistry := newTLSTestRegistry(t)
	image, _ := newTestImage(t, Platform{OS: "linux", Architecture: "amd64"}, "tls")
	reference := testRegistry.writeImage(t, "devices/tls:latest", image)
	caBundle := pem.EncodeToMemory(
		&pem.Block{Type: "CERTIFICATE", Bytes: testRegistry.server.Certificate().Raw},
	)

	metadata, err := (Resolver{}).Resolve(context.Background(), Request{
		Reference: reference.Name(),
		Platform:  Platform{OS: "linux", Architecture: "amd64"},
		Trust: &RegistryTrust{
			Registry: reference.Context().RegistryStr(),
			CABundle: caBundle,
		},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if metadata.Config.Cmd[1] != "tls" {
		t.Fatalf("resolved command = %#v", metadata.Config.Cmd)
	}
}

func TestRegistryTrustPolicySelectsOnlyExactRegistry(t *testing.T) {
	t.Parallel()

	tlsRegistry := newTLSTestRegistry(t)
	tlsAuthority := strings.TrimPrefix(tlsRegistry.server.URL, "https://")
	caBundle := pem.EncodeToMemory(
		&pem.Block{Type: "CERTIFICATE", Bytes: tlsRegistry.server.Certificate().Raw},
	)
	policy, err := NewRegistryTrustPolicy([]RegistryTrust{
		{Registry: tlsAuthority, CABundle: caBundle},
		{Registry: "plain.example.test:5000", PlainHTTP: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	tlsTrust := policy.ForReference(tlsAuthority + "/devices/router:1")
	if tlsTrust == nil || tlsTrust.Registry != tlsAuthority ||
		!bytes.Equal(tlsTrust.CABundle, caBundle) || tlsTrust.PlainHTTP {
		t.Fatalf("TLS registry trust = %#v", tlsTrust)
	}
	plainTrust := policy.ForReference("plain.example.test:5000/devices/router:1")
	if plainTrust == nil || !plainTrust.PlainHTTP || len(plainTrust.CABundle) != 0 {
		t.Fatalf("plain HTTP registry trust = %#v", plainTrust)
	}
	if trust := policy.ForReference("sub.plain.example.test:5000/devices/router:1"); trust != nil {
		t.Fatalf("trust policy matched a different registry: %#v", trust)
	}

	// Callers cannot mutate the indexed CA bundle through a selected entry.
	tlsTrust.CABundle[0] ^= 0xff
	if next := policy.ForReference(tlsAuthority + "/devices/router:1"); next == nil ||
		!bytes.Equal(next.CABundle, caBundle) {
		t.Fatalf("trust policy retained caller mutation: %#v", next)
	}
}

func TestRegistryTrustPolicyRejectsAmbiguousOrInvalidEntries(t *testing.T) {
	t.Parallel()

	tlsRegistry := newTLSTestRegistry(t)
	caBundle := pem.EncodeToMemory(
		&pem.Block{Type: "CERTIFICATE", Bytes: tlsRegistry.server.Certificate().Raw},
	)
	for _, entries := range [][]RegistryTrust{
		{{Registry: "https://registry.example.test", PlainHTTP: true}},
		{{Registry: "registry.example.test/repository", PlainHTTP: true}},
		{{Registry: "registry.example.test"}},
		{{Registry: "registry.example.test", CABundle: []byte("not PEM")}},
		{{Registry: "registry.example.test", CABundle: caBundle, PlainHTTP: true}},
		{
			{Registry: "docker.io", CABundle: caBundle},
			{Registry: "index.docker.io", CABundle: caBundle},
		},
	} {
		if _, err := NewRegistryTrustPolicy(entries); err == nil {
			t.Fatalf("NewRegistryTrustPolicy() accepted %#v", entries)
		}
	}
}

func TestResolveRejectsMismatchedTrustAndAuthenticationScopes(t *testing.T) {
	t.Parallel()

	testRegistry := newTestRegistry(t)
	image, _ := newTestImage(t, Platform{OS: "linux", Architecture: "amd64"}, "scoped")
	reference := testRegistry.writeImage(t, "devices/scoped:latest", image)
	authentication, err := AuthenticationFromPullSecrets(
		"other.example.test/device:latest",
		[]k8scorev1.Secret{
			dockerConfigSecret(t, "lab", "other", "other.example.test", "user", "password"),
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = (Resolver{Transport: testRegistry.transport}).Resolve(context.Background(), Request{
		Reference:      reference.Name(),
		Platform:       Platform{OS: "linux", Architecture: "amd64"},
		Authentication: authentication,
		Trust:          testRegistry.plainHTTPTrust(),
	})
	assertErrorCode(t, err, ErrorInvalidAuthentication)

	_, err = (Resolver{Transport: testRegistry.transport}).Resolve(context.Background(), Request{
		Reference: reference.Name(),
		Platform:  Platform{OS: "linux", Architecture: "amd64"},
		Trust:     &RegistryTrust{Registry: "other.example.test", PlainHTTP: true},
	})
	assertErrorCode(t, err, ErrorInvalidTrust)

	_, err = (Resolver{Transport: testRegistry.transport}).Resolve(context.Background(), Request{
		Reference: reference.Name(),
		Platform:  Platform{OS: "linux", Architecture: "amd64"},
		Trust: &RegistryTrust{
			Registry: reference.Context().RegistryStr(),
			CABundle: []byte("not a certificate"),
		},
	})
	assertErrorCode(t, err, ErrorInvalidTrust)
}

type testRegistry struct {
	server    *httptest.Server
	transport http.RoundTripper
	recorder  *recordingHandler
}

func newTestRegistry(t *testing.T) *testRegistry {
	t.Helper()

	recorder := &recordingHandler{next: registry.New(registry.Logger(log.New(io.Discard, "", 0)))}
	server := httptest.NewServer(recorder)
	t.Cleanup(server.Close)
	return &testRegistry{
		server:    server,
		transport: server.Client().Transport,
		recorder:  recorder,
	}
}

func newTLSTestRegistry(t *testing.T) *testRegistry {
	t.Helper()

	recorder := &recordingHandler{next: registry.New(registry.Logger(log.New(io.Discard, "", 0)))}
	server := httptest.NewTLSServer(recorder)
	t.Cleanup(server.Close)
	return &testRegistry{
		server:    server,
		transport: server.Client().Transport,
		recorder:  recorder,
	}
}

func (r *testRegistry) writeImage(t *testing.T, path string, image v1.Image) name.Tag {
	t.Helper()

	nameOptions := []name.Option{name.StrictValidation}
	if strings.HasPrefix(r.server.URL, "http://") {
		nameOptions = append(nameOptions, name.Insecure)
	}
	reference, err := name.NewTag(
		strings.TrimPrefix(strings.TrimPrefix(r.server.URL, "http://"), "https://")+"/"+path,
		nameOptions...)
	if err != nil {
		t.Fatal(err)
	}
	if err = remote.Write(reference, image, remote.WithTransport(r.transport)); err != nil {
		t.Fatal(err)
	}
	return reference
}

func (r *testRegistry) writeIndex(t *testing.T, path string, index v1.ImageIndex) name.Tag {
	t.Helper()

	nameOptions := []name.Option{name.StrictValidation}
	if strings.HasPrefix(r.server.URL, "http://") {
		nameOptions = append(nameOptions, name.Insecure)
	}
	reference, err := name.NewTag(
		strings.TrimPrefix(strings.TrimPrefix(r.server.URL, "http://"), "https://")+"/"+path,
		nameOptions...)
	if err != nil {
		t.Fatal(err)
	}
	if err = remote.WriteIndex(reference, index, remote.WithTransport(r.transport)); err != nil {
		t.Fatal(err)
	}
	return reference
}

func (r *testRegistry) plainHTTPTrust() *RegistryTrust {
	return &RegistryTrust{
		Registry:  strings.TrimPrefix(r.server.URL, "http://"),
		PlainHTTP: true,
	}
}

func newTestImage(t *testing.T, platform Platform, marker string) (v1.Image, v1.Hash) {
	t.Helper()

	config := &v1.ConfigFile{
		Architecture: platform.Architecture,
		OS:           platform.OS,
		Variant:      platform.Variant,
		OSVersion:    platform.OSVersion,
		OSFeatures:   platform.OSFeatures,
		Config: v1.Config{
			Entrypoint:   []string{"/device"},
			Cmd:          []string{"serve", marker},
			Env:          []string{"PATH=/bin", "DEVICE=" + marker},
			ExposedPorts: map[string]struct{}{"57400/tcp": {}, "22/tcp": {}},
			Healthcheck: &v1.HealthConfig{
				Test:     []string{"CMD", "/health"},
				Interval: 5 * time.Second,
				Timeout:  time.Second,
				Retries:  3,
			},
			Labels:     map[string]string{"z": "last", "a": "first"},
			StopSignal: "SIGTERM",
			User:       "1000:1000",
			Volumes:    map[string]struct{}{"/config": {}},
			WorkingDir: "/work",
		},
	}
	image, err := mutate.ConfigFile(empty.Image, config)
	if err != nil {
		t.Fatal(err)
	}
	layer := static.NewLayer([]byte("device-layer-"+marker), types.OCILayer)
	image, err = mutate.AppendLayers(image, layer)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := layer.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return image, digest
}

type recordingHandler struct {
	next http.Handler

	mutex                 sync.Mutex
	requested             []string
	requiredAuthorization string
	unauthorizedBody      string
}

func (h *recordingHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	h.mutex.Lock()
	h.requested = append(h.requested, request.Method+" "+request.URL.Path)
	requiredAuthorization := h.requiredAuthorization
	unauthorizedBody := h.unauthorizedBody
	h.mutex.Unlock()
	if requiredAuthorization != "" && request.Header.Get("Authorization") != requiredAuthorization {
		writer.Header().Set("WWW-Authenticate", `Basic realm="test-registry"`)
		writer.WriteHeader(http.StatusUnauthorized)
		// The body is test-owned diagnostic text, not rendered browser content.
		//nolint:gosec
		_, _ = fmt.Fprint(
			writer,
			unauthorizedBody,
		)
		return
	}
	h.next.ServeHTTP(writer, request)
}

func (h *recordingHandler) protect(requiredAuthorization, unauthorizedBody string) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.requiredAuthorization = requiredAuthorization
	h.unauthorizedBody = unauthorizedBody
}

func (h *recordingHandler) reset() {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.requested = nil
}

func (h *recordingHandler) paths() []string {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	return append([]string(nil), h.requested...)
}

func assertErrorCode(t *testing.T, err error, want ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want code %q", want)
	}
	var resolverErr *Error
	if !errors.As(err, &resolverErr) {
		t.Fatalf("error = %T %v, want *Error", err, err)
	}
	if resolverErr.Code != want {
		t.Fatalf("error code = %q, want %q (error: %v)", resolverErr.Code, want, err)
	}
}

func platformEqual(left, right Platform) bool {
	return left.OS == right.OS &&
		left.Architecture == right.Architecture &&
		left.Variant == right.Variant &&
		left.OSVersion == right.OSVersion &&
		slicesEqual(left.OSFeatures, right.OSFeatures)
}

func slicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func keyValuesEqual(left, right []KeyValue) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func dockerConfigSecret(
	t *testing.T,
	namespace, secretName, registryHost, username, password string,
) k8scorev1.Secret {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"auths": map[string]any{
			registryHost: map[string]string{
				"username": username,
				"password": password,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return k8scorev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: secretName},
		Type:       k8scorev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{k8scorev1.DockerConfigJsonKey: raw},
	}
}
