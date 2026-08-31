//go:build linux

//nolint:err113,gocognit,gocyclo,nestif // single-pass boundary logic with structured one-off diagnostics and protocol literals.
package directruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"
	"time"

	clabconstants "github.com/srl-labs/containerlab/constants"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const (
	// fabricPeerResolveTimeout bounds one peer transport name resolution.
	fabricPeerResolveTimeout = 3 * time.Second
)

func fabricShortHash(identity string) string {
	digest := sha256.Sum256([]byte(identity))

	return hex.EncodeToString(digest[:])[:8]
}

// fabricSidecarLegName derives the sidecar-side wire leg name for one plan interface.
func fabricSidecarLegName(interfaceID string) string {
	return "c9ss" + fabricShortHash(interfaceID)
}

// hostTransferLegName derives the temporary Pod-side name a host Link's worker leg carries
// before it moves into the worker namespace.
func hostTransferLegName(interfaceID string) string {
	return "c9sh" + fabricShortHash(interfaceID)
}

// EnsureFabricEndpoint realizes one cross-Pod endpoint entirely inside the Pod namespace: the
// device leg + sidecar leg pair and the leg's registration with the Pod's fabric wire, the
// userspace UDP transport that segments frames to the underlay and propagates carrier state.
// A device may adopt, rename, or move the device leg after boot; the sidecar leg remains
// sidecar-owned and is converged idempotently, and once registered its admin state belongs to
// the wire's carrier state machine.
func (o netlinkOperations) EnsureFabricEndpoint(
	spec FabricEndpointSpec,
) (FabricEndpointResult, error) {
	result := FabricEndpointResult{}

	podAddress, err := netip.ParseAddr(spec.PodAddress)
	if err != nil || !podAddress.Is4() {
		return result, fmt.Errorf("fabric endpoint Pod address %q is invalid", spec.PodAddress)
	}

	if spec.WireID <= 0 || spec.InterfaceID == "" || spec.InterfaceName == "" ||
		spec.Owner == "" || spec.OwnerPrefix == "" ||
		!strings.HasPrefix(spec.Owner, spec.OwnerPrefix) {
		return result, errors.New("fabric endpoint spec is incomplete")
	}

	// The requested MTU is honored exactly, containerlab-default when unset: the wire
	// fragments to the underlay, so the underlay never bounds a Link. The device-facing leg
	// must be born with its final MTU because a device may adopt and move it where the
	// sidecar can never adjust it again.
	mtu := spec.MTU
	if mtu <= 0 {
		mtu = clabconstants.DefaultLinkMTU
	}

	// The underlay MTU only sizes wire fragments; it is discovered locally and never
	// coordinated with peers.
	underlay, err := podFabricUnderlayMTU(podAddress)
	if err != nil {
		return result, err
	}

	legName := fabricSidecarLegName(spec.InterfaceID)

	if err = ensureFabricPair(spec, legName, mtu); err != nil {
		return result, err
	}

	if _, present, lookupErr := lookupLink(legName); lookupErr != nil || !present {
		return result, errors.Join(errors.New("fabric sidecar leg is unavailable"), lookupErr)
	}

	// The device-side stack must materialize checksums in software: the wire captures raw
	// frames off the veth, and an offloaded transmit leaves them incomplete for the far end.
	if _, present, lookupErr := lookupLink(spec.InterfaceName); lookupErr == nil && present {
		if err = (netlinkOperations{}).DisableTxChecksumOffload(spec.InterfaceName); err != nil {
			return result, err
		}
	}

	wire, err := ensurePodFabricWire(podAddress, underlay)
	if err != nil {
		return result, err
	}

	remote, resolveReason, err := o.resolveFabricPeer(spec)
	if err != nil {
		return result, err
	}

	resolved := resolveReason == ""

	// The link is registered even while the peer is unresolvable so the wire holds the
	// device leg carrier-down: an unplugged far end must look like a dead cable, not a live
	// one.
	if err = wire.EnsureLink(fabricWireLinkSpec{
		LinkID:        uint32(spec.WireID), //nolint:gosec // positive bound checked.
		Owner:         spec.Owner,
		LegName:       legName,
		PeerTransport: spec.PeerTransport,
		PeerAddress:   remote,
		MTU:           mtu,
	}); err != nil {
		return result, err
	}

	if !resolved {
		result.Reason = resolveReason

		return result, nil
	}

	result.Ready = true

	return result, nil
}

