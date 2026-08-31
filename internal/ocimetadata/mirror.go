//nolint:nlreturn,noinlineerr,wsl_v5 // Keep mirror guards compact.
package ocimetadata

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
)

// RegistryMirror redirects controller-only OCI metadata requests for one source registry to a
// mirror endpoint. Only the HTTP hop is rewritten: the parsed reference, the reported source and
// digest identities, and Pod image strings keep the original registry. There is no origin
// fallback; a failing mirror fails the request.
type RegistryMirror struct {
	// Registry is the source registry whose metadata requests are redirected.
	Registry string
	// Endpoint is the mirror URL: scheme, host, and an optional registry API path prefix.
	Endpoint string
	// OverridePath treats the endpoint path as the mirror's registry API root for the source
	// registry, replacing the standard /v2 prefix on rewritten request paths (containerd
	// hosts.toml override_path semantics). Required whenever the endpoint carries a path.
	OverridePath bool
}

// EndpointHost returns the mirror connection authority (host[:port]) or "" for an endpoint that
// does not parse; compile then reports the precise validation failure.
func (m *RegistryMirror) EndpointHost() string {
	if m == nil {
		return ""
	}
	parsed, err := url.Parse(strings.TrimSpace(m.Endpoint))
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Host)
}

// standardAPIRoot is the distribution registry API version root.
const standardAPIRoot = "/v2/"

// registryMirrorTarget is a validated, normalized mirror ready to rewrite requests.
type registryMirrorTarget struct {
	registry     string
	scheme       string
	host         string
	pathPrefix   string
	apiRoot      string
	repoPrefix   string
	overridePath bool
}

//nolint:err113 // Callers wrap validation details in their stable diagnostics.
func (m *RegistryMirror) compile() (*registryMirrorTarget, error) {
	registry, err := name.NewRegistry(strings.TrimSpace(m.Registry), name.StrictValidation)
	if err != nil {
		return nil, fmt.Errorf("mirror for %q has an invalid source registry authority", m.Registry)
	}
	endpoint, err := url.Parse(strings.TrimSpace(m.Endpoint))
	if err != nil || endpoint.Host == "" || endpoint.Opaque != "" {
		return nil, fmt.Errorf("mirror endpoint %q is not an absolute URL", m.Endpoint)
	}
	if endpoint.Scheme != "https" && endpoint.Scheme != "http" {
		return nil, fmt.Errorf("mirror endpoint %q must use the https or http scheme", m.Endpoint)
	}
	if endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, fmt.Errorf(
			"mirror endpoint %q must not carry credentials, a query, or a fragment",
			m.Endpoint,
		)
	}
	pathPrefix, err := normalizeMirrorPath(endpoint.Path)
	if err != nil {
		return nil, fmt.Errorf("mirror endpoint %q %w", m.Endpoint, err)
	}
	// A path without overridePath is the hostname-only footgun: the request would ask the mirror
	// host for the original repository path and silently select the wrong content root.
	if pathPrefix != "" && !m.OverridePath {
		return nil, fmt.Errorf(
			"mirror endpoint %q has a path; set overridePath to rewrite the registry API root",
			m.Endpoint,
		)
	}
	if pathPrefix == "" && m.OverridePath {
		return nil, fmt.Errorf(
			"mirror endpoint %q sets overridePath without an endpoint path",
			m.Endpoint,
		)
	}

	apiRoot := mirrorAPIRoot(pathPrefix)
	repoPrefix := strings.TrimPrefix(pathPrefix, strings.TrimSuffix(apiRoot, "/"))

	return &registryMirrorTarget{
		registry:     normalizeRegistry(registry.RegistryStr()),
		scheme:       endpoint.Scheme,
		host:         strings.ToLower(endpoint.Host),
		pathPrefix:   pathPrefix,
		apiRoot:      apiRoot,
		repoPrefix:   strings.Trim(repoPrefix, "/"),
		overridePath: m.OverridePath,
	}, nil
}

//nolint:err113 // compile wraps these details with the offending endpoint.
func normalizeMirrorPath(path string) (string, error) {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return "", nil
	}
	for segment := range strings.SplitSeq(trimmed, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", errors.New("has an empty or relative path segment")
		}
		for _, character := range segment {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') &&
				character != '.' && character != '_' && character != '-' {
				return "", errors.New("has an unsupported character in its path")
			}
		}
	}
	return "/" + trimmed, nil
}

// mirrorAPIRoot locates the mirror's own API version root for the /v2/ ping: every distribution
// registry answers its version check at <prefix>/v2/, before any repository path rewriting.
func mirrorAPIRoot(pathPrefix string) string {
	segments := strings.Split(strings.Trim(pathPrefix, "/"), "/")
	for index, segment := range segments {
		if segment == "v2" {
			return "/" + strings.Join(segments[:index+1], "/") + "/"
		}
	}
	return standardAPIRoot
}

// wrap returns a RoundTripper that moves every request addressed to the source registry onto the
// mirror endpoint. Requests to other hosts (the mirror's own token realm, storage redirects) pass
// through with at most their auth-token scope prefixed, so the mirror answers its own auth
// challenges. repository is the origin repository being resolved through this transport.
func (t *registryMirrorTarget) wrap(inner http.RoundTripper, repository string) http.RoundTripper {
	return &mirrorTransport{inner: inner, target: t, repository: repository}
}

