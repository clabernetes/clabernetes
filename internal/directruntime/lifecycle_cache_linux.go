//go:build linux

package directruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/sys/unix"
)

const (
	lifecycleCacheVersions       = 4
	lifecycleCachePrivateMode    = 0o600
	lifecycleCacheExecutableMode = 0o555
)

var errLifecycleCache = errors.New("invalid lifecycle binary cache")

// The image overlay and the Pod emptyDir are different filesystems from the VFS's
// perspective. Stage one verified copy on the worker filesystem so FICLONE can share its
// extents with independent Pod-owned files. Neither links nor SELinux labels are shared.
func cloneLifecycleBinary(source, destination *os.File, cacheRoot string) (bool, error) {
	if err := unix.IoctlFileClone(int(destination.Fd()), int(source.Fd())); err == nil {
		return true, nil
	} else if !unsupportedLifecycleClone(err) {
		return false, err
	}
	if cacheRoot == "" {
		return false, nil
	}
	if !filepath.IsAbs(cacheRoot) || filepath.Clean(cacheRoot) == "/" {
		return false, errLifecycleCache
	}
	digest, err := lifecycleBinaryDigest(source)
	if err != nil {
		return false, err
	}
	root, err := os.OpenRoot(cacheRoot)
	if err != nil {
		return false, err
	}
	defer func() { _ = root.Close() }()
	name := digest + ".bin"
	cached, err := verifiedLifecycleCache(root, name, digest)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		// The cache is an optimization. Reject unsafe entries, but let the caller
		// copy the trusted image binary rather than wedging every Pod on this host.
		return false, nil
	}
	if cached == nil {
		cached, err = populateLifecycleCache(root, source, name, digest)
		if err != nil {
			return false, nil
		}
	}
	defer func() { _ = cached.Close() }()
	if err = unix.IoctlFileClone(int(destination.Fd()), int(cached.Fd())); err != nil {
		if unsupportedLifecycleClone(err) {
			return false, nil
		}

		return false, err
	}

	return true, nil
}

func unsupportedLifecycleClone(err error) bool {
	return errors.Is(err, unix.EXDEV) || errors.Is(err, unix.EOPNOTSUPP) ||
		errors.Is(err, unix.ENOTTY) || errors.Is(err, unix.EINVAL)
}

func lifecycleBinaryDigest(file *os.File) (string, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(file, maxLifecycleBinaryBytes+1))
	_, seekErr := file.Seek(0, io.SeekStart)
	if err = errors.Join(err, seekErr); err != nil {
		return "", err
	}
	if written == 0 || written > maxLifecycleBinaryBytes {
		return "", errLifecycleCache
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

// Every reuse checks content, type and mode. Warm readers need no lock; an open descriptor
// remains valid if another installer prunes the cache while a Pod is being prepared.
func verifiedLifecycleCache(root *os.Root, name, digest string) (*os.File, error) {
	file, err := root.OpenFile(name, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil, os.ErrNotExist
	}
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()

		return nil, errors.Join(errLifecycleCache, err)
	}
	actual, err := lifecycleBinaryDigest(file)
	if err != nil {
		_ = file.Close()

		return nil, err
	}
	if actual != digest || info.Mode().Perm() != lifecycleCacheExecutableMode {
		_ = file.Close()

		return nil, os.ErrNotExist
	}

	return file, nil
}

func populateLifecycleCache(root *os.Root, source *os.File, name, digest string) (*os.File, error) {
	lock, err := root.OpenFile(
		"install.lock",
		os.O_CREATE|os.O_RDWR|unix.O_NOFOLLOW,
		lifecycleCachePrivateMode,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = lock.Close() }()
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return nil, err
	}
	defer func() { _ = unix.Flock(int(lock.Fd()), unix.LOCK_UN) }()
	cached, checkErr := verifiedLifecycleCache(root, name, digest)
	if cached != nil || (checkErr != nil && !errors.Is(checkErr, os.ErrNotExist)) {
		return cached, checkErr
	}

	return publishLifecycleCache(root, source, name, digest)
}

func publishLifecycleCache(root *os.Root, source *os.File, name, digest string) (*os.File, error) {
	// All writers hold the same lock, and os.Root keeps even hostile symlinks scoped.
	stagedName := "staged.bin"
	staged, err := root.OpenFile(
		stagedName,
		os.O_CREATE|os.O_EXCL|os.O_RDWR,
		lifecycleCachePrivateMode,
	)
	if errors.Is(err, os.ErrExist) {
		if err = root.Remove(stagedName); err == nil {
			staged, err = root.OpenFile(
				stagedName,
				os.O_CREATE|os.O_EXCL|os.O_RDWR,
				lifecycleCachePrivateMode,
			)
		}
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = staged.Close() }()
	defer func() { _ = root.Remove(stagedName) }()
	_, err = io.Copy(staged, io.LimitReader(source, maxLifecycleBinaryBytes+1))
	_, seekErr := source.Seek(0, io.SeekStart)
	if err = errors.Join(err, seekErr); err != nil {
		return nil, err
	}
	actual, err := lifecycleBinaryDigest(staged)
	if err != nil || actual != digest {
		return nil, errors.Join(errLifecycleCache, err)
	}
	if err = staged.Chmod(lifecycleCacheExecutableMode); err != nil {
		return nil, err
	}
	if err = staged.Sync(); err != nil {
		return nil, err
	}
	if err = root.Rename(stagedName, name); err != nil {
		return nil, err
	}
	if err = pruneLifecycleCache(root, name); err != nil {
		return nil, err
	}

	return root.OpenFile(name, os.O_RDONLY|unix.O_NOFOLLOW, 0)
}

func pruneLifecycleCache(root *os.Root, keep string) error {
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	entries, err := directory.Readdir(-1)
	if err != nil {
		return err
	}
	entries = slices.DeleteFunc(entries, func(info os.FileInfo) bool {
		name := strings.TrimSuffix(info.Name(), ".bin")
		decoded, decodeErr := hex.DecodeString(name)

		return !info.Mode().IsRegular() || info.Name() == keep ||
			!strings.HasSuffix(
				info.Name(),
				".bin",
			) || decodeErr != nil || len(decoded) != sha256.Size
	})
	slices.SortFunc(entries, func(a, b os.FileInfo) int { return b.ModTime().Compare(a.ModTime()) })
	for index, entry := range entries {
		if index < lifecycleCacheVersions-1 {
			continue
		}
		if err = root.Remove(entry.Name()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("pruning lifecycle binary cache: %w", err)
		}
	}

	return nil
}
