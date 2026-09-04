//go:build linux

//nolint:err113,gocognit,gocyclo,mnd,nestif // single-pass boundary logic with structured one-off diagnostics and protocol literals.
package directruntime

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"

	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// interpositionOwnerAlias marks sidecar-created interposition interfaces so reconciliation can
// distinguish them from device- and CNI-owned links.
const interpositionOwnerAlias = "c9s:interposition:v1"

// meshVTEPLinkType is the kernel link type of the management mesh VTEP.
const meshVTEPLinkType = "vxlan"

// capturedRoute is one CNI-installed IPv4 route snapshotted before the transport rename. The
// exact set must be replayed: CNIs like kindnet route the Pod subnet via the gateway with a
// scope-link /32 for the gateway itself, and the kernel auto-connected prefix route the rename
// resurrects must not survive.
type capturedRoute struct {
	Destination string `json:"destination,omitempty"`
	Gateway     string `json:"gateway,omitempty"`
	Source      string `json:"source,omitempty"`
	Scope       int    `json:"scope"`
}

func captureTransportRoutes(linkIndex int) ([]capturedRoute, error) {
	routes, err := netlink.RouteListFiltered(
		netlink.FAMILY_V4,
		&netlink.Route{Table: unix.RT_TABLE_MAIN},
		netlink.RT_FILTER_TABLE,
	)
	if err != nil {
		return nil, fmt.Errorf("listing transport routes: %w", err)
	}

	captured := []capturedRoute{}

	for _, route := range routes {
		if route.LinkIndex != linkIndex {
			continue
		}

		entry := capturedRoute{Scope: int(route.Scope)}

		if route.Dst != nil {
			entry.Destination = route.Dst.String()
		}

		if route.Gw != nil {
			entry.Gateway = route.Gw.String()
		}

		if route.Src != nil {
			entry.Source = route.Src.String()
		}

		captured = append(captured, entry)
	}

	return captured, nil
}

// replayTransportRoutes converges the given table to exactly the captured set for the
// transport interface: scope-link routes first (gateway reachability), then gatewayed routes,
// and any interface route absent from the snapshot (the resurrected connected prefix) removed.
func replayTransportRoutes(
	linkIndex, table int,
	captured []capturedRoute,
	removeForeign bool,
) error {
	toRoute := func(entry capturedRoute) *netlink.Route {
		route := &netlink.Route{
			LinkIndex: linkIndex,
			Table:     table,
			Scope:     netlink.Scope(entry.Scope), //nolint:gosec // kernel scope byte round-trips.
		}

		if entry.Destination != "" {
			if _, parsed, err := net.ParseCIDR(entry.Destination); err == nil {
				route.Dst = parsed
			}
		}

		if entry.Gateway != "" {
			route.Gw = net.ParseIP(entry.Gateway)
		}

		if entry.Source != "" {
			route.Src = net.ParseIP(entry.Source)
		}

		return route
	}

	for _, pass := range []bool{true, false} {
		for _, entry := range captured {
			isLink := entry.Gateway == ""
			if isLink != pass {
				continue
			}

			if err := netlink.RouteReplace(toRoute(entry)); err != nil {
				return fmt.Errorf("replaying transport route %+v: %w", entry, err)
			}
		}
	}

	if !removeForeign {
		return nil
	}

	desired := map[string]bool{}
	for _, entry := range captured {
		desired[entry.Destination+"|"+entry.Gateway] = true
	}

	current, err := netlink.RouteListFiltered(
		netlink.FAMILY_V4,
		&netlink.Route{Table: table},
		netlink.RT_FILTER_TABLE,
	)
	if err != nil {
		return fmt.Errorf("listing routes for replay cleanup: %w", err)
	}

	for _, route := range current {
		if route.LinkIndex != linkIndex {
			continue
		}

		destination := ""
		if route.Dst != nil {
			destination = route.Dst.String()
		}

		gateway := ""
		if route.Gw != nil {
			gateway = route.Gw.String()
		}

		if desired[destination+"|"+gateway] {
			continue
		}

		if err := netlink.RouteDel(&route); err != nil {
			return fmt.Errorf("removing foreign transport route %v: %w", route, err)
		}
	}

	return nil
}

