//go:build linux

//nolint:testpackage // exercises the unexported linux realization directly.
package directruntime

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	clabconstants "github.com/srl-labs/containerlab/constants"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

const fabricSweepNetlinkChild = "C9S_FABRIC_SWEEP_NETLINK_TEST_CHILD"

const fabricAdoptNetlinkChild = "C9S_FABRIC_ADOPT_NETLINK_TEST_CHILD"

const fabricMTUNetlinkChild = "C9S_FABRIC_MTU_NETLINK_TEST_CHILD"

var errWorkerInterfaceOwnerNotAdopted = errors.New("worker interface owner was not adopted")

func TestSweepTransportStateDeletesBothVethEndsInIsolatedNamespace(t *testing.T) {
	runFabricNetlinkTest(t, fabricSweepNetlinkChild, func() {
		testSweepTransportStateDeletesBothVethEnds(t)
		testStaleTransportLinkDeleteIsIdempotent(t)
	})
}

func TestEndpointTransportsAdoptRecabledDeviceLegInIsolatedNamespace(t *testing.T) {
	runFabricNetlinkTest(t, fabricAdoptNetlinkChild, func() {
		testEnsureFabricPairAdoptsRecabledDeviceLeg(t)
		testEnsureHostInterfaceAdoptsRecabledDeviceLeg(t)
	})
}

func TestFabricEndpointMTUCoherenceInIsolatedNamespace(t *testing.T) {
	runFabricNetlinkTest(t, fabricMTUNetlinkChild, func() {
		createFabricTestUnderlay(t)
		testFabricEndpointDefaultsUnsetMTUEverywhere(t)
		testFabricEndpointHonorsMTUAboveUnderlay(t)
		testFabricEndpointHonorsSmallMTU(t)
		testFabricEndpointConvergesDriftedMTU(t)
	})
}

func runFabricNetlinkTest(t *testing.T, childEnvironment string, test func()) {
	t.Helper()

	if os.Getenv(childEnvironment) == "1" {
		test()

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
		"-test.run=^" + t.Name() + "$",
	}
	if os.Geteuid() == 0 {
		unshareArguments[0] = "-n"
	}

	command := exec.CommandContext( //nolint:gosec // The current test binary is executed in a new namespace.
		t.Context(),
		unshare,
		unshareArguments...,
	)

	command.Env = append(os.Environ(), childEnvironment+"=1")

	output, err := command.CombinedOutput()
	if err != nil {
		if strings.Contains(strings.ToLower(string(output)), "operation not permitted") {
			t.Skip(
				"the kernel restricts required netlink operations in an unprivileged user namespace",
			)
		}

		t.Fatalf("isolated fabric netlink test failed: %v\n%s", err, output)
	}
}

