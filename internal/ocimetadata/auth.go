//nolint:mnd,nlreturn,noinlineerr,wsl_v5 // Keep authentication guards compact.
package ocimetadata

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	k8scorev1 "k8s.io/api/core/v1"
)

// SecretSource identifies the Kubernetes Secret used for registry credentials.
type SecretSource struct {
	Namespace string
	Name      string
}

// Authentication contains registry-scoped credentials parsed from a pull Secret.
type Authentication struct {
	registry string
	source   SecretSource
	config   authn.AuthConfig
}

// Source reports the Secret identity without exposing its credential data.
func (a *Authentication) Source() SecretSource {
	if a == nil {
		return SecretSource{}
	}
	return a.source
}

// RegistryTrust defines explicit transport trust for one registry host.
type RegistryTrust struct {
	// Registry is the exact host[:port] this policy is allowed to affect.
	Registry string
	// CABundle extends, rather than replaces, the host's system trust roots.
	CABundle []byte
	// PlainHTTP permits HTTP for this registry. It never disables TLS verification.
	PlainHTTP bool
}

// RegistryTrustPolicy selects at most one exact controller metadata trust exception per image
// registry. It never supplies kubelet or node-runtime registry configuration.
type RegistryTrustPolicy struct {
	byRegistry map[string]RegistryTrust
}

// NewRegistryTrustPolicy validates and indexes exact registry metadata trust entries.
//
//nolint:err113 // The caller translates configuration failures into its stable API diagnostic.
func NewRegistryTrustPolicy(entries []RegistryTrust) (*RegistryTrustPolicy, error) {
	policy := &RegistryTrustPolicy{byRegistry: make(map[string]RegistryTrust, len(entries))}
	for index, entry := range entries {
		registry, err := name.NewRegistry(strings.TrimSpace(entry.Registry), name.StrictValidation)
		if err != nil {
			return nil, fmt.Errorf(
				"registry metadata trust entry %d has an invalid registry authority",
				index,
			)
		}
		registryName := normalizeRegistry(registry.RegistryStr())
		if _, exists := policy.byRegistry[registryName]; exists {
			return nil, fmt.Errorf(
				"registry metadata trust entry %d duplicates registry %s",
				index,
				registryName,
			)
		}
		if entry.PlainHTTP && len(entry.CABundle) != 0 {
			return nil, fmt.Errorf(
				"registry metadata trust entry %d combines HTTP and a TLS CA bundle",
				index,
			)
		}
		if !entry.PlainHTTP && len(entry.CABundle) == 0 {
			return nil, fmt.Errorf(
				"registry metadata trust entry %d contains no trust exception",
				index,
			)
		}
		if len(entry.CABundle) != 0 {
			roots := x509.NewCertPool()
			if !roots.AppendCertsFromPEM(entry.CABundle) {
				return nil, fmt.Errorf(
					"registry metadata trust entry %d has no valid PEM certificate",
					index,
				)
			}
		}
		entry.Registry = registryName
		entry.CABundle = slices.Clone(entry.CABundle)
		policy.byRegistry[registryName] = entry
	}

	return policy, nil
}

// ForReference returns a defensive copy of the exact trust entry for an image reference. An
// invalid reference deliberately selects nothing; the resolver then reports its InvalidRequest
// diagnostic through the normal fail-closed path.
func (p *RegistryTrustPolicy) ForReference(reference string) *RegistryTrust {
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
	entry.CABundle = slices.Clone(entry.CABundle)

	return &entry
}

type dockerConfigJSON struct {
	Auths map[string]json.RawMessage `json:"auths"`
}

