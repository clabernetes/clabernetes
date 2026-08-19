//go:build linux

//nolint:gocritic,gocyclo,noinlineerr,testpackage,wsl_v5 // Explicit namespace safety sequence.
package hostendpoint

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	"golang.org/x/sys/unix"
)

const hostEndpointNetlinkChild = "C9S_HOST_ENDPOINT_NETLINK_TEST_CHILD"

func TestLinuxOperationsOwnOnlyUIDMarkedVeths(t *testing.T) {
	if os.Getenv(hostEndpointNetlinkChild) == "1" {
		testLinuxOperationsOwnOnlyUIDMarkedVeths(t)

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
	arguments := []string{
		"-Urn",
		executable,
		"-test.run=^TestLinuxOperationsOwnOnlyUIDMarkedVeths$",
	}
	if os.Geteuid() == 0 {
		arguments[0] = "-n"
	}
	command := exec.CommandContext( //nolint:gosec // Executes this test binary in an isolated netns.
		t.Context(),
		unshare,
		arguments...,
	)
	command.Env = append(os.Environ(), hostEndpointNetlinkChild+"=1")
	output, err := command.CombinedOutput()
	if err != nil {
		if strings.Contains(strings.ToLower(string(output)), "operation not permitted") {
			t.Skip("the kernel restricts the required isolated netlink operations")
		}
		t.Fatalf("isolated host-endpoint test failed: %v\n%s", err, output)
	}
}

func testLinuxOperationsOwnOnlyUIDMarkedVeths(t *testing.T) {
	t.Helper()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hostNamespace, err := netns.Get()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hostNamespace.Close() }()
	podNamespace, err := netns.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = podNamespace.Close() }()
	if err = netns.Set(hostNamespace); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = netns.Set(hostNamespace) }()

	operations := linuxOperations{}
	pod := testIdentity("lab", "router-pod", "pod-uid")
	endpoint := testEndpoint(
		"lab",
		"host-link",
		"link-uid",
		"router",
		"node-uid",
		"host-a",
		"eth1",
	)
	if err = operations.Ensure(context.Background(), endpoint, pod, int(podNamespace)); err != nil {
		t.Fatal(err)
	}
	owned, err := operations.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(owned) != 1 || owned[0].HostInterface != endpoint.HostInterface ||
		owned[0].Ownership != ownershipFor(endpoint, pod) {
		links, _ := netlink.LinkList()
		kernel := make([]string, 0, len(links))
		for _, link := range links {
			kernel = append(kernel, fmt.Sprintf(
				"%s/%s/%q",
				link.Type(),
				link.Attrs().Name,
				link.Attrs().Alias,
			))
		}
		t.Fatalf("unexpected host ownership inventory: %#v, kernel=%#v", owned, kernel)
	}
	assertHostEndpointPair(t, endpoint, pod, podNamespace)
	assertHostEndpointTraffic(t, endpoint, podNamespace, hostNamespace)

	endpoint.PodInterface = "eth2"
	if err = operations.Ensure(context.Background(), endpoint, pod, int(podNamespace)); err != nil {
		t.Fatal(err)
	}
	assertHostEndpointPair(t, endpoint, pod, podNamespace)
	if err = operations.Delete(context.Background(), owned[0]); err != nil {
		t.Fatal(err)
	}
	owned, err = operations.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(owned) != 0 {
		t.Fatalf("owned host endpoint leaked after deletion: %#v", owned)
	}
	podHandle, err := netlink.NewHandleAt(podNamespace, unix.NETLINK_ROUTE)
	if err != nil {
		t.Fatal(err)
	}
	defer podHandle.Close()
	if _, exists, lookupErr := linkByName(podHandle, endpoint.PodInterface); lookupErr != nil ||
		exists {
		t.Fatalf("Pod peer leaked after host deletion: exists=%t err=%v", exists, lookupErr)
	}

	attributes := netlink.NewLinkAttrs()
	attributes.Name = "foreign0"
	if err = netlink.LinkAdd(&netlink.Dummy{LinkAttrs: attributes}); err != nil {
		t.Fatal(err)
	}
	endpoint.HostInterface = "foreign0"
	if err = operations.Ensure(
		context.Background(), endpoint, pod, int(podNamespace),
	); err == nil {
		t.Fatal("foreign host interface collision was accepted")
	}
	foreign, err := netlink.LinkByName("foreign0")
	if err != nil || foreign.Type() != "dummy" {
		t.Fatalf("foreign host state was modified: %#v %v", foreign, err)
	}
}

