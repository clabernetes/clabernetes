//go:build linux

//nolint:testpackage // exercises the unexported linux realization directly.
package directruntime

import (
	"net"
	"net/netip"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// listMeshForwardingEntries returns the VTEP's self forwarding entries: identity to Pod address.
func listMeshForwardingEntries(t *testing.T, vtep netlink.Link) map[string]string {
	t.Helper()

	entries, err := netlink.NeighList(vtep.Attrs().Index, unix.AF_BRIDGE)
	if err != nil {
		t.Fatalf("listing mesh forwarding entries: %v", err)
	}

	forwarding := map[string]string{}

	for _, entry := range entries {
		if entry.Flags&unix.NTF_SELF == 0 || entry.IP == nil {
			continue
		}

		forwarding[entry.HardwareAddr.String()] = entry.IP.String()
	}

	return forwarding
}

// listMeshNeighbors returns the VTEP's permanent neighbor entries: address to identity.
func listMeshNeighbors(t *testing.T, vtep netlink.Link, family int) map[string]string {
	t.Helper()

	entries, err := netlink.NeighList(vtep.Attrs().Index, family)
	if err != nil {
		t.Fatalf("listing mesh neighbors: %v", err)
	}

	neighbors := map[string]string{}

	for _, entry := range entries {
		if entry.State&netlink.NUD_PERMANENT == 0 || entry.IP == nil {
			continue
		}

		neighbors[entry.IP.String()] = entry.HardwareAddr.String()
	}

	return neighbors
}

func assertStringMap(t *testing.T, step string, got, want map[string]string) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("%s: entries = %v, want %v", step, got, want)
	}

	for key, value := range want {
		if got[key] != value {
			t.Fatalf("%s: entries = %v, want %v", step, got, want)
		}
	}
}

// assertMeshPeerReconciliation exercises per-peer state maintenance directly against the
// realized VTEP: a peer set installs exactly one neighbor and one forwarding entry per peer
// (self excluded), a relocated peer moves its forwarding entry, shrinking removes exactly the
// departed peer's entries, and a flood entry left by the earlier bridged shape is removed.
func assertMeshPeerReconciliation(t *testing.T, spec InterpositionSpec) {
	t.Helper()

	vtep, err := netlink.LinkByName(MeshVTEPName)
	if err != nil {
		t.Fatalf("mesh VTEP is absent: %v", err)
	}

	pod := netip.MustParseAddr(spec.PodAddress)
	own := netip.MustParsePrefix(spec.ManagementIPv4).Addr()

	spec.MeshPeers = []MeshPeer{
		{ManagementIPv4: "172.80.80.12", PodAddress: "10.244.1.5"},
		{ManagementIPv4: "172.80.80.13", PodAddress: "10.244.2.9"},
		// The Pod's own identity never becomes a peer, however it is listed.
		{ManagementIPv4: "172.80.80.11", PodAddress: "10.244.2.134"},
		{ManagementIPv4: "172.80.80.99", PodAddress: spec.PodAddress},
	}

	if err = ensureMeshPeers(spec, vtep, pod, own, false); err != nil {
		t.Fatalf("ensureMeshPeers() install pass: %v", err)
	}

	assertStringMap(t, "install forwarding", listMeshForwardingEntries(t, vtep), map[string]string{
		"06:c9:ac:50:50:0c": "10.244.1.5",
		"06:c9:ac:50:50:0d": "10.244.2.9",
	})
	assertStringMap(t, "install neighbors", listMeshNeighbors(t, vtep, netlink.FAMILY_V4),
		map[string]string{
			"172.80.80.12": "06:c9:ac:50:50:0c",
			"172.80.80.13": "06:c9:ac:50:50:0d",
		})

	// A flood entry from the earlier head-end-replicated shape must be converged away exactly.
	if err = netlink.NeighAppend(&netlink.Neigh{
		LinkIndex:    vtep.Attrs().Index,
		Family:       unix.AF_BRIDGE,
		Flags:        unix.NTF_SELF,
		State:        netlink.NUD_PERMANENT | netlink.NUD_NOARP,
		IP:           net.ParseIP("10.244.9.9"),
		HardwareAddr: make(net.HardwareAddr, 6),
	}); err != nil {
		t.Fatalf("planting a flood entry: %v", err)
	}

	// Peer 12 moves to another Pod, peer 13 departs.
	spec.MeshPeers = []MeshPeer{{ManagementIPv4: "172.80.80.12", PodAddress: "10.244.3.2"}}

	if err = ensureMeshPeers(spec, vtep, pod, own, false); err != nil {
		t.Fatalf("ensureMeshPeers() relocate pass: %v", err)
	}

	assertStringMap(t, "relocate forwarding", listMeshForwardingEntries(t, vtep),
		map[string]string{"06:c9:ac:50:50:0c": "10.244.3.2"})
	assertStringMap(t, "relocate neighbors", listMeshNeighbors(t, vtep, netlink.FAMILY_V4),
		map[string]string{"172.80.80.12": "06:c9:ac:50:50:0c"})

	// An unchanged pass is a no-op.
	if err = ensureMeshPeers(spec, vtep, pod, own, false); err != nil {
		t.Fatalf("ensureMeshPeers() steady pass: %v", err)
	}

	assertStringMap(t, "steady forwarding", listMeshForwardingEntries(t, vtep),
		map[string]string{"06:c9:ac:50:50:0c": "10.244.3.2"})
}

