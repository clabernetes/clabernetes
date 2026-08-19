//go:build linux

package directruntime

import (
	"net"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

const vxlanNetlinkChild = "C9S_VXLAN_NETLINK_TEST_CHILD"

func TestValidLinuxSysctlName(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		valid bool
	}{
		{name: "net.ipv4.ip_forward", valid: true},
		{name: "net.ipv6.conf.all.disable_ipv6", valid: true},
		{name: "kernel.hostname", valid: true},
		{name: ""},
		{name: "hostname"},
		{name: " net.ipv4.ip_forward"},
		{name: "net..ipv4"},
		{name: "../kernel.hostname"},
		{name: "net/ipv4/ip_forward"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := validLinuxSysctlName(test.name); got != test.valid {
				t.Fatalf("validLinuxSysctlName(%q) = %t, want %t", test.name, got, test.valid)
			}
		})
	}
}

func TestNetlinkOperationsReconcileVXLANInIsolatedNamespace(t *testing.T) {
	if os.Getenv(vxlanNetlinkChild) == "1" {
		testNetlinkOperationsReconcileVXLAN(t)
		testManagementAddressPreservesPodTransport(t)
		testManagementDualStackReachability(t)
		testVXLANDataPlaneAcrossNamespaces(t)

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
		"-test.run=^TestNetlinkOperationsReconcileVXLANInIsolatedNamespace$",
	}
	if os.Geteuid() == 0 {
		unshareArguments[0] = "-n"
	}
	command := exec.Command( //nolint:gosec // The current test binary is executed in a new namespace.
		unshare,
		unshareArguments...,
	)
	command.Env = append(os.Environ(), vxlanNetlinkChild+"=1")
	output, err := command.CombinedOutput()
	if err != nil {
		if strings.Contains(strings.ToLower(string(output)), "operation not permitted") {
			t.Skip(
				"the kernel restricts required netlink operations in an unprivileged user namespace",
			)
		}
		t.Fatalf("isolated VXLAN netlink test failed: %v\n%s", err, output)
	}
}

func testManagementDualStackReachability(t *testing.T) {
	t.Helper()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	originalNamespace, err := netns.Get()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = originalNamespace.Close()
	}()
	peerNamespace, err := netns.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = peerNamespace.Close()
	}()
	if err = netns.Set(originalNamespace); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = netns.Set(originalNamespace)
	}()

	attributes := netlink.NewLinkAttrs()
	attributes.Name = "management-a"
	veth := netlink.NewVeth(attributes)
	veth.PeerName = "management-b"
	if err = netlink.LinkAdd(veth); err != nil {
		t.Fatal(err)
	}
	peerLink, err := netlink.LinkByName("management-b")
	if err != nil {
		t.Fatal(err)
	}
	if err = netlink.LinkSetNsFd(peerLink, int(peerNamespace)); err != nil {
		t.Fatal(err)
	}
	configureTestLink(t, "management-a", "10.244.10.2/24")
	if err = netns.Set(peerNamespace); err != nil {
		t.Fatal(err)
	}
	configureTestLink(t, "management-b", "10.244.10.3/24")
	if err = netns.Set(originalNamespace); err != nil {
		t.Fatal(err)
	}
	left := netlinkOperations{}
	for _, address := range []string{"198.51.100.10/24", "2001:db8:1::10/64"} {
		if err = left.EnsureManagementAddress("management-a", address, "c9s:management:left"); err != nil {
			t.Fatal(err)
		}
	}
	if err = netns.Set(peerNamespace); err != nil {
		t.Fatal(err)
	}
	right := netlinkOperations{}
	for _, address := range []string{"198.51.100.11/24", "2001:db8:1::11/64"} {
		if err = right.EnsureManagementAddress(
			"management-b",
			address,
			"c9s:management:right",
		); err != nil {
			t.Fatal(err)
		}
	}

	ping, err := exec.LookPath("ping")
	if err != nil {
		t.Skip("ping is unavailable")
	}
	for _, arguments := range [][]string{
		{"-c", "1", "-W", "2", "198.51.100.10"},
		{"-6", "-c", "1", "-W", "2", "2001:db8:1::10"},
	} {
		command := exec.Command( //nolint:gosec // Fixed diagnostic command in an isolated namespace.
			ping,
			arguments...,
		)
		if output, pingErr := command.CombinedOutput(); pingErr != nil {
			t.Fatalf("management dataplane ping %v failed: %v\n%s", arguments, pingErr, output)
		}
	}
}

