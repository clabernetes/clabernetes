//go:build linux

package directruntime

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// markProbeSocket is a net.Dialer control that puts the sidecar's probe mark on the socket, so
// the policy rules can tell the sidecar's own management connections from the device's local
// traffic. Marking needs CAP_NET_ADMIN; where it is missing (an unprivileged test run) the
// connection is made unmarked rather than refused.
func markProbeSocket(_, _ string, raw syscall.RawConn) error {
	return raw.Control(func(fd uintptr) {
		_ = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_MARK, interpositionProbeMark)
	})
}
