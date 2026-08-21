//go:build linux

//nolint:testpackage // exercises the unexported linux realization directly.
package directruntime

import (
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/vishvananda/netlink"
)

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

	}

	assertInterposedState("cold pass")

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