func testManagementAddressPreservesPodTransport(t *testing.T) {
	t.Helper()
	attributes := netlink.NewLinkAttrs()
	attributes.Name = "pod-transport"
	if err := netlink.LinkAdd(&netlink.Dummy{LinkAttrs: attributes}); err != nil {
		t.Fatal(err)
	}
	configureTestLink(t, "pod-transport", "10.244.0.12/24")
	link, err := netlink.LinkByName("pod-transport")
	if err != nil {
		t.Fatal(err)
	}
	_, clusterNetwork, err := net.ParseCIDR("10.96.0.0/12")
	if err != nil {
		t.Fatal(err)
	}
	if err = netlink.RouteAdd(&netlink.Route{
		LinkIndex: link.Attrs().Index,
		Dst:       clusterNetwork,
		Table:     syscall.RT_TABLE_MAIN,
	}); err != nil {
		t.Fatal(err)
	}

	operations := netlinkOperations{}
	transportInterface, err := operations.ResolvePodTransportInterface("10.244.0.12")
	if err != nil {
		t.Fatal(err)
	}
	if transportInterface != "pod-transport" {
		t.Fatalf("resolved Pod transport interface = %q", transportInterface)
	}
	owner := "c9s:management:test"
	for _, address := range []string{"192.0.2.10/24", "2001:db8::10/64"} {
		if err = operations.EnsureManagementAddress("pod-transport", address, owner); err != nil {
			t.Fatal(err)
		}
		if err = operations.EnsureManagementAddress("pod-transport", address, owner); err != nil {
			t.Fatalf("management address reconciliation is not idempotent: %v", err)
		}
	}
	addresses, err := netlink.AddrList(link, netlink.FAMILY_ALL)
	if err != nil {
		t.Fatal(err)
	}
	present := map[string]bool{}
	for _, address := range addresses {
		present[address.IPNet.String()] = true
	}
	for _, expected := range []string{
		"10.244.0.12/24",
		"192.0.2.10/24",
		"2001:db8::10/64",
	} {
		if !present[expected] {
			t.Fatalf("addresses after management realization = %#v, missing %q", present, expected)
		}
	}
	if err = operations.EnsureManagementRoute(
		"pod-transport",
		"192.0.2.10/24",
		"0.0.0.0/0",
		"192.0.2.1",
		0,
		managementRouteTableBase,
		owner,
	); err != nil {
		t.Fatal(err)
	}
	mainRoutes, err := netlink.RouteListFiltered(
		netlink.FAMILY_V4,
		&netlink.Route{Table: syscall.RT_TABLE_MAIN},
		netlink.RT_FILTER_TABLE,
	)
	if err != nil {
		t.Fatal(err)
	}
	clusterRoutePresent := false
	for _, route := range mainRoutes {
		if route.Dst != nil && route.Dst.String() == clusterNetwork.String() {
			clusterRoutePresent = true
		}
		if route.Dst != nil && route.Dst.String() == "0.0.0.0/0" &&
			route.Src != nil && route.Src.Equal(net.ParseIP("192.0.2.10")) {
			t.Fatalf("management default route leaked into the main table: %#v", route)
		}
	}
	if !clusterRoutePresent {
		t.Fatalf("Kubernetes transport route was removed: %#v", mainRoutes)
	}
	managementRoutes, err := netlink.RouteListFiltered(
		netlink.FAMILY_V4,
		&netlink.Route{Table: managementRouteTableBase},
		netlink.RT_FILTER_TABLE,
	)
	if err != nil {
		t.Fatal(err)
	}
	managementDefaultPresent := false
	for _, route := range managementRoutes {
		if route.Dst != nil && route.Dst.String() == "0.0.0.0/0" &&
			route.Gw.Equal(net.ParseIP("192.0.2.1")) {
			managementDefaultPresent = true
		}
	}
	if !managementDefaultPresent {
		t.Fatalf("source-specific management default route is absent: %#v", managementRoutes)
	}
}

