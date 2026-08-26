//go:build linux

//nolint:testpackage // exercises the unexported linux realization directly.
package directruntime

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

var errStaticMeshResolver = errors.New("transient resolver failure")

type staticMeshResolver struct {
	addresses []netip.Addr
	err       error
}

func (s staticMeshResolver) LookupNetIP(
	_ context.Context,
	_, _ string,
) ([]netip.Addr, error) {
	return s.addresses, s.err
}

// listMeshHeadEndPeers returns the destinations of the VTEP's zero-MAC head-end entries.
func listMeshHeadEndPeers(t *testing.T, vtep netlink.Link) map[string]bool {
	t.Helper()

	entries, err := netlink.NeighList(vtep.Attrs().Index, unix.AF_BRIDGE)
	if err != nil {
		t.Fatalf("listing mesh forwarding entries: %v", err)
	}

	peers := map[string]bool{}

	for _, entry := range entries {
		if entry.Flags&unix.NTF_SELF == 0 || entry.IP == nil {
			continue
		}

		if entry.HardwareAddr.String() == "00:00:00:00:00:00" {
			peers[entry.IP.String()] = true
		}
	}

	return peers
}

// assertMeshPeerReconciliation exercises head-end replication maintenance directly against the
// realized VTEP: discovery adds peers minus self, shrinking removes exactly the departed peer,
// and a resolver failure keeps the last-known set.
func assertMeshPeerReconciliation(t *testing.T) {
	t.Helper()

	vtep, err := netlink.LinkByName(MeshVTEPName)
	if err != nil {
		t.Fatalf("mesh VTEP is absent: %v", err)
	}

	pod := netip.MustParseAddr("10.244.2.134")
	// A non-address Pod identity routes resolution through the injected resolver seam.
	spec := InterpositionSpec{PodAddress: "resolver-seam", MeshPeerService: "peers"}

	operations := netlinkOperations{resolver: staticMeshResolver{addresses: []netip.Addr{
		netip.MustParseAddr("10.244.1.5"),
		netip.MustParseAddr("10.244.2.9"),
		pod,
	}}}
	if err = operations.ensureMeshPeers(spec, vtep, pod); err != nil {
		t.Fatalf("ensureMeshPeers() discovery pass: %v", err)
	}

	peers := listMeshHeadEndPeers(t, vtep)
	if len(peers) != 2 || !peers["10.244.1.5"] || !peers["10.244.2.9"] {
		t.Fatalf("head-end peers = %v, want the discovered set minus self", peers)
	}

	operations = netlinkOperations{resolver: staticMeshResolver{addresses: []netip.Addr{
		netip.MustParseAddr("10.244.1.5"),
	}}}
	if err = operations.ensureMeshPeers(spec, vtep, pod); err != nil {
		t.Fatalf("ensureMeshPeers() shrink pass: %v", err)
	}

	peers = listMeshHeadEndPeers(t, vtep)
	if len(peers) != 1 || !peers["10.244.1.5"] {
		t.Fatalf("head-end peers = %v, want exactly the remaining peer", peers)
	}

	operations = netlinkOperations{resolver: staticMeshResolver{
		err: errStaticMeshResolver,
	}}
	if err = operations.ensureMeshPeers(spec, vtep, pod); err != nil {
		t.Fatalf("ensureMeshPeers() failure pass: %v", err)
	}

	peers = listMeshHeadEndPeers(t, vtep)
	if len(peers) != 1 || !peers["10.244.1.5"] {
		t.Fatalf("head-end peers = %v, want the last-known set kept", peers)
	}
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
		MeshPeerService:    "c9s-management-mesh",
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

		haveDefault := false

		for _, route := range routes {
			if isDefaultRouteDestination(route.Dst) && route.Gw != nil &&
				route.Gw.String() == "10.244.2.1" {
				haveDefault = true
			}
		}

		if !haveDefault {
			t.Fatalf("%s: transport table carries no default route: %+v", step, routes)
		}

		transport, transportErr := netlink.LinkByName(TransportInterfaceName)
		if transportErr != nil {
			t.Fatalf("%s: preserved transport interface is absent: %v", step, transportErr)
		}

		// The exact CNI route shape must survive in the transport table: subnet VIA the
		// gateway, never a resurrected kernel connected prefix.
		for _, route := range routes {
			if route.Dst != nil && route.Dst.String() == "10.244.2.0/24" && route.Gw == nil {
				t.Fatalf("%s: connected prefix route resurrected in transport table", step)
			}
		}

		addresses, _ := netlink.AddrList(transport, netlink.FAMILY_V4)
		if len(addresses) != 1 || addresses[0].IP.String() != "10.244.2.134" {
			t.Fatalf("%s: transport addresses = %+v", step, addresses)
		}

		device, deviceErr := netlink.LinkByName("eth0")
		if deviceErr != nil {
			t.Fatalf("%s: synthetic device leg is absent: %v", step, deviceErr)
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

		// The management rule must cover exactly the local device address: a subnet-wide rule
		// would pull peer management traffic into the isolated gateway leg instead of the mesh.
		haveLocalManagementRule := false

		for _, rule := range rules {
			if rule.Table == interpositionTransportTable &&
				rule.Priority == interpositionManagementRulePriority &&
				rule.Dst != nil && rule.Dst.String() == "172.80.80.11/32" {
				haveLocalManagementRule = true
			}
		}

		if !haveLocalManagementRule {
			t.Fatalf("%s: management rule is not scoped to the local device address", step)
		}

		bridge, bridgeErr := netlink.LinkByName(MeshBridgeName)
		if bridgeErr != nil || bridge.Type() != "bridge" {
			t.Fatalf("%s: mesh bridge is absent: %v", step, bridgeErr)
		}

		for portName, wantIsolated := range map[string]bool{
			MeshDevicePortName:  false,
			MeshGatewayPortName: true,
			MeshVTEPName:        true,
		} {
			port, portErr := netlink.LinkByName(portName)
			if portErr != nil {
				t.Fatalf("%s: mesh port %q is absent: %v", step, portName, portErr)
			}

			if port.Attrs().MasterIndex != bridge.Attrs().Index {
				t.Fatalf("%s: mesh port %q is not enslaved to the bridge", step, portName)
			}

			protinfo, protErr := netlink.LinkGetProtinfo(port)
			if protErr != nil {
				t.Fatalf("%s: reading mesh port %q protinfo: %v", step, portName, protErr)
			}

			if protinfo.Isolated != wantIsolated {
				t.Fatalf(
					"%s: mesh port %q isolated = %t, want %t",
					step, portName, protinfo.Isolated, wantIsolated,
				)
			}
		}

		vtepLink, vtepErr := netlink.LinkByName(MeshVTEPName)
		if vtepErr != nil {
			t.Fatalf("%s: mesh VTEP is absent: %v", step, vtepErr)
		}

		vxlan, isVXLAN := vtepLink.(*netlink.Vxlan)
		if !isVXLAN || vxlan.VxlanId != 16_100_007 || !vxlan.Learning ||
			vxlan.Port != 14789 || vxlan.SrcAddr.String() != "10.244.2.134" {
			t.Fatalf("%s: mesh VTEP does not conform: %+v", step, vtepLink)
		}

		router, routerErr := netlink.LinkByName(RouterInterfaceName)
		if routerErr != nil ||
			router.Attrs().HardwareAddr.String() != "02:c9:aa:bb:cc:dd" {
			t.Fatalf(
				"%s: router leg gateway MAC = %v, want pinned deterministic identity (%v)",
				step, router.Attrs().HardwareAddr, routerErr,
			)
		}

		// The fake CNI underlay is 1500; every mesh element must carry underlay minus
		// encapsulation overhead so device segment sizes fit the cross-Pod path.
		for _, name := range []string{
			MeshBridgeName, MeshDevicePortName, MeshGatewayPortName,
			MeshVTEPName, RouterInterfaceName, "eth0",
		} {
			link, linkErr := netlink.LinkByName(name)
			if linkErr != nil {
				t.Fatalf("%s: mesh element %q is absent: %v", step, name, linkErr)
			}

			if link.Attrs().MTU != 1450 {
				t.Fatalf("%s: mesh element %q MTU = %d, want 1450", step, name, link.Attrs().MTU)
			}
		}
	}

	assertInterposedState("cold pass")

	assertReversePathFiltersCleared(t)

	assertMeshPeerReconciliation(t)

	// Second pass must be idempotent.
	if err = operations.EnsureInterposition(spec); err != nil {
		t.Fatalf("EnsureInterposition() steady pass: %v", err)
	}

	assertInterposedState("steady pass")

	// A device stripping every table's routes must be converged back from the recorded gateway.
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
		if isDefaultRouteDestination(route.Dst) {
			_ = netlink.RouteDel(&route)
		}
	}

	if err = operations.EnsureInterposition(spec); err != nil {
		t.Fatalf("EnsureInterposition() re-assertion pass: %v", err)
	}

	assertInterposedState("re-assertion after device strip")
}