// AuthenticationFromPullSecrets resolves the first credential matching the
// image registry in Kubernetes imagePullSecrets order. It reads only Secret
// data supplied by the caller and never consults a local Docker configuration.
//
//nolint:err113,nilnil // ErrorCode classifies failures; nil selects anonymous access.
func AuthenticationFromPullSecrets(
	reference string,
	secrets []k8scorev1.Secret,
) (*Authentication, error) {
	parsed, err := name.ParseReference(strings.TrimSpace(reference))
	if err != nil {
		return nil, &Error{
			Code:      ErrorInvalidRequest,
			Reference: strings.TrimSpace(reference),
			Err:       errors.New("image reference is invalid"),
		}
	}
	targetRegistry := normalizeRegistry(parsed.Context().RegistryStr())

	for index := range secrets {
		secret := &secrets[index]
		auths, parseErr := secretAuths(secret)
		if parseErr != nil {
			return nil, &Error{
				Code:      ErrorInvalidAuthentication,
				Reference: parsed.Name(),
				Err: fmt.Errorf(
					"pull Secret %s is invalid: %w",
					secretIdentity(secret),
					parseErr,
				),
			}
		}

		matchingKeys := make([]string, 0, len(auths))
		for key := range auths {
			if normalizeRegistry(key) == targetRegistry {
				matchingKeys = append(matchingKeys, key)
			}
		}
		slices.SortFunc(matchingKeys, func(left, right string) int {
			leftExact := normalizeRegistryAddress(left) == targetRegistry
			rightExact := normalizeRegistryAddress(right) == targetRegistry
			if leftExact != rightExact {
				if leftExact {
					return -1
				}
				return 1
			}
			return strings.Compare(left, right)
		})
		if len(matchingKeys) == 0 {
			continue
		}

		config := authn.AuthConfig{}
		if err = json.Unmarshal(auths[matchingKeys[0]], &config); err != nil {
			return nil, &Error{
				Code:      ErrorInvalidAuthentication,
				Reference: parsed.Name(),
				Err: fmt.Errorf(
					"pull Secret %s has invalid credentials for %s",
					secretIdentity(secret),
					targetRegistry,
				),
			}
		}
		if config == (authn.AuthConfig{}) {
			return nil, &Error{
				Code:      ErrorInvalidAuthentication,
				Reference: parsed.Name(),
				Err: fmt.Errorf(
					"pull Secret %s has empty credentials for %s",
					secretIdentity(secret),
					targetRegistry,
				),
			}
		}
		return &Authentication{
			registry: targetRegistry,
			source: SecretSource{
				Namespace: secret.Namespace,
				Name:      secret.Name,
			},
			config: config,
		}, nil
	}

	return nil, nil
}

//nolint:err113 // Callers wrap parser details in the stable InvalidAuthentication error code.
func secretAuths(secret *k8scorev1.Secret) (map[string]json.RawMessage, error) {
	var raw []byte

	// Every non-Docker Secret type is rejected by default; enumerating unrelated
	// Kubernetes Secret types would weaken that fail-closed contract.
	switch secret.Type {
	case k8scorev1.SecretTypeDockerConfigJson:
		raw = secret.Data[k8scorev1.DockerConfigJsonKey]
		if len(raw) == 0 {
			return nil, fmt.Errorf("missing %s", k8scorev1.DockerConfigJsonKey)
		}
		config := dockerConfigJSON{}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		if err := decoder.Decode(&config); err != nil {
			return nil, errors.New("malformed Docker config JSON")
		}
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			return nil, errors.New("Docker config JSON has trailing data")
		}
		if config.Auths == nil {
			return nil, errors.New("Docker config JSON has no auths object")
		}
		return config.Auths, nil
	case k8scorev1.SecretTypeDockercfg:
		raw = secret.Data[k8scorev1.DockerConfigKey]
		if len(raw) == 0 {
			return nil, fmt.Errorf("missing %s", k8scorev1.DockerConfigKey)
		}
		auths := map[string]json.RawMessage{}
		if err := json.Unmarshal(raw, &auths); err != nil {
			return nil, errors.New("malformed legacy Docker config JSON")
		}
		return auths, nil
	default:
		return nil, fmt.Errorf("unsupported Secret type %q", secret.Type)
	}
}