func assertHostEndpointTraffic(
	t *testing.T,
	endpoint Endpoint,
	podNamespace,
	hostNamespace netns.NsHandle,
) {
	t.Helper()
	hostLink, err := netlink.LinkByName(endpoint.HostInterface)
	if err != nil {
		t.Fatal(err)
	}
	hostAddress, err := netlink.ParseAddr("198.18.0.1/30")
	if err != nil {
		t.Fatal(err)
	}
	if err = netlink.AddrAdd(hostLink, hostAddress); err != nil {
		t.Fatal(err)
	}
	podHandle, err := netlink.NewHandleAt(podNamespace, unix.NETLINK_ROUTE)
	if err != nil {
		t.Fatal(err)
	}
	defer podHandle.Close()
	podLink, err := podHandle.LinkByName(endpoint.PodInterface)
	if err != nil {
		t.Fatal(err)
	}
	podAddress, err := netlink.ParseAddr("198.18.0.2/30")
	if err != nil {
		t.Fatal(err)
	}
	if err = podHandle.AddrAdd(podLink, podAddress); err != nil {
		t.Fatal(err)
	}
	hostConnection, err := net.ListenUDP("udp4", &net.UDPAddr{
		IP: net.ParseIP("198.18.0.1"), Port: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hostConnection.Close() }()
	if err = netns.Set(podNamespace); err != nil {
		t.Fatal(err)
	}
	hostDestination, ok := hostConnection.LocalAddr().(*net.UDPAddr)
	if !ok {
		t.Fatalf("host UDP address has unexpected type %T", hostConnection.LocalAddr())
	}
	podConnection, dialErr := net.DialUDP(
		"udp4",
		&net.UDPAddr{IP: net.ParseIP("198.18.0.2")},
		hostDestination,
	)
	if restoreErr := netns.Set(hostNamespace); dialErr == nil {
		dialErr = restoreErr
	}
	if dialErr != nil {
		t.Fatal(dialErr)
	}
	defer func() { _ = podConnection.Close() }()
	payload := []byte("host-link-traffic")
	if _, err = podConnection.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err = hostConnection.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	received := make([]byte, len(payload))
	if _, _, err = hostConnection.ReadFromUDP(received); err != nil {
		t.Fatal(err)
	}
	if string(received) != string(payload) {
		t.Fatalf("host endpoint traffic payload = %q", received)
	}
}

func assertHostEndpointPair(
	t *testing.T,
	endpoint Endpoint,
	pod ObjectIdentity,
	podNamespace netns.NsHandle,
) {
	t.Helper()
	hostLink, err := netlink.LinkByName(endpoint.HostInterface)
	if err != nil {
		t.Fatal(err)
	}
	podHandle, err := netlink.NewHandleAt(podNamespace, unix.NETLINK_ROUTE)
	if err != nil {
		t.Fatal(err)
	}
	defer podHandle.Close()
	podLink, err := podHandle.LinkByName(endpoint.PodInterface)
	if err != nil {
		t.Fatal(err)
	}
	hostAlias, _ := ownerAlias(ownerRoleHost, ownershipFor(endpoint, pod))
	podAlias, _ := ownerAlias(ownerRolePod, ownershipFor(endpoint, pod))
	if hostLink.Type() != hostEndpointLinkType || podLink.Type() != hostEndpointLinkType ||
		hostLink.Attrs().Alias != hostAlias || podLink.Attrs().Alias != podAlias ||
		hostLink.Attrs().MTU != endpoint.MTU || podLink.Attrs().MTU != endpoint.MTU ||
		hostLink.Attrs().Flags&net.FlagUp == 0 || podLink.Attrs().Flags&net.FlagUp == 0 ||
		!vethsArePeers(hostLink, podLink) {
		t.Fatalf("host endpoint pair is not exact: host=%#v Pod=%#v", hostLink, podLink)
	}
}
