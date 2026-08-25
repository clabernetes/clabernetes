//go:build linux

//nolint:err113,gocognit,gocyclo,mnd,nestif // single-pass boundary logic with structured one-off diagnostics and protocol literals.
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

	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const (
	fabricVTEPLinkType = "vxlan"
	// fabricFilterPriority is the fixed tc filter priority used for fabric redirects.
	fabricFilterPriority = 42
	// fabricPeerResolveTimeout bounds one peer transport name resolution.
	fabricPeerResolveTimeout = 3 * time.Second
)

func fabricShortHash(identity string) string {
	digest := sha256.Sum256([]byte(identity))

	return hex.EncodeToString(digest[:])[:8]
}

// fabricVTEPName derives the sidecar-owned VTEP name for one plan interface.
func fabricVTEPName(interfaceID string) string {
	return "c9sx" + fabricShortHash(interfaceID)
}

// fabricSidecarLegName derives the sidecar-side stitch leg name for one plan interface.
func fabricSidecarLegName(interfaceID string) string {
	return "c9ss" + fabricShortHash(interfaceID)
}

// hostTransferLegName derives the temporary Pod-side name a host Link's worker leg carries
// before it moves into the worker namespace.
func hostTransferLegName(interfaceID string) string {
	return "c9sh" + fabricShortHash(interfaceID)
}

// EnsureFabricEndpoint realizes one cross-Pod endpoint entirely inside the Pod namespace: the
// device leg + sidecar leg pair, the VTEP on the preserved underlay, and the tc stitch between
// them. Every interface in that chain carries one effective MTU -- the requested MTU bounded to
// what the underlay can carry encapsulated -- so the device can never emit a frame the tunnel
// silently drops. A device may adopt, rename, or move the device leg after boot; the sidecar
// leg and VTEP remain sidecar-owned and are converged idempotently.
func (o netlinkOperations) EnsureFabricEndpoint(
	spec FabricEndpointSpec,
) (FabricEndpointResult, error) {
	result := FabricEndpointResult{}

	podAddress, err := netip.ParseAddr(spec.PodAddress)
	if err != nil || !podAddress.Is4() {
		return result, fmt.Errorf("fabric endpoint Pod address %q is invalid", spec.PodAddress)
	}

	if spec.TunnelID <= 0 || spec.InterfaceID == "" || spec.InterfaceName == "" ||
		spec.Owner == "" || spec.OwnerPrefix == "" ||
		!strings.HasPrefix(spec.Owner, spec.OwnerPrefix) {
		return result, errors.New("fabric endpoint spec is incomplete")
	}

	// The encapsulation bound depends only on the Pod underlay, so it is computed before any
	// link exists: the device-facing leg must be born with the effective MTU because a device
	// may adopt and move it where the sidecar can never adjust it again.
	mtu, underlay, err := clampPodFabricMTU(spec.MTU, podAddress)
	if err != nil {
		return result, err
	}

	result.EffectiveMTU = mtu
	result.UnderlayMTU = underlay

	legName := fabricSidecarLegName(spec.InterfaceID)

	if err = ensureFabricPair(spec, legName, mtu); err != nil {
		return result, err
	}

	leg, present, err := lookupLink(legName)
	if err != nil || !present {
		return result, errors.Join(errors.New("fabric sidecar leg is unavailable"), err)
	}

	if err = (netlinkOperations{}).DisableTxChecksumOffload(legName); err != nil {
		return result, err
	}

	if _, present, lookupErr := lookupLink(spec.InterfaceName); lookupErr == nil && present {
		if err = (netlinkOperations{}).DisableTxChecksumOffload(spec.InterfaceName); err != nil {
			return result, err
		}
	}

	remote, resolved, err := o.resolveFabricPeer(spec.PeerTransport, spec.PodAddress)
	if err != nil {
		return result, err
	}

	if !resolved {
		result.Reason = "peer transport is not yet resolvable"

		return result, nil
	}

	vtep, err := ensurePodFabricVTEP(
		fabricVTEPName(spec.InterfaceID),
		spec.Owner,
		spec.TunnelID,
		mtu,
		podAddress,
		remote,
	)
	if err != nil {
		return result, err
	}

	if err = ensurePodFabricRedirect(leg, vtep); err != nil {
		return result, err
	}

	if err = ensurePodFabricRedirect(vtep, leg); err != nil {
		return result, err
	}

	result.Ready = true

	return result, nil
}