func testVXLANDataPlaneAcrossNamespaces(t *testing.T) {
	t.Helper()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	originalNamespace, err := netns.Get()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = originalNamespace.Close()
	}()
	peerNamespace, err := netns.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = peerNamespace.Close()
	}()
	if err = netns.Set(originalNamespace); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = netns.Set(originalNamespace)
	}()

	attributes := netlink.NewLinkAttrs()
	attributes.Name = "underlay-a"
	veth := netlink.NewVeth(attributes)
	veth.PeerName = "underlay-b"
	if err = netlink.LinkAdd(veth); err != nil {
		t.Fatal(err)
	}
	peerLink, err := netlink.LinkByName("underlay-b")
	if err != nil {
		t.Fatal(err)
	}
	if err = netlink.LinkSetNsFd(peerLink, int(peerNamespace)); err != nil {
		t.Fatal(err)
	}
	configureTestLink(t, "lo", "")
	configureTestLink(t, "underlay-a", "172.31.0.1/24")
	leftOperations := netlinkOperations{}
	if err = leftOperations.EnsureVXLANInterface(
		"dataplane-a",
		91,
		1400,
		14789,
		"c9s:direct:v1:left:vxlan:wire",
	); err != nil {
		t.Fatal(err)
	}
	if err = leftOperations.EnsureVXLANPeer(
		"dataplane-a",
		"172.31.0.2",
		"c9s:direct:v1:left:vxlan:wire",
	); err != nil {
		t.Fatal(err)
	}
	configureTestLink(t, "dataplane-a", "198.18.0.1/24")

	if err = netns.Set(peerNamespace); err != nil {
		t.Fatal(err)
	}
	configureTestLink(t, "lo", "")
	configureTestLink(t, "underlay-b", "172.31.0.2/24")
	rightOperations := netlinkOperations{}
	if err = rightOperations.EnsureVXLANInterface(
		"dataplane-b",
		91,
		1400,
		14789,
		"c9s:direct:v1:right:vxlan:wire",
	); err != nil {
		t.Fatal(err)
	}
	if err = rightOperations.EnsureVXLANPeer(
		"dataplane-b",
		"172.31.0.1",
		"c9s:direct:v1:right:vxlan:wire",
	); err != nil {
		t.Fatal(err)
	}
	configureTestLink(t, "dataplane-b", "198.18.0.2/24")
	ping, err := exec.LookPath("ping")
	if err != nil {
		t.Skip("ping is unavailable")
	}
	command := exec.Command( //nolint:gosec // Fixed diagnostic command in the isolated namespace.
		ping,
		"-c",
		"1",
		"-W",
		"2",
		"198.18.0.1",
	)
	if output, pingErr := command.CombinedOutput(); pingErr != nil {
		t.Fatalf("VXLAN dataplane ping failed: %v\n%s", pingErr, output)
	}
}

func configureTestLink(t *testing.T, name, address string) {
	t.Helper()
	link, err := netlink.LinkByName(name)
	if err != nil {
		t.Fatal(err)
	}
	if address != "" {
		parsed, parseErr := netlink.ParseAddr(address)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		if err = netlink.AddrAdd(link, parsed); err != nil {
			t.Fatal(err)
		}
	}
	if err = netlink.LinkSetUp(link); err != nil {
		t.Fatal(err)
	}
}