type mirrorTransport struct {
	inner      http.RoundTripper
	target     *registryMirrorTarget
	repository string
}

func (m *mirrorTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if normalizeRegistry(request.URL.Host) != m.target.registry {
		return m.rewriteTokenScope(request)
	}
	cloned := request.Clone(request.Context())
	cloned.URL.Scheme = m.target.scheme
	cloned.URL.Host = m.target.host
	cloned.URL.RawPath = ""
	cloned.Host = ""
	if m.target.overridePath {
		switch {
		case cloned.URL.Path == standardAPIRoot || cloned.URL.Path == "/v2":
			// The API version check probes the mirror server itself, not a repository path.
			cloned.URL.Path = m.target.apiRoot
		case strings.HasPrefix(cloned.URL.Path, standardAPIRoot):
			cloned.URL.Path = m.target.pathPrefix + strings.TrimPrefix(cloned.URL.Path, "/v2")
		}
	}
	return m.inner.RoundTrip(cloned)
}

// rewriteTokenScope prefixes the resolved repository inside auth-token scope parameters. Clients
// derive the token scope from the origin repository path, but a path-rewriting mirror grants
// access under its own repository prefix, and it answers a wrong-scope token with a challenge
// that carries no corrected scope, so following challenges cannot recover (a CRI resolver avoids
// this only by fetching its token after an anonymous request). Only the exact origin repository
// is prefixed; scopes issued by other hosts' own challenges pass through untouched.
func (m *mirrorTransport) rewriteTokenScope(request *http.Request) (*http.Response, error) {
	if !m.target.overridePath || m.target.repoPrefix == "" || m.repository == "" ||
		request.URL.RawQuery == "" {
		return m.inner.RoundTrip(request)
	}
	query := request.URL.Query()
	scopes, hasScope := query["scope"]
	if !hasScope {
		return m.inner.RoundTrip(request)
	}
	origin := "repository:" + m.repository + ":"
	mirrored := "repository:" + m.target.repoPrefix + "/" + m.repository + ":"
	changed := false
	for scopeIndex, scope := range scopes {
		// One scope parameter may carry several space-separated scope values.
		values := strings.Split(scope, " ")
		for valueIndex, value := range values {
			if rest, isOrigin := strings.CutPrefix(value, origin); isOrigin {
				values[valueIndex] = mirrored + rest
				changed = true
			}
		}
		scopes[scopeIndex] = strings.Join(values, " ")
	}
	if !changed {
		return m.inner.RoundTrip(request)
	}
	cloned := request.Clone(request.Context())
	query["scope"] = scopes
	cloned.URL.RawQuery = query.Encode()
	return m.inner.RoundTrip(cloned)
}

// RegistryMirrorPolicy selects at most one controller metadata mirror per source registry. Like
// RegistryTrustPolicy it never supplies kubelet or node-runtime registry configuration.
type RegistryMirrorPolicy struct {
	byRegistry map[string]RegistryMirror
}

// NewRegistryMirrorPolicy validates and indexes controller metadata mirror entries.
//
//nolint:err113 // The caller translates configuration failures into its stable API diagnostic.
func NewRegistryMirrorPolicy(entries []RegistryMirror) (*RegistryMirrorPolicy, error) {
	policy := &RegistryMirrorPolicy{byRegistry: make(map[string]RegistryMirror, len(entries))}
	for index, entry := range entries {
		target, err := entry.compile()
		if err != nil {
			return nil, fmt.Errorf("registry metadata mirror entry %d is invalid: %w", index, err)
		}
		if _, exists := policy.byRegistry[target.registry]; exists {
			return nil, fmt.Errorf(
				"registry metadata mirror entry %d duplicates registry %s",
				index,
				target.registry,
			)
		}
		entry.Registry = target.registry
		policy.byRegistry[target.registry] = entry
	}

	return policy, nil
}

// ForReference returns a defensive copy of the mirror for an image reference. An invalid
// reference deliberately selects nothing; the resolver then reports its InvalidRequest diagnostic
// through the normal fail-closed path.
func (p *RegistryMirrorPolicy) ForReference(reference string) *RegistryMirror {
	if p == nil {
		return nil
	}
	parsed, err := name.ParseReference(strings.TrimSpace(reference))
	if err != nil {
		return nil
	}
	entry, exists := p.byRegistry[normalizeRegistry(parsed.Context().RegistryStr())]
	if !exists {
		return nil
	}

	return &entry
}

// ForRegistry returns a defensive copy of the exact trust entry for a registry authority; mirror
// selection uses it to apply trust to the mirror connection host instead of the image-ref registry.
func (p *RegistryTrustPolicy) ForRegistry(registry string) *RegistryTrust {
	if p == nil {
		return nil
	}
	entry, exists := p.byRegistry[normalizeRegistry(registry)]
	if !exists {
		return nil
	}
	entry.CABundle = slices.Clone(entry.CABundle)

	return &entry
}