// EnsureInterposition converges the Pod namespace to the interposition spec: the CNI interface
// is preserved under the sidecar-owned transport name, the synthetic management pair exists with
// the device-expected identity, the routed management mesh tunnel endpoint carries the Pod's
// derived identity, the unconditional hardening baseline is applied, Kubernetes transport lives
// in the sidecar-owned policy table, and (when asked) the per-peer mesh state is converged to
// the spec's peer set. Every step is idempotent; the device's own state (main table, its
// chains, its sysctls) is never touched.
func (o netlinkOperations) EnsureInterposition(spec InterpositionSpec) error {
	podAddress, err := netip.ParseAddr(spec.PodAddress)
	if err != nil || !podAddress.Is4() {
		return fmt.Errorf("interposition pod address %q is invalid", spec.PodAddress)
	}

	managementPrefix, err := netip.ParsePrefix(spec.ManagementIPv4)
	if err != nil {
		return fmt.Errorf("interposition management address %q is invalid", spec.ManagementIPv4)
	}

	gateway, err := netip.ParseAddr(spec.GatewayIPv4)
	if err != nil {
		return fmt.Errorf("interposition gateway %q is invalid", spec.GatewayIPv4)
	}

	switch spec.DeviceInterface {
	case spec.TransportInterface, spec.RouterInterface, MeshVTEPName:
		return fmt.Errorf(
			"interposition device interface %q collides with a sidecar-owned name",
			spec.DeviceInterface,
		)
	}

	gatewayMAC, err := net.ParseMAC(spec.MeshGatewayMAC)
	if err != nil || spec.MeshTunnelID <= 0 {
		return fmt.Errorf(
			"interposition mesh membership is incomplete (tunnel %d, gateway MAC %q)",
			spec.MeshTunnelID,
			spec.MeshGatewayMAC,
		)
	}

	meshMAC, err := net.ParseMAC(spec.MeshMAC)
	if err != nil {
		return fmt.Errorf("interposition mesh tunnel-endpoint identity %q is invalid", spec.MeshMAC)
	}

	capturedRoutes, transportIndex, err := preserveTransportInterface(spec, podAddress)
	if err != nil {
		return err
	}

	// A device may strip routes from every table at boot; the route set captured while the CNI
	// state was pristine is the durable truth for re-assertion.
	capturedRoutes = rememberTransportRoutes(spec.StateDirectory, capturedRoutes)

	// Mesh packets cross the Pod underlay kernel-encapsulated; every mesh element carries the
	// underlay-bounded MTU so device-derived segment sizes fit the cross-Pod path.
	meshMTU, err := podManagementMeshMTU(podAddress)
	if err != nil {
		return err
	}

	if err := ensureSyntheticPair(
		spec, managementPrefix, gateway, gatewayMAC, meshMTU,
	); err != nil {
		return err
	}

	vtep, err := ensureMeshVTEP(spec, podAddress, meshMAC, meshMTU)
	if err != nil {
		return err
	}

	// A device whose management port size is fixed by the application cannot be told the mesh
	// MTU; clamping the segment size the handshake advertises keeps its management flows inside
	// what the mesh carries.
	if err := ensureMeshSegmentClamp(meshMTU); err != nil {
		return err
	}

	if err := applyInterpositionSysctls(spec); err != nil {
		return err
	}

	if err := o.DisableTxChecksumOffload(spec.RouterInterface); err != nil {
		return err
	}

	// The device leg may already be renamed or adopted by a running device; offload state on it
	// is best-effort after boot because the rename moves the ethtool identity with the link.
	if _, present, lookupErr := lookupLink(spec.DeviceInterface); lookupErr == nil && present {
		if err := o.DisableTxChecksumOffload(spec.DeviceInterface); err != nil {
			return err
		}
	}

	if err := ensureTransportTable(
		spec, podAddress, managementPrefix, capturedRoutes, transportIndex, vtep,
	); err != nil {
		return err
	}

	if !spec.ReconcileMeshPeers {
		return nil
	}

	return ensureMeshPeers(spec, vtep, podAddress, managementPrefix.Addr())
}

// preserveTransportInterface renames the CNI interface carrying the Pod address to the
// sidecar-owned transport name, restoring its default route, and returns the transport gateway.
func preserveTransportInterface(
	spec InterpositionSpec,
	podAddress netip.Addr,
) ([]capturedRoute, int, error) {
	transport, present, err := lookupLink(spec.TransportInterface)
	if err != nil {
		return nil, 0, err
	}

	if !present {
		// First pass: find the interface carrying the exact Pod address, snapshot its exact
		// CNI-installed route set, and preserve both through the rename.
		original, findErr := linkByAddress(podAddress)
		if findErr != nil {
			return nil, 0, findErr
		}

		captured, captureErr := captureTransportRoutes(original.Attrs().Index)
		if captureErr != nil {
			return nil, 0, captureErr
		}

		if err = netlink.LinkSetDown(original); err != nil {
			return nil, 0, fmt.Errorf("preparing transport interface rename: %w", err)
		}

		if err = netlink.LinkSetName(original, spec.TransportInterface); err != nil {
			_ = netlink.LinkSetUp(original)

			return nil, 0, fmt.Errorf("renaming transport interface: %w", err)
		}

		if err = netlink.LinkSetUp(original); err != nil {
			return nil, 0, fmt.Errorf("restoring transport interface: %w", err)
		}

		transport, _, err = lookupLink(spec.TransportInterface)
		if err != nil || transport == nil {
			return nil, 0, fmt.Errorf("transport interface vanished after rename: %w", err)
		}

		// The rename resurrects the kernel auto-connected prefix route, which the CNI may
		// intentionally not use; converge main back to the exact snapshot once, before any
		// device starts. Main is never touched again afterwards.
		if err = replayTransportRoutes(
			transport.Attrs().Index,
			unix.RT_TABLE_MAIN,
			captured,
			true,
		); err != nil {
			return nil, 0, err
		}

		return captured, transport.Attrs().Index, nil
	}

	// Steady state: the preserved interface must still carry the Pod address.
	addresses, err := netlink.AddrList(transport, netlink.FAMILY_V4)
	if err != nil {
		return nil, 0, fmt.Errorf("reading transport addresses: %w", err)
	}

	carried := false

	for _, address := range addresses {
		if address.IP.String() == podAddress.String() {
			carried = true

			break
		}
	}

	if !carried {
		// A device enumerating the shared namespace may flush the sidecar-owned transport
		// address (cSRX strips every interface it inventories at boot). The address is
		// kubelet-assigned state the sidecar owns, so restore it instead of failing the
		// helper into a restart loop the device would immediately re-break.
		restore := &netlink.Addr{IPNet: &net.IPNet{
			IP:   podAddress.AsSlice(),
			Mask: net.CIDRMask(32, 32),
		}}
		if err = netlink.AddrAdd(transport, restore); err != nil {
			return nil, 0, fmt.Errorf(
				"restoring the Pod address %q on preserved transport %q: %w",
				spec.PodAddress,
				spec.TransportInterface,
				err,
			)
		}

		fmt.Fprintf(
			os.Stderr,
			"connectivity: restored Pod address %s on preserved transport %s\n",
			spec.PodAddress,
			spec.TransportInterface,
		)
	}

	return nil, transport.Attrs().Index, nil
}

