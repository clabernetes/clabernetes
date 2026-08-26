//nolint:nlreturn,noinlineerr,wsl_v5 // Keep cache state transitions compact.
package ocimetadata

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"golang.org/x/sync/singleflight"
)

const (
	defaultCacheTTL        = 5 * time.Minute
	defaultCacheMaxEntries = 256
)

var (
	errNegativeCacheTTL       = errors.New("OCI metadata cache TTL must not be negative")
	errNegativeCacheEntries   = errors.New("OCI metadata cache entry limit must not be negative")
	errUnexpectedCachedValue  = errors.New("OCI metadata cache returned an unexpected value")
	errInvalidCachedMetadata  = errors.New("invalid cached OCI metadata")
	errTrailingCachedMetadata = errors.New("cached OCI metadata contains trailing JSON values")
)

// CacheOptions controls metadata lifetime, capacity, and the testable time source.
type CacheOptions struct {
	TTL        time.Duration
	MaxEntries int
	Now        func() time.Time
}

// Cache is a bounded, integrity-checked OCI metadata cache with request coalescing.
type Cache struct {
	resolver   Resolver
	ttl        time.Duration
	maxEntries int
	now        func() time.Time

	mutex    sync.Mutex
	entries  map[string]cacheEntry
	requests singleflight.Group
}

type cacheEntry struct {
	metadataDigest string
	metadataJSON   []byte
	expiresAt      time.Time
	lastAccess     time.Time
}

// NewCache constructs a bounded cache around resolver.
func NewCache(resolver Resolver, options CacheOptions) (*Cache, error) {
	if options.TTL < 0 {
		return nil, errNegativeCacheTTL
	}
	if options.MaxEntries < 0 {
		return nil, errNegativeCacheEntries
	}
	if options.TTL == 0 {
		options.TTL = defaultCacheTTL
	}
	if options.MaxEntries == 0 {
		options.MaxEntries = defaultCacheMaxEntries
	}
	if options.Now == nil {
		options.Now = time.Now
	}

	return &Cache{
		resolver:   resolver,
		ttl:        options.TTL,
		maxEntries: options.MaxEntries,
		now:        options.Now,
		entries:    make(map[string]cacheEntry, options.MaxEntries),
	}, nil
}

// Resolve returns validated metadata from the cache or resolves and stores it.
func (c *Cache) Resolve(ctx context.Context, request Request) (*Metadata, error) {
	if ctx == nil {
		return c.resolver.Resolve(ctx, request)
	}

	key := requestCacheKey(&request)
	if metadata := c.load(key); metadata != nil {
		return metadata, nil
	}

	result, err, _ := c.requests.Do(key, func() (any, error) {
		if metadata := c.load(key); metadata != nil {
			return metadata.CanonicalJSON()
		}

		metadata, resolveErr := c.resolver.Resolve(ctx, request)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if validationErr := validateMetadata(metadata); validationErr != nil {
			return nil, resolverError(
				ErrorInvalidConfig,
				request,
				fmt.Errorf("resolved metadata failed cache validation: %w", validationErr),
			)
		}
		encoded, encodeErr := metadata.CanonicalJSON()
		if encodeErr != nil {
			return nil, encodeErr
		}
		c.store(key, encoded)
		return encoded, nil
	})
	if err != nil {
		return nil, err
	}

	encoded, ok := result.([]byte)
	if !ok {
		return nil, resolverError(
			ErrorInvalidConfig,
			request,
			fmt.Errorf("%w: %T", errUnexpectedCachedValue, result),
		)
	}
	metadata, err := decodeMetadata(encoded)
	if err != nil {
		c.delete(key)
		return nil, resolverError(
			ErrorInvalidConfig,
			request,
			fmt.Errorf("decoding cached metadata: %w", err),
		)
	}
	return metadata, nil
}

func (c *Cache) load(key string) *Metadata {
	now := c.now()
	c.mutex.Lock()
	entry, ok := c.entries[key]
	if !ok {
		c.mutex.Unlock()
		return nil
	}
	if !now.Before(entry.expiresAt) ||
		metadataJSONDigest(entry.metadataJSON) != entry.metadataDigest {
		delete(c.entries, key)
		c.mutex.Unlock()
		return nil
	}
	entry.lastAccess = now
	c.entries[key] = entry
	encoded := bytes.Clone(entry.metadataJSON)
	c.mutex.Unlock()

	metadata, err := decodeMetadata(encoded)
	if err != nil || validateMetadata(metadata) != nil {
		c.delete(key)
		return nil
	}
	return metadata
}

func (c *Cache) store(key string, encoded []byte) {
	now := c.now()
	c.mutex.Lock()
	defer c.mutex.Unlock()

	for entryKey, entry := range c.entries {
		if !now.Before(entry.expiresAt) {
			delete(c.entries, entryKey)
		}
	}
	if _, exists := c.entries[key]; !exists && len(c.entries) >= c.maxEntries {
		oldestKey := ""
		var oldestAccess time.Time
		for entryKey, entry := range c.entries {
			if oldestKey == "" || entry.lastAccess.Before(oldestAccess) ||
				(entry.lastAccess.Equal(oldestAccess) && entryKey < oldestKey) {
				oldestKey = entryKey
				oldestAccess = entry.lastAccess
			}
		}
		delete(c.entries, oldestKey)
	}
	c.entries[key] = cacheEntry{
		metadataDigest: metadataJSONDigest(encoded),
		metadataJSON:   bytes.Clone(encoded),
		expiresAt:      now.Add(c.ttl),
		lastAccess:     now,
	}
}

