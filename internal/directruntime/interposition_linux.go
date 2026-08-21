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

	if spec.DeviceInterface == spec.TransportInterface ||
		spec.DeviceInterface == spec.RouterInterface {
		return fmt.Errorf(
			"interposition device interface %q collides with a sidecar-owned name",
			spec.DeviceInterface,
		)
	}

	capturedRoutes, transportIndex, err := preserveTransportInterface(spec, podAddress)
	if err != nil {
		return err
	}

	// A device may strip routes from every table at boot; the route set captured while the CNI
	// state was pristine is the durable truth for re-assertion.
	capturedRoutes = rememberTransportRoutes(spec.StateDirectory, capturedRoutes)

	if err := ensureSyntheticPair(spec, managementPrefix, gateway); err != nil {
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

// ensureSyntheticPair creates the device/router veth pair with the plan identity and addresses.
//
//nolint:funlen // one linear creation-and-addressing pass.
func ensureSyntheticPair(
	spec InterpositionSpec,
	managementPrefix netip.Prefix,
	gateway netip.Addr,
) error {
	router, present, err := lookupLink(spec.RouterInterface)
	if err != nil {
		return err
	}

	if !present {
		attributes := netlink.NewLinkAttrs()
		attributes.Name = spec.DeviceInterface
		attributes.Alias = interpositionOwnerAlias

		if spec.DeviceMAC != "" {
			mac, macErr := net.ParseMAC(spec.DeviceMAC)
			if macErr != nil {
				return fmt.Errorf("interposition device MAC %q is invalid", spec.DeviceMAC)
			}

			attributes.HardwareAddr = mac
		}

		pair := &netlink.Veth{LinkAttrs: attributes, PeerName: spec.RouterInterface}
		if err = netlink.LinkAdd(pair); err != nil {
			return fmt.Errorf("creating synthetic management pair: %w", err)
		}

		router, _, err = lookupLink(spec.RouterInterface)
		if err != nil || router == nil {
			return fmt.Errorf("router leg vanished after creation: %w", err)
		}

		if err = netlink.LinkSetAlias(router, interpositionOwnerAlias); err != nil {
			return fmt.Errorf("marking router leg ownership: %w", err)
		}

		if device, devicePresent, deviceErr := lookupLink(spec.DeviceInterface); deviceErr == nil &&
			devicePresent {
			if err = netlink.LinkSetAlias(device, interpositionOwnerAlias); err != nil {
				return fmt.Errorf("marking device leg ownership: %w", err)
			}
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
	}

	// The device leg's forwarding stays off so a device keeping the physical leg in the Pod
	// namespace (with its own delivery mechanism) never sees kernel re-forwarding loops. The
	// leg may already be renamed by the device; missing is fine.
	if _, present, err := lookupLink(spec.DeviceInterface); err == nil && present {
		settings = append(
			settings,
			[2]string{"net.ipv4.conf." + spec.DeviceInterface + ".forwarding", "0"},
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
			haveManagementRule = true
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
		rule.Dst = &net.IPNet{
			IP:   managementPrefix.Masked().Addr().AsSlice(),
			Mask: net.CIDRMask(managementPrefix.Bits(), 32),
		}

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