// ensureFabricPair converges the device/sidecar veth pair on the endpoint MTU, tolerating a
// device that adopted and moved the device leg out of the root namespace: an existing
// sidecar-owned leg without a visible device leg is accepted state, never an error and never
// recreated. The sidecar leg converges to the endpoint MTU exactly; a visible device leg is
// only ever clamped down so a device that deliberately lowered its own MTU is not fought.
// Admin state is raised only at creation: a registered leg's admin state belongs to the wire's
// carrier state machine, and a device leg's admin state belongs to the device.
func ensureFabricPair(spec FabricEndpointSpec, legName string, mtu int) error {
	leg, legPresent, err := lookupLink(legName)
	if err != nil {
		return err
	}

	device, devicePresent, err := lookupLink(spec.InterfaceName)
	if err != nil {
		return err
	}

	if legPresent {
		if leg.Attrs().Alias != spec.Owner ||
			(devicePresent && device.Attrs().Alias != spec.Owner) {
			if !devicePresent {
				return fmt.Errorf("fabric leg %q collides with unrelated state", legName)
			}

			return adoptFabricPair(spec, device, legName, mtu)
		}

		if mtu > 0 && leg.Attrs().MTU != mtu {
			if err = netlink.LinkSetMTU(leg, mtu); err != nil {
				return fmt.Errorf("converging fabric leg MTU: %w", err)
			}
		}

		if leg.Attrs().Flags&net.FlagUp == 0 && !podFabricWireHoldsLegDown(spec.WireID) {
			if err = netlink.LinkSetUp(leg); err != nil {
				return fmt.Errorf("bringing fabric leg up: %w", err)
			}
		}

		if devicePresent && mtu > 0 && device.Attrs().MTU > mtu {
			if err = netlink.LinkSetMTU(device, mtu); err != nil {
				return fmt.Errorf("clamping fabric device leg MTU: %w", err)
			}
		}

		return nil
	}

	if devicePresent {
		if device.Attrs().Alias == spec.Owner {
			return fmt.Errorf(
				"fabric device leg %q lost its sidecar leg %q",
				spec.InterfaceName,
				legName,
			)
		}

		return adoptFabricPair(spec, device, legName, mtu)
	}

	attributes := netlink.NewLinkAttrs()
	attributes.Name = spec.InterfaceName
	attributes.Alias = spec.Owner

	if mtu != 0 {
		attributes.MTU = mtu
	}

	pair := netlink.NewVeth(attributes)
	pair.PeerName = legName

	if mtu > 0 {
		pair.PeerMTU = uint32(mtu) //nolint:gosec // positive bound checked.
	}

	if err = netlink.LinkAdd(pair); err != nil {
		return fmt.Errorf("creating fabric pair %q: %w", spec.InterfaceName, err)
	}

	for _, name := range []string{spec.InterfaceName, legName} {
		link, present, lookupErr := lookupLink(name)
		if lookupErr != nil || !present {
			return errors.Join(
				fmt.Errorf("fabric link %q vanished after creation", name),
				lookupErr,
			)
		}

		if err = netlink.LinkSetAlias(link, spec.Owner); err != nil {
			return fmt.Errorf("marking fabric link ownership: %w", err)
		}

		if err = netlink.LinkSetUp(link); err != nil {
			return fmt.Errorf("bringing fabric link up: %w", err)
		}
	}

	return nil
}

