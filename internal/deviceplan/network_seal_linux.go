//go:build linux

//nolint:err113 // diagnostics are structured one-off errors carrying typed classification.
package deviceplan

import (
	"errors"
	"fmt"
	"net"

	"github.com/vishvananda/netlink"
)

func sealPlannerNetwork() error {
	links, err := netlink.LinkList()
	if err != nil {
		return err
	}

	sealed := 0

	for _, link := range links {
		attributes := link.Attrs()
		if attributes == nil || attributes.Flags&net.FlagLoopback != 0 {
			continue
		}

		if err = netlink.LinkSetDown(link); err != nil {
			return fmt.Errorf("setting planner interface down: %w", err)
		}

		sealed++
	}

	if sealed == 0 {
		return errors.New("planner network namespace has no non-loopback interface to seal")
	}

	return nil
}
