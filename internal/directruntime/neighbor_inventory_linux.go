//go:build linux

package directruntime

import (
	"errors"
	"fmt"
	"slices"
	"syscall"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const (
	meshNeighborReceiveBuffer = 1 << 20
	meshNeighborDatagramSize  = 64 << 10
	meshNeighborDrainBudget   = 16_384
)

var errMeshNeighborInventory = errors.New("mesh neighbor inventory did not converge")

type neighborScope struct {
	index  int
	family int
	proxy  bool
}

// meshNeighborInventory belongs to the connectivity reconcile goroutine. Its socket queues
// kernel notifications between passes, avoiding host-wide neighbor hash walks on every peer
// update. A new VTEP starts empty; an existing interface or a lost notification needs one dump.
// No background goroutine mutates the snapshot, and Close releases the subscription.
type meshNeighborInventory struct {
	fd       int
	entries  map[neighborScope]map[string]netlink.Neigh
	resolver meshNeighborResolver
	dump     func(int, int, bool) ([]netlink.Neigh, error)
}

func newMeshNeighborInventory() *meshNeighborInventory {
	return &meshNeighborInventory{
		fd:      -1,
		entries: make(map[neighborScope]map[string]netlink.Neigh),
		dump:    dumpMeshNeighbors,
	}
}

func dumpMeshNeighbors(index, family int, proxy bool) ([]netlink.Neigh, error) {
	if proxy {
		return netlink.NeighProxyList(index, family)
	}

	return netlink.NeighList(index, family)
}

func (inventory *meshNeighborInventory) start() error {
	if inventory == nil || inventory.fd >= 0 {
		return nil
	}
	fd, err := unix.Socket(
		unix.AF_NETLINK,
		unix.SOCK_RAW|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK,
		unix.NETLINK_ROUTE,
	)
	if err != nil {
		return err
	}
	// A cold full mesh emits thousands of notifications before the next pass. Force the
	// buffer when CAP_NET_ADMIN permits it; overflow detection still protects the fallback.
	if err = unix.SetsockoptInt(
		fd, unix.SOL_SOCKET, unix.SO_RCVBUFFORCE, meshNeighborReceiveBuffer,
	); err != nil {
		if err = unix.SetsockoptInt(
			fd, unix.SOL_SOCKET, unix.SO_RCVBUF, meshNeighborReceiveBuffer,
		); err != nil {
			_ = unix.Close(fd)

			return err
		}
	}
	if err = unix.Bind(fd, &unix.SockaddrNetlink{
		Family: unix.AF_NETLINK, Groups: 1 << (unix.RTNLGRP_NEIGH - 1),
	}); err != nil {
		_ = unix.Close(fd)

		return err
	}
	inventory.fd = fd

	return nil
}

func (inventory *meshNeighborInventory) close() error {
	if inventory == nil {
		return nil
	}
	inventory.resolver.close()
	if inventory.fd < 0 {
		return nil
	}
	err := unix.Close(inventory.fd)
	inventory.fd = -1
	clear(inventory.entries)

	return err
}

func (inventory *meshNeighborInventory) created(index int) {
	if inventory == nil {
		return
	}
	for scope := range inventory.entries {
		if scope.index == index {
			delete(inventory.entries, scope)
		}
	}
	for _, family := range []int{unix.AF_INET, unix.AF_INET6, unix.AF_BRIDGE} {
		inventory.entries[neighborScope{index: index, family: family}] = make(
			map[string]netlink.Neigh,
		)
	}
}

func meshNeighborKey(entry netlink.Neigh) string {
	if entry.Family == unix.AF_BRIDGE {
		return entry.HardwareAddr.String() + "|" + entry.IP.String()
	}

	return entry.IP.String()
}

func (inventory *meshNeighborInventory) apply(message syscall.NetlinkMessage) error {
	if message.Header.Type == unix.NLMSG_OVERRUN {
		clear(inventory.entries)

		return nil
	}
	if message.Header.Type != unix.RTM_NEWNEIGH && message.Header.Type != unix.RTM_DELNEIGH {
		return nil
	}
	if len(message.Data) < (&netlink.Ndmsg{}).Len() {
		clear(inventory.entries)

		return fmt.Errorf("%w: truncated neighbor notification", errMeshNeighborInventory)
	}
	entry, err := netlink.NeighDeserialize(message.Data)
	if err != nil {
		clear(inventory.entries)

		return err
	}
	scope := neighborScope{
		index:  entry.LinkIndex,
		family: entry.Family,
		proxy:  entry.Flags&unix.NTF_PROXY != 0,
	}
	entries, known := inventory.entries[scope]
	if !known {
		return nil
	}
	key := meshNeighborKey(*entry)
	if message.Header.Type == unix.RTM_DELNEIGH {
		delete(entries, key)
	} else {
		// Netlink deserialization aliases the receive buffer, which the next datagram reuses.
		entry.IP = slices.Clone(entry.IP)
		entry.HardwareAddr = slices.Clone(entry.HardwareAddr)
		entry.LLIPAddr = slices.Clone(entry.LLIPAddr)
		entries[key] = *entry
	}

	return nil
}

func (inventory *meshNeighborInventory) drain() error {
	buffer := make([]byte, meshNeighborDatagramSize)
	for range meshNeighborDrainBudget {
		count, sender, err := unix.Recvfrom(inventory.fd, buffer, unix.MSG_DONTWAIT|unix.MSG_TRUNC)
		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) {
			return nil
		}
		if errors.Is(err, unix.ENOBUFS) {
			clear(inventory.entries)

			continue
		}
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		address, ok := sender.(*unix.SockaddrNetlink)
		if !ok || address.Pid != 0 {
			continue
		}
		if count > len(buffer) {
			clear(inventory.entries)

			continue
		}
		messages, err := syscall.ParseNetlinkMessage(buffer[:count])
		if err != nil {
			return err
		}
		for _, message := range messages {
			if err = inventory.apply(message); err != nil {
				return err
			}
		}
	}
	clear(inventory.entries)

	return errMeshNeighborInventory
}

func (inventory *meshNeighborInventory) list(
	index, family int,
	proxy bool,
) ([]netlink.Neigh, error) {
	if inventory == nil {
		return dumpMeshNeighbors(index, family, proxy)
	}
	if err := inventory.start(); err != nil {
		return nil, err
	}
	scope := neighborScope{index: index, family: family, proxy: proxy}
	for range 2 {
		if err := inventory.drain(); err != nil {
			return nil, err
		}
		if _, known := inventory.entries[scope]; !known {
			entries, err := inventory.dump(index, family, proxy)
			if err != nil {
				return nil, err
			}
			inventory.entries[scope] = make(map[string]netlink.Neigh, len(entries))
			for _, entry := range entries {
				if (entry.Flags&unix.NTF_PROXY != 0) == proxy {
					inventory.entries[scope][meshNeighborKey(entry)] = entry
				}
			}
			// Replay notifications queued during the dump before trusting the snapshot.
			if err = inventory.drain(); err != nil {
				return nil, err
			}
		}
		if entries, known := inventory.entries[scope]; known {
			result := make([]netlink.Neigh, 0, len(entries))
			for _, entry := range entries {
				result = append(result, entry)
			}

			return result, nil
		}
	}

	return nil, fmt.Errorf("%w: interface %d family %d", errMeshNeighborInventory, index, family)
}
