//go:build linux

//nolint:testpackage // Exercises the syscall retry seam without changing the live namespace.
package directruntime

import (
	"errors"
	"fmt"
	"testing"

	"github.com/vishvananda/netlink"
)

func TestRetryLinkDumpDiscardsInterruptedResults(t *testing.T) {
	t.Parallel()

	calls := 0
	links, err := retryLinkDump(func() ([]netlink.Link, error) {
		calls++
		if calls == 1 {
			return []netlink.Link{&netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: "partial"}}},
				fmt.Errorf("dump: %w", netlink.ErrDumpInterrupted)
		}

		return []netlink.Link{&netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: "complete"}}}, nil
	})
	if err != nil || calls != 2 || len(links) != 1 || links[0].Attrs().Name != "complete" {
		t.Fatalf("dump = %v, %v after %d calls", links, err, calls)
	}
}

func TestRetryLinkDumpFailsClosed(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		err   error
		calls int
	}{
		{"interrupted", netlink.ErrDumpInterrupted, netlinkDumpAttempts},
		{"other", errors.ErrUnsupported, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			calls := 0
			links, err := retryLinkDump(func() ([]netlink.Link, error) {
				calls++

				return []netlink.Link{&netlink.Dummy{}}, test.err
			})
			if !errors.Is(err, test.err) || links != nil || calls != test.calls {
				t.Fatalf("dump = %v, %v after %d calls", links, err, calls)
			}
		})
	}
}