//nolint:gocyclo // sequentially asserts each preserved kernel identity and ownership invariant.
func testEnsureFabricPairAdoptsRecabledDeviceLeg(t *testing.T) {
	t.Helper()

	const (
		interfaceName = "eth1"
		ownerPrefix   = "c9s:direct:v1:test:transport:"
		oldOwner      = ownerPrefix + "old"
		newOwner      = ownerPrefix + "new"
	)

	oldSpec := FabricEndpointSpec{
		InterfaceID: interfaceName + "-old", InterfaceName: interfaceName,
		Owner: oldOwner, OwnerPrefix: ownerPrefix, MTU: 1500,
	}
	oldLegName := fabricSidecarLegName(oldSpec.InterfaceID)

	if err := ensureFabricPair(oldSpec, oldLegName, oldSpec.MTU); err != nil {
		t.Fatalf("creating initial fabric pair: %v", err)
	}

	device, err := netlink.LinkByName(interfaceName)
	if err != nil {
		t.Fatal(err)
	}

	address, err := netlink.ParseAddr("192.0.2.2/24")
	if err != nil {
		t.Fatal(err)
	}

	if err = netlink.AddrAdd(device, address); err != nil {
		t.Fatalf("addressing initial device leg: %v", err)
	}

	originalIndex := device.Attrs().Index
	originalMAC := device.Attrs().HardwareAddr.String()

	newSpec := FabricEndpointSpec{
		InterfaceID: interfaceName + "-new", InterfaceName: interfaceName,
		Owner: newOwner, OwnerPrefix: ownerPrefix, MTU: 1500,
	}
	newLegName := fabricSidecarLegName(newSpec.InterfaceID)

	if err = ensureFabricPair(newSpec, newLegName, newSpec.MTU); err != nil {
		t.Fatalf("adopting recabled fabric pair: %v", err)
	}

	device, err = netlink.LinkByName(interfaceName)
	if err != nil {
		t.Fatal(err)
	}

	if device.Attrs().Index != originalIndex ||
		device.Attrs().HardwareAddr.String() != originalMAC {
		t.Fatalf(
			"device identity changed: index %d -> %d, MAC %s -> %s",
			originalIndex,
			device.Attrs().Index,
			originalMAC,
			device.Attrs().HardwareAddr,
		)
	}

	addresses, err := netlink.AddrList(device, netlink.FAMILY_V4)
	if err != nil {
		t.Fatal(err)
	}

	if len(addresses) != 1 {
		t.Fatalf("device addresses changed: got %v, want %s", addresses, address)
	}

	addressPrefix, addressBits := address.Mask.Size()
	actualPrefix, actualBits := addresses[0].Mask.Size()

	if !addresses[0].IP.Equal(address.IP) ||
		actualPrefix != addressPrefix || actualBits != addressBits {
		t.Fatalf("device addresses changed: got %v, want %s", addresses, address)
	}

	leg, err := netlink.LinkByName(newLegName)
	if err != nil {
		t.Fatal(err)
	}

	if device.Attrs().Alias != newOwner || leg.Attrs().Alias != newOwner {
		t.Fatalf(
			"recabled ownership = device %q leg %q, want %q",
			device.Attrs().Alias,
			leg.Attrs().Alias,
			newOwner,
		)
	}

	if _, err = netlink.LinkByName(oldLegName); !errors.As(err, &netlink.LinkNotFoundError{}) {
		t.Fatalf("old sidecar leg still exists or lookup failed unexpectedly: %v", err)
	}
}