// linkByAddress finds the exact interface carrying the given address.
func linkByAddress(address netip.Addr) (netlink.Link, error) {
	links, err := netlink.LinkList()
	if err != nil {
		return nil, fmt.Errorf("listing interfaces: %w", err)
	}

	for _, link := range links {
		addresses, addrErr := netlink.AddrList(link, netlink.FAMILY_V4)
		if addrErr != nil {
			continue
		}

		for _, candidate := range addresses {
			if candidate.IP.String() == address.String() {
				return link, nil
			}
		}
	}

	return nil, fmt.Errorf("no interface carries the Pod address %q", address)
}

// podManagementMeshMTU bounds the management mesh MTU to what the Pod underlay can carry
// kernel-encapsulated; a zero result means the underlay was not identifiable and mesh elements
// keep their defaults.
func podManagementMeshMTU(podAddress netip.Addr) (int, error) {
	underlay, err := podFabricUnderlayMTU(podAddress)
	if err != nil || underlay == 0 {
		return 0, err
	}

	meshMTU := underlay - fabricEncapsulationOverhead
	if meshMTU < 68 {
		return 0, fmt.Errorf(
			"management mesh underlay MTU %d cannot carry encapsulation",
			underlay,
		)
	}

	return meshMTU, nil
}

// ensureSyntheticPair creates the synthetic management pair: the device leg with the plan
// identity and address, and the router leg carrying the gateway address and identity. The two
// legs are the two ends of one veth, so every frame the device sends beyond its own leg
// arrives on the router leg to be routed.
func ensureSyntheticPair(
	spec InterpositionSpec,
	managementPrefix netip.Prefix,
	gateway netip.Addr,
	gatewayMAC net.HardwareAddr,
	meshMTU int,
) error {
	// The router leg is the durable presence marker: unlike the device leg it is never renamed
	// or adopted by a device (a device may even move the device leg into a namespace of its
	// own; the router leg stays behind as the pair's far end).
	router, present, err := lookupLink(spec.RouterInterface)
	if err != nil {
		return err
	}

	if !present {
		attributes := netlink.NewLinkAttrs()
		attributes.Name = spec.DeviceInterface
		attributes.Alias = interpositionOwnerAlias

		if meshMTU != 0 {
			attributes.MTU = meshMTU
		}

		if spec.DeviceMAC != "" {
			mac, macErr := net.ParseMAC(spec.DeviceMAC)
			if macErr != nil {
				return fmt.Errorf("interposition device MAC %q is invalid", spec.DeviceMAC)
			}

			attributes.HardwareAddr = mac
		}

		pair := &netlink.Veth{
			LinkAttrs:        attributes,
			PeerName:         spec.RouterInterface,
			PeerHardwareAddr: gatewayMAC,
		}
		if meshMTU != 0 {
			pair.PeerMTU = uint32(meshMTU) //nolint:gosec // bounded by the underlay MTU.
		}

		if err = netlink.LinkAdd(pair); err != nil {
			return fmt.Errorf("creating synthetic management pair: %w", err)
		}

		if err = adoptSyntheticLegs(
			meshMTU, spec.RouterInterface, spec.DeviceInterface,
		); err != nil {
			return err
		}

		router, _, err = lookupLink(spec.RouterInterface)
		if err != nil || router == nil {
			return fmt.Errorf("router leg vanished after creation: %w", err)
		}
	}

	// The gateway identity is pinned: it is what every proxy ARP answer for a peer carries, so
	// a device sees one stable next hop for everything beyond its own leg.
	if !bytesEqualMAC(router.Attrs().HardwareAddr, gatewayMAC) {
		if err = netlink.LinkSetHardwareAddr(router, gatewayMAC); err != nil {
			return fmt.Errorf("pinning router leg gateway identity: %w", err)
		}
	}

	if err = addressDeviceLeg(spec); err != nil {
		return err
	}

	prefixLength := managementPrefix.Bits()

	routerAddress, err := netlink.ParseAddr(
		fmt.Sprintf("%s/%d", gateway, prefixLength),
	)
	if err != nil {
		return fmt.Errorf("parsing gateway address: %w", err)
	}

	if err = netlink.AddrReplace(router, routerAddress); err != nil {
		return fmt.Errorf("addressing router leg: %w", err)
	}

	if spec.GatewayIPv6 != "" && spec.ManagementIPv6 != "" {
		if v6Prefix, v6Err := netip.ParsePrefix(spec.ManagementIPv6); v6Err == nil {
			if v6Gateway, gwErr := netlink.ParseAddr(
				fmt.Sprintf("%s/%d", spec.GatewayIPv6, v6Prefix.Bits()),
			); gwErr == nil {
				_ = netlink.AddrReplace(router, v6Gateway)
			}
		}
	}

	if err = netlink.LinkSetUp(router); err != nil {
		return fmt.Errorf("bringing router leg up: %w", err)
	}

	return nil
}