func (a *Authentication) authenticator() authn.Authenticator {
	if a == nil {
		return authn.Anonymous
	}
	return authn.FromConfig(a.config)
}

func (a *Authentication) sensitiveValues() []string {
	if a == nil {
		return nil
	}
	values := []string{
		a.config.Username,
		a.config.Password,
		a.config.Auth,
		a.config.IdentityToken,
		a.config.RegistryToken,
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}

// defaultResolverTransport bounds header waits so an unresponsive registry cannot hold a
// reconcile worker: the resolver's callers otherwise carry no deadline of their own.
var defaultResolverTransport = func() http.RoundTripper { //nolint:gochecknoglobals // shared resolver transport.
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return http.DefaultTransport
	}
	cloned := base.Clone()
	cloned.ResponseHeaderTimeout = 30 * time.Second
	return cloned
}()

//nolint:err113 // Callers wrap trust details in the stable InvalidTrust error code.
func (r Resolver) transportForTrust(trust *RegistryTrust) (http.RoundTripper, error) {
	transport := r.Transport
	if transport == nil {
		transport = defaultResolverTransport
	}
	if trust == nil || len(trust.CABundle) == 0 {
		return transport, nil
	}

	httpTransport, ok := transport.(*http.Transport)
	if !ok {
		return nil, errors.New("custom CA requires an HTTP transport that can be cloned")
	}
	cloned := httpTransport.Clone()
	if cloned.TLSClientConfig == nil {
		cloned.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	} else {
		cloned.TLSClientConfig = cloned.TLSClientConfig.Clone()
	}
	if cloned.TLSClientConfig.RootCAs == nil {
		roots, err := x509.SystemCertPool()
		if err != nil {
			return nil, fmt.Errorf("loading system CA pool: %w", err)
		}
		cloned.TLSClientConfig.RootCAs = roots
	} else {
		cloned.TLSClientConfig.RootCAs = cloned.TLSClientConfig.RootCAs.Clone()
	}
	if !cloned.TLSClientConfig.RootCAs.AppendCertsFromPEM(trust.CABundle) {
		return nil, errors.New("CA bundle contains no valid PEM certificate")
	}
	return cloned, nil
}

//nolint:err113 // The returned ErrorCode is the stable public classification.
func validateRequestSecurity(request *Request, registry string) (ErrorCode, error) {
	targetRegistry := normalizeRegistry(registry)
	if request.Authentication != nil && request.Authentication.registry != targetRegistry {
		return ErrorInvalidAuthentication, fmt.Errorf(
			"pull Secret credentials are scoped to %s, not %s",
			request.Authentication.registry,
			targetRegistry,
		)
	}
	if request.Trust != nil {
		trustedRegistry := normalizeRegistryAddress(request.Trust.Registry)
		if trustedRegistry == "" {
			return ErrorInvalidTrust, errors.New("registry trust policy has no registry")
		}
		if trustedRegistry != targetRegistry {
			return ErrorInvalidTrust, fmt.Errorf(
				"registry trust policy is scoped to %s, not %s",
				trustedRegistry,
				targetRegistry,
			)
		}
	}
	return "", nil
}

func normalizeRegistry(value string) string {
	host := normalizeRegistryAddress(value)
	switch host {
	case "docker.io", "registry-1.docker.io":
		return name.DefaultRegistry
	default:
		return host
	}
}

func normalizeRegistryAddress(value string) string {
	value = strings.TrimSpace(value)
	if parsed, err := url.Parse(value); err == nil && parsed.Host != "" {
		value = parsed.Host
	} else {
		value = strings.Split(value, "/")[0]
	}
	return strings.ToLower(strings.TrimSuffix(value, "/"))
}

func secretIdentity(secret *k8scorev1.Secret) string {
	if secret.Namespace == "" {
		return secret.Name
	}
	return secret.Namespace + "/" + secret.Name
}