//nolint:gocyclo // sequentially asserts each preserved kernel identity and namespace invariant.
func testEnsureHostInterfaceAdoptsRecabledDeviceLeg(t *testing.T) {
	t.Helper()
	runtime.LockOSThread()

	targetHandle, err := netns.Get()
	if err != nil {
		runtime.UnlockOSThread()

		t.Fatal(err)
	}

	workerHandle, err := netns.New()
	if err != nil {
		_ = targetHandle.Close()

		runtime.UnlockOSThread()

		t.Fatal(err)
	}

	if err = netns.Set(targetHandle); err != nil {
		_ = workerHandle.Close()
		_ = targetHandle.Close()

		runtime.UnlockOSThread()

		t.Fatal(err)
	}

	runtime.UnlockOSThread()

	defer targetHandle.Close() //nolint:errcheck // test namespace handle cleanup.
	defer workerHandle.Close() //nolint:errcheck // test namespace handle cleanup.

	targetFile, err := os.Open(fmt.Sprintf("/proc/self/fd/%d", int(targetHandle)))
	if err != nil {
		t.Fatal(err)
	}

	workerFile, err := os.Open(fmt.Sprintf("/proc/self/fd/%d", int(workerHandle)))
	if err != nil {
		_ = targetFile.Close()

		t.Fatal(err)
	}

	namespace := &linuxEndpointNamespace{target: targetFile, host: workerFile}
	defer namespace.Close() //nolint:errcheck // test namespace duplicate cleanup.

	const (
		interfaceName = "eth9"
		ownerPrefix   = "c9s:direct:v1:test:transport:"
		oldOwner      = ownerPrefix + "host-old"
		newOwner      = ownerPrefix + "host-new"
	)

	operations := netlinkOperations{namespace: namespace}
	oldSpec := HostInterfaceSpec{
		InterfaceID: "host-old", InterfaceName: interfaceName, HostInterface: "old-host",
		Owner: oldOwner, OwnerPrefix: ownerPrefix, MTU: 1500,
	}

	if err = operations.EnsureHostInterface(oldSpec); err != nil {
		t.Fatalf("creating initial host interface: %v", err)
	}

	device, err := netlink.LinkByName(interfaceName)
	if err != nil {
		t.Fatal(err)
	}

	address, err := netlink.ParseAddr("198.51.100.2/24")
	if err != nil {
		t.Fatal(err)
	}

	if err = netlink.AddrAdd(device, address); err != nil {
		t.Fatalf("addressing initial host device leg: %v", err)
	}

	originalIndex := device.Attrs().Index
	originalMAC := device.Attrs().HardwareAddr.String()
	newSpec := HostInterfaceSpec{
		InterfaceID: "host-new", InterfaceName: interfaceName, HostInterface: "new-host",
		Owner: newOwner, OwnerPrefix: ownerPrefix, MTU: 1500,
	}

	if err = operations.EnsureHostInterface(newSpec); err != nil {
		t.Fatalf("adopting recabled host interface: %v", err)
	}

	device, err = netlink.LinkByName(interfaceName)
	if err != nil {
		t.Fatal(err)
	}

	if device.Attrs().Index != originalIndex ||
		device.Attrs().HardwareAddr.String() != originalMAC ||
		device.Attrs().Alias != newOwner {
		t.Fatalf(
			"host device identity changed: index %d -> %d, MAC %s -> %s, owner %q",
			originalIndex,
			device.Attrs().Index,
			originalMAC,
			device.Attrs().HardwareAddr,
			device.Attrs().Alias,
		)
	}

	addresses, err := netlink.AddrList(device, netlink.FAMILY_V4)
	if err != nil {
		t.Fatal(err)
	}

	if len(addresses) != 1 {
		t.Fatalf("host device addresses changed: got %v, want %s", addresses, address)
	}

	addressPrefix, addressBits := address.Mask.Size()
	actualPrefix, actualBits := addresses[0].Mask.Size()

	if !addresses[0].IP.Equal(address.IP) ||
		actualPrefix != addressPrefix || actualBits != addressBits {
		t.Fatalf("host device addresses changed: got %v, want %s", addresses, address)
	}

	if err = namespace.Execute(func() error {
		handle, handleErr := netlink.NewHandle()
		if handleErr != nil {
			return handleErr
		}

		defer handle.Close()

		worker, lookupErr := handle.LinkByName(newSpec.HostInterface)
		if lookupErr != nil {
			return lookupErr
		}

		if worker.Attrs().Alias != newOwner {
			return errWorkerInterfaceOwnerNotAdopted
		}

		if _, lookupErr = handle.LinkByName(oldSpec.HostInterface); !errors.As(
			lookupErr,
			&netlink.LinkNotFoundError{},
		) {
			return fmt.Errorf("old worker interface still exists: %w", lookupErr)
		}

		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

const (
	fabricTestUnderlayMTU    = 1500
	fabricTestPodAddress     = "192.0.2.10"
	fabricTestPeerAddress    = "192.0.2.20"
	fabricTestMTUOwnerPrefix = "c9s:direct:v1:test:transport:"
)

// createFabricTestUnderlay realizes the preserved Pod underlay the clamp derives its bound
// from: an addressed veth end with a known MTU.
func createFabricTestUnderlay(t *testing.T) {
	t.Helper()

	attributes := netlink.NewLinkAttrs()
	attributes.Name = "underlay0"
	attributes.MTU = fabricTestUnderlayMTU

	if err := netlink.LinkAdd(&netlink.Veth{
		LinkAttrs: attributes,
		PeerName:  "underlay0p",
	}); err != nil {
		t.Fatalf("creating test underlay pair: %v", err)
	}

	underlay, err := netlink.LinkByName("underlay0")
	if err != nil {
		t.Fatal(err)
	}

	address, err := netlink.ParseAddr(fabricTestPodAddress + "/24")
	if err != nil {
		t.Fatal(err)
	}

	if err = netlink.AddrAdd(underlay, address); err != nil {
		t.Fatalf("addressing test underlay: %v", err)
	}

	if err = netlink.LinkSetUp(underlay); err != nil {
		t.Fatalf("bringing test underlay up: %v", err)
	}
}

func realizeFabricTestEndpoint(
	t *testing.T,
	spec FabricEndpointSpec,
) FabricEndpointResult {
	t.Helper()

	result, err := (netlinkOperations{}).EnsureFabricEndpoint(spec)
	if err != nil {
		t.Fatalf("realizing fabric endpoint %q: %v", spec.InterfaceID, err)
	}

	if !result.Ready {
		t.Fatalf("fabric endpoint %q is not ready: %s", spec.InterfaceID, result.Reason)
	}

	return result
}

func fabricTestEndpointSpec(
	interfaceID, interfaceName string,
	mtu, tunnelID int,
) FabricEndpointSpec {
	return FabricEndpointSpec{
		InterfaceID:   interfaceID,
		InterfaceName: interfaceName,
		Owner:         fabricTestMTUOwnerPrefix + interfaceID,
		OwnerPrefix:   fabricTestMTUOwnerPrefix,
		TunnelID:      tunnelID,
		MTU:           mtu,
		PeerTransport: fabricTestPeerAddress,
		PodAddress:    fabricTestPodAddress,
	}
}

func fabricTestLinkMTU(t *testing.T, name string) int {
	t.Helper()

	link, err := netlink.LinkByName(name)
	if err != nil {
		t.Fatalf("reading %q for MTU assertion: %v", name, err)
	}

	return link.Attrs().MTU
}

// assertFabricEndpointMTUs asserts the invariant the fabric realization guarantees: the device
// leg and the sidecar leg both carry one effective MTU.
func assertFabricEndpointMTUs(t *testing.T, spec FabricEndpointSpec, want int) {
	t.Helper()

	for _, name := range []string{
		spec.InterfaceName,
		fabricSidecarLegName(spec.InterfaceID),
	} {
		if actual := fabricTestLinkMTU(t, name); actual != want {
			t.Fatalf("endpoint %q interface %q MTU = %d, want %d",
				spec.InterfaceID, name, actual, want)
		}
	}
}

// testFabricEndpointDefaultsUnsetMTUEverywhere pins containerlab parity: with no requested
// MTU, every interface carries the containerlab default link MTU regardless of the 1500-byte
// underlay, because the wire fragments to the underlay instead of bounding the Link.
func testFabricEndpointDefaultsUnsetMTUEverywhere(t *testing.T) {
	t.Helper()

	spec := fabricTestEndpointSpec("mtu-unset", "ethmtu0", 0, 101)

	realizeFabricTestEndpoint(t, spec)
	assertFabricEndpointMTUs(t, spec, clabconstants.DefaultLinkMTU)
}

// testFabricEndpointHonorsMTUAboveUnderlay pins the wire's core promise: a requested MTU far
// above the underlay is realized exactly, with zero configuration.
func testFabricEndpointHonorsMTUAboveUnderlay(t *testing.T) {
	t.Helper()

	spec := fabricTestEndpointSpec("mtu-jumbo", "ethmtu1", 9000, 102)

	realizeFabricTestEndpoint(t, spec)
	assertFabricEndpointMTUs(t, spec, 9000)
}

func testFabricEndpointHonorsSmallMTU(t *testing.T) {
	t.Helper()

	spec := fabricTestEndpointSpec("mtu-fitting", "ethmtu2", 1400, 103)

	realizeFabricTestEndpoint(t, spec)
	assertFabricEndpointMTUs(t, spec, 1400)
}

// testFabricEndpointConvergesDriftedMTU asserts re-assertion semantics: the sidecar leg always
// converges to the requested MTU, while a device leg is clamped down but never raised behind a
// device that deliberately lowered its own MTU.
func testFabricEndpointConvergesDriftedMTU(t *testing.T) {
	t.Helper()

	spec := fabricTestEndpointSpec("mtu-drift", "ethmtu3", 1450, 104)

	realizeFabricTestEndpoint(t, spec)

	legName := fabricSidecarLegName(spec.InterfaceID)

	for _, name := range []string{spec.InterfaceName, legName} {
		link, err := netlink.LinkByName(name)
		if err != nil {
			t.Fatal(err)
		}

		if err = netlink.LinkSetMTU(link, fabricTestUnderlayMTU); err != nil {
			t.Fatalf("drifting %q MTU: %v", name, err)
		}
	}

	realizeFabricTestEndpoint(t, spec)
	assertFabricEndpointMTUs(t, spec, 1450)

	device, err := netlink.LinkByName(spec.InterfaceName)
	if err != nil {
		t.Fatal(err)
	}

	if err = netlink.LinkSetMTU(device, 1300); err != nil {
		t.Fatalf("lowering device leg MTU: %v", err)
	}

	realizeFabricTestEndpoint(t, spec)

	if actual := fabricTestLinkMTU(t, spec.InterfaceName); actual != 1300 {
		t.Fatalf("device-lowered MTU was overridden: %d, want 1300", actual)
	}

	if actual := fabricTestLinkMTU(t, legName); actual != 1450 {
		t.Fatalf("sidecar leg MTU = %d, want 1450", actual)
	}
}

func testStaleTransportLinkDeleteIsIdempotent(t *testing.T) {
	t.Helper()

	attributes := netlink.NewLinkAttrs()

	attributes.Name = "deleted-left"
	if err := netlink.LinkAdd(&netlink.Veth{
		LinkAttrs: attributes,
		PeerName:  "stale-right",
	}); err != nil {
		t.Fatalf("creating transport pair for stale-delete test: %v", err)
	}

	left, err := netlink.LinkByName("deleted-left")
	if err != nil {
		t.Fatal(err)
	}

	right, err := netlink.LinkByName("stale-right")
	if err != nil {
		t.Fatal(err)
	}

	if err = netlink.LinkDel(left); err != nil {
		t.Fatalf("deleting the first transport end: %v", err)
	}

	if err = deleteStaleTransportLink(right); err != nil {
		t.Fatalf("deleting an already-removed transport peer: %v", err)
	}
}

func testSweepTransportStateDeletesBothVethEnds(t *testing.T) {
	t.Helper()

	const (
		leftName    = "stale-left"
		rightName   = "stale-right"
		ownerPrefix = "c9s:direct:v1:test:transport:"
		owner       = ownerPrefix + "stale"
	)

	attributes := netlink.NewLinkAttrs()

	attributes.Name = leftName
	attributes.Alias = owner

	if err := netlink.LinkAdd(&netlink.Veth{
		LinkAttrs: attributes,
		PeerName:  rightName,
	}); err != nil {
		t.Fatalf("creating stale transport pair: %v", err)
	}

	right, err := netlink.LinkByName(rightName)
	if err != nil {
		t.Fatal(err)
	}

	if err = netlink.LinkSetAlias(right, owner); err != nil {
		t.Fatalf("marking stale peer ownership: %v", err)
	}

	if err = (netlinkOperations{}).SweepTransportState(ownerPrefix, nil); err != nil {
		t.Fatalf("sweeping stale transport pair: %v", err)
	}

	for _, name := range []string{leftName, rightName} {
		if _, lookupErr := netlink.LinkByName(name); !errors.As(
			lookupErr,
			&netlink.LinkNotFoundError{},
		) {
			t.Fatalf(
				"stale transport link %q still exists or lookup failed unexpectedly: %v",
				name,
				lookupErr,
			)
		}
	}
}