// addressDeviceLeg addresses the device leg when it is still present and untouched. The leg
// may have been renamed or moved by the device after adoption; only a present leg is addressed,
// and only with sidecar-owned addresses.
func addressDeviceLeg(spec InterpositionSpec) error {
	device, present, err := lookupLink(spec.DeviceInterface)
	if err != nil || !present {
		return nil //nolint:nilerr // a moved or renamed leg is the device's to own.
	}

	deviceAddress, err := netlink.ParseAddr(spec.ManagementIPv4)
	if err != nil {
		return fmt.Errorf("parsing management address: %w", err)
	}

	addresses, err := netlink.AddrList(device, netlink.FAMILY_V4)
	if err != nil {
		return fmt.Errorf("reading device leg addresses: %w", err)
	}

	// A device that adopted the leg and stripped its kernel address owns the leg's addressing
	// from then on; only an untouched leg is (re)addressed, so the sidecar converges the
	// pre-boot state without fighting the device afterwards.
	if device.Attrs().Alias == interpositionOwnerAlias && len(addresses) == 0 &&
		device.Attrs().OperState != netlink.OperUp {
		if err = netlink.AddrReplace(device, deviceAddress); err != nil {
			return fmt.Errorf("addressing device leg: %w", err)
		}
	}

	if spec.ManagementIPv6 != "" {
		if v6, v6Err := netlink.ParseAddr(spec.ManagementIPv6); v6Err == nil {
			v6Addresses, _ := netlink.AddrList(device, netlink.FAMILY_V6)
			if device.Attrs().Alias == interpositionOwnerAlias && len(v6Addresses) == 0 {
				_ = netlink.AddrReplace(device, v6)
			}
		}
	}

	if err = netlink.LinkSetUp(device); err != nil {
		return fmt.Errorf("bringing device leg up: %w", err)
	}

	return nil
}

// applyInterpositionSysctls applies the unconditional hardening baseline.
func applyInterpositionSysctls(spec InterpositionSpec) error {
	settings := [][2]string{
		{"net.ipv4.ip_forward", "1"},
		{"net.ipv4.conf.all.rp_filter", "0"},
		// A new network namespace copies the init namespace's IPv4 devconf template, so hosts
		// that set rp_filter (Ubuntu ships 2) poison conf/default here -- and every interface a
		// device creates later (SR Linux's internal management-gateway leg, for one) inherits
		// that value at creation, where the effective filter is max(all, interface). Management
		// egress rides an asymmetric internal-gateway path that reverse-path validation then
		// drops, so the template must be cleared before the device boots.
		{"net.ipv4.conf.default.rp_filter", "0"},
		{"net.ipv4.conf." + spec.RouterInterface + ".rp_filter", "0"},
		{"net.ipv4.conf." + spec.RouterInterface + ".accept_local", "1"},
		{"net.ipv4.conf." + spec.RouterInterface + ".forwarding", "1"},
		// ARP-flux scoping: without arp_ignore=1 every interface of the namespace answers ARP
		// for the gateway, so gateway resolution would return multiple identities.
		{"net.ipv4.conf." + spec.RouterInterface + ".arp_ignore", "1"},
		{"net.ipv4.conf." + MeshVTEPName + ".arp_ignore", "1"},
		// The router leg answers ARP for every remote peer with the gateway identity: the
		// device keeps its connected management route and resolves peers exactly as on a shared
		// segment, while the frames it then sends are routed to the peer's tunnel endpoint. The
		// kernel proxies only targets whose route leaves through another interface, which is
		// precisely the management subnet route via the mesh tunnel endpoint; the Pod's own
		// address (routed back to the router leg) and the gateway (local) are never proxied.
		// The default proxy delay exists to let a real host answer first; there is none here.
		{"net.ipv4.conf." + spec.RouterInterface + ".proxy_arp", "1"},
		{"net.ipv4.neigh." + spec.RouterInterface + ".proxy_delay", "0"},
	}

	if spec.ManagementIPv6 != "" && spec.GatewayIPv6 != "" {
		// IPv6 peers ride the same routed path; the kernel gates IPv6 forwarding on the
		// namespace-wide setting, and the router leg proxies neighbor discovery for the peer
		// addresses the sidecar lists on it.
		settings = append(settings,
			[2]string{"net.ipv6.conf.all.forwarding", "1"},
			[2]string{"net.ipv6.conf." + spec.RouterInterface + ".proxy_ndp", "1"},
			[2]string{"net.ipv6.conf." + MeshVTEPName + ".disable_ipv6", "0"},
			[2]string{"net.ipv6.conf." + MeshVTEPName + ".accept_ra", "0"},
		)
	} else {
		// Without an IPv6 management identity the tunnel endpoint never sources neighbor
		// discovery or link-local traffic onto the mesh.
		settings = append(
			settings,
			[2]string{"net.ipv6.conf." + MeshVTEPName + ".disable_ipv6", "1"},
		)
	}

	// The device leg's forwarding stays off so a device keeping the physical leg in the Pod
	// namespace (with its own delivery mechanism) never sees kernel re-forwarding loops, and
	// its ARP responder is scoped: a single-namespace kind shares its stack with the router
	// leg, so without arp_ignore=1 it would flux-answer ARP for the gateway. The leg may
	// already be renamed by the device; missing is fine.
	if _, present, err := lookupLink(spec.DeviceInterface); err == nil && present {
		settings = append(
			settings,
			[2]string{"net.ipv4.conf." + spec.DeviceInterface + ".forwarding", "0"},
			[2]string{"net.ipv4.conf." + spec.DeviceInterface + ".arp_ignore", "1"},
		)
	}

	operations := netlinkOperations{}

	for _, setting := range settings {
		if err := operations.EnsureSysctl(setting[0], setting[1]); err != nil {
			return fmt.Errorf("applying %s=%s: %w", setting[0], setting[1], err)
		}
	}

	return clearExistingReversePathFilters()
}

