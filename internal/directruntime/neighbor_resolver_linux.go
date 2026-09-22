//go:build linux

package directruntime

import (
	"errors"
	"fmt"
	"maps"
	"net"
	"net/netip"
	"sync"
	"syscall"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// meshNeighborResolver answers the kernel's app_solicit requests from the trusted
// peer directory. Only peers actually contacted receive permanent kernel entries;
// adding an idle device no longer creates a neighbor in every other namespace.
type meshNeighborResolver struct {
	mu        sync.Mutex
	index     int
	peers     map[netip.Addr]meshPeerState
	requested map[netip.Addr]bool
	failure   error
	handle    *netlink.Handle
	fd        int
	stop      chan struct{}
	finished  chan struct{}
}

func (r *meshNeighborResolver) start(index int) error {
	if r.handle != nil && r.index == index {
		r.mu.Lock()
		defer r.mu.Unlock()

		return r.failure
	}
	r.close()
	handle, err := netlink.NewHandle(unix.NETLINK_ROUTE)
	if err != nil {
		return err
	}
	fd, err := unix.Socket(
		unix.AF_NETLINK,
		unix.SOCK_RAW|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK,
		unix.NETLINK_ROUTE,
	)
	if err != nil {
		handle.Close()

		return err
	}
	if err = unix.Bind(fd, &unix.SockaddrNetlink{
		Family: unix.AF_NETLINK, Groups: 1 << (unix.RTNLGRP_NEIGH - 1),
	}); err != nil {
		_ = unix.Close(fd)
		handle.Close()

		return err
	}
	r.index, r.fd, r.handle = index, fd, handle
	r.peers = make(map[netip.Addr]meshPeerState)
	r.requested = make(map[netip.Addr]bool)
	r.failure = nil
	r.stop, r.finished = make(chan struct{}), make(chan struct{})
	go r.run()

	return nil
}

func (r *meshNeighborResolver) close() {
	if r == nil || r.handle == nil {
		return
	}
	close(r.stop)
	<-r.finished
	_ = unix.Close(r.fd)
	r.handle.Close()
	r.handle = nil
}

func (r *meshNeighborResolver) run() {
	defer close(r.finished)
	buffer := make([]byte, meshNeighborDatagramSize)
	fd := int32(r.fd) //nolint:gosec // kernel file descriptor, bounded by the descriptor table.
	poll := []unix.PollFd{{Fd: fd, Events: unix.POLLIN}}
	for {
		select {
		case <-r.stop:
			return
		default:
		}
		if _, err := unix.Poll(poll, 100); err != nil { //nolint:mnd // bounds shutdown without another descriptor.
			if errors.Is(err, unix.EINTR) {
				continue
			}
			r.fail(err)

			return
		}
		count, sender, err := unix.Recvfrom(r.fd, buffer, unix.MSG_DONTWAIT|unix.MSG_TRUNC)
		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR) ||
			errors.Is(err, unix.ENOBUFS) {
			// app_solicit retries unresolved requests; no learned state is trusted here.
			continue
		}
		if err != nil {
			r.fail(err)

			return
		}
		origin, ok := sender.(*unix.SockaddrNetlink)
		if !ok || origin.Pid != 0 || count > len(buffer) {
			continue
		}
		if err = r.acceptRequests(buffer[:count]); err != nil {
			r.fail(err)

			return
		}
	}
}

func (r *meshNeighborResolver) acceptRequests(data []byte) error {
	messages, err := syscall.ParseNetlinkMessage(data)
	if err != nil {
		return err
	}
	for _, message := range messages {
		if message.Header.Type != unix.RTM_GETNEIGH ||
			len(message.Data) < (&netlink.Ndmsg{}).Len() {
			continue
		}
		request, decodeErr := netlink.NeighDeserialize(message.Data)
		if decodeErr != nil {
			return decodeErr
		}
		if err = r.resolve(*request); err != nil {
			return err
		}
	}

	return nil
}

func (r *meshNeighborResolver) fail(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failure = fmt.Errorf("management neighbor resolution: %w", err)
}

func (r *meshNeighborResolver) resolve(request netlink.Neigh) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if request.LinkIndex != r.index ||
		(request.Family != unix.AF_INET && request.Family != unix.AF_INET6) ||
		request.Flags&unix.NTF_PROXY != 0 {
		return nil
	}
	address, valid := netip.AddrFromSlice(request.IP)
	if !valid {
		return nil
	}
	address = address.Unmap()
	peer, known := r.peers[address]
	if !known {
		return nil
	}
	r.requested[address] = true
	// Publish forwarding first so releasing the kernel's queued packet cannot race
	// the VXLAN destination. Entries remain controller-owned and cannot be learned
	// from an untrusted ARP reply.
	if err := r.handle.NeighSet(&netlink.Neigh{
		LinkIndex: r.index, Family: unix.AF_BRIDGE, Flags: unix.NTF_SELF,
		State: netlink.NUD_PERMANENT | netlink.NUD_NOARP, IP: peer.pod, HardwareAddr: peer.mac,
	}); err != nil {
		return err
	}

	return r.handle.NeighSet(&netlink.Neigh{
		LinkIndex: r.index, Family: request.Family, State: netlink.NUD_PERMANENT,
		IP: net.IP(address.AsSlice()), HardwareAddr: peer.mac,
	})
}

// selectRequested runs with mu held across the subsequent kernel reconciliation,
// preventing a new request from racing the sweep of existing owned entries.
func (r *meshNeighborResolver) selectRequested(
	v4, v6 map[netip.Addr]meshPeerState,
) (map[netip.Addr]meshPeerState, map[netip.Addr]meshPeerState) {
	r.peers = make(map[netip.Addr]meshPeerState, len(v4)+len(v6))
	maps.Copy(r.peers, v4)
	maps.Copy(r.peers, v6)
	activeV4, activeV6 := map[netip.Addr]meshPeerState{}, map[netip.Addr]meshPeerState{}
	for address := range r.requested {
		peer, present := r.peers[address]
		switch {
		case !present:
			delete(r.requested, address)
		case address.Is4():
			activeV4[address] = peer
		default:
			activeV6[address] = peer
		}
	}

	return activeV4, activeV6
}
