//go:build linux

//nolint:err113,gocognit,gocyclo,mnd,nestif // single-pass boundary logic with structured one-off diagnostics and protocol literals.
package directruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// interpositionOwnerAlias marks sidecar-created interposition interfaces so reconciliation can
// distinguish them from device- and CNI-owned links.
const interpositionOwnerAlias = "c9s:interposition:v1"

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
// the device-expected identity, the unconditional hardening baseline is applied, and Kubernetes
// transport lives in the sidecar-owned policy table. Every step is idempotent; the device's own
// state (main table, its chains, its sysctls) is never touched.
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
	case spec.TransportInterface, spec.RouterInterface,
		MeshBridgeName, MeshVTEPName, MeshDevicePortName, MeshGatewayPortName:
		return fmt.Errorf(
			"interposition device interface %q collides with a sidecar-owned name",
			spec.DeviceInterface,
		)
	}

	gatewayMAC, err := net.ParseMAC(spec.MeshGatewayMAC)
	if err != nil || spec.MeshTunnelID <= 0 || spec.MeshPeerService == "" {
		return fmt.Errorf(
			"interposition mesh membership is incomplete (tunnel %d, gateway MAC %q, peers %q)",
			spec.MeshTunnelID,
			spec.MeshGatewayMAC,
			spec.MeshPeerService,
		)
	}

	capturedRoutes, transportIndex, err := preserveTransportInterface(spec, podAddress)
	if err != nil {
		return err
	}

	// A device may strip routes from every table at boot; the route set captured while the CNI
	// state was pristine is the durable truth for re-assertion.
	capturedRoutes = rememberTransportRoutes(spec.StateDirectory, capturedRoutes)

	// Mesh frames cross the Pod underlay encapsulated; every mesh element carries the clamped
	// MTU so device-derived segment sizes fit the cross-Pod path.
	meshMTU, err := clampPodFabricMTU(0, podAddress)
	if err != nil {
		return err
	}

	if err := ensureSyntheticPair(
		spec, managementPrefix, gateway, gatewayMAC, meshMTU,
	); err != nil {
		return err
	}

	if err := o.ensureManagementMesh(spec, podAddress, gatewayMAC, meshMTU); err != nil {
		return err
	}

	if err := applyInterpositionSysctls(spec); err != nil {
		return err
	}

	if err := o.DisableTxChecksumOffload(spec.RouterInterface); err != nil {
		return err
	}

	if err := o.DisableTxChecksumOffload(MeshDevicePortName); err != nil {
		return err
	}

	// The device leg may already be renamed or adopted by a running device; offload state on it
	// is best-effort after boot because the rename moves the ethtool identity with the link.
	if _, present, lookupErr := lookupLink(spec.DeviceInterface); lookupErr == nil && present {
		if err := o.DisableTxChecksumOffload(spec.DeviceInterface); err != nil {
			return err
		}
	}

	return ensureTransportTable(spec, podAddress, managementPrefix, capturedRoutes, transportIndex)
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
		return nil, 0, fmt.Errorf(
			"preserved transport interface %q no longer carries the Pod address %q",
			spec.TransportInterface,
			spec.PodAddress,
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

// ensureSyntheticPair creates the device pair (device leg + Pod-side mesh port) and the gateway
// pair (router leg + bridge-side mesh port) with the plan identity and addresses. The legs only
// exchange frames through the management mesh bridge.
func ensureSyntheticPair(
	spec InterpositionSpec,
	managementPrefix netip.Prefix,
	gateway netip.Addr,
	gatewayMAC net.HardwareAddr,
	meshMTU int,
) error {
	// The Pod-side leg is the durable presence marker: unlike the device leg it is never
	// renamed or adopted by a device.
	if _, present, err := lookupLink(MeshDevicePortName); err != nil {
		return err
	} else if !present {
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

		pair := &netlink.Veth{LinkAttrs: attributes, PeerName: MeshDevicePortName}
		if err = netlink.LinkAdd(pair); err != nil {
			return fmt.Errorf("creating synthetic device pair: %w", err)
		}

		if err = adoptSyntheticLegs(meshMTU, MeshDevicePortName, spec.DeviceInterface); err != nil {
			return err
		}
	}

	router, present, err := lookupLink(spec.RouterInterface)
	if err != nil {
		return err
	}

	if !present {
		attributes := netlink.NewLinkAttrs()
		attributes.Name = spec.RouterInterface
		attributes.Alias = interpositionOwnerAlias
		attributes.HardwareAddr = gatewayMAC

		if meshMTU != 0 {
			attributes.MTU = meshMTU
		}

		pair := &netlink.Veth{LinkAttrs: attributes, PeerName: MeshGatewayPortName}
		if err = netlink.LinkAdd(pair); err != nil {
			return fmt.Errorf("creating synthetic gateway pair: %w", err)
		}

		router, _, err = lookupLink(spec.RouterInterface)
		if err != nil || router == nil {
			return fmt.Errorf("router leg vanished after creation: %w", err)
		}

		if err = adoptSyntheticLegs(
			meshMTU, spec.RouterInterface, MeshGatewayPortName,
		); err != nil {
			return err
		}
	}

	// The device leg may have been renamed or moved by the device after adoption; only address
	// the legs that are still present, and only with sidecar-owned addresses.
	if device, devicePresent, lookupErr := lookupLink(spec.DeviceInterface); lookupErr == nil &&
		devicePresent {
		deviceAddress, parseErr := netlink.ParseAddr(spec.ManagementIPv4)
		if parseErr != nil {
			return fmt.Errorf("parsing management address: %w", parseErr)
		}

		addresses, listErr := netlink.AddrList(device, netlink.FAMILY_V4)
		if listErr != nil {
			return fmt.Errorf("reading device leg addresses: %w", listErr)
		}

		// A device that adopted the leg and stripped its kernel address owns the leg's
		// addressing from then on; only an untouched leg is (re)addressed, so the sidecar
		// converges the pre-boot state without fighting the device afterwards.
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

// applyInterpositionSysctls applies the unconditional hardening baseline.
func applyInterpositionSysctls(spec InterpositionSpec) error {
	settings := [][2]string{
		{"net.ipv4.ip_forward", "1"},
		{"net.ipv4.conf.all.rp_filter", "0"},
		{"net.ipv4.conf." + spec.RouterInterface + ".rp_filter", "0"},
		{"net.ipv4.conf." + spec.RouterInterface + ".accept_local", "1"},
		{"net.ipv4.conf." + spec.RouterInterface + ".forwarding", "1"},
		// ARP-flux scoping: without arp_ignore=1 every interface of the namespace answers ARP
		// for the gateway (and for peer gateways arriving over the mesh through bridge-self
		// delivery), so gateway resolution would return multiple identities.
		{"net.ipv4.conf." + spec.RouterInterface + ".arp_ignore", "1"},
		{"net.ipv4.conf." + MeshBridgeName + ".arp_ignore", "1"},
		{"net.ipv4.conf." + MeshDevicePortName + ".arp_ignore", "1"},
		{"net.ipv4.conf." + MeshGatewayPortName + ".arp_ignore", "1"},
		{"net.ipv4.conf." + MeshVTEPName + ".arp_ignore", "1"},
		// The bridge and its ports are pure L2 elements; IPv6 stays off so they never source
		// NDP or link-local traffic onto the mesh.
		{"net.ipv6.conf." + MeshBridgeName + ".disable_ipv6", "1"},
		{"net.ipv6.conf." + MeshDevicePortName + ".disable_ipv6", "1"},
		{"net.ipv6.conf." + MeshGatewayPortName + ".disable_ipv6", "1"},
		{"net.ipv6.conf." + MeshVTEPName + ".disable_ipv6", "1"},
	}

	// Kubernetes nodes load br_netfilter for kube-proxy and Pod namespaces inherit its
	// defaults, which would push every bridged mesh frame through the Pod's netfilter a second
	// time after the L3 gateway hop -- conntrack clash resolution then re-NATs translated
	// flows and replies can no longer match. The mesh bridge is pure L2 by design: bridged
	// frames bypass netfilter in this namespace. The sysctls exist only when the module is
	// loaded; absent means already off.
	for _, name := range []string{
		"net.bridge.bridge-nf-call-iptables",
		"net.bridge.bridge-nf-call-ip6tables",
		"net.bridge.bridge-nf-call-arptables",
	} {
		parts := append([]string{"/proc/sys"}, strings.Split(name, ".")...)

		path := filepath.Join(parts...)
		if _, err := os.Stat(path); err == nil {
			settings = append(settings, [2]string{name, "0"})
		}
	}

	// The device leg's forwarding stays off so a device keeping the physical leg in the Pod
	// namespace (with its own delivery mechanism) never sees kernel re-forwarding loops, and
	// its ARP responder is scoped: a single-namespace kind shares its stack with the gateway
	// leg, so without arp_ignore=1 it would flux-answer mesh-flooded ARP for the gateway (and
	// for every peer address the namespace holds). The leg may already be renamed by the
	// device; missing is fine.
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

	return nil
}

// ensureTransportTable owns Kubernetes transport in a dedicated policy table selected ahead of
// the main table, so device rewrites of main never affect it. The table replays the exact
// captured CNI route set plus the management-subnet route.
//
//nolint:funlen // one linear rule-and-route convergence pass.
func ensureTransportTable(
	spec InterpositionSpec,
	podAddress netip.Addr,
	managementPrefix netip.Prefix,
	captured []capturedRoute,
	transportIndex int,
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

	managementRoute := &netlink.Route{
		Table:     interpositionTransportTable,
		LinkIndex: router.Attrs().Index,
		Dst: &net.IPNet{
			IP:   managementPrefix.Masked().Addr().AsSlice(),
			Mask: net.CIDRMask(managementPrefix.Bits(), 32),
		},
		Scope: netlink.SCOPE_LINK,
	}
	if err = netlink.RouteReplace(managementRoute); err != nil {
		return fmt.Errorf("asserting management subnet route: %w", err)
	}

	rules, err := netlink.RuleList(netlink.FAMILY_V4)
	if err != nil {
		return fmt.Errorf("listing routing rules: %w", err)
	}

	// The management rule covers exactly the Pod's own management address: hooks and the
	// Pod-address translation reach the local device through the gateway leg, while peer
	// management addresses fall through to the device leg's connected route and ride the mesh.
	managementAddress := &net.IPNet{
		IP:   managementPrefix.Addr().AsSlice(),
		Mask: net.CIDRMask(32, 32),
	}

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

// ensureManagementMesh converges the pure-L2 bridge stitching the device leg, the gateway leg,
// and the management mesh VTEP, then reconciles the head-end replication peer set discovered
// through the mesh peer Service.
func (o netlinkOperations) ensureManagementMesh(
	spec InterpositionSpec,
	podAddress netip.Addr,
	gatewayMAC net.HardwareAddr,
	meshMTU int,
) error {
	bridge, err := ensureMeshBridge(gatewayMAC, meshMTU)
	if err != nil {
		return err
	}

	vtep, err := ensureMeshVTEP(spec, podAddress, meshMTU)
	if err != nil {
		return err
	}

	// The gateway leg and the VTEP are both isolated: gateway traffic can never cross the mesh
	// in either direction, which is what contains the duplicate gateway identity every Pod
	// hosts. The device port is a normal port so devices reach both.
	if err := ensureMeshPort(MeshDevicePortName, bridge, false, meshMTU); err != nil {
		return err
	}

	if err := ensureMeshPort(MeshGatewayPortName, bridge, true, meshMTU); err != nil {
		return err
	}

	if err := ensureMeshPort(MeshVTEPName, bridge, true, meshMTU); err != nil {
		return err
	}

	// The router leg is sidecar-owned; assert its MTU every pass so device segment sizes derive
	// from what the cross-Pod path carries.
	if meshMTU != 0 {
		if router, present, lookupErr := lookupLink(spec.RouterInterface); lookupErr == nil &&
			present && router.Attrs().MTU != meshMTU {
			if err := netlink.LinkSetMTU(router, meshMTU); err != nil {
				return fmt.Errorf("clamping router leg MTU: %w", err)
			}
		}
	}

	return o.ensureMeshPeers(spec, vtep, podAddress)
}

// ensureMeshBridge converges the sidecar-owned pure-L2 bridge. Its MAC is pinned (derived from
// the gateway identity) because an unpinned bridge inherits the lowest port MAC and churns with
// port changes.
func ensureMeshBridge(gatewayMAC net.HardwareAddr, meshMTU int) (netlink.Link, error) {
	bridgeMAC := append(net.HardwareAddr(nil), gatewayMAC...)
	if len(bridgeMAC) > 1 {
		bridgeMAC[1] ^= 0x02
	}

	existing, present, err := lookupLink(MeshBridgeName)
	if err != nil {
		return nil, err
	}

	if present {
		if existing.Type() != "bridge" || existing.Attrs().Alias != interpositionOwnerAlias {
			return nil, fmt.Errorf(
				"mesh bridge name %q collides with unrelated state",
				MeshBridgeName,
			)
		}
	} else {
		attributes := netlink.NewLinkAttrs()
		attributes.Name = MeshBridgeName
		attributes.HardwareAddr = bridgeMAC

		if meshMTU != 0 {
			attributes.MTU = meshMTU
		}

		if err = netlink.LinkAdd(&netlink.Bridge{LinkAttrs: attributes}); err != nil {
			return nil, fmt.Errorf("creating management mesh bridge: %w", err)
		}

		existing, present, err = lookupLink(MeshBridgeName)
		if err != nil || !present {
			return nil, errors.Join(errors.New("mesh bridge vanished after creation"), err)
		}

		if err = netlink.LinkSetAlias(existing, interpositionOwnerAlias); err != nil {
			return nil, fmt.Errorf("marking mesh bridge ownership: %w", err)
		}
	}

	if !bytesEqualMAC(existing.Attrs().HardwareAddr, bridgeMAC) {
		if err = netlink.LinkSetHardwareAddr(existing, bridgeMAC); err != nil {
			return nil, fmt.Errorf("pinning mesh bridge MAC: %w", err)
		}
	}

	if err = netlink.LinkSetUp(existing); err != nil {
		return nil, fmt.Errorf("bringing mesh bridge up: %w", err)
	}

	return existing, nil
}

// ensureMeshVTEP converges the management mesh VTEP: learning enabled, no default remote --
// unicast destinations are learned from mesh traffic while unknown destinations flood through
// the head-end replication entries.
func ensureMeshVTEP(
	spec InterpositionSpec,
	podAddress netip.Addr,
	meshMTU int,
) (netlink.Link, error) {
	localIP := net.IP(podAddress.AsSlice())

	existing, exists, err := lookupLink(MeshVTEPName)
	if err != nil {
		return nil, err
	}

	if exists {
		if existing.Type() != fabricVTEPLinkType ||
			existing.Attrs().Alias != interpositionOwnerAlias {
			return nil, fmt.Errorf(
				"mesh VTEP name %q collides with unrelated state",
				MeshVTEPName,
			)
		}

		vxlan, isVXLAN := existing.(*netlink.Vxlan)

		conforms := isVXLAN && vxlan.VxlanId == spec.MeshTunnelID &&
			vxlan.SrcAddr.Equal(localIP) &&
			vxlan.Port == clabernetesconstants.VXLANServicePort && vxlan.Learning &&
			(meshMTU == 0 || vxlan.Attrs().MTU == meshMTU)
		if conforms {
			return existing, nil
		}

		if err = netlink.LinkDel(existing); err != nil {
			return nil, fmt.Errorf("replacing stale mesh VTEP: %w", err)
		}
	}

	attributes := netlink.NewLinkAttrs()
	attributes.Name = MeshVTEPName

	if meshMTU != 0 {
		attributes.MTU = meshMTU
	}

	vtep := &netlink.Vxlan{
		LinkAttrs: attributes,
		VxlanId:   spec.MeshTunnelID,
		SrcAddr:   localIP,
		Port:      clabernetesconstants.VXLANServicePort,
		Learning:  true,
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

	return created, nil
}

// ensureMeshPort enslaves one link to the mesh bridge, asserts its MTU, and applies bridge port
// isolation where required. Isolation is core bridge behavior (kernel 4.18+); a kernel rejecting
// it fails interposition closed with this exact reason.
func ensureMeshPort(name string, bridge netlink.Link, isolated bool, meshMTU int) error {
	link, present, err := lookupLink(name)
	if err != nil {
		return err
	}

	if !present {
		return fmt.Errorf("mesh port %q is unavailable", name)
	}

	if link.Attrs().MasterIndex != bridge.Attrs().Index {
		if err = netlink.LinkSetMaster(link, bridge); err != nil {
			return fmt.Errorf("enslaving mesh port %q: %w", name, err)
		}
	}

	if meshMTU != 0 && link.Attrs().MTU != meshMTU {
		if err = netlink.LinkSetMTU(link, meshMTU); err != nil {
			return fmt.Errorf("clamping mesh port %q MTU: %w", name, err)
		}
	}

	if err = netlink.LinkSetUp(link); err != nil {
		return fmt.Errorf("bringing mesh port %q up: %w", name, err)
	}

	if isolated {
		if err = netlink.LinkSetIsolated(link, true); err != nil {
			return fmt.Errorf(
				"isolating mesh port %q (bridge port isolation requires kernel 4.18+): %w",
				name,
				err,
			)
		}
	}

	return nil
}

// ensureMeshPeers reconciles the head-end replication entries toward every discovered peer Pod.
// Resolution failure keeps the last-known peer set: absence of the discovery record is not an
// error, and a transient resolver failure must not tear the mesh down.
func (o netlinkOperations) ensureMeshPeers(
	spec InterpositionSpec,
	vtep netlink.Link,
	podAddress netip.Addr,
) error {
	resolver := peerAddressResolver(podBoundResolver(spec.PodAddress))
	if net.ParseIP(spec.PodAddress) == nil && o.resolver != nil {
		resolver = o.resolver
	}

	ctx, cancel := context.WithTimeout(context.Background(), fabricPeerResolveTimeout)
	defer cancel()

	addresses, err := resolver.LookupNetIP(ctx, "ip4", spec.MeshPeerService)
	if err != nil {
		// Absence keeps the last-known peer set by design.
		return nil
	}

	desired := map[netip.Addr]bool{}

	for _, address := range addresses {
		address = address.Unmap()
		if !address.Is4() || address == podAddress {
			continue
		}

		desired[address] = true
	}

	entries, err := netlink.NeighList(vtep.Attrs().Index, unix.AF_BRIDGE)
	if err != nil {
		return fmt.Errorf("listing mesh forwarding entries: %w", err)
	}

	zeroMAC := make(net.HardwareAddr, 6)
	present := map[netip.Addr]bool{}

	for _, entry := range entries {
		if entry.Flags&unix.NTF_SELF == 0 || entry.IP == nil {
			continue
		}

		destination, ok := netip.AddrFromSlice(entry.IP)
		if !ok {
			continue
		}

		destination = destination.Unmap()

		if desired[destination] {
			if bytesEqualMAC(entry.HardwareAddr, zeroMAC) {
				present[destination] = true
			}

			continue
		}

		// A stale head-end entry, or a learned unicast path toward a departed Pod address.
		stale := entry
		if err = netlink.NeighDel(&stale); err != nil {
			return fmt.Errorf("removing stale mesh peer %q: %w", destination, err)
		}
	}

	for destination := range desired {
		if present[destination] {
			continue
		}

		if err = netlink.NeighAppend(&netlink.Neigh{
			LinkIndex:    vtep.Attrs().Index,
			Family:       unix.AF_BRIDGE,
			Flags:        unix.NTF_SELF,
			State:        netlink.NUD_PERMANENT | netlink.NUD_NOARP,
			IP:           net.IP(destination.AsSlice()),
			HardwareAddr: zeroMAC,
		}); err != nil {
			return fmt.Errorf("adding mesh peer %q: %w", destination, err)
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