func (c *Cache) delete(key string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	delete(c.entries, key)
}

func requestCacheKey(request *Request) string {
	platform := normalizePlatform(request.Platform)

	return fingerprintValues(
		strings.TrimSpace(request.Reference),
		platform.OS,
		platform.Architecture,
		platform.Variant,
		platform.OSVersion,
		fingerprintValues(platform.OSFeatures...),
		authenticationFingerprint(request.Authentication),
		trustFingerprint(request.Trust),
	)
}

func authenticationFingerprint(authentication *Authentication) string {
	if authentication == nil {
		return "anonymous"
	}
	return fingerprintValues(
		authentication.registry,
		authentication.config.Username,
		authentication.config.Password,
		authentication.config.Auth,
		authentication.config.IdentityToken,
		authentication.config.RegistryToken,
	)
}

func trustFingerprint(trust *RegistryTrust) string {
	if trust == nil {
		return "system"
	}
	caDigest := sha256.Sum256(trust.CABundle)

	return fingerprintValues(
		normalizeRegistryAddress(trust.Registry),
		strconv.FormatBool(trust.PlainHTTP),
		hex.EncodeToString(caDigest[:]),
	)
}

func fingerprintValues(values ...string) string {
	var input strings.Builder
	for _, value := range values {
		input.WriteString(strconv.Itoa(len(value)))
		input.WriteByte(':')
		input.WriteString(value)
		input.WriteByte(';')
	}
	digest := sha256.Sum256([]byte(input.String()))

	return hex.EncodeToString(digest[:])
}

func metadataJSONDigest(encoded []byte) string {
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func decodeMetadata(encoded []byte) (*Metadata, error) {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	metadata := &Metadata{}
	if err := decoder.Decode(metadata); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errTrailingCachedMetadata
		}
		return nil, err
	}
	return metadata, nil
}

func validateMetadata(metadata *Metadata) error {
	if metadata == nil {
		return fmt.Errorf("%w: metadata is nil", errInvalidCachedMetadata)
	}
	if metadata.SchemaVersion != SchemaVersion {
		return fmt.Errorf(
			"%w: schema version is %q, want %q",
			errInvalidCachedMetadata,
			metadata.SchemaVersion,
			SchemaVersion,
		)
	}
	for field, value := range map[string]string{
		"root digest":     metadata.RootDigest,
		"manifest digest": metadata.ManifestDigest,
		"config digest":   metadata.ConfigDigest,
	} {
		hash, err := v1.NewHash(value)
		if err != nil {
			return fmt.Errorf("%w: %s is invalid: %w", errInvalidCachedMetadata, field, err)
		}
		if hash.Algorithm != "sha256" {
			return fmt.Errorf(
				"%w: %s uses unsupported algorithm %q",
				errInvalidCachedMetadata,
				field,
				hash.Algorithm,
			)
		}
	}

	digestReference, err := name.NewDigest(metadata.DigestReference, name.StrictValidation)
	if err != nil {
		return fmt.Errorf("%w: digest reference is invalid: %w", errInvalidCachedMetadata, err)
	}
	if digestReference.DigestStr() != metadata.ManifestDigest {
		return fmt.Errorf(
			"%w: digest reference selects %s, metadata declares %s",
			errInvalidCachedMetadata,
			digestReference.DigestStr(),
			metadata.ManifestDigest,
		)
	}
	if digestReference.Context().Name() != referenceRepository(metadata.SourceReference) {
		return fmt.Errorf(
			"%w: source and digest references use different repositories",
			errInvalidCachedMetadata,
		)
	}
	if sourceDigest, ok := sourceReferenceDigest(metadata.SourceReference); ok &&
		sourceDigest != metadata.RootDigest {
		return fmt.Errorf(
			"%w: source reference selects %s, root digest is %s",
			errInvalidCachedMetadata,
			sourceDigest,
			metadata.RootDigest,
		)
	}
	if metadata.Platform.OS == "" || metadata.Platform.Architecture == "" {
		return fmt.Errorf("%w: resolved platform is incomplete", errInvalidCachedMetadata)
	}
	if !slices.IsSorted(metadata.Platform.OSFeatures) {
		return fmt.Errorf(
			"%w: resolved platform features are not normalized",
			errInvalidCachedMetadata,
		)
	}
	return nil
}

func referenceRepository(reference string) string {
	parsed, err := name.ParseReference(reference)
	if err != nil {
		return ""
	}
	return parsed.Context().Name()
}

func sourceReferenceDigest(reference string) (string, bool) {
	digest, err := name.NewDigest(reference, name.StrictValidation)
	if err != nil {
		return "", false
	}
	return digest.DigestStr(), true
}