// clearExistingReversePathFilters zeroes rp_filter on every interface already present in the Pod
// namespace: interfaces created before the interposition baseline ran (the CNI transport, the
// mesh elements) captured the inherited conf/default template, and conf/all cannot mask them
// because the kernel takes the maximum of the two. Direct writes are used because interface
// names may contain dots. A concurrently removed interface is not an error.
func clearExistingReversePathFilters() error {
	const confRoot = "/proc/sys/net/ipv4/conf"

	entries, err := os.ReadDir(confRoot)
	if err != nil {
		return fmt.Errorf("listing IPv4 interface configuration: %w", err)
	}

	for _, entry := range entries {
		path := filepath.Join(confRoot, entry.Name(), "rp_filter")

		if err := os.WriteFile(path, []byte("0"), 0o644); err != nil { //nolint:gosec // kernel-owned sysctl file.
			if os.IsNotExist(err) {
				continue
			}

			return fmt.Errorf("clearing reverse-path filter for %q: %w", entry.Name(), err)
		}
	}

	return nil
}

// ensureTransportTable owns Kubernetes transport in a dedicated policy table selected ahead of
// the main table, so device rewrites of main never affect it. The table replays the exact
// captured CNI route set plus the management routes: the Pod's own management address via the
// router leg and the management subnet via the mesh tunnel endpoint.
//
//nolint:funlen // one linear rule-and-route convergence pass.
func ensureTransportTable(
	spec InterpositionSpec,
	podAddress netip.Addr,
	managementPrefix netip.Prefix,
	captured []capturedRoute,
	transportIndex int,
	vtep netlink.Link,
) error {
	if transportIndex == 0 {
		transport, present, err := lookupLink(spec.TransportInterface)
		if err != nil || !present {
			return errors.Join(
				fmt.Errorf("transport interface %q is unavailable", spec.TransportInterface),
				err,
			)
		}

		transportIndex = transport.Attrs().Index
	}

	router, present, err := lookupLink(spec.RouterInterface)
	if err != nil || !present {
		return errors.Join(
			fmt.Errorf("router interface %q is unavailable", spec.RouterInterface),
			err,
		)
	}

	if len(captured) != 0 {
		if err = replayTransportRoutes(
			transportIndex,
			interpositionTransportTable,
			captured,
			false,
		); err != nil {
			return err
		}
	}

	managementAddress := &net.IPNet{
		IP:   managementPrefix.Addr().AsSlice(),
		Mask: net.CIDRMask(32, 32),
	}

	managementSubnet := &net.IPNet{
		IP:   managementPrefix.Masked().Addr().AsSlice(),
		Mask: net.CIDRMask(managementPrefix.Bits(), 32),
	}

	if err = ensureManagementRoutes(
		router, vtep, managementAddress, managementSubnet,
	); err != nil {
		return err
	}

	rules, err := netlink.RuleList(netlink.FAMILY_V4)
	if err != nil {
		return fmt.Errorf("listing routing rules: %w", err)
	}

	// The management rule covers exactly the Pod's own management address: hooks, the
	// Pod-address translation, and mesh-delivered peer traffic reach the local device through
	// the router leg, while the device's own peer-bound traffic selects the table through the
	// router rule and follows the subnet route to the mesh tunnel endpoint.

	haveRouterRule, haveTransportRule, haveManagementRule := false, false, false

	for _, rule := range rules {
		if rule.Table != interpositionTransportTable {
			continue
		}

		switch rule.Priority {
		case interpositionRouterRulePriority:
			haveRouterRule = rule.IifName == spec.RouterInterface
		case interpositionTransportRulePriority:
			haveTransportRule = true
		case interpositionManagementRulePriority:
			if rule.Dst != nil && rule.Dst.String() == managementAddress.String() {
				haveManagementRule = true

				continue
			}

			// A subnet-wide rule from an earlier shape would hijack peer management
			// addresses into the isolated gateway leg; converge it away.
			stale := rule
			if err = netlink.RuleDel(&stale); err != nil {
				return fmt.Errorf("removing stale management transport rule: %w", err)
			}
		}
	}

	if !haveRouterRule {
		rule := netlink.NewRule()
		rule.Priority = interpositionRouterRulePriority
		rule.Table = interpositionTransportTable
		rule.IifName = spec.RouterInterface

		if err = netlink.RuleAdd(rule); err != nil {
			return fmt.Errorf("asserting router transport rule: %w", err)
		}
	}

	if !haveTransportRule {
		rule := netlink.NewRule()
		rule.Priority = interpositionTransportRulePriority
		rule.Table = interpositionTransportTable
		rule.Src = &net.IPNet{
			IP:   podAddress.AsSlice(),
			Mask: net.CIDRMask(32, 32),
		}

		if err = netlink.RuleAdd(rule); err != nil {
			return fmt.Errorf("asserting transport rule: %w", err)
		}
	}

	if !haveManagementRule {
		rule := netlink.NewRule()
		rule.Priority = interpositionManagementRulePriority
		rule.Table = interpositionTransportTable
		rule.Dst = managementAddress

		if err = netlink.RuleAdd(rule); err != nil {
			return fmt.Errorf("asserting management transport rule: %w", err)
		}
	}

	if spec.ManagementIPv6 == "" || spec.GatewayIPv6 == "" {
		return nil
	}

	return ensureTransportTableIPv6(spec, router, vtep)
}

