//go:build linux

package directruntime

import (
	"errors"
	"time"

	"github.com/vishvananda/netlink"
)

const (
	netlinkDumpAttempts = 5
	netlinkDumpBackoff  = 20 * time.Millisecond
)

// listPodLinks retries an interrupted dump from scratch. Interface creation during device
// boot can invalidate a dump; none of that attempt's partial results may drive reconciliation
// or stale-link deletion. Persistent interruptions and other errors still fail closed.
func listPodLinks() ([]netlink.Link, error) {
	return retryLinkDump(netlink.LinkList)
}

func retryLinkDump(list func() ([]netlink.Link, error)) ([]netlink.Link, error) {
	var err error

	for attempt := range netlinkDumpAttempts {
		var links []netlink.Link
		links, err = list()
		if err == nil {
			return links, nil
		}

		if !errors.Is(err, netlink.ErrDumpInterrupted) {
			return nil, err
		}

		if attempt+1 < netlinkDumpAttempts {
			time.Sleep(netlinkDumpBackoff)
		}
	}

	return nil, err
}
