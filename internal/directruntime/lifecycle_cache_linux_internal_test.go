//go:build linux

package directruntime

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func cacheTestSource(t *testing.T, content []byte) *os.File {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "source-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	if _, err = file.Write(content); err != nil {
		t.Fatal(err)
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}

	return file
}

func TestLifecycleCacheConcurrentReuseAndRepair(t *testing.T) {
	t.Parallel()
	content := bytes.Repeat([]byte("runtime binary\n"), 1024)
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	var workers sync.WaitGroup
	for range 8 {
		source := cacheTestSource(t, content)
		workers.Go(func() {
			digest, digestErr := lifecycleBinaryDigest(source)
			if digestErr != nil {
				t.Error(digestErr)

				return
			}
			cached, cacheErr := populateLifecycleCache(root, source, digest+".bin", digest)
			if cacheErr != nil {
				t.Error(cacheErr)

				return
			}
			defer func() { _ = cached.Close() }()
			got, readErr := io.ReadAll(cached)
			if readErr != nil || !bytes.Equal(got, content) {
				t.Errorf("cached content differs: %v", readErr)
			}
		})
	}
	workers.Wait()
	source := cacheTestSource(t, content)
	digest, err := lifecycleBinaryDigest(source)
	if err != nil {
		t.Fatal(err)
	}
	name := digest + ".bin"
	before, err := root.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	cached, err := populateLifecycleCache(root, source, name, digest)
	if err != nil {
		t.Fatal(err)
	}
	_ = cached.Close()
	after, err := root.Stat(name)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("warm cache rewrote its inode: %v", err)
	}
	assertLifecycleCacheRepair(t, root, source, name, digest, content)
}

func assertLifecycleCacheRepair(
	t *testing.T,
	root *os.Root,
	source *os.File,
	name, digest string,
	content []byte,
) {
	t.Helper()
	if err := root.Chmod(name, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root.Name(), name), []byte("damaged"), 0o600); err != nil {
		t.Fatal(err)
	}
	cached, err := populateLifecycleCache(root, source, name, digest)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cached.Close() }()
	got, err := io.ReadAll(cached)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("cache was not repaired from the trusted image: %v", err)
	}
}

func TestLifecycleCachePrunesVersionsAndRejectsSymlinks(t *testing.T) {
	t.Parallel()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	for index := range lifecycleCacheVersions + 2 {
		source := cacheTestSource(t, bytes.Repeat([]byte{byte(index)}, 4096))
		digest, digestErr := lifecycleBinaryDigest(source)
		if digestErr != nil {
			t.Fatal(digestErr)
		}
		cached, cacheErr := populateLifecycleCache(root, source, digest+".bin", digest)
		if cacheErr != nil {
			t.Fatal(cacheErr)
		}
		_ = cached.Close()
	}
	entries, err := os.ReadDir(root.Name())
	if err != nil || len(entries) != lifecycleCacheVersions+1 {
		t.Fatalf("cache entries = %d, want %d binaries and one lock: %v",
			len(entries), lifecycleCacheVersions, err)
	}
	outside := cacheTestSource(t, []byte("must remain unchanged"))
	digest, err := lifecycleBinaryDigest(outside)
	if err != nil {
		t.Fatal(err)
	}
	if err = root.Symlink(outside.Name(), digest+".bin"); err != nil {
		t.Fatal(err)
	}
	if file, verifyErr := verifiedLifecycleCache(root, digest+".bin", digest); verifyErr == nil {
		if file != nil {
			_ = file.Close()
		}
		t.Fatal("cache accepted a symlink")
	}
}

func TestLifecycleCloneKeepsSourceAndFallbackPosition(t *testing.T) {
	t.Parallel()
	content := bytes.Repeat([]byte("independent runtime file\n"), 1024)
	source := cacheTestSource(t, content)
	destination := cacheTestSource(t, nil)
	cloned, err := cloneLifecycleBinary(source, destination, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !cloned {
		if _, err = io.Copy(destination, source); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(destination.Name())
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("clone or fallback content differs: %v", err)
	}
	if _, err = destination.WriteAt([]byte("changed"), 0); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(source.Name())
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("destination write changed its source: %v", err)
	}
}