const interpositionNetlinkChild = "C9S_INTERPOSITION_NETLINK_TEST_CHILD"

func TestEnsureInterpositionConvergesIsolatedNamespace(t *testing.T) {
	if os.Getenv(interpositionNetlinkChild) == "1" {
		testEnsureInterpositionConverges(t)

		return
	}

	unshare, err := exec.LookPath("unshare")
	if err != nil {
		t.Skip("unshare is unavailable")
	}

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	unshareArguments := []string{
		"-Urn",
		executable,
		"-test.run=^TestEnsureInterpositionConvergesIsolatedNamespace$",
	}
	if os.Geteuid() == 0 {
		unshareArguments[0] = "-n"
	}

	command := exec.CommandContext( //nolint:gosec // The current test binary is executed in a new namespace.
		t.Context(),
		unshare,
		unshareArguments...,
	)

	command.Env = append(os.Environ(), interpositionNetlinkChild+"=1")

	output, err := command.CombinedOutput()
	if err != nil {
		if strings.Contains(strings.ToLower(string(output)), "operation not permitted") {
			t.Skip(
				"the kernel restricts required netlink operations in an unprivileged user namespace",
			)
		}

		t.Fatalf("isolated interposition test failed: %v\n%s", err, output)
	}
}

func readSysctl(t *testing.T, path string) string {
	t.Helper()

	raw, err := os.ReadFile(path) //nolint:gosec // fixed sysctl tree, package-owned names.
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	return strings.TrimSpace(string(raw))
}