// assertReversePathFiltersCleared verifies the interposition cleared the inherited rp_filter
// state: the namespace template (so interfaces a device creates after boot, like SR Linux's
// internal management-gateway leg, start unfiltered) and every pre-existing interface (which
// captured the poisoned template at creation).
func assertReversePathFiltersCleared(t *testing.T) {
	t.Helper()

	for _, name := range []string{"default", "all", TransportInterfaceName, RouterInterfaceName} {
		raw, err := os.ReadFile( //nolint:gosec // fixed sysctl tree, package-owned names.
			"/proc/sys/net/ipv4/conf/" + name + "/rp_filter",
		)
		if err != nil {
			t.Fatalf("reading rp_filter for %q: %v", name, err)
		}

		if value := strings.TrimSpace(string(raw)); value != "0" {
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

	raw, err := os.ReadFile("/proc/sys/net/ipv4/conf/post0/rp_filter")
	if err != nil {
		t.Fatalf("reading rp_filter for post-interposition interface: %v", err)
	}

	if value := strings.TrimSpace(string(raw)); value != "0" {
		t.Fatalf("post-interposition interface rp_filter = %s, want 0", value)
	}

	if err = netlink.LinkDel(&netlink.Dummy{
		LinkAttrs: netlink.LinkAttrs{Name: "post0"},
	}); err != nil {
		t.Fatalf("removing post-interposition interface: %v", err)
	}
}