// ensureManagementRoutes converges the two management routes of the transport table: the Pod's
// own address is a host route via the router leg, and the whole subnet is on-link via the mesh
// tunnel endpoint, where each peer resolves through its static neighbor entry. A subnet route
// via the router leg (the earlier bridged shape) would send peer traffic back to the device;
// it is converged away.
func ensureManagementRoutes(
	router, vtep netlink.Link,
	managementAddress, managementSubnet *net.IPNet,
) error {
	if err := netlink.RouteReplace(&netlink.Route{
		Table:     interpositionTransportTable,
		LinkIndex: router.Attrs().Index,
		Dst:       managementAddress,
		Scope:     netlink.SCOPE_LINK,
	}); err != nil {
		return fmt.Errorf("asserting own management route: %w", err)
	}

	if err := netlink.RouteReplace(&netlink.Route{
		Table:     interpositionTransportTable,
		LinkIndex: vtep.Attrs().Index,
		Dst:       managementSubnet,
		Scope:     netlink.SCOPE_LINK,
	}); err != nil {
		return fmt.Errorf("asserting management mesh route: %w", err)
	}

	family := netlink.FAMILY_V4
	if managementSubnet.IP.To4() == nil {
		family = netlink.FAMILY_V6
	}

	routes, err := netlink.RouteListFiltered(
		family,
		&netlink.Route{Table: interpositionTransportTable},
		netlink.RT_FILTER_TABLE,
	)
	if err != nil {
		return fmt.Errorf("listing transport table routes: %w", err)
	}

	for _, route := range routes {
		if route.LinkIndex != router.Attrs().Index || route.Dst == nil ||
			route.Dst.String() != managementSubnet.String() {
			continue
		}

		stale := route
		if err = netlink.RouteDel(&stale); err != nil {
			return fmt.Errorf("removing stale management subnet route: %w", err)
		}
	}

	return nil
}

// ensureTransportTableIPv6 mirrors the IPv4 management routing for an optional IPv6 management
// identity: the router-ingress and own-address rules select the transport table, which carries
// the own address via the router leg and the subnet via the mesh tunnel endpoint.
func ensureTransportTableIPv6(spec InterpositionSpec, router, vtep netlink.Link) error {
	prefix, err := netip.ParsePrefix(spec.ManagementIPv6)
	if err != nil {
		return fmt.Errorf(
			"interposition IPv6 management address %q is invalid",
			spec.ManagementIPv6,
		)
	}

	ownAddress := &net.IPNet{IP: prefix.Addr().AsSlice(), Mask: net.CIDRMask(128, 128)}
	subnet := &net.IPNet{
		IP:   prefix.Masked().Addr().AsSlice(),
		Mask: net.CIDRMask(prefix.Bits(), 128),
	}

	if err = ensureManagementRoutes(router, vtep, ownAddress, subnet); err != nil {
		return err
	}

	rules, err := netlink.RuleList(netlink.FAMILY_V6)
	if err != nil {
		return fmt.Errorf("listing IPv6 routing rules: %w", err)
	}

	haveRouterRule, haveManagementRule := false, false

	for _, rule := range rules {
		if rule.Table != interpositionTransportTable {
			continue
		}

		switch rule.Priority {
		case interpositionRouterRulePriority:
			haveRouterRule = rule.IifName == spec.RouterInterface
		case interpositionManagementRulePriority:
			haveManagementRule = rule.Dst != nil && rule.Dst.String() == ownAddress.String()
		}
	}

	if !haveRouterRule {
		rule := netlink.NewRule()
		rule.Family = netlink.FAMILY_V6
		rule.Priority = interpositionRouterRulePriority
		rule.Table = interpositionTransportTable
		rule.IifName = spec.RouterInterface

		if err = netlink.RuleAdd(rule); err != nil {
			return fmt.Errorf("asserting IPv6 router transport rule: %w", err)
		}
	}

	if !haveManagementRule {
		rule := netlink.NewRule()
		rule.Family = netlink.FAMILY_V6
		rule.Priority = interpositionManagementRulePriority
		rule.Table = interpositionTransportTable
		rule.Dst = ownAddress

		if err = netlink.RuleAdd(rule); err != nil {
			return fmt.Errorf("asserting IPv6 management transport rule: %w", err)
		}
	}

	return nil
}

// isDefaultRouteDestination reports whether a route destination is the IPv4 default: the
// netlink library returns default routes with either a nil destination or an explicit 0.0.0.0/0.
func isDefaultRouteDestination(destination *net.IPNet) bool {
	if destination == nil {
		return true
	}

	ones, _ := destination.Mask.Size()

	return ones == 0 && destination.IP.IsUnspecified()
}

// transportRoutesRecordName is the state file carrying the captured CNI route set.
const transportRoutesRecordName = "transport-routes"

// rememberTransportRoutes persists a freshly captured route set and recalls the recorded one on
// later passes (a device may strip every table after boot).
func rememberTransportRoutes(stateDirectory string, captured []capturedRoute) []capturedRoute {
	if stateDirectory == "" {
		return captured
	}

	record := filepath.Join(filepath.Clean(stateDirectory), transportRoutesRecordName)

	if len(captured) != 0 {
		if raw, err := json.Marshal(captured); err == nil {
			//nolint:gosec,mnd // non-sensitive sidecar-owned state, standard mode.
			_ = os.WriteFile(record, raw, 0o644)
		}

		return captured
	}

	raw, err := os.ReadFile(record)
	if err != nil {
		return captured
	}

	recalled := []capturedRoute{}
	if err := json.Unmarshal(raw, &recalled); err != nil {
		return captured
	}

	return recalled
}