// ensureFabricPair converges the device/sidecar veth pair on the effective MTU, tolerating a
// device that adopted and moved the device leg out of the root namespace: an existing
// sidecar-owned leg without a visible device leg is accepted state, never an error and never
// recreated. The sidecar leg converges to the effective MTU exactly; a visible device leg is
// only ever clamped down so a device that deliberately lowered its own MTU is not fought.
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

		if leg.Attrs().Flags&net.FlagUp == 0 {
			if err = netlink.LinkSetUp(leg); err != nil {
				return fmt.Errorf("bringing fabric leg up: %w", err)
			}
		}

		if devicePresent {
			if mtu > 0 && device.Attrs().MTU > mtu {
				if err = netlink.LinkSetMTU(device, mtu); err != nil {
					return fmt.Errorf("clamping fabric device leg MTU: %w", err)
				}
			}

			if device.Attrs().Flags&net.FlagUp == 0 {
				if err = netlink.LinkSetUp(device); err != nil {
					return fmt.Errorf("bringing fabric device leg up: %w", err)
				}
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

		converge := mtu > 0 && link.Attrs().MTU != mtu
		if link.Attrs().Index == device.Attrs().Index {
			converge = mtu > 0 && link.Attrs().MTU > mtu
		}

		if converge {
			if err = netlink.LinkSetMTU(link, mtu); err != nil {
				return fmt.Errorf("setting adopted fabric pair MTU: %w", err)
			}
		}

		if link.Attrs().Flags&net.FlagUp == 0 {
			if err = netlink.LinkSetUp(link); err != nil {
				return fmt.Errorf("bringing adopted fabric pair up: %w", err)
			}
		}
	}

	return nil
}

// resolveFabricPeer resolves a fabric peer transport to one IPv4 address. Absence is unready,
// not an error: headless Service records appear when the peer Pod exists. Lookups bind the Pod
// address so resolution rides the source-scoped transport rule even when a device rewrote the
// main routing table.
func (o netlinkOperations) resolveFabricPeer(
	peerTransport, podAddress string,
) (netip.Addr, bool, error) {
	var remote netip.Addr

	if peerTransport == "" {
		return remote, false, errors.New("fabric endpoint has no peer transport")
	}

	if parsed, err := netip.ParseAddr(peerTransport); err == nil {
		return parsed, true, nil
	}

	resolver := peerAddressResolver(podBoundResolver(podAddress))
	if net.ParseIP(podAddress) == nil && o.resolver != nil {
		resolver = o.resolver
	}

	ctx, cancel := context.WithTimeout(context.Background(), fabricPeerResolveTimeout)
	defer cancel()

	addresses, err := resolver.LookupNetIP(ctx, "ip4", peerTransport)
	if err != nil || len(addresses) == 0 {
		return remote, false, nil
	}

	return addresses[0].Unmap(), true, nil
}

// podBoundResolver resolves through sockets bound to the Pod address, keeping DNS on the
// sidecar-owned transport table regardless of main-table state.
func podBoundResolver(podAddress string) *net.Resolver {
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

			return dialer.DialContext(ctx, network, address)
		},
	}
}

// clampPodFabricMTU bounds the requested MTU to what the Pod underlay can carry encapsulated,
// reporting the observed underlay MTU alongside; a zero underlay means the underlay interface
// was not identifiable and the requested value passes through unbounded.
func clampPodFabricMTU(requested int, podAddress netip.Addr) (int, int, error) {
	underlay := 0

	links, err := netlink.LinkList()
	if err != nil {
		return 0, 0, fmt.Errorf("listing Pod interfaces: %w", err)
	}

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

	if underlay == 0 {
		return requested, 0, nil
	}

	transport := underlay - fabricEncapsulationOverhead
	if transport < 68 {
		return 0, underlay, fmt.Errorf(
			"fabric underlay MTU %d cannot carry encapsulation",
			underlay,
		)
	}

	if requested == 0 || requested > transport {
		return transport, underlay, nil
	}

	return requested, underlay, nil
}