func adoptFabricPair(spec FabricEndpointSpec, device netlink.Link, legName string, mtu int) error {
	if device.Type() != vethLinkType ||
		!strings.HasPrefix(device.Attrs().Alias, spec.OwnerPrefix) {
		return fmt.Errorf(
			"fabric device leg name %q collides with unrelated state",
			spec.InterfaceName,
		)
	}

	peerIndex, err := netlink.VethPeerIndex(&netlink.Veth{LinkAttrs: *device.Attrs()})
	if err != nil {
		return fmt.Errorf("reading fabric peer for %q: %w", spec.InterfaceName, err)
	}

	leg, err := netlink.LinkByIndex(peerIndex)
	if err != nil {
		return fmt.Errorf("reading fabric peer for %q: %w", spec.InterfaceName, err)
	}

	if leg.Type() != vethLinkType || !strings.HasPrefix(leg.Attrs().Name, "c9ss") ||
		!strings.HasPrefix(leg.Attrs().Alias, spec.OwnerPrefix) ||
		(device.Attrs().Alias != leg.Attrs().Alias && device.Attrs().Alias != spec.Owner &&
			leg.Attrs().Alias != spec.Owner) {
		return fmt.Errorf("fabric peer for %q has unrelated state", spec.InterfaceName)
	}

	if existing, present, lookupErr := lookupLink(legName); lookupErr != nil {
		return lookupErr
	} else if present && existing.Attrs().Index != leg.Attrs().Index {
		return fmt.Errorf("fabric leg %q collides with unrelated state", legName)
	}

	if leg.Attrs().Name != legName {
		// The kernel refuses to rename an interface that is administratively up. The leg is
		// sidecar-owned, so a brief admin-down for the rename is safe; the adoption pass below
		// restores admin state under the wire's carrier rules.
		if leg.Attrs().Flags&net.FlagUp != 0 {
			if err = netlink.LinkSetDown(leg); err != nil {
				return fmt.Errorf("lowering adopted fabric leg for rename: %w", err)
			}
		}

		if err = netlink.LinkSetName(leg, legName); err != nil {
			return fmt.Errorf("renaming adopted fabric leg to %q: %w", legName, err)
		}

		var present bool

		leg, present, err = lookupLink(legName)
		if err != nil || !present {
			return errors.Join(
				fmt.Errorf("renamed fabric leg %q is unavailable", legName),
				err,
			)
		}
	}

	for _, link := range []netlink.Link{leg, device} {
		if link.Attrs().Alias != spec.Owner {
			if err = netlink.LinkSetAlias(link, spec.Owner); err != nil {
				return fmt.Errorf("adopting fabric pair ownership: %w", err)
			}
		}

		isDevice := link.Attrs().Index == device.Attrs().Index

		converge := mtu > 0 && link.Attrs().MTU != mtu
		if isDevice {
			converge = mtu > 0 && link.Attrs().MTU > mtu
		}

		if converge {
			if err = netlink.LinkSetMTU(link, mtu); err != nil {
				return fmt.Errorf("setting adopted fabric pair MTU: %w", err)
			}
		}

		// An adopted pair was live before this pass: the device leg's admin state belongs to
		// the device, and the sidecar leg's belongs to the wire once it holds the leg down.
		if !isDevice && link.Attrs().Flags&net.FlagUp == 0 &&
			!podFabricWireHoldsLegDown(spec.WireID) {
			if err = netlink.LinkSetUp(link); err != nil {
				return fmt.Errorf("bringing adopted fabric pair up: %w", err)
			}
		}
	}

	return nil
}

// resolveFabricPeer resolves a fabric peer transport to one IPv4 address. Absence is unready
// with a reason, not an error: headless Service records appear when the peer Pod exists.
// Lookups bind the Pod address so resolution rides the source-scoped transport rule even when a
// device rewrote the main routing table, and a captured resolver configuration keeps the
// nameservers and search domains independent of the shared /etc/resolv.conf a device may own.
// An empty returned reason means the address is resolved.
func (o netlinkOperations) resolveFabricPeer(
	spec FabricEndpointSpec,
) (netip.Addr, string, error) {
	var remote netip.Addr

	if spec.PeerTransport == "" {
		return remote, "", errors.New("fabric endpoint has no peer transport")
	}

	if parsed, err := netip.ParseAddr(spec.PeerTransport); err == nil {
		return parsed, "", nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), fabricPeerResolveTimeout)
	defer cancel()

	if net.ParseIP(spec.PodAddress) == nil && o.resolver != nil {
		addresses, err := o.resolver.LookupNetIP(ctx, "ip4", spec.PeerTransport)
		if err != nil || len(addresses) == 0 {
			return remote, fmt.Sprintf(
				"peer transport %q is not yet resolvable", spec.PeerTransport,
			), nil
		}

		return addresses[0].Unmap(), "", nil
	}

	if spec.Resolver != nil && spec.Resolver.usable() {
		captured, capturedReason := resolveWithCapturedConfig(ctx, spec)

		return captured, capturedReason, nil
	}

	resolver := peerAddressResolver(podBoundResolver(spec.PodAddress))

	addresses, err := resolver.LookupNetIP(ctx, "ip4", spec.PeerTransport)
	if err != nil || len(addresses) == 0 {
		return remote, fmt.Sprintf(
			"peer transport %q is not yet resolvable through the shared resolver configuration",
			spec.PeerTransport,
		), nil
	}

	return addresses[0].Unmap(), "", nil
}