// adoptSyntheticLegs marks freshly created pair legs with the interposition owner alias and
// asserts the mesh MTU on each leg that is still present.
func adoptSyntheticLegs(meshMTU int, names ...string) error {
	for _, name := range names {
		leg, present, err := lookupLink(name)
		if err != nil || !present {
			continue
		}

		if err = netlink.LinkSetAlias(leg, interpositionOwnerAlias); err != nil {
			return fmt.Errorf("marking synthetic leg %q ownership: %w", name, err)
		}

		if meshMTU != 0 && leg.Attrs().MTU != meshMTU {
			if err = netlink.LinkSetMTU(leg, meshMTU); err != nil {
				return fmt.Errorf("clamping synthetic leg %q MTU: %w", name, err)
			}
		}
	}

	return nil
}

// ensureMeshVTEP converges the routed management mesh tunnel endpoint: learning off, no default
// remote, no flood entries, the Pod's derived link-layer identity. Unicast toward a peer
// resolves through the static neighbor and forwarding entries the sidecar installs; an
// unknown destination fails resolution locally instead of flooding anywhere.
func ensureMeshVTEP(
	spec InterpositionSpec,
	podAddress netip.Addr,
	meshMAC net.HardwareAddr,
	meshMTU int,
) (netlink.Link, error) {
	localIP := net.IP(podAddress.AsSlice())

	existing, exists, err := lookupLink(MeshVTEPName)
	if err != nil {
		return nil, err
	}

	if exists {
		if existing.Type() != meshVTEPLinkType ||
			existing.Attrs().Alias != interpositionOwnerAlias {
			return nil, fmt.Errorf(
				"mesh VTEP name %q collides with unrelated state",
				MeshVTEPName,
			)
		}

		vxlan, isVXLAN := existing.(*netlink.Vxlan)

		conforms := isVXLAN && vxlan.VxlanId == spec.MeshTunnelID &&
			vxlan.SrcAddr.Equal(localIP) &&
			vxlan.Port == clabernetesconstants.ManagementMeshVXLANPort && !vxlan.Learning &&
			bytesEqualMAC(vxlan.Attrs().HardwareAddr, meshMAC) &&
			(meshMTU == 0 || vxlan.Attrs().MTU == meshMTU)
		if conforms {
			if err = netlink.LinkSetUp(existing); err != nil {
				return nil, fmt.Errorf("bringing mesh VTEP up: %w", err)
			}

			return existing, nil
		}

		if err = netlink.LinkDel(existing); err != nil {
			return nil, fmt.Errorf("replacing stale mesh VTEP: %w", err)
		}
	}

	attributes := netlink.NewLinkAttrs()
	attributes.Name = MeshVTEPName
	attributes.HardwareAddr = meshMAC

	if meshMTU != 0 {
		attributes.MTU = meshMTU
	}

	vtep := &netlink.Vxlan{
		LinkAttrs: attributes,
		VxlanId:   spec.MeshTunnelID,
		SrcAddr:   localIP,
		Port:      clabernetesconstants.ManagementMeshVXLANPort,
		Learning:  false,
	}
	if err = netlink.LinkAdd(vtep); err != nil {
		return nil, fmt.Errorf("creating mesh VTEP: %w", err)
	}

	created, exists, err := lookupLink(MeshVTEPName)
	if err != nil || !exists {
		return nil, errors.Join(errors.New("mesh VTEP vanished after creation"), err)
	}

	if err = netlink.LinkSetAlias(created, interpositionOwnerAlias); err != nil {
		return nil, errors.Join(
			fmt.Errorf("marking mesh VTEP ownership: %w", err),
			netlink.LinkDel(created),
		)
	}

	if err = netlink.LinkSetUp(created); err != nil {
		return nil, fmt.Errorf("bringing mesh VTEP up: %w", err)
	}

	return created, nil
}

// meshPeerState is the exact kernel state one peer requires.
type meshPeerState struct {
	pod net.IP
	mac net.HardwareAddr
}

// ensureMeshPeers converges the per-peer mesh state to the spec's peer set: on the tunnel
// endpoint one permanent neighbor entry per peer management address (toward the peer's derived
// identity) and one forwarding entry per peer identity (toward the peer's Pod address), and on
// the router leg one neighbor-discovery proxy entry per peer IPv6 address. Stale entries,
// including any flood entry left by an earlier shape, are removed exactly; nothing here is ever
// learned from traffic.
func ensureMeshPeers(
	spec InterpositionSpec,
	vtep netlink.Link,
	podAddress netip.Addr,
	ownManagement netip.Addr,
) error {
	peersV4 := map[netip.Addr]meshPeerState{}
	peersV6 := map[netip.Addr]meshPeerState{}
	forwarding := map[string]meshPeerState{}

	for _, peer := range spec.MeshPeers {
		pod, err := netip.ParseAddr(peer.PodAddress)
		if err != nil || !pod.Unmap().Is4() || pod.Unmap() == podAddress {
			continue
		}

		management, err := netip.ParseAddr(peer.ManagementIPv4)
		if err != nil || !management.Unmap().Is4() || management.Unmap() == ownManagement {
			continue
		}

		mac, err := ManagementMeshMAC(management)
		if err != nil {
			continue
		}

		state := meshPeerState{pod: net.IP(pod.Unmap().AsSlice()), mac: mac}
		peersV4[management.Unmap()] = state
		forwarding[mac.String()] = state

		// IPv6 peer state exists only alongside an IPv6 management identity of this Pod: the
		// tunnel endpoint keeps IPv6 disabled otherwise.
		if peer.ManagementIPv6 == "" || spec.ManagementIPv6 == "" || spec.GatewayIPv6 == "" {
			continue
		}

		if v6, v6Err := netip.ParseAddr(peer.ManagementIPv6); v6Err == nil && v6.Is6() &&
			!v6.Is4In6() {
			peersV6[v6] = state
		}
	}

	if err := ensureMeshForwardingEntries(vtep, forwarding); err != nil {
		return err
	}

	if err := ensureMeshNeighbors(vtep, netlink.FAMILY_V4, peersV4); err != nil {
		return err
	}

	if err := ensureMeshNeighbors(vtep, netlink.FAMILY_V6, peersV6); err != nil {
		return err
	}

	router, present, err := lookupLink(spec.RouterInterface)
	if err != nil || !present {
		return errors.Join(
			fmt.Errorf("router interface %q is unavailable", spec.RouterInterface),
			err,
		)
	}

	return ensureNeighborProxies(router, peersV6)
}