// ensurePodFabricVTEP converges the sidecar-owned VTEP terminating on the Pod underlay.
func ensurePodFabricVTEP(
	name, owner string,
	tunnelID, mtu int,
	localAddress, remoteAddress netip.Addr,
) (netlink.Link, error) {
	localIP := net.IP(localAddress.AsSlice())
	remoteIP := net.IP(remoteAddress.AsSlice())

	existing, exists, err := lookupLink(name)
	if err != nil {
		return nil, err
	}

	if exists {
		if existing.Type() != fabricVTEPLinkType || existing.Attrs().Alias != owner {
			return nil, fmt.Errorf("VTEP name %q collides with unrelated state", name)
		}

		vxlan, isVXLAN := existing.(*netlink.Vxlan)

		conforms := isVXLAN && vxlan.VxlanId == tunnelID &&
			vxlan.SrcAddr.Equal(localIP) && vxlan.Group.Equal(remoteIP) &&
			vxlan.Port == clabernetesconstants.VXLANServicePort && !vxlan.Learning &&
			(mtu == 0 || vxlan.Attrs().MTU == mtu)
		if conforms {
			if existing.Attrs().Flags&net.FlagUp == 0 {
				if err = netlink.LinkSetUp(existing); err != nil {
					return nil, fmt.Errorf("bringing VTEP up: %w", err)
				}
			}

			return existing, nil
		}

		if err = netlink.LinkDel(existing); err != nil {
			return nil, fmt.Errorf("replacing stale VTEP %q: %w", name, err)
		}
	}

	attributes := netlink.NewLinkAttrs()
	attributes.Name = name

	if mtu != 0 {
		attributes.MTU = mtu
	}

	vtep := &netlink.Vxlan{
		LinkAttrs: attributes,
		VxlanId:   tunnelID,
		SrcAddr:   localIP,
		Group:     remoteIP,
		Port:      clabernetesconstants.VXLANServicePort,
		Learning:  false,
	}
	if err = netlink.LinkAdd(vtep); err != nil {
		return nil, fmt.Errorf("creating fabric VTEP %q: %w", name, err)
	}

	created, exists, err := lookupLink(name)
	if err != nil || !exists {
		return nil, errors.Join(errors.New("fabric VTEP vanished after creation"), err)
	}

	if err = netlink.LinkSetAlias(created, owner); err != nil {
		return nil, errors.Join(
			fmt.Errorf("marking fabric VTEP ownership: %w", err),
			netlink.LinkDel(created),
		)
	}

	if err = netlink.LinkSetUp(created); err != nil {
		return nil, fmt.Errorf("bringing VTEP up: %w", err)
	}

	return created, nil
}

// ensurePodFabricRedirect wires one direction of the stitch: every frame received on from is
// redirected to the egress of to, with checksums materialized first, giving point-to-point wire
// semantics without bridge state.
func ensurePodFabricRedirect(from, to netlink.Link) error {
	qdisc := &netlink.GenericQdisc{
		QdiscAttrs: netlink.QdiscAttrs{
			LinkIndex: from.Attrs().Index,
			Handle:    netlink.MakeHandle(0xffff, 0),
			Parent:    netlink.HANDLE_CLSACT,
		},
		QdiscType: "clsact",
	}
	if err := netlink.QdiscReplace(qdisc); err != nil {
		return fmt.Errorf("installing fabric redirect qdisc on %q: %w", from.Attrs().Name, err)
	}

	existing, err := netlink.FilterList(from, netlink.HANDLE_MIN_INGRESS)
	if err != nil {
		return fmt.Errorf("listing fabric redirect filters on %q: %w", from.Attrs().Name, err)
	}

	for _, candidate := range existing {
		if candidate.Attrs().Priority != fabricFilterPriority {
			continue
		}

		matchAll, isMatchAll := candidate.(*netlink.MatchAll)
		if isMatchAll && len(matchAll.Actions) == 1 {
			mirred, isMirred := matchAll.Actions[0].(*netlink.MirredAction)

			if isMirred && mirred.Ifindex == to.Attrs().Index {
				return nil
			}
		}

		if err = netlink.FilterDel(candidate); err != nil {
			return fmt.Errorf(
				"removing stale fabric redirect filter on %q: %w",
				from.Attrs().Name,
				err,
			)
		}
	}

	// Checksum completeness comes from disabling TX offload on both veth legs rather than an
	// act_csum action: csum-before-mirred into a VXLAN device silently drops every frame on
	// current kernels, while software-checksummed frames stitch cleanly.
	filter := &netlink.MatchAll{
		FilterAttrs: netlink.FilterAttrs{
			LinkIndex: from.Attrs().Index,
			Parent:    netlink.HANDLE_MIN_INGRESS,
			Priority:  fabricFilterPriority,
			Protocol:  unix.ETH_P_ALL,
		},
		Actions: []netlink.Action{netlink.NewMirredAction(to.Attrs().Index)},
	}
	if err = netlink.FilterAdd(filter); err != nil {
		return fmt.Errorf("installing fabric redirect filter on %q: %w", from.Attrs().Name, err)
	}

	return nil
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

	if err = netlink.LinkSetNsFd(transfer, int(handleFile.Fd())); err != nil { //nolint:gosec // kernel-issued fd.
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

// SweepTransportState deletes sidecar-owned transport links (fabric pairs, VTEPs, host legs)
// whose owners are no longer part of the desired plan. Deleting either veth end removes both,
// including a host Link's worker-side end.
func (netlinkOperations) SweepTransportState(ownerPrefix string, keepOwners []string) error {
	if ownerPrefix == "" {
		return errors.New("transport sweep owner prefix is empty")
	}

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

		if link.Type() != "veth" && link.Type() != fabricVTEPLinkType {
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