// resolveWithCapturedConfig answers a peer lookup through the captured Pod DNS configuration:
// each search-completed candidate is queried against each captured nameserver from a socket
// bound to the Pod address. The candidates are rooted, so the underlying resolver performs no
// /etc/resolv.conf search expansion of its own.
func resolveWithCapturedConfig(
	ctx context.Context,
	spec FabricEndpointSpec,
) (netip.Addr, string) {
	var remote netip.Addr

	var lastErr error

	for _, server := range spec.Resolver.Nameservers {
		resolver := podBoundResolverForServer(spec.PodAddress, server)

		for _, candidate := range resolverCandidates(spec.PeerTransport, spec.Resolver.Search) {
			addresses, err := resolver.LookupNetIP(ctx, "ip4", candidate)
			if err != nil {
				lastErr = err

				continue
			}

			if len(addresses) > 0 {
				return addresses[0].Unmap(), ""
			}
		}
	}

	reason := fmt.Sprintf("peer transport %q is not yet resolvable", spec.PeerTransport)
	if lastErr != nil {
		reason = fmt.Sprintf(
			"peer transport %q lookup failed: %v", spec.PeerTransport, lastErr,
		)
	}

	return remote, reason
}

// podBoundResolverForServer resolves through sockets bound to the Pod address. A non-empty
// server replaces whatever nameserver the process's own resolver configuration names, keeping
// lookups on the captured configuration.
func podBoundResolverForServer(podAddress, server string) *net.Resolver {
	local := net.ParseIP(podAddress)

	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			dialer := &net.Dialer{Timeout: fabricPeerResolveTimeout}

			if local != nil {
				switch {
				case len(network) >= 3 && network[:3] == "tcp":
					dialer.LocalAddr = &net.TCPAddr{IP: local}
				default:
					dialer.LocalAddr = &net.UDPAddr{IP: local}
				}
			}

			if server != "" {
				address = net.JoinHostPort(server, "53")
			}

			return dialer.DialContext(ctx, network, address)
		},
	}
}

// podBoundResolver resolves through sockets bound to the Pod address, keeping DNS on the
// sidecar-owned transport table regardless of main-table state.
func podBoundResolver(podAddress string) *net.Resolver {
	return podBoundResolverForServer(podAddress, "")
}

// podFabricUnderlayMTU observes the MTU of the interface carrying the Pod address. A zero
// result means the underlay interface was not identifiable; consumers fall back to their
// conservative defaults.
func podFabricUnderlayMTU(podAddress netip.Addr) (int, error) {
	links, err := netlink.LinkList()
	if err != nil {
		return 0, fmt.Errorf("listing Pod interfaces: %w", err)
	}

	underlay := 0

	for _, link := range links {
		addresses, addressErr := netlink.AddrList(link, netlink.FAMILY_V4)
		if addressErr != nil {
			continue
		}

		for _, address := range addresses {
			if address.IP.String() == podAddress.String() {
				underlay = link.Attrs().MTU
			}
		}
	}

	return underlay, nil
}

