//go:build !linux

package directruntime

import "syscall"

// markProbeSocket is a no-op outside Linux, where socket marks do not exist.
func markProbeSocket(_, _ string, _ syscall.RawConn) error {
	return nil
}
