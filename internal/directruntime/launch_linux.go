//go:build linux

//nolint:err113,funlen,gocyclo // single-pass boundary logic with structured one-off diagnostics and protocol literals.
package directruntime

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// launchTmpfsFilesystem is the only mount filesystem the launch boundary realizes.
const launchTmpfsFilesystem = "tmpfs"

type linuxLaunchOperations struct{}

func newLaunchOperations() LaunchOperations {
	return linuxLaunchOperations{}
}

func (linuxLaunchOperations) Delay(duration time.Duration) error {
	time.Sleep(duration)

	return nil
}

func (linuxLaunchOperations) MountFilesystem(
	source,
	destination,
	filesystem string,
	options []string,
) error {
	if source != launchTmpfsFilesystem || filesystem != launchTmpfsFilesystem {
		return fmt.Errorf("filesystem operation %q from %q is unsupported", filesystem, source)
	}

	flags := uintptr(0)

	data := make([]string, 0, len(options))
	for _, option := range options {
		key, _, _ := strings.Cut(option, "=")
		switch key {
		case "", "defaults", "rw":
			flags &^= unix.MS_RDONLY
		case "ro":
			flags |= unix.MS_RDONLY
		case "suid":
			flags &^= unix.MS_NOSUID
		case "nosuid":
			flags |= unix.MS_NOSUID
		case "dev":
			flags &^= unix.MS_NODEV
		case "nodev":
			flags |= unix.MS_NODEV
		case "exec":
			flags &^= unix.MS_NOEXEC
		case "noexec":
			flags |= unix.MS_NOEXEC
		case "sync":
			flags |= unix.MS_SYNCHRONOUS
		case "async":
			flags &^= unix.MS_SYNCHRONOUS
		case "dirsync":
			flags |= unix.MS_DIRSYNC
		case "atime":
			flags &^= unix.MS_NOATIME
		case "noatime":
			flags |= unix.MS_NOATIME
		case "diratime":
			flags &^= unix.MS_NODIRATIME
		case "nodiratime":
			flags |= unix.MS_NODIRATIME
		case "relatime":
			flags |= unix.MS_RELATIME
		case "norelatime":
			flags &^= unix.MS_RELATIME
		case "strictatime":
			flags |= unix.MS_STRICTATIME
		case "nostrictatime":
			flags &^= unix.MS_STRICTATIME
		case "bind", "rbind", "remount", "move", "private", "rprivate", "shared",
			"rshared", "slave", "rslave", "unbindable", "runbindable":
			return fmt.Errorf("tmpfs option %q changes the scoped mount operation", option)
		default:
			// Filesystem-specific data such as size, mode, uid, gid, huge, and mpol is
			// intentionally passed through to the kernel. This keeps the boundary generic.
			data = append(data, option)
		}
	}

	if err := unix.Mount(source, destination, filesystem,
		flags, strings.Join(data, ",")); err != nil {
		return fmt.Errorf("mounting scoped filesystem at %q: %w", destination, err)
	}

	return nil
}

func (linuxLaunchOperations) UpdateFile(
	path string,
	update func(current []byte) (updated []byte, write bool),
) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0) //nolint:gosec // fixed runtime-owned path.
	if err != nil {
		return fmt.Errorf("opening %q for in-place update: %w", path, err)
	}

	defer file.Close() //nolint:errcheck // read-modify-write is flushed by the explicit write.

	// The advisory lock serializes sibling launch boundaries in the same Pod; the kubelet does
	// not take it, but a kubelet rewrite is always followed by the (re)started container's own
	// launch, which restores the content.
	//nolint:gosec // kernel-issued fd.
	if err = unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
		return fmt.Errorf("locking %q: %w", path, err)
	}

	//nolint:errcheck,gosec // kernel-issued fd; the lock is released on close anyway.
	defer unix.Flock(int(file.Fd()), unix.LOCK_UN)

	current, err := io.ReadAll(file)
	if err != nil {
		return fmt.Errorf("reading %q: %w", path, err)
	}

	updated, write := update(current)
	if !write {
		return nil
	}

	// The file is a bind mount (kubelet-managed), so it cannot be replaced by rename; truncate
	// and rewrite in place instead.
	if err = file.Truncate(0); err != nil {
		return fmt.Errorf("truncating %q: %w", path, err)
	}

	if _, err = file.WriteAt(updated, 0); err != nil {
		return fmt.Errorf("rewriting %q: %w", path, err)
	}

	return nil
}

func (linuxLaunchOperations) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path) //nolint:gosec // fixed runtime-owned path.
}

func (linuxLaunchOperations) Hostname() (string, error) {
	return os.Hostname()
}

func (linuxLaunchOperations) LimitOpenFiles(limit uint64) error {
	current := unix.Rlimit{}
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &current); err != nil {
		return fmt.Errorf("reading open-file limit: %w", err)
	}

	if current.Max <= limit {
		return nil
	}

	return unix.Setrlimit(unix.RLIMIT_NOFILE, &unix.Rlimit{Cur: limit, Max: limit})
}

func (linuxLaunchOperations) Exec(argv []string) error {
	executable, err := exec.LookPath(argv[0])
	if err != nil {
		return err
	}

	return unix.Exec(executable, argv, os.Environ())
}