//nolint:gocognit,gocyclo // one straight-line verification pass.
func testEnsureInterpositionConverges(t *testing.T) {
	t.Helper()
	runtime.LockOSThread()

	defer runtime.UnlockOSThread()

	// Model a host whose init namespace sets rp_filter (Ubuntu ships 2): the namespace template
	// is poisoned before any interface exists, exactly as an inherited devconf looks on such
	// hosts, so interfaces created from here capture the poisoned value.
	for _, name := range []string{"default", "all"} {
		if err := os.WriteFile(
			"/proc/sys/net/ipv4/conf/"+name+"/rp_filter",
			[]byte("2"),
			0o600,
		); err != nil {
			t.Fatalf("seeding inherited rp_filter template: %v", err)
		}
	}

	// Fake the CNI state: an interface carrying the Pod address with a default route, exactly
	// as a sandbox looks before the sidecar runs.
	if err := netlink.LinkAdd(&netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{Name: "eth0"},
		PeerName:  "cni-peer",
	}); err != nil {
		t.Fatalf("creating fake CNI pair: %v", err)
	}

	cni, err := netlink.LinkByName("eth0")
	if err != nil {
		t.Fatal(err)
	}

	address, _ := netlink.ParseAddr("10.244.2.134/24")
	if err = netlink.AddrAdd(cni, address); err != nil {
		t.Fatalf("addressing fake CNI interface: %v", err)
	}

	if err = netlink.LinkSetUp(cni); err != nil {
		t.Fatal(err)
	}

	peer, _ := netlink.LinkByName("cni-peer")
	_ = netlink.LinkSetUp(peer)

	// kindnet-style routing: gateway /32 on-link, subnet via gateway, default via gateway —
	// and NO kernel connected prefix route (the CNI removes it).
	gatewayNet := &net.IPNet{IP: net.ParseIP("10.244.2.1"), Mask: net.CIDRMask(32, 32)}
	if err = netlink.RouteAdd(&netlink.Route{
		LinkIndex: cni.Attrs().Index,
		Dst:       gatewayNet,
		Scope:     netlink.SCOPE_LINK,
	}); err != nil {
		t.Fatalf("installing fake CNI gateway route: %v", err)
	}

	_, subnetNet, _ := net.ParseCIDR("10.244.2.0/24")
	if err = netlink.RouteReplace(&netlink.Route{
		LinkIndex: cni.Attrs().Index,
		Dst:       subnetNet,
		Gw:        net.ParseIP("10.244.2.1"),
		Src:       net.ParseIP("10.244.2.134"),
	}); err != nil {
		t.Fatalf("installing fake CNI subnet route: %v", err)
	}

	if err = netlink.RouteAdd(&netlink.Route{
		LinkIndex: cni.Attrs().Index,
		Gw:        net.ParseIP("10.244.2.1"),
	}); err != nil {
		t.Fatalf("installing fake CNI default route: %v", err)
	}

	state := t.TempDir()

	spec := InterpositionSpec{
		PodAddress:         "10.244.2.134",
		TransportInterface: TransportInterfaceName,
		RouterInterface:    RouterInterfaceName,
		DeviceInterface:    "eth0",
		ManagementIPv4:     "172.80.80.11/24",
		GatewayIPv4:        "172.80.80.1",
		StateDirectory:     state,
		MeshTunnelID:       16_100_007,
		MeshGatewayMAC:     "02:c9:aa:bb:cc:dd",
		MeshMAC:            "06:c9:ac:50:50:0b",
		MeshPeers: []MeshPeer{
			{ManagementIPv4: "172.80.80.21", PodAddress: "10.244.1.21"},
		},
		ReconcileMeshPeers: true,
	}

	// The device interface name equals the original CNI name, exactly like real kinds: the
	// rename must free the name before the synthetic pair claims it.
	operations := netlinkOperations{}

	if err = operations.EnsureInterposition(spec); err != nil {
		t.Fatalf("EnsureInterposition() cold pass: %v", err)
	}

	assertInterposedState := func(step string) {
		routes, listErr := netlink.RouteListFiltered(
			netlink.FAMILY_V4,
			&netlink.Route{Table: interpositionTransportTable},
			netlink.RT_FILTER_TABLE,
		)
		if listErr != nil {
			t.Fatalf("%s: listing transport table: %v", step, listErr)
		}

		transport, transportErr := netlink.LinkByName(TransportInterfaceName)
		if transportErr != nil {
			t.Fatalf("%s: preserved transport interface is absent: %v", step, transportErr)
		}

		router, routerErr := netlink.LinkByName(RouterInterfaceName)
		if routerErr != nil ||
			router.Attrs().HardwareAddr.String() != "02:c9:aa:bb:cc:dd" {
			t.Fatalf(
				"%s: router leg gateway MAC = %v, want pinned deterministic identity (%v)",
				step, router.Attrs().HardwareAddr, routerErr,
			)
		}

		vtepLink, vtepErr := netlink.LinkByName(MeshVTEPName)
		if vtepErr != nil {
			t.Fatalf("%s: mesh VTEP is absent: %v", step, vtepErr)
		}

		haveDefault, haveOwn, haveMesh := false, false, false

		for _, route := range routes {
			switch {
			case isDefaultRouteDestination(route.Dst):
				haveDefault = route.Gw != nil && route.Gw.String() == "10.244.2.1"
			case route.Dst.String() == "10.244.2.0/24" && route.Gw == nil:
				// The exact CNI route shape must survive in the transport table: subnet VIA
				// the gateway, never a resurrected kernel connected prefix.
				t.Fatalf("%s: connected prefix route resurrected in transport table", step)
			case route.Dst.String() == "172.80.80.11/32":
				haveOwn = route.LinkIndex == router.Attrs().Index
			case route.Dst.String() == "172.80.80.0/24":
				// The subnet rides the mesh tunnel endpoint; via the router leg it would send
				// peer traffic straight back to the device.
				if route.LinkIndex == router.Attrs().Index {
					t.Fatalf("%s: management subnet routed via the router leg", step)
				}

				haveMesh = route.LinkIndex == vtepLink.Attrs().Index
			}
		}

		if !haveDefault || !haveOwn || !haveMesh {
			t.Fatalf(
				"%s: transport table routes (default %t, own %t, mesh %t): %+v",
				step, haveDefault, haveOwn, haveMesh, routes,
			)
		}

		addresses, _ := netlink.AddrList(transport, netlink.FAMILY_V4)
		if len(addresses) != 1 || addresses[0].IP.String() != "10.244.2.134" {
			t.Fatalf("%s: transport addresses = %+v", step, addresses)
		}

		device, deviceErr := netlink.LinkByName("eth0")
		if deviceErr != nil {
			t.Fatalf("%s: synthetic device leg is absent: %v", step, deviceErr)
		}

		// The device leg and the router leg are the two ends of one pair: nothing sits between
		// the device and the routing decision.
		if device.Attrs().ParentIndex != router.Attrs().Index {
			t.Fatalf("%s: device leg peer index %d, want router leg %d",
				step, device.Attrs().ParentIndex, router.Attrs().Index)
		}

		deviceAddresses, _ := netlink.AddrList(device, netlink.FAMILY_V4)
		if len(deviceAddresses) != 1 || deviceAddresses[0].IP.String() != "172.80.80.11" {
			t.Fatalf("%s: device leg addresses = %+v", step, deviceAddresses)
		}

		rules, _ := netlink.RuleList(netlink.FAMILY_V4)

		haveRouter, haveTransport := false, false

		for _, rule := range rules {
			if rule.Table != interpositionTransportTable {
				continue
			}

			switch rule.Priority {
			case interpositionRouterRulePriority:
				haveRouter = true
			case interpositionTransportRulePriority:
				haveTransport = true
			}
		}

		if !haveRouter || !haveTransport {
			t.Fatalf("%s: transport rules missing: %+v", step, rules)
		}

		// The device leg still carries the management address here (a single-namespace
		// device), so inbound traffic from the mesh and from the transport selects the
		// transport table through ingress-scoped rules ahead of the re-homed local lookup and
		// crosses the pair onto the device leg, while nothing catches locally originated or
		// device-leg traffic. The kernel's priority-0 local lookup is gone.
		ingressRules := map[string]bool{}
		localPriorities := map[int]bool{}

		for _, rule := range rules {
			if rule.Table == interpositionTransportTable &&
				rule.Priority == interpositionManagementRulePriority {
				t.Fatalf("%s: unscoped own-address rule for a kernel-held address: %+v", step, rule)
			}

			if rule.Table == interpositionTransportTable &&
				rule.Priority == interpositionIngressRulePriority {
				if rule.Dst == nil || rule.Dst.String() != "172.80.80.11/32" {
					t.Fatalf("%s: ingress rule is not scoped to the address: %+v", step, rule)
				}

				ingressRules[rule.IifName] = true
			}

			if rule.Table == unix.RT_TABLE_LOCAL {
				localPriorities[rule.Priority] = true
			}
		}

		if !ingressRules[MeshVTEPName] || !ingressRules[TransportInterfaceName] ||
			len(ingressRules) != 2 {
			t.Fatalf("%s: ingress rules for a kernel-held address = %v", step, ingressRules)
		}

		if !localPriorities[interpositionLocalRulePriority] || localPriorities[0] {
			t.Fatalf("%s: local lookup priorities = %v, want re-homed only", step, localPriorities)
		}

		// A kernel-held address hairpins gateway-bound replies across the pair: the gateway,
		// which every inbound translated flow carries as its client, resolves through the
		// device leg unless the packet entered on the router leg, where it is local.
		hairpinRules := map[int]string{}

		for _, rule := range rules {
			if rule.Dst == nil || rule.Dst.String() != "172.80.80.1/32" {
				continue
			}

			if (rule.Priority == interpositionGatewayReturnRulePriority &&
				rule.Table == unix.RT_TABLE_LOCAL) ||
				(rule.Priority == interpositionGatewayHairpinRulePriority &&
					rule.Table == interpositionTransportTable) {
				hairpinRules[rule.Priority] = rule.IifName
			}
		}

		if returnIif, ok := hairpinRules[interpositionGatewayReturnRulePriority]; !ok ||
			returnIif != RouterInterfaceName {
			t.Fatalf("%s: gateway return rule = %v, want the router leg to local", step,
				hairpinRules)
		}

		if hairpinIif, ok := hairpinRules[interpositionGatewayHairpinRulePriority]; !ok ||
			hairpinIif != "" {
			t.Fatalf("%s: gateway hairpin rule = %v, want unscoped to the transport table", step,
				hairpinRules)
		}

		hairpinDecision, hairpinErr := netlink.RouteGetWithOptions(
			net.ParseIP("172.80.80.1"),
			&netlink.RouteGetOptions{SrcAddr: net.ParseIP("172.80.80.11")},
		)
		if hairpinErr != nil || len(hairpinDecision) == 0 ||
			hairpinDecision[0].Type == unix.RTN_LOCAL ||
			hairpinDecision[0].LinkIndex != device.Attrs().Index {
			t.Fatalf("%s: a reply to the gateway resolves to %+v (%v), want the device leg",
				step, hairpinDecision, hairpinErr)
		}

		returnDecision, returnErr := netlink.RouteGetWithOptions(
			net.ParseIP("172.80.80.1"),
			&netlink.RouteGetOptions{
				Iif:     RouterInterfaceName,
				SrcAddr: net.ParseIP("172.80.80.12"),
			},
		)
		if returnErr != nil || len(returnDecision) == 0 ||
			returnDecision[0].Type != unix.RTN_LOCAL {
			t.Fatalf("%s: gateway-bound traffic entering on the router leg resolves to %+v (%v), "+
				"want local delivery", step, returnDecision, returnErr)
		}

		routerLink, _ := netlink.LinkByName(RouterInterfaceName)

		decision, decisionErr := netlink.RouteGetWithOptions(
			net.ParseIP("172.80.80.11"),
			&netlink.RouteGetOptions{Iif: MeshVTEPName, SrcAddr: net.ParseIP("172.80.80.12")},
		)
		if decisionErr != nil || len(decision) == 0 ||
			decision[0].LinkIndex != routerLink.Attrs().Index {
			t.Fatalf("%s: mesh-delivered traffic to the kernel-held address resolves to %+v (%v), "+
				"want the router leg toward the device leg", step, decision, decisionErr)
		}

		if readSysctl(t, "/proc/sys/net/ipv4/conf/eth0/accept_local") != "1" ||
			readSysctl(t, "/proc/sys/net/ipv4/conf/eth0/forwarding") != "1" {
			t.Fatalf("%s: device leg does not accept the local gateway source or forward", step)
		}

		for _, rule := range rules {
			if rule.Priority == interpositionDeviceLegRulePriority {
				t.Fatalf(
					"%s: device-leg blackhole present for a kernel-held address: %+v",
					step,
					rule,
				)
			}
		}

		// The router leg holds the gateway without a prefix route: a main-table connected route
		// via it would compete with the device leg's own and strand single-namespace replies.
		mainRoutes, _ := netlink.RouteListFiltered(
			netlink.FAMILY_V4,
			&netlink.Route{Table: unix.RT_TABLE_MAIN},
			netlink.RT_FILTER_TABLE,
		)
		for _, route := range mainRoutes {
			if route.LinkIndex == router.Attrs().Index && route.Dst != nil &&
				route.Dst.String() == "172.80.80.0/24" {
				t.Fatalf("%s: main table carries the management prefix via the router leg", step)
			}
		}

		// The bridged shape is gone: no bridge, no gateway pair.
		for _, name := range []string{"c9sb0", "c9sd0", "c9sg0"} {
			if _, bridgedErr := netlink.LinkByName(name); bridgedErr == nil {
				t.Fatalf("%s: bridged-shape element %q exists", step, name)
			}
		}

		vxlan, isVXLAN := vtepLink.(*netlink.Vxlan)
		if !isVXLAN || vxlan.VxlanId != 16_100_007 || vxlan.Learning ||
			vxlan.Port != 14789 || vxlan.SrcAddr.String() != "10.244.2.134" ||
			vxlan.Attrs().HardwareAddr.String() != "06:c9:ac:50:50:0b" ||
			vxlan.Attrs().MasterIndex != 0 {
			t.Fatalf("%s: mesh VTEP does not conform: %+v", step, vtepLink)
		}

		// The router leg proxies ARP for peers with no delay; the VTEP never answers; early
		// demux stays off so the routing decision governs every kernel-held delivery.
		if readSysctl(t, "/proc/sys/net/ipv4/conf/"+RouterInterfaceName+"/proxy_arp") != "1" ||
			readSysctl(t, "/proc/sys/net/ipv4/neigh/"+RouterInterfaceName+"/proxy_delay") != "0" ||
			readSysctl(t, "/proc/sys/net/ipv4/conf/"+MeshVTEPName+"/arp_ignore") != "1" ||
			readSysctl(t, "/proc/sys/net/ipv4/ip_early_demux") != "0" {
			t.Fatalf("%s: router leg proxy ARP, VTEP scoping, or early demux sysctls are not set",
				step)
		}

		// The fake CNI underlay is 1500; every mesh element must carry underlay minus
		// encapsulation overhead so device segment sizes fit the cross-Pod path.
		for _, name := range []string{MeshVTEPName, RouterInterfaceName, "eth0"} {
			link, linkErr := netlink.LinkByName(name)
			if linkErr != nil {
				t.Fatalf("%s: mesh element %q is absent: %v", step, name, linkErr)
			}

			if link.Attrs().MTU != 1450 {
				t.Fatalf("%s: mesh element %q MTU = %d, want 1450", step, name, link.Attrs().MTU)
			}
		}

		// The peer given to EnsureInterposition is installed through the same path the tick
		// uses: one neighbor entry and one forwarding entry, nothing flooded.
		assertStringMap(t, step+" forwarding", listMeshForwardingEntries(t, vtepLink),
			map[string]string{"06:c9:ac:50:50:15": "10.244.1.21"})
		assertStringMap(t, step+" neighbors", listMeshNeighbors(t, vtepLink, netlink.FAMILY_V4),
			map[string]string{"172.80.80.21": "06:c9:ac:50:50:15"})
	}

	assertInterposedState("cold pass")

	// Without an IPv6 management identity the VTEP never sources IPv6 onto the mesh.
	if readSysctl(t, "/proc/sys/net/ipv6/conf/"+MeshVTEPName+"/disable_ipv6") != "1" {
		t.Fatal("cold pass: VTEP keeps IPv6 enabled without an IPv6 management identity")
	}

	assertReversePathFiltersCleared(t)

	// A pass not asked to reconcile peers leaves the peer state alone even with a different
	// peer list, so unchanged ticks never touch the neighbor tables.
	untouched := spec
	untouched.MeshPeers = nil
	untouched.ReconcileMeshPeers = false

	if err = operations.EnsureInterposition(untouched); err != nil {
		t.Fatalf("EnsureInterposition() steady pass: %v", err)
	}

	assertInterposedState("steady pass")

	assertMeshPeerReconciliation(t, spec)

	// A device stripping every table's routes must be converged back from the recorded gateway,
	// and the mesh routes re-asserted with it.
	routes, _ := netlink.RouteListFiltered(
		netlink.FAMILY_V4,
		&netlink.Route{},
		0,
	)
	for _, route := range routes {
		if isDefaultRouteDestination(route.Dst) && route.Gw != nil {
			_ = netlink.RouteDel(&route)
		}
	}

	strippedRoutes, _ := netlink.RouteListFiltered(
		netlink.FAMILY_V4,
		&netlink.Route{Table: interpositionTransportTable},
		netlink.RT_FILTER_TABLE,
	)
	for _, route := range strippedRoutes {
		if isDefaultRouteDestination(route.Dst) ||
			(route.Dst != nil && route.Dst.String() == "172.80.80.0/24") {
			_ = netlink.RouteDel(&route)
		}
	}

	if err = operations.EnsureInterposition(spec); err != nil {
		t.Fatalf("EnsureInterposition() re-assertion pass: %v", err)
	}

	assertInterposedState("re-assertion after device strip")

	// SONiC re-inserts the kernel's local lookup at priority 1001, behind every rule the
	// sidecar installs. A reply arriving on the tunnel endpoint for the kernel-held address
	// must still resolve to local delivery, never to the router leg.
	localRules, _ := netlink.RuleList(netlink.FAMILY_V4)
	for _, rule := range localRules {
		if rule.Priority != 0 || rule.Table != unix.RT_TABLE_LOCAL {
			continue
		}

		moved := netlink.NewRule()
		moved.Priority = 1001
		moved.Table = unix.RT_TABLE_LOCAL

		if err = netlink.RuleAdd(moved); err != nil {
			t.Fatalf("re-inserting the local rule like SONiC: %v", err)
		}

		stale := rule
		if err = netlink.RuleDel(&stale); err != nil {
			t.Fatalf("removing the priority-0 local rule like SONiC: %v", err)
		}
	}

	if err = operations.EnsureInterposition(spec); err != nil {
		t.Fatalf("EnsureInterposition() with the local rule moved: %v", err)
	}

	assertInterposedState("local lookup moved like SONiC")

	// Once it re-enters on the device leg, the local lookup still delivers it: no
	// ingress-scoped rule catches the device leg, so it cannot loop.
	decision, err := netlink.RouteGetWithOptions(
		net.ParseIP("172.80.80.11"),
		&netlink.RouteGetOptions{Iif: "eth0", SrcAddr: net.ParseIP("172.80.80.12")},
	)
	if err != nil || len(decision) == 0 || decision[0].Type != unix.RTN_LOCAL {
		t.Fatalf("device-leg arrival of the kernel-held address resolves to %+v (%v), want local",
			decision, err)
	}

	// A device that took the address into its own stack leaves the device leg bare; the
	// own-address rule then routes hooks and mesh-delivered traffic through the router leg.
	device, _ := netlink.LinkByName("eth0")
	bare, _ := netlink.ParseAddr("172.80.80.11/24")

	if err = netlink.AddrDel(device, bare); err != nil {
		t.Fatalf("stripping the device leg address like a device stack would: %v", err)
	}

	if err = operations.EnsureInterposition(spec); err != nil {
		t.Fatalf("EnsureInterposition() with a bare device leg: %v", err)
	}

	bareOwnRules := map[string]bool{}
	bareLocalLookups := map[string]bool{}
	bareBlackhole := false

	bareRules, _ := netlink.RuleList(netlink.FAMILY_V4)
	for _, rule := range bareRules {
		if rule.Table == interpositionTransportTable &&
			rule.Priority == interpositionManagementRulePriority &&
			rule.Dst != nil && rule.Dst.String() == "172.80.80.11/32" {
			bareOwnRules[rule.IifName] = true
		}

		if rule.Priority == interpositionLocalRulePriority && rule.Table == unix.RT_TABLE_LOCAL {
			bareLocalLookups[rule.IifName] = true
		}
	}

	// The device-leg copy of a frame the device's own stack consumed is blackholed instead of
	// being forwarded back through the router leg. iproute2 renders the action, which the
	// netlink library does not decode on listing.
	if shown, showErr := exec.CommandContext( //nolint:gosec // fixed iproute2 invocation.
		t.Context(),
		"ip", "rule", "show", "pref", strconv.Itoa(interpositionDeviceLegRulePriority),
	).CombinedOutput(); showErr == nil && strings.Contains(string(shown), "blackhole") &&
		strings.Contains(string(shown), "iif eth0") &&
		strings.Contains(string(shown), "172.80.80.11") {
		bareBlackhole = true
	}

	if !bareBlackhole {
		t.Fatalf("device-leg blackhole rule is missing for a device-held address: %+v", bareRules)
	}

	if !bareOwnRules[""] || len(bareOwnRules) != 1 {
		t.Fatalf("own-address rules for a device-held address = %v, want one unscoped rule",
			bareOwnRules)
	}

	if !bareLocalLookups[""] {
		t.Fatalf("re-homed local lookup is missing for a device-held address: %v", bareLocalLookups)
	}

	// The device's own stack answers the gateway through the pair; the hairpin of the
	// kernel-held shape (rules and the device-leg host route) is converged away.
	for _, rule := range bareRules {
		if rule.Priority == interpositionGatewayReturnRulePriority ||
			rule.Priority == interpositionGatewayHairpinRulePriority {
			t.Fatalf("gateway hairpin rule present for a device-held address: %+v", rule)
		}
	}

	bareTransportRoutes, _ := netlink.RouteListFiltered(
		netlink.FAMILY_V4,
		&netlink.Route{Table: interpositionTransportTable},
		netlink.RT_FILTER_TABLE,
	)
	for _, route := range bareTransportRoutes {
		if route.Dst != nil && route.Dst.String() == "172.80.80.1/32" {
			t.Fatalf("gateway hairpin route present for a device-held address: %+v", route)
		}
	}

	if err = netlink.AddrAdd(device, bare); err != nil {
		t.Fatalf("restoring the device leg address: %v", err)
	}

	// A device that disables IPv6 in the shared namespace (EOS does) must not fail the IPv4
	// mesh closed over an IPv6 management identity it cannot carry: the pass succeeds and
	// simply installs no IPv6 state.
	if err = os.WriteFile(
		"/proc/sys/net/ipv6/conf/all/disable_ipv6", []byte("1"), 0o600,
	); err != nil {
		t.Fatalf("disabling IPv6 like a device would: %v", err)
	}

	withIPv6 := spec
	withIPv6.ManagementIPv6 = "3fff:172:80:80::11/64"
	withIPv6.GatewayIPv6 = "3fff:172:80:80::1"
	withIPv6.MeshPeers = []MeshPeer{{
		ManagementIPv4: "172.80.80.21", ManagementIPv6: "3fff:172:80:80::21",
		PodAddress: "10.244.1.21",
	}}

	if err = operations.EnsureInterposition(withIPv6); err != nil {
		t.Fatalf("EnsureInterposition() with IPv6 disabled in the namespace: %v", err)
	}

	assertInterposedState("IPv6 disabled by the device")

	v6Rules, _ := netlink.RuleList(netlink.FAMILY_V6)
	for _, rule := range v6Rules {
		if rule.Table == interpositionTransportTable {
			t.Fatalf("IPv6 transport rule installed while IPv6 is disabled: %+v", rule)
		}
	}

	// SR Linux takes the device leg down, renames it to mgmt0, and brings it back up while it
	// boots; the kernel refuses to rename an interface that is up. A re-assertion pass between
	// the down and the rename must therefore leave the leg's administrative state alone, or
	// the device never gets its management interface.
	leg, _ := netlink.LinkByName("eth0")
	if err = netlink.LinkSetDown(leg); err != nil {
		t.Fatalf("taking the device leg down like SR Linux: %v", err)
	}

	if err = operations.EnsureInterposition(withIPv6); err != nil {
		t.Fatalf("EnsureInterposition() with the device leg down: %v", err)
	}

	leg, _ = netlink.LinkByName("eth0")
	if leg.Attrs().Flags&net.FlagUp != 0 {
		t.Fatal("a re-assertion pass brought the device leg back up behind the device")
	}

	if err = netlink.LinkSetName(leg, "mgmt0"); err != nil {
		t.Fatalf("renaming the device leg like SR Linux: %v", err)
	}

	renamed, _ := netlink.LinkByName("mgmt0")
	if err = netlink.LinkSetUp(renamed); err != nil {
		t.Fatalf("bringing the renamed leg up like SR Linux: %v", err)
	}

	if err = operations.EnsureInterposition(withIPv6); err != nil {
		t.Fatalf("EnsureInterposition() after the device renamed its leg: %v", err)
	}

	if _, staleErr := netlink.LinkByName("eth0"); staleErr == nil {
		t.Fatal("a pass recreated the device leg under its old name after the device renamed it")
	}

	router, _ := netlink.LinkByName(RouterInterfaceName)
	renamed, _ = netlink.LinkByName("mgmt0")
	if renamed == nil || router.Attrs().ParentIndex != renamed.Attrs().Index {
		t.Fatalf("router leg peer index %d, want the renamed device leg %+v",
			router.Attrs().ParentIndex, renamed)
	}
}

