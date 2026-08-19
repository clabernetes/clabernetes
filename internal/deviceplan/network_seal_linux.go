//go:build linux

package deviceplan

import (
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
		return fmt.Errorf("planner network namespace has no non-loopback interface to seal")
	}

	return nil
}