func testNetlinkOperationsReconcileVXLAN(t *testing.T) {
	t.Helper()
	operations := netlinkOperations{}
	owner := "c9s:direct:v1:test:vxlan:test"
	if err := operations.EnsureVXLANInterface("package-a", 73, 1450, 14789, owner); err != nil {
		t.Fatal(err)
	}
	interfaces, err := operations.ListVXLANInterfaces("c9s:direct:v1:test:vxlan:")
	if err != nil {
		t.Fatal(err)
	}
	if len(interfaces) != 1 || interfaces[0].Name != "package-a" ||
		interfaces[0].Owner != owner || interfaces[0].TunnelID != 73 ||
		interfaces[0].MTU != 1450 || interfaces[0].DestinationPort != 14789 {
		links, _ := netlink.LinkList()
		kernel := []string{}
		for _, link := range links {
			kernel = append(kernel, link.Type()+"/"+link.Attrs().Name+"/"+link.Attrs().Alias)
		}
		t.Fatalf("VXLAN inventory = %#v; kernel links = %#v", interfaces, kernel)
	}
	if err = operations.EnsureVXLANPeer("package-a", "192.0.2.17", owner); err != nil {
		t.Fatal(err)
	}
	assertVXLANFloodPeer(t, "package-a", "192.0.2.17")
	link, err := netlink.LinkByName("package-a")
	if err != nil {
		t.Fatal(err)
	}
	if err = netlink.NeighAppend(&netlink.Neigh{
		Family: syscall.AF_BRIDGE, LinkIndex: link.Attrs().Index,
		State: netlink.NUD_REACHABLE, Flags: netlink.NTF_SELF,
		IP:           net.ParseIP("192.0.2.17"),
		HardwareAddr: net.HardwareAddr{0x02, 0, 0, 0, 0, 1},
	}); err != nil {
		t.Fatal(err)
	}
	if err = operations.EnsureVXLANPeer("package-a", "192.0.2.17", owner); err != nil {
		t.Fatalf("learned dataplane entry was treated as peer drift: %v", err)
	}
	originalIndex := link.Attrs().Index
	if err = operations.EnsureVXLANPeer("package-a", "192.0.2.31", owner); err != nil {
		t.Fatal(err)
	}
	assertVXLANFloodPeer(t, "package-a", "192.0.2.31")
	link, err = netlink.LinkByName("package-a")
	if err != nil {
		t.Fatal(err)
	}
	if link.Attrs().Index != originalIndex {
		t.Fatalf(
			"VXLAN peer update replaced interface index %d with %d",
			originalIndex,
			link.Attrs().Index,
		)
	}
	if err = operations.DeleteVXLANInterface("package-a", owner); err != nil {
		t.Fatal(err)
	}
	interfaces, err = operations.ListVXLANInterfaces("c9s:direct:v1:test:vxlan:")
	if err != nil {
		t.Fatal(err)
	}
	if len(interfaces) != 0 {
		t.Fatalf("deleted VXLAN inventory = %#v", interfaces)
	}
}

func assertVXLANFloodPeer(t *testing.T, name, expected string) {
	t.Helper()
	link, err := netlink.LinkByName(name)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := netlink.NeighList(link.Attrs().Index, syscall.AF_BRIDGE)
	if err != nil {
		t.Fatal(err)
	}
	peers := []string{}
	for index := range entries {
		entry := &entries[index]
		if entry.IP != nil && zeroHardwareAddress(entry.HardwareAddr) {
			peers = append(peers, entry.IP.String())
		}
	}
	slices.Sort(peers)
	if !slices.Equal(peers, []string{expected}) {
		t.Fatalf("VXLAN flood peers = %#v, want %q", peers, expected)
	}
}