// assertReversePathFiltersCleared verifies the interposition cleared the inherited rp_filter
// state: the namespace template (so interfaces a device creates after boot, like SR Linux's
// internal management-gateway leg, start unfiltered) and every pre-existing interface (which
// captured the poisoned template at creation).
func assertReversePathFiltersCleared(t *testing.T) {
	t.Helper()

	for _, name := range []string{"default", "all", TransportInterfaceName, RouterInterfaceName} {
		if value := readSysctl(t, "/proc/sys/net/ipv4/conf/"+name+"/rp_filter"); value != "0" {
			t.Fatalf("rp_filter for %q = %s, want 0", name, value)
		}
	}

	// An interface born after the interposition baseline must start unfiltered purely through
	// the cleared template -- this is the device-created interface case.
	if err := netlink.LinkAdd(&netlink.Dummy{
		LinkAttrs: netlink.LinkAttrs{Name: "post0"},
	}); err != nil {
		t.Fatalf("creating post-interposition interface: %v", err)
	}

	if value := readSysctl(t, "/proc/sys/net/ipv4/conf/post0/rp_filter"); value != "0" {
		t.Fatalf("post-interposition interface rp_filter = %s, want 0", value)
	}

	if err := netlink.LinkDel(&netlink.Dummy{
		LinkAttrs: netlink.LinkAttrs{Name: "post0"},
	}); err != nil {
		t.Fatalf("removing post-interposition interface: %v", err)
	}
}