// EnsureHostInterface realizes one host Link: the device-facing leg stays in the Pod namespace
// and its peer moves into the worker namespace through the sidecar's read-only namespace
// handle. Both ends die with either namespace, so forced Pod deletion leaves no worker residue.
//
//nolint:funlen // one linear create-mark-move-name pass with explicit rollback.
func (o netlinkOperations) EnsureHostInterface(spec HostInterfaceSpec) error {
	if spec.InterfaceID == "" || spec.InterfaceName == "" || spec.HostInterface == "" ||
		spec.Owner == "" || spec.OwnerPrefix == "" ||
		!strings.HasPrefix(spec.Owner, spec.OwnerPrefix) {
		return errors.New("host interface spec is incomplete")
	}

	if o.namespace == nil {
		return errors.New("host Link realization requires the worker namespace handle")
	}

	pod, present, err := lookupLink(spec.InterfaceName)
	if err != nil {
		return err
	}

	if present {
		if pod.Attrs().Alias != spec.Owner {
			return o.adoptHostInterface(spec, pod)
		}

		if pod.Attrs().Flags&net.FlagUp == 0 {
			if err = netlink.LinkSetUp(pod); err != nil {
				return fmt.Errorf("bringing host Link Pod leg up: %w", err)
			}
		}

		return nil
	}

	transferName := hostTransferLegName(spec.InterfaceID)

	attributes := netlink.NewLinkAttrs()
	attributes.Name = spec.InterfaceName
	attributes.Alias = spec.Owner

	if spec.MTU != 0 {
		attributes.MTU = spec.MTU
	}

	pair := netlink.NewVeth(attributes)
	pair.PeerName = transferName

	if spec.MTU > 0 {
		pair.PeerMTU = uint32(spec.MTU) //nolint:gosec // positive bound checked.
	}

	if err = netlink.LinkAdd(pair); err != nil {
		return fmt.Errorf("creating host Link pair %q: %w", spec.InterfaceName, err)
	}

	cleanup := func() {
		if link, exists, lookupErr := lookupLink(spec.InterfaceName); lookupErr == nil && exists {
			_ = netlink.LinkDel(link)
		}
	}

	// LinkAdd does not persist the alias attribute; ownership must be marked explicitly or a
	// sidecar restart would treat its own leg as foreign state.
	created, present, err := lookupLink(spec.InterfaceName)
	if err != nil || !present {
		cleanup()

		return errors.Join(errors.New("host Link Pod leg vanished after creation"), err)
	}

	if err = netlink.LinkSetAlias(created, spec.Owner); err != nil {
		cleanup()

		return fmt.Errorf("marking host Link Pod leg ownership: %w", err)
	}

	transfer, present, err := lookupLink(transferName)
	if err != nil || !present {
		cleanup()

		return errors.Join(errors.New("host Link transfer leg vanished after creation"), err)
	}

	handleFile, err := os.Open(o.namespace.WorkerPath())
	if err != nil {
		cleanup()

		return fmt.Errorf("opening worker namespace handle: %w", err)
	}

	defer handleFile.Close() //nolint:errcheck // read-only handle.

	if err = netlink.LinkSetNsFd(transfer, int(handleFile.Fd())); err != nil {
		cleanup()

		return fmt.Errorf("moving host Link leg to the worker namespace: %w", err)
	}

	if err = o.namespace.Execute(func() error {
		// The package-level netlink API caches its socket in the namespace of first use, so
		// worker-side operations need a handle created inside the entered namespace.
		handle, handleErr := netlink.NewHandle()
		if handleErr != nil {
			return fmt.Errorf("opening worker namespace netlink handle: %w", handleErr)
		}

		defer handle.Close()

		moved, lookupErr := handle.LinkByName(transferName)
		if lookupErr != nil {
			return errors.Join(
				errors.New("host Link leg vanished in the worker namespace"),
				lookupErr,
			)
		}

		if renameErr := handle.LinkSetName(moved, spec.HostInterface); renameErr != nil {
			_ = handle.LinkDel(moved)

			return fmt.Errorf(
				"naming worker interface %q: %w",
				spec.HostInterface,
				renameErr,
			)
		}

		renamed, lookupErr := handle.LinkByName(spec.HostInterface)
		if lookupErr != nil {
			return errors.Join(errors.New("worker interface vanished after rename"), lookupErr)
		}

		if aliasErr := handle.LinkSetAlias(renamed, spec.Owner); aliasErr != nil {
			return fmt.Errorf("marking worker interface ownership: %w", aliasErr)
		}

		return handle.LinkSetUp(renamed)
	}); err != nil {
		cleanup()

		return err
	}

	pod, present, err = lookupLink(spec.InterfaceName)
	if err != nil || !present {
		return errors.Join(errors.New("host Link Pod leg vanished after transfer"), err)
	}

	if err = netlink.LinkSetUp(pod); err != nil {
		return fmt.Errorf("bringing host Link Pod leg up: %w", err)
	}

	return nil
}

