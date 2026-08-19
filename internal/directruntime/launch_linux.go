//go:build linux

package directruntime

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

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
	if source != "tmpfs" || filesystem != "tmpfs" {
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
	if err := unix.Mount(source, destination, filesystem, flags, strings.Join(data, ",")); err != nil {
		return fmt.Errorf("mounting scoped filesystem at %q: %w", destination, err)
	}

	return nil
}

func (linuxLaunchOperations) Exec(argv []string) error {
	executable, err := exec.LookPath(argv[0])
	if err != nil {
		return err
	}

	return unix.Exec(executable, argv, os.Environ())
}
