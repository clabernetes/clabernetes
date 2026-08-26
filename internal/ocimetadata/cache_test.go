//nolint:noinlineerr,testpackage,wsl_v5 // Internal cache tests intentionally inspect corruption and key state.
package ocimetadata

import (
	"context"
	"encoding/base64"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	k8scorev1 "k8s.io/api/core/v1"
)

func TestCacheReusesMetadataUntilTTLAndReturnsCopies(t *testing.T) {
	t.Parallel()

	testRegistry := newTestRegistry(t)
	image, _ := newTestImage(t, Platform{OS: "linux", Architecture: "amd64"}, "cached")
	reference := testRegistry.writeImage(t, "devices/cache:latest", image)
	request := Request{
		Reference: reference.Name(),
		Platform:  Platform{OS: "linux", Architecture: "amd64"},
		Trust:     testRegistry.plainHTTPTrust(),
	}
	now := time.Date(2026, time.August, 18, 12, 0, 0, 0, time.UTC)
	cache, err := NewCache(Resolver{Transport: testRegistry.transport}, CacheOptions{
		TTL:        time.Minute,
		MaxEntries: 4,
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	testRegistry.recorder.reset()

	first, err := cache.Resolve(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	requestsAfterFirst := len(testRegistry.recorder.paths())
	first.Config.Cmd[1] = "caller-mutated"

	second, err := cache.Resolve(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(testRegistry.recorder.paths()), requestsAfterFirst; got != want {
		t.Fatalf("cache hit made registry requests: got %d total, want %d", got, want)
	}
	if got, want := second.Config.Cmd[1], "cached"; got != want {
		t.Fatalf("cached metadata was mutated through caller result: got %q, want %q", got, want)
	}

	now = now.Add(time.Minute)
	if _, err = cache.Resolve(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if got := len(testRegistry.recorder.paths()); got <= requestsAfterFirst {
		t.Fatalf(
			"expired cache entry made no new registry requests: got %d, first resolve made %d",
			got,
			requestsAfterFirst,
		)
	}
}

func TestCacheKeyIncludesCredentialsAndTrust(t *testing.T) {
	t.Parallel()

	testRegistry := newTestRegistry(t)
	image, _ := newTestImage(t, Platform{OS: "linux", Architecture: "amd64"}, "private-cache")
	reference := testRegistry.writeImage(t, "devices/cache:private", image)
	registryHost := reference.Context().RegistryStr()
	goodUsername := "cache-user"
	goodPassword := "cache-password"
	goodAuthentication, err := AuthenticationFromPullSecrets(reference.Name(), []k8scorev1.Secret{
		dockerConfigSecret(t, "lab", "good", registryHost, goodUsername, goodPassword),
	})
	if err != nil {
		t.Fatal(err)
	}
	badAuthentication, err := AuthenticationFromPullSecrets(reference.Name(), []k8scorev1.Secret{
		dockerConfigSecret(t, "lab", "bad", registryHost, "bad-user", "bad-password"),
	})
	if err != nil {
		t.Fatal(err)
	}
	testRegistry.recorder.protect(
		"Basic "+base64.StdEncoding.EncodeToString([]byte(goodUsername+":"+goodPassword)),
		"denied",
	)

	cache, err := NewCache(Resolver{Transport: testRegistry.transport}, CacheOptions{})
	if err != nil {
		t.Fatal(err)
	}
	baseRequest := Request{
		Reference:      reference.Name(),
		Platform:       Platform{OS: "linux", Architecture: "amd64"},
		Authentication: goodAuthentication,
		Trust:          testRegistry.plainHTTPTrust(),
	}
	if _, err = cache.Resolve(context.Background(), baseRequest); err != nil {
		t.Fatal(err)
	}

	badCredentialRequest := baseRequest
	badCredentialRequest.Authentication = badAuthentication
	_, err = cache.Resolve(context.Background(), badCredentialRequest)
	assertErrorCode(t, err, ErrorResolveManifest)

	badTrustRequest := baseRequest
	badTrustRequest.Trust = &RegistryTrust{
		Registry: baseRequest.Trust.Registry,
		CABundle: []byte("different and invalid CA"),
	}
	_, err = cache.Resolve(context.Background(), badTrustRequest)
	assertErrorCode(t, err, ErrorInvalidTrust)

	cache.mutex.Lock()
	defer cache.mutex.Unlock()
	for key := range cache.entries {
		for _, secretValue := range []string{goodUsername, goodPassword, badAuthentication.config.Password} {
			if strings.Contains(key, secretValue) {
				t.Fatalf("cache key leaked credential %q: %q", secretValue, key)
			}
		}
	}
}

func TestCacheKeyIncludesPlatform(t *testing.T) {
	t.Parallel()

	testRegistry := newTestRegistry(t)
	amd64Image, _ := newTestImage(t, Platform{OS: "linux", Architecture: "amd64"}, "amd64-cache")
	arm64Image, _ := newTestImage(t, Platform{OS: "linux", Architecture: "arm64"}, "arm64-cache")
	index := mutate.AppendManifests(
		empty.Index,
		mutate.IndexAddendum{
			Add:        amd64Image,
			Descriptor: v1.Descriptor{Platform: &v1.Platform{OS: "linux", Architecture: "amd64"}},
		},
		mutate.IndexAddendum{
			Add:        arm64Image,
			Descriptor: v1.Descriptor{Platform: &v1.Platform{OS: "linux", Architecture: "arm64"}},
		},
	)
	reference := testRegistry.writeIndex(t, "devices/cache:multi", index)
	cache, err := NewCache(Resolver{Transport: testRegistry.transport}, CacheOptions{})
	if err != nil {
		t.Fatal(err)
	}

	amd64Metadata, err := cache.Resolve(context.Background(), Request{
		Reference: reference.Name(), Platform: Platform{OS: "linux", Architecture: "amd64"}, Trust: testRegistry.plainHTTPTrust(),
	})
	if err != nil {
		t.Fatal(err)
	}
	arm64Metadata, err := cache.Resolve(context.Background(), Request{
		Reference: reference.Name(), Platform: Platform{OS: "linux", Architecture: "arm64"}, Trust: testRegistry.plainHTTPTrust(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if amd64Metadata.ManifestDigest == arm64Metadata.ManifestDigest {
		t.Fatalf(
			"platform cache entries resolved the same manifest %s",
			amd64Metadata.ManifestDigest,
		)
	}
	if got, want := arm64Metadata.Config.Cmd[1], "arm64-cache"; got != want {
		t.Fatalf("arm64 command = %q, want %q", got, want)
	}
}

func TestCacheIsBoundedAndEvictsLeastRecentlyUsedEntry(t *testing.T) {
	t.Parallel()

	testRegistry := newTestRegistry(t)
	firstImage, _ := newTestImage(t, Platform{OS: "linux", Architecture: "amd64"}, "first")
	secondImage, _ := newTestImage(t, Platform{OS: "linux", Architecture: "amd64"}, "second")
	firstReference := testRegistry.writeImage(t, "devices/cache:first", firstImage)
	secondReference := testRegistry.writeImage(t, "devices/cache:second", secondImage)
	now := time.Now()
	cache, err := NewCache(Resolver{Transport: testRegistry.transport}, CacheOptions{
		TTL: time.Minute, MaxEntries: 1, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	request := func(reference string) Request {
		return Request{
			Reference: reference,
			Platform:  Platform{OS: "linux", Architecture: "amd64"},
			Trust:     testRegistry.plainHTTPTrust(),
		}
	}

	if _, err = cache.Resolve(context.Background(), request(firstReference.Name())); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if _, err = cache.Resolve(context.Background(), request(secondReference.Name())); err != nil {
		t.Fatal(err)
	}
	requestsBeforeReload := len(testRegistry.recorder.paths())
	now = now.Add(time.Second)
	if _, err = cache.Resolve(context.Background(), request(firstReference.Name())); err != nil {
		t.Fatal(err)
	}
	if got := len(testRegistry.recorder.paths()); got <= requestsBeforeReload {
		t.Fatalf(
			"evicted entry was returned without registry request: got %d, before reload %d",
			got,
			requestsBeforeReload,
		)
	}
	cache.mutex.Lock()
	entryCount := len(cache.entries)
	cache.mutex.Unlock()
	if entryCount != 1 {
		t.Fatalf("cache contains %d entries, want 1", entryCount)
	}
}

func TestCacheDiscardsCorruptMetadata(t *testing.T) {
	t.Parallel()

	testRegistry := newTestRegistry(t)
	image, _ := newTestImage(t, Platform{OS: "linux", Architecture: "amd64"}, "integrity")
	reference := testRegistry.writeImage(t, "devices/cache:integrity", image)
	request := Request{
		Reference: reference.Name(), Platform: Platform{OS: "linux", Architecture: "amd64"}, Trust: testRegistry.plainHTTPTrust(),
	}
	cache, err := NewCache(Resolver{Transport: testRegistry.transport}, CacheOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = cache.Resolve(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	requestsBeforeCorruption := len(testRegistry.recorder.paths())

	key := requestCacheKey(&request)
	cache.mutex.Lock()
	entry := cache.entries[key]
	entry.metadataJSON[0] ^= 0xff
	cache.entries[key] = entry
	cache.mutex.Unlock()

	if _, err = cache.Resolve(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if got := len(testRegistry.recorder.paths()); got <= requestsBeforeCorruption {
		t.Fatalf(
			"corrupt cache entry was used without registry request: got %d, before corruption %d",
			got,
			requestsBeforeCorruption,
		)
	}
}

func TestCacheCoalescesConcurrentResolution(t *testing.T) {
	t.Parallel()

	testRegistry := newTestRegistry(t)
	image, _ := newTestImage(t, Platform{OS: "linux", Architecture: "amd64"}, "coalesced")
	reference := testRegistry.writeImage(t, "devices/cache:coalesced", image)
	request := Request{
		Reference: reference.Name(), Platform: Platform{OS: "linux", Architecture: "amd64"}, Trust: testRegistry.plainHTTPTrust(),
	}
	cache, err := NewCache(Resolver{Transport: testRegistry.transport}, CacheOptions{})
	if err != nil {
		t.Fatal(err)
	}
	testRegistry.recorder.reset()

	const callers = 20
	var waitGroup sync.WaitGroup
	errorsByCaller := make(chan error, callers)
	for range callers {
		waitGroup.Go(func() {
			_, resolveErr := cache.Resolve(context.Background(), request)
			errorsByCaller <- resolveErr
		})
	}
	waitGroup.Wait()
	close(errorsByCaller)
	for resolveErr := range errorsByCaller {
		if resolveErr != nil {
			t.Errorf("Resolve() error = %v", resolveErr)
		}
	}

	manifestRequests := 0
	for _, path := range testRegistry.recorder.paths() {
		if strings.Contains(path, "/manifests/coalesced") {
			manifestRequests++
		}
	}
	if manifestRequests != 1 {
		t.Fatalf(
			"concurrent resolves made %d manifest requests, want 1; all requests: %#v",
			manifestRequests,
			testRegistry.recorder.paths(),
		)
	}
}

func TestNewCacheRejectsUnboundedOptions(t *testing.T) {
	t.Parallel()

	if _, err := NewCache(Resolver{}, CacheOptions{TTL: -time.Second}); err == nil {
		t.Fatal("NewCache() accepted negative TTL")
	}
	if _, err := NewCache(Resolver{}, CacheOptions{MaxEntries: -1}); err == nil {
		t.Fatal("NewCache() accepted negative entry limit")
	}
}