func (o netlinkOperations) adoptHostInterface(spec HostInterfaceSpec, pod netlink.Link) error {
	if pod.Type() != vethLinkType || !strings.HasPrefix(pod.Attrs().Alias, spec.OwnerPrefix) {
		return fmt.Errorf(
			"host Link Pod interface %q collides with unrelated state",
			spec.InterfaceName,
		)
	}

	peerIndex, err := netlink.VethPeerIndex(&netlink.Veth{LinkAttrs: *pod.Attrs()})
	if err != nil {
		return fmt.Errorf("reading host Link peer for %q: %w", spec.InterfaceName, err)
	}

	err = o.namespace.Execute(func() error {
		handle, handleErr := netlink.NewHandle()
		if handleErr != nil {
			return fmt.Errorf("opening worker namespace netlink handle: %w", handleErr)
		}

		defer handle.Close()

		worker, lookupErr := handle.LinkByIndex(peerIndex)
		if lookupErr != nil {
			return fmt.Errorf("reading host Link worker peer: %w", lookupErr)
		}

		if worker.Type() != vethLinkType ||
			!strings.HasPrefix(worker.Attrs().Alias, spec.OwnerPrefix) ||
			(pod.Attrs().Alias != worker.Attrs().Alias && pod.Attrs().Alias != spec.Owner &&
				worker.Attrs().Alias != spec.Owner) {
			return fmt.Errorf(
				"host Link worker peer for %q has unrelated state",
				spec.InterfaceName,
			)
		}

		existing, existingErr := handle.LinkByName(spec.HostInterface)
		if existingErr == nil && existing.Attrs().Index != worker.Attrs().Index {
			return fmt.Errorf(
				"host Link worker interface %q collides with unrelated state",
				spec.HostInterface,
			)
		}

		if existingErr != nil && !errors.As(existingErr, &netlink.LinkNotFoundError{}) {
			return fmt.Errorf("checking host Link worker interface: %w", existingErr)
		}

		if worker.Attrs().Name != spec.HostInterface {
			if renameErr := handle.LinkSetName(worker, spec.HostInterface); renameErr != nil {
				return fmt.Errorf(
					"renaming adopted worker interface %q: %w",
					spec.HostInterface,
					renameErr,
				)
			}

			worker, lookupErr = handle.LinkByName(spec.HostInterface)
			if lookupErr != nil {
				return fmt.Errorf("reading renamed worker interface: %w", lookupErr)
			}
		}

		if worker.Attrs().Alias != spec.Owner {
			if aliasErr := handle.LinkSetAlias(worker, spec.Owner); aliasErr != nil {
				return fmt.Errorf("adopting worker interface ownership: %w", aliasErr)
			}
		}

		if spec.MTU > 0 && worker.Attrs().MTU != spec.MTU {
			if mtuErr := handle.LinkSetMTU(worker, spec.MTU); mtuErr != nil {
				return fmt.Errorf("setting adopted worker interface MTU: %w", mtuErr)
			}
		}

		if worker.Attrs().Flags&net.FlagUp == 0 {
			if upErr := handle.LinkSetUp(worker); upErr != nil {
				return fmt.Errorf("bringing adopted worker interface up: %w", upErr)
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	if pod.Attrs().Alias != spec.Owner {
		if err = netlink.LinkSetAlias(pod, spec.Owner); err != nil {
			return fmt.Errorf("adopting host Link Pod interface ownership: %w", err)
		}
	}

	if spec.MTU > 0 && pod.Attrs().MTU != spec.MTU {
		if err = netlink.LinkSetMTU(pod, spec.MTU); err != nil {
			return fmt.Errorf("setting adopted host Link Pod interface MTU: %w", err)
		}
	}

	if pod.Attrs().Flags&net.FlagUp == 0 {
		if err = netlink.LinkSetUp(pod); err != nil {
			return fmt.Errorf("bringing adopted host Link Pod interface up: %w", err)
		}
	}

	return nil
}

// SweepTransportState deletes sidecar-owned transport links (fabric pairs and host legs)
// whose owners are no longer part of the desired plan. Deleting either veth end removes both,
// including a host Link's worker-side end.
func (netlinkOperations) SweepTransportState(ownerPrefix string, keepOwners []string) error {
	if ownerPrefix == "" {
		return errors.New("transport sweep owner prefix is empty")
	}

	sweepPodFabricWireLinks(ownerPrefix, keepOwners)

	keep := make(map[string]bool, len(keepOwners))
	for _, owner := range keepOwners {
		keep[owner] = true
	}

	links, err := netlink.LinkList()
	if err != nil {
		return fmt.Errorf("listing interfaces for transport sweep: %w", err)
	}

	for _, link := range links {
		alias := link.Attrs().Alias
		if alias == "" || keep[alias] || len(alias) < len(ownerPrefix) ||
			alias[:len(ownerPrefix)] != ownerPrefix {
			continue
		}

		if link.Type() != "veth" {
			continue
		}

		if err = deleteStaleTransportLink(link); err != nil {
			return fmt.Errorf(
				"sweeping stale transport link %q: %w",
				link.Attrs().Name,
				err,
			)
		}
	}

	return nil
}

func deleteStaleTransportLink(link netlink.Link) error {
	err := netlink.LinkDel(link)
	if errors.Is(err, unix.ENODEV) {
		return nil
	}

	return err
}
