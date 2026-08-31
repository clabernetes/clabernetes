//nolint:nlreturn,noinlineerr,testpackage,wsl_v5 // Internal protocol tests inspect transport rewrites.
package ocimetadata

import (
	"context"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	k8scorev1 "k8s.io/api/core/v1"
)

// The origin registry is deliberately unresolvable: a broken mirror rewrite fails the request
// instead of silently reaching the public registry.
const mirrorTestOrigin = "origin.example.test"

func (r *testRegistry) host() string {
	return strings.TrimPrefix(strings.TrimPrefix(r.server.URL, "http://"), "https://")
}

func TestResolveFollowsPathRewriteMirror(t *testing.T) {
	t.Parallel()

	mirrorRegistry := newTestRegistry(t)
	image, layerDigest := newTestImage(t, Platform{OS: "linux", Architecture: "amd64"}, "mirrored")
	// The mirror stores the source registry's content under its own path prefix, exactly like a
	// Harbor proxy project: /v2/ghcr/<repo>/... exists, /v2/<repo>/... does not.
	mirrorRegistry.writeImage(t, "ghcr/srl-labs/network-multitool:latest", image)
	username := "mirror-user"
	password := "mirror-secret"
	authentication, err := AuthenticationFromPullSecrets(
		mirrorTestOrigin+"/srl-labs/network-multitool:latest",
		[]k8scorev1.Secret{
			dockerConfigSecret(t, "lab", "pull", mirrorTestOrigin, username, password),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	mirrorRegistry.recorder.protect(
		"Basic "+base64.StdEncoding.EncodeToString([]byte(username+":"+password)),
		"denied",
	)
	mirrorRegistry.recorder.reset()

	metadata, err := (Resolver{Transport: mirrorRegistry.transport}).Resolve(
		context.Background(),
		Request{
			Reference:      mirrorTestOrigin + "/srl-labs/network-multitool:latest",
			Platform:       Platform{OS: "linux", Architecture: "amd64"},
			Authentication: authentication,
			Mirror: &RegistryMirror{
				Registry:     mirrorTestOrigin,
				Endpoint:     "http://" + mirrorRegistry.host() + "/v2/ghcr",
				OverridePath: true,
			},
		},
	)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if got, want := metadata.SourceReference, mirrorTestOrigin+"/srl-labs/network-multitool:latest"; got != want {
		t.Errorf("source reference = %q, want the public reference %q", got, want)
	}
	if !strings.HasPrefix(
		metadata.DigestReference,
		mirrorTestOrigin+"/srl-labs/network-multitool@sha256:",
	) {
		t.Errorf(
			"digest reference = %q, want the public repository identity",
			metadata.DigestReference,
		)
	}

	requested := mirrorRegistry.recorder.paths()
	requestedPaths := strings.Join(requested, "\n")
	if !strings.Contains(
		requestedPaths,
		"GET /v2/ghcr/srl-labs/network-multitool/manifests/latest",
	) {
		t.Errorf("mirror did not receive the path-rewritten manifest request:\n%s", requestedPaths)
	}
	for _, path := range requested {
		if strings.Contains(path, "/v2/srl-labs/") {
			t.Errorf("request used the un-rewritten repository path: %s", path)
		}
	}
	// The API version check probes the mirror server's own /v2/ root, not a repository path.
	if !strings.Contains(requestedPaths, "GET /v2/\n") &&
		!strings.HasSuffix(requestedPaths, "GET /v2/") {
		t.Errorf("mirror did not receive the /v2/ version check:\n%s", requestedPaths)
	}
	if strings.Contains(requestedPaths, layerDigest.String()) {
		t.Errorf("resolver fetched a layer through the mirror:\n%s", requestedPaths)
	}
}

// Docker Hub aliasing: a short docker.io reference resolves via index.docker.io and must map onto
// the mirror's Docker Hub path prefix.
func TestResolveFollowsDockerHubAliasMirror(t *testing.T) {
	t.Parallel()

	mirrorRegistry := newTestRegistry(t)
	image, _ := newTestImage(t, Platform{OS: "linux", Architecture: "amd64"}, "hub")
	mirrorRegistry.writeImage(t, "docker/grafana/alloy:v1.16.0", image)
	mirrorRegistry.recorder.reset()

	metadata, err := (Resolver{Transport: mirrorRegistry.transport}).Resolve(
		context.Background(),
		Request{
			Reference: "grafana/alloy:v1.16.0",
			Platform:  Platform{OS: "linux", Architecture: "amd64"},
			Mirror: &RegistryMirror{
				Registry:     "docker.io",
				Endpoint:     "http://" + mirrorRegistry.host() + "/v2/docker",
				OverridePath: true,
			},
		},
	)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got, want := metadata.SourceReference, "grafana/alloy:v1.16.0"; got != want {
		t.Errorf("source reference = %q, want %q", got, want)
	}

	requestedPaths := strings.Join(mirrorRegistry.recorder.paths(), "\n")
	if !strings.Contains(requestedPaths, "GET /v2/docker/grafana/alloy/manifests/v1.16.0") {
		t.Errorf("mirror did not receive the aliased Docker Hub request:\n%s", requestedPaths)
	}
}

// A registryMetadataTrust CA entry for the mirror host applies while fetching a reference from a
// completely different source registry through that mirror.
func TestResolveAppliesMirrorEndpointTrust(t *testing.T) {
	t.Parallel()

	mirrorRegistry := newTLSTestRegistry(t)
	image, _ := newTestImage(t, Platform{OS: "linux", Architecture: "amd64"}, "mirror-tls")
	mirrorRegistry.writeImage(t, "ghcr/devices/router:latest", image)
	caBundle := pem.EncodeToMemory(
		&pem.Block{Type: "CERTIFICATE", Bytes: mirrorRegistry.server.Certificate().Raw},
	)

	metadata, err := (Resolver{}).Resolve(context.Background(), Request{
		Reference: mirrorTestOrigin + "/devices/router:latest",
		Platform:  Platform{OS: "linux", Architecture: "amd64"},
		Trust: &RegistryTrust{
			Registry: mirrorRegistry.host(),
			CABundle: caBundle,
		},
		Mirror: &RegistryMirror{
			Registry:     mirrorTestOrigin,
			Endpoint:     "https://" + mirrorRegistry.host() + "/v2/ghcr",
			OverridePath: true,
		},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if metadata.Config.Cmd[1] != "mirror-tls" {
		t.Fatalf("resolved command = %#v", metadata.Config.Cmd)
	}
}

// harborLikeHandler reproduces Harbor's proxy-project auth behavior: tokens come from its own
// realm, access is granted only for the mirror-prefixed repository scope, and a request carrying
// a wrong-scope token gets a challenge without a corrected scope, so a client cannot recover by
// following challenges. The resolver must therefore request the prefixed scope up front.
type harborLikeHandler struct {
	next http.Handler

	mutex         sync.Mutex
	realm         string
	requiredScope string
	enforcing     bool
	issuedScopes  []string
}

func (h *harborLikeHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	h.mutex.Lock()
	realm := h.realm
	requiredScope := h.requiredScope
	enforcing := h.enforcing
	if request.URL.Path == "/token" {
		h.issuedScopes = append(h.issuedScopes, request.URL.Query().Get("scope"))
	}
	h.mutex.Unlock()
	if !enforcing {
		h.next.ServeHTTP(writer, request)
		return
	}
	if request.URL.Path == "/token" {
		token := "limited"
		if request.URL.Query().Get("scope") == requiredScope {
			token = "full-access"
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(writer, `{"token":%q}`, token)
		return
	}
	if request.URL.Path == "/v2/" && request.Header.Get("Authorization") == "" {
		writer.Header().
			Set("WWW-Authenticate", `Bearer realm="`+realm+`",service="test-harbor"`)
		writer.WriteHeader(http.StatusUnauthorized)
		return
	}
	if request.Header.Get("Authorization") != "Bearer full-access" &&
		request.URL.Path != "/v2/" {
		// Like Harbor, the wrong-scope dead end carries no usable Bearer challenge.
		writer.Header().Set("WWW-Authenticate", `Basic realm="test-harbor"`)
		writer.WriteHeader(http.StatusUnauthorized)
		return
	}
	h.next.ServeHTTP(writer, request)
}

func (h *harborLikeHandler) enforce(realm string) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.realm = realm
	h.enforcing = true
}

func (h *harborLikeHandler) scopes() []string {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	return append([]string(nil), h.issuedScopes...)
}

func TestResolveRequestsMirrorPrefixedTokenScope(t *testing.T) {
	t.Parallel()

	handler := &harborLikeHandler{
		next:          registry.New(registry.Logger(log.New(io.Discard, "", 0))),
		requiredScope: "repository:ghcr/srl-labs/network-multitool:pull",
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	host := strings.TrimPrefix(server.URL, "http://")

	image, _ := newTestImage(t, Platform{OS: "linux", Architecture: "amd64"}, "token-scoped")
	writeReference, err := name.NewTag(
		host+"/ghcr/srl-labs/network-multitool:latest",
		name.StrictValidation, name.Insecure,
	)
	if err != nil {
		t.Fatal(err)
	}
	// Seed the registry before arming auth enforcement.
	if err = remote.Write(
		writeReference, image, remote.WithTransport(server.Client().Transport),
	); err != nil {
		t.Fatal(err)
	}
	// A hostname realm: the client's SSRF guard rejects private/loopback IP-literal realms.
	handler.enforce("http://localhost:" + strings.Split(host, ":")[1] + "/token")

	metadata, err := (Resolver{Transport: server.Client().Transport}).Resolve(
		context.Background(),
		Request{
			Reference: mirrorTestOrigin + "/srl-labs/network-multitool:latest",
			Platform:  Platform{OS: "linux", Architecture: "amd64"},
			Mirror: &RegistryMirror{
				Registry:     mirrorTestOrigin,
				Endpoint:     "http://" + host + "/v2/ghcr",
				OverridePath: true,
			},
		},
	)
	if err != nil {
		t.Fatalf("Resolve() error = %v (issued token scopes: %v)", err, handler.scopes())
	}
	if metadata.Config.Cmd[1] != "token-scoped" {
		t.Fatalf("resolved command = %#v", metadata.Config.Cmd)
	}
	for _, scope := range handler.scopes() {
		if strings.Contains(scope, "repository:srl-labs/") {
			t.Errorf("token exchange used the un-prefixed origin scope %q", scope)
		}
	}
}

func TestResolveRejectsInconsistentMirrorRequests(t *testing.T) {
	t.Parallel()

	mirrorHost := "harbor.example.test"
	tests := []struct {
		name   string
		trust  *RegistryTrust
		mirror *RegistryMirror
		want   ErrorCode
	}{
		{
			name:  "trust scoped to the source registry instead of the mirror endpoint",
			trust: &RegistryTrust{Registry: mirrorTestOrigin, PlainHTTP: true},
			mirror: &RegistryMirror{
				Registry: mirrorTestOrigin, Endpoint: "http://" + mirrorHost + "/v2/ghcr",
				OverridePath: true,
			},
			want: ErrorInvalidTrust,
		},
		{
			name:  "plain-HTTP trust with an HTTPS endpoint",
			trust: &RegistryTrust{Registry: mirrorHost, PlainHTTP: true},
			mirror: &RegistryMirror{
				Registry: mirrorTestOrigin, Endpoint: "https://" + mirrorHost + "/v2/ghcr",
				OverridePath: true,
			},
			want: ErrorInvalidTrust,
		},
		{
			name:  "CA bundle trust with a plain-HTTP endpoint",
			trust: &RegistryTrust{Registry: mirrorHost, CABundle: []byte("pem")},
			mirror: &RegistryMirror{
				Registry: mirrorTestOrigin, Endpoint: "http://" + mirrorHost + "/v2/ghcr",
				OverridePath: true,
			},
			want: ErrorInvalidTrust,
		},
		{
			name: "mirror scoped to a different source registry",
			mirror: &RegistryMirror{
				Registry: "other.example.test", Endpoint: "http://" + mirrorHost + "/v2/ghcr",
				OverridePath: true,
			},
			want: ErrorInvalidMirror,
		},
		{
			name: "endpoint path without overridePath",
			mirror: &RegistryMirror{
				Registry: mirrorTestOrigin, Endpoint: "http://" + mirrorHost + "/v2/ghcr",
			},
			want: ErrorInvalidMirror,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := (Resolver{}).Resolve(context.Background(), Request{
				Reference: mirrorTestOrigin + "/devices/router:latest",
				Platform:  Platform{OS: "linux", Architecture: "amd64"},
				Trust:     test.trust,
				Mirror:    test.mirror,
			})
			assertErrorCode(t, err, test.want)
		})
	}
}

func TestNewRegistryMirrorPolicyRejectsAmbiguousOrInvalidEntries(t *testing.T) {
	t.Parallel()

	for _, entries := range [][]RegistryMirror{
		{{Registry: "https://ghcr.io", Endpoint: "https://harbor.example.test"}},
		{{Registry: "ghcr.io/repository", Endpoint: "https://harbor.example.test"}},
		{{Registry: "ghcr.io", Endpoint: "harbor.example.test"}},
		{{Registry: "ghcr.io", Endpoint: "ftp://harbor.example.test"}},
		{{Registry: "ghcr.io", Endpoint: "https://harbor.example.test/v2/ghcr"}},
		{{Registry: "ghcr.io", Endpoint: "https://harbor.example.test", OverridePath: true}},
		{{Registry: "ghcr.io", Endpoint: "https://harbor.example.test/v2/ghcr?x=1", OverridePath: true}},
		{{Registry: "ghcr.io", Endpoint: "https://user@harbor.example.test/v2/ghcr", OverridePath: true}},
		{{Registry: "ghcr.io", Endpoint: "https://harbor.example.test/v2/../ghcr", OverridePath: true}},
		{
			{Registry: "docker.io", Endpoint: "https://harbor.example.test/v2/docker", OverridePath: true},
			{Registry: "index.docker.io", Endpoint: "https://harbor.example.test/v2/hub", OverridePath: true},
		},
	} {
		if _, err := NewRegistryMirrorPolicy(entries); err == nil {
			t.Fatalf("NewRegistryMirrorPolicy() accepted %#v", entries)
		}
	}
}

func TestRegistryMirrorPolicySelectsByNormalizedRegistry(t *testing.T) {
	t.Parallel()

	policy, err := NewRegistryMirrorPolicy([]RegistryMirror{
		{
			Registry:     "docker.io",
			Endpoint:     "https://harbor.example.test/v2/docker",
			OverridePath: true,
		},
		{Registry: "ghcr.io", Endpoint: "http://harbor.example.test:8080"},
	})
	if err != nil {
		t.Fatal(err)
	}

	hub := policy.ForReference("grafana/alloy:v1.16.0")
	if hub == nil || hub.Endpoint != "https://harbor.example.test/v2/docker" || !hub.OverridePath {
		t.Fatalf("Docker Hub alias selected mirror %#v", hub)
	}
	ghcr := policy.ForReference("ghcr.io/srl-labs/network-multitool")
	if ghcr == nil || ghcr.EndpointHost() != "harbor.example.test:8080" || ghcr.OverridePath {
		t.Fatalf("ghcr.io selected mirror %#v", ghcr)
	}
	if unmatched := policy.ForReference("quay.io/devices/router:1"); unmatched != nil {
		t.Fatalf("unmatched registry selected mirror %#v", unmatched)
	}
}

func TestMirrorAPIRootFollowsEndpointPath(t *testing.T) {
	t.Parallel()

	for endpoint, want := range map[string]string{
		"https://harbor.example.test/v2/ghcr":    "/v2/",
		"https://example.test/harbor/v2/ghcr":    "/harbor/v2/",
		"https://harbor.example.test/proxy/ghcr": "/v2/",
	} {
		target, err := (&RegistryMirror{
			Registry: "ghcr.io", Endpoint: endpoint, OverridePath: true,
		}).compile()
		if err != nil {
			t.Fatal(err)
		}
		if target.apiRoot != want {
			t.Errorf("endpoint %q API root = %q, want %q", endpoint, target.apiRoot, want)
		}
	}
}

func TestRequestCacheKeyDistinguishesMirrors(t *testing.T) {
	t.Parallel()

	base := Request{
		Reference: "ghcr.io/devices/router:1",
		Platform:  Platform{OS: "linux", Architecture: "amd64"},
	}
	mirrored := base
	mirrored.Mirror = &RegistryMirror{
		Registry: "ghcr.io", Endpoint: "https://harbor.example.test/v2/ghcr", OverridePath: true,
	}
	otherEndpoint := base
	otherEndpoint.Mirror = &RegistryMirror{
		Registry: "ghcr.io", Endpoint: "https://harbor.example.test/v2/other", OverridePath: true,
	}
	if requestCacheKey(&base) == requestCacheKey(&mirrored) {
		t.Fatal("cache key ignores the mirror")
	}
	if requestCacheKey(&mirrored) == requestCacheKey(&otherEndpoint) {
		t.Fatal("cache key ignores the mirror endpoint")
	}
}