// ensureMeshForwardingEntries converges the tunnel endpoint's forwarding entries (peer identity
// to peer Pod address) to exactly the desired set.
func ensureMeshForwardingEntries(vtep netlink.Link, desired map[string]meshPeerState) error {
	entries, err := netlink.NeighList(vtep.Attrs().Index, unix.AF_BRIDGE)
	if err != nil {
		return fmt.Errorf("listing mesh forwarding entries: %w", err)
	}

	present := map[string]bool{}

	for _, entry := range entries {
		if entry.Flags&unix.NTF_SELF == 0 {
			continue
		}

		key := entry.HardwareAddr.String()
		if want, ok := desired[key]; ok && entry.IP != nil && entry.IP.Equal(want.pod) {
			present[key] = true

			continue
		}

		// A departed or relocated peer, or a flood entry from the earlier shape.
		stale := entry
		if err = netlink.NeighDel(&stale); err != nil {
			return fmt.Errorf("removing stale mesh forwarding entry %q: %w", key, err)
		}
	}

	for key, state := range desired {
		if present[key] {
			continue
		}

		if err = netlink.NeighAppend(&netlink.Neigh{
			LinkIndex:    vtep.Attrs().Index,
			Family:       unix.AF_BRIDGE,
			Flags:        unix.NTF_SELF,
			State:        netlink.NUD_PERMANENT | netlink.NUD_NOARP,
			IP:           state.pod,
			HardwareAddr: state.mac,
		}); err != nil {
			return fmt.Errorf("adding mesh forwarding entry %q: %w", key, err)
		}
	}

	return nil
}

// ensureMeshNeighbors converges the tunnel endpoint's permanent neighbor entries of one address
// family (peer management address to peer identity) to exactly the desired set. Non-permanent
// entries are the kernel's own transient resolution state for unknown addresses and are left
// alone.
func ensureMeshNeighbors(
	vtep netlink.Link,
	family int,
	desired map[netip.Addr]meshPeerState,
) error {
	entries, err := netlink.NeighList(vtep.Attrs().Index, family)
	if err != nil {
		return fmt.Errorf("listing mesh neighbor entries: %w", err)
	}

	present := map[netip.Addr]bool{}

	for _, entry := range entries {
		if entry.State&netlink.NUD_PERMANENT == 0 || entry.IP == nil {
			continue
		}

		address, ok := netip.AddrFromSlice(entry.IP)
		if !ok {
			continue
		}

		address = address.Unmap()

		if want, ok := desired[address]; ok && bytesEqualMAC(entry.HardwareAddr, want.mac) {
			present[address] = true

			continue
		}

		stale := entry
		if err = netlink.NeighDel(&stale); err != nil {
			return fmt.Errorf("removing stale mesh neighbor %q: %w", address, err)
		}
	}

	for address, state := range desired {
		if present[address] {
			continue
		}

		if err = netlink.NeighSet(&netlink.Neigh{
			LinkIndex:    vtep.Attrs().Index,
			Family:       family,
			State:        netlink.NUD_PERMANENT,
			IP:           net.IP(address.AsSlice()),
			HardwareAddr: state.mac,
		}); err != nil {
			return fmt.Errorf("adding mesh neighbor %q: %w", address, err)
		}
	}

	return nil
}

// ensureNeighborProxies converges the router leg's IPv6 neighbor-discovery proxy entries to
// exactly the peer IPv6 addresses, the IPv6 counterpart of proxy ARP.
func ensureNeighborProxies(router netlink.Link, peers map[netip.Addr]meshPeerState) error {
	entries, err := netlink.NeighProxyList(router.Attrs().Index, netlink.FAMILY_V6)
	if err != nil {
		return fmt.Errorf("listing neighbor proxies: %w", err)
	}

	present := map[netip.Addr]bool{}

	for _, entry := range entries {
		if entry.IP == nil {
			continue
		}

		address, ok := netip.AddrFromSlice(entry.IP)
		if !ok {
			continue
		}

		if _, wanted := peers[address]; wanted {
			present[address] = true

			continue
		}

		stale := entry
		if err = netlink.NeighDel(&stale); err != nil {
			return fmt.Errorf("removing stale neighbor proxy %q: %w", address, err)
		}
	}

	for address := range peers {
		if present[address] {
			continue
		}

		if err = netlink.NeighAdd(&netlink.Neigh{
			LinkIndex: router.Attrs().Index,
			Family:    netlink.FAMILY_V6,
			Flags:     unix.NTF_PROXY,
			IP:        net.IP(address.AsSlice()),
		}); err != nil {
			return fmt.Errorf("adding neighbor proxy %q: %w", address, err)
		}
	}

	return nil
}

// bytesEqualMAC compares two hardware addresses byte for byte.
func bytesEqualMAC(left, right net.HardwareAddr) bool {
	if len(left) != len(right) {
		return false
	}

	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}

	return true
}
