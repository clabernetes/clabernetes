//go:build linux

//nolint:testpackage // exercises real kernel neighbor solicitations in an isolated namespace.
package directruntime

import (
	"fmt"
	"net"
	"net/netip"
	"os"
	"testing"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

//nolint:gocognit,gocyclo // one isolated kernel fixture checks idle scale and both address families.
func TestMeshNeighborsResolveOnDemandAtScale(t *testing.T) {
	runFabricNetlinkTest(t, "C9S_DEMAND_NEIGHBOR_TEST_CHILD", func() {
		wireTestVethPair(t, "underlay0", "underlay-peer", wireTestCraftMTU)
		underlay, err := netlink.LinkByName("underlay0")
		if err != nil {
			t.Fatal(err)
		}
		address, err := netlink.ParseAddr("192.0.2.1/24")
		if err != nil {
			t.Fatal(err)
		}
		if err = netlink.AddrAdd(underlay, address); err != nil {
			t.Fatal(err)
		}
		vtep := &netlink.Vxlan{
			LinkAttrs: netlink.LinkAttrs{Name: MeshVTEPName, MTU: 1450},
			VxlanId:   1, VtepDevIndex: underlay.Attrs().Index, SrcAddr: net.ParseIP("192.0.2.1"),
			Port: 4789, Learning: false,
		}
		if err = netlink.LinkAdd(vtep); err != nil {
			t.Fatal(err)
		}
		if err = netlink.LinkSetUp(vtep); err != nil {
			t.Fatal(err)
		}
		for _, cidr := range []string{"172.30.0.1/21", "fd00::1/64"} {
			address, err = netlink.ParseAddr(cidr)
			if err != nil {
				t.Fatal(err)
			}
			address.Flags = unix.IFA_F_NODAD
			if err = netlink.AddrAdd(vtep, address); err != nil {
				t.Fatal(err)
			}
		}
		for _, family := range []string{"ipv4", "ipv6"} {
			for key, value := range map[string]string{"app_solicit": "3", "ucast_solicit": "0", "mcast_solicit": "0"} {
				path := "/proc/sys/net/" + family + "/neigh/" + MeshVTEPName + "/" + key
				if err = os.WriteFile(path, []byte(value), 0o600); err != nil {
					t.Fatal(err)
				}
			}
		}
		inventory := newMeshNeighborInventory()
		defer func() { _ = inventory.close() }()
		spec := InterpositionSpec{RouterInterface: "underlay0"}
		for number := 2; number < 1902; number++ {
			spec.MeshPeers = append(spec.MeshPeers, MeshPeer{
				ManagementIPv4: fmt.Sprintf("172.30.%d.%d", number/256, number%256),
				PodAddress:     fmt.Sprintf("10.244.%d.%d", number/256, number%256),
			})
		}
		spec.MeshPeers[0].ManagementIPv6 = "fd00::2"
		if err = ensureMeshPeers(spec, vtep, netip.MustParseAddr("192.0.2.1"),
			netip.MustParseAddr("172.30.0.1"), true, inventory); err != nil {
			t.Fatal(err)
		}
		for _, family := range []int{unix.AF_INET, unix.AF_INET6} {
			if neighbors := listMeshNeighbors(t, vtep, family); len(neighbors) != 0 {
				t.Fatalf("idle namespace prepopulated %d neighbors", len(neighbors))
			}
		}
		if entries := listMeshForwardingEntries(t, vtep); len(entries) != 0 {
			t.Fatalf("idle namespace prepopulated %d forwarding entries", len(entries))
		}
		for _, request := range []netlink.Neigh{
			{LinkIndex: underlay.Attrs().Index, Family: unix.AF_INET, IP: net.ParseIP("172.30.0.2")},
			{LinkIndex: vtep.Attrs().Index, Family: unix.AF_INET, IP: net.ParseIP("172.30.7.250")},
		} {
			if err = inventory.resolver.resolve(request); err != nil {
				t.Fatal(err)
			}
		}
		if entries := listMeshForwardingEntries(t, vtep); len(entries) != 0 {
			t.Fatal("foreign-interface or unknown-peer request created forwarding state")
		}
		requestTestMeshPeer(t, "172.30.0.2")
		requestTestMeshPeer(t, "fd00::2")
		for _, family := range []int{unix.AF_INET, unix.AF_INET6} {
			if neighbors := listMeshNeighbors(t, vtep, family); len(neighbors) != 1 {
				t.Fatalf("one contacted peer populated %d neighbors", len(neighbors))
			}
		}
		if entries := listMeshForwardingEntries(t, vtep); len(entries) != 1 {
			t.Fatalf("one dual-stack peer populated %d forwarding entries", len(entries))
		}
	})
}
