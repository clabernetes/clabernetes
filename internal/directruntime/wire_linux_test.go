//go:build linux

//nolint:testpackage // exercises the unexported wire pump directly.
package directruntime

import (
	"bytes"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const fabricWireNetlinkChild = "C9S_FABRIC_WIRE_NETLINK_TEST_CHILD"

const (
	wireTestAddressA     = "192.0.2.1"
	wireTestAddressB     = "192.0.2.2"
	wireTestAddressCraft = "192.0.2.3"
	wireTestJumboMTU     = 9500
	wireTestCraftMTU     = 1500
	wireTestLinkAB       = 101
	wireTestLinkCraft    = 202
	// wireTestFragmentPayload forces multi-fragment jumbo frames over the loopback-delivered
	// underlay regardless of the loopback MTU.
	wireTestFragmentPayload = 1430
	wireTestSettleTimeout   = 8 * time.Second
	wireTestDeliverTimeout  = 3 * time.Second
	wireTestCarrierTimeout  = 2 * time.Second
)

// TestFabricWirePumpInIsolatedNamespace runs two wire endpoints against loopback-delivered UDP
// in one isolated network namespace and exercises the full wire contract: session
// establishment, jumbo frame round-trips over a small fragment payload, loss-preserving
// reassembly, robustness against malformed and stale datagrams, and carrier propagation in
// both the graceful and the peer-loss case.
func TestFabricWirePumpInIsolatedNamespace(t *testing.T) {
	runFabricNetlinkTest(t, fabricWireNetlinkChild, func() {
		harness := newWireTestHarness(t)
		defer harness.close()

		testWireCarrierEstablishes(t, harness)
		testWireRoundTripsFrames(t, harness)
		testWireDropsFrameOnFragmentLoss(t, harness)
		testWireSurvivesMalformedDatagrams(t, harness)
		testWireRejectsStaleGenerations(t, harness)
		testWireCarrierPropagation(t, harness)
		testWireHeartbeatTimeout(t, harness)
	})
}

type wireTestHarness struct {
	wireA *fabricWire
	wireB *fabricWire
	// craftFD is a plain UDP socket standing in for a third peer so tests can send crafted
	// datagrams straight onto the wire.
	craftFD  int
	craftGen uint64
}

func wireTestVethPair(t *testing.T, name, peer string, mtu int) {
	t.Helper()

	attributes := netlink.NewLinkAttrs()
	attributes.Name = name
	attributes.MTU = mtu

	pair := netlink.NewVeth(attributes)
	pair.PeerName = peer
	pair.PeerMTU = uint32(mtu) //nolint:gosec // positive test constant.

	if err := netlink.LinkAdd(pair); err != nil {
		t.Fatalf("creating wire test pair %q: %v", name, err)
	}

	for _, linkName := range []string{name, peer} {
		link, err := netlink.LinkByName(linkName)
		if err != nil {
			t.Fatal(err)
		}

		if err = netlink.LinkSetUp(link); err != nil {
			t.Fatalf("bringing wire test link %q up: %v", linkName, err)
		}
	}
}

func newWireTestHarness(t *testing.T) *wireTestHarness {
	t.Helper()

	loopback, err := netlink.LinkByName("lo")
	if err != nil {
		t.Fatal(err)
	}

	if err = netlink.LinkSetUp(loopback); err != nil {
		t.Fatalf("bringing loopback up: %v", err)
	}

	wireTestVethPair(t, "underlay0", "underlay0p", 1500)

	underlay, err := netlink.LinkByName("underlay0")
	if err != nil {
		t.Fatal(err)
	}

	for _, address := range []string{wireTestAddressA, wireTestAddressB, wireTestAddressCraft} {
		parsed, parseErr := netlink.ParseAddr(address + "/24")
		if parseErr != nil {
			t.Fatal(parseErr)
		}

		if err = netlink.AddrAdd(underlay, parsed); err != nil {
			t.Fatalf("addressing wire test underlay with %s: %v", address, err)
		}
	}

	wireTestVethPair(t, "devA", "legA", wireTestJumboMTU)
	wireTestVethPair(t, "devB", "legB", wireTestJumboMTU)
	wireTestVethPair(t, "devC", "legC", wireTestCraftMTU)

	wireA, err := newFabricWire(fabricWireConfig{
		LocalAddress:    netip.MustParseAddr(wireTestAddressA),
		FragmentPayload: wireTestFragmentPayload,
	})
	if err != nil {
		t.Fatalf("starting wire endpoint A: %v", err)
	}

	wireB, err := newFabricWire(fabricWireConfig{
		LocalAddress:    netip.MustParseAddr(wireTestAddressB),
		FragmentPayload: wireTestFragmentPayload,
	})
	if err != nil {
		t.Fatalf("starting wire endpoint B: %v", err)
	}

	if err = wireA.EnsureLink(fabricWireLinkSpec{
		LinkID:        wireTestLinkAB,
		Owner:         "test:wire:a",
		LegName:       "legA",
		PeerTransport: wireTestAddressB,
		PeerAddress:   netip.MustParseAddr(wireTestAddressB),
		MTU:           wireTestJumboMTU,
	}); err != nil {
		t.Fatalf("registering wire link on A: %v", err)
	}

	if err = wireB.EnsureLink(fabricWireLinkSpec{
		LinkID:        wireTestLinkAB,
		Owner:         "test:wire:b",
		LegName:       "legB",
		PeerTransport: wireTestAddressA,
		PeerAddress:   netip.MustParseAddr(wireTestAddressA),
		MTU:           wireTestJumboMTU,
	}); err != nil {
		t.Fatalf("registering wire link on B: %v", err)
	}

	if err = wireB.EnsureLink(fabricWireLinkSpec{
		LinkID:        wireTestLinkCraft,
		Owner:         "test:wire:craft",
		LegName:       "legC",
		PeerTransport: wireTestAddressCraft,
		PeerAddress:   netip.MustParseAddr(wireTestAddressCraft),
		MTU:           wireTestCraftMTU,
	}); err != nil {
		t.Fatalf("registering crafted-peer wire link on B: %v", err)
	}

	craftFD, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("opening crafted-peer socket: %v", err)
	}

	if err = unix.Bind(craftFD, &unix.SockaddrInet4{
		Port: 14790,
		Addr: netip.MustParseAddr(wireTestAddressCraft).As4(),
	}); err != nil {
		t.Fatalf("binding crafted-peer socket: %v", err)
	}

	generation, err := newFabricWireGeneration()
	if err != nil {
		t.Fatal(err)
	}

	return &wireTestHarness{
		wireA:    wireA,
		wireB:    wireB,
		craftFD:  craftFD,
		craftGen: generation,
	}
}

func (h *wireTestHarness) close() {
	_ = h.wireA.Close()
	_ = h.wireB.Close()
	_ = unix.Close(h.craftFD)
}

func (h *wireTestHarness) craftSend(t *testing.T, datagram []byte) {
	t.Helper()

	if err := unix.Sendto(h.craftFD, datagram, 0, &unix.SockaddrInet4{
		Port: 14790,
		Addr: netip.MustParseAddr(wireTestAddressB).As4(),
	}); err != nil {
		t.Fatalf("sending crafted wire datagram: %v", err)
	}
}

// craftEstablish makes the crafted peer's session and its link carrier live from the wire's
// point of view: a heartbeat plus an oper-up link state advertisement.
func (h *wireTestHarness) craftEstablish(t *testing.T) {
	t.Helper()

	h.craftSend(t, encodeFabricWireHeartbeat(h.craftGen))
	h.craftSend(t, encodeFabricWireLinkStates(h.craftGen, []fabricWireLinkState{
		{LinkID: wireTestLinkCraft, OperUp: true},
	}))

	waitForWireCondition(t, wireTestSettleTimeout, "crafted-peer link carrier", func() bool {
		return wireTestLinkOperUp(t, "devC")
	})
}

func waitForWireCondition(t *testing.T, timeout time.Duration, what string, check func() bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return
		}

		time.Sleep(25 * time.Millisecond)
	}

	t.Fatalf("%s did not converge within %s", what, timeout)
}

func wireTestLinkOperUp(t *testing.T, name string) bool {
	t.Helper()

	link, err := netlink.LinkByName(name)
	if err != nil {
		t.Fatal(err)
	}

	return link.Attrs().OperState == netlink.OperUp
}

func wireTestLinkAdminUp(t *testing.T, name string) bool {
	t.Helper()

	link, err := netlink.LinkByName(name)
	if err != nil {
		t.Fatal(err)
	}

	return link.Attrs().Flags&net.FlagUp != 0
}

func wireTestSetAdmin(t *testing.T, name string, up bool) {
	t.Helper()

	link, err := netlink.LinkByName(name)
	if err != nil {
		t.Fatal(err)
	}

	if up {
		err = netlink.LinkSetUp(link)
	} else {
		err = netlink.LinkSetDown(link)
	}

	if err != nil {
		t.Fatalf("setting %q admin state: %v", name, err)
	}
}

func openWireTestPacketSocket(t *testing.T, name string) int {
	t.Helper()

	link, err := netlink.LinkByName(name)
	if err != nil {
		t.Fatal(err)
	}

	fd, err := openFabricWireCapture(link.Attrs().Index)
	if err != nil {
		t.Fatal(err)
	}

	return fd
}

// buildWireTestFrame renders one recognizable Ethernet frame of exactly size bytes.
func buildWireTestFrame(size int, tag byte) []byte {
	frame := make([]byte, size)
	copy(frame, []byte{
		0x02, 0xc9, 0x00, 0x00, 0x00, tag, // destination
		0x02, 0xc9, 0x00, 0x00, 0x01, tag, // source
		0x88, 0xb5, // local experimental EtherType
	})

	for index := 14; index < size; index++ {
		frame[index] = byte(index * 31 % 256)
	}

	return frame
}

// expectWireTestFrame reports whether the wanted frame arrives on the packet socket before the
// timeout, skipping unrelated traffic such as IPv6 autoconfiguration.
func expectWireTestFrame(t *testing.T, fd int, want []byte, timeout time.Duration) bool {
	t.Helper()

	buffer := make([]byte, fabricWireMaximumFrameSize+1)
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		n, from, err := unix.Recvfrom(fd, buffer, unix.MSG_DONTWAIT)
		if err != nil {
			if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) {
				pollFabricWireFD(fd)

				continue
			}

			t.Fatalf("reading wire test packet socket: %v", err)
		}

		source, isLinkLayer := from.(*unix.SockaddrLinklayer)
		if isLinkLayer && source.Pkttype == unix.PACKET_OUTGOING {
			continue
		}

		if bytes.Equal(buffer[:n], want) {
			return true
		}
	}

	return false
}

// testWireCarrierEstablishes proves that a link starts carrier-down and comes up only once
// heartbeats and link state flow in both directions.
func testWireCarrierEstablishes(t *testing.T, _ *wireTestHarness) {
	t.Helper()

	waitForWireCondition(t, wireTestSettleTimeout, "wire A/B carrier", func() bool {
		return wireTestLinkOperUp(t, "devA") && wireTestLinkOperUp(t, "devB") &&
			wireTestLinkAdminUp(t, "legA") && wireTestLinkAdminUp(t, "legB")
	})
}

func testWireRoundTripsFrames(t *testing.T, _ *wireTestHarness) {
	t.Helper()

	injector := openWireTestPacketSocket(t, "devA")

	defer func() { _ = unix.Close(injector) }()

	capture := openWireTestPacketSocket(t, "devB")

	defer func() { _ = unix.Close(capture) }()

	for tag, size := range map[byte]int{0x01: 100, 0x02: wireTestJumboMTU + 14} {
		frame := buildWireTestFrame(size, tag)

		if _, err := unix.Write(injector, frame); err != nil {
			t.Fatalf("injecting %d-byte wire test frame: %v", size, err)
		}

		if !expectWireTestFrame(t, capture, frame, wireTestDeliverTimeout) {
			t.Fatalf("%d-byte frame did not cross the wire", size)
		}
	}
}

// craftFragments renders the data datagrams for one frame on the crafted-peer link.
func (h *wireTestHarness) craftFragments(
	t *testing.T,
	frameID uint32,
	frame []byte,
) [][]byte {
	t.Helper()

	fragments, err := splitFabricWireFrame(frame, 400)
	if err != nil {
		t.Fatal(err)
	}

	datagrams := make([][]byte, 0, len(fragments))
	for index, fragment := range fragments {
		datagrams = append(datagrams, encodeFabricWireFragment(h.craftGen, fabricWireDataHeader{
			LinkID:      wireTestLinkCraft,
			FrameID:     frameID,
			FragIndex:   uint8(index),
			FragCount:   uint8(len(fragments)), //nolint:gosec // bounded by the split.
			TotalLength: uint16(len(frame)),    //nolint:gosec // bounded by the split.
		}, fragment))
	}

	return datagrams
}

// testWireDropsFrameOnFragmentLoss proves loss-preserving semantics: a frame missing one
// fragment never appears, and the next complete frame is unaffected.
func testWireDropsFrameOnFragmentLoss(t *testing.T, h *wireTestHarness) {
	t.Helper()

	h.craftEstablish(t)

	capture := openWireTestPacketSocket(t, "devC")

	defer func() { _ = unix.Close(capture) }()

	lossy := buildWireTestFrame(1200, 0x10)
	for index, datagram := range h.craftFragments(t, 1000, lossy) {
		if index == 1 {
			continue
		}

		h.craftSend(t, datagram)
	}

	if expectWireTestFrame(t, capture, lossy, 500*time.Millisecond) {
		t.Fatal("frame with a lost fragment was delivered")
	}

	complete := buildWireTestFrame(1200, 0x11)
	for _, datagram := range h.craftFragments(t, 1001, complete) {
		h.craftSend(t, datagram)
	}

	if !expectWireTestFrame(t, capture, complete, wireTestDeliverTimeout) {
		t.Fatal("complete frame after a lossy frame was not delivered")
	}
}

// testWireSurvivesMalformedDatagrams fuzzes the datagram surface and proves the pump still
// forwards honest traffic afterwards.
func testWireSurvivesMalformedDatagrams(t *testing.T, h *wireTestHarness) {
	t.Helper()

	h.craftEstablish(t)

	malformed := [][]byte{
		{},
		{fabricWireVersion},
		{0xff, 0xff, 0xff},
		encodeFabricWireHeartbeat(h.craftGen)[:fabricWireHeaderSize-1],
		append(
			encodeFabricWireLinkStates(h.craftGen, []fabricWireLinkState{{LinkID: 1}}),
			0xbe,
		),
		encodeFabricWireFragment(h.craftGen, fabricWireDataHeader{
			LinkID: wireTestLinkCraft, FrameID: 2000, FragIndex: 9, FragCount: 4,
			TotalLength: 100,
		}, []byte("bogus")),
		encodeFabricWireFragment(h.craftGen, fabricWireDataHeader{
			LinkID: 0xffffffff, FrameID: 2001, FragIndex: 0, FragCount: 1, TotalLength: 5,
		}, []byte("alien")),
		bytes.Repeat([]byte{0xa5}, 4096),
	}
	for _, datagram := range malformed {
		if len(datagram) == 0 {
			continue
		}

		h.craftSend(t, datagram)
	}

	capture := openWireTestPacketSocket(t, "devC")

	defer func() { _ = unix.Close(capture) }()

	frame := buildWireTestFrame(900, 0x22)
	for _, datagram := range h.craftFragments(t, 2002, frame) {
		h.craftSend(t, datagram)
	}

	if !expectWireTestFrame(t, capture, frame, wireTestDeliverTimeout) {
		t.Fatal("wire stopped forwarding after malformed datagrams")
	}
}

// testWireRejectsStaleGenerations proves that datagrams from a superseded peer process
// generation are dropped while the successor generation flows.
func testWireRejectsStaleGenerations(t *testing.T, h *wireTestHarness) {
	t.Helper()

	h.craftEstablish(t)

	staleGen := h.craftGen

	// The peer restarts: a strictly newer generation takes over the session.
	h.craftGen += 1 << 32
	h.craftEstablish(t)

	capture := openWireTestPacketSocket(t, "devC")

	defer func() { _ = unix.Close(capture) }()

	currentGen := h.craftGen

	h.craftGen = staleGen
	stale := buildWireTestFrame(700, 0x33)

	for _, datagram := range h.craftFragments(t, 3000, stale) {
		h.craftSend(t, datagram)
	}

	h.craftGen = currentGen

	if expectWireTestFrame(t, capture, stale, 500*time.Millisecond) {
		t.Fatal("stale-generation frame was delivered")
	}

	fresh := buildWireTestFrame(700, 0x34)
	for _, datagram := range h.craftFragments(t, 3001, fresh) {
		h.craftSend(t, datagram)
	}

	if !expectWireTestFrame(t, capture, fresh, wireTestDeliverTimeout) {
		t.Fatal("current-generation frame was not delivered")
	}
}

// testWireCarrierPropagation proves the graceful path: an admin-down on one device leg shows
// up as loss of carrier on the peer device leg, which itself stays admin-up, and recovery
// follows the same wire.
func testWireCarrierPropagation(t *testing.T, _ *wireTestHarness) {
	t.Helper()

	wireTestSetAdmin(t, "devA", false)

	waitForWireCondition(t, wireTestCarrierTimeout, "peer carrier loss", func() bool {
		return !wireTestLinkOperUp(t, "devB")
	})

	if !wireTestLinkAdminUp(t, "devB") {
		t.Fatal("carrier propagation touched the peer device leg admin state")
	}

	wireTestSetAdmin(t, "devA", true)

	waitForWireCondition(t, wireTestCarrierTimeout, "peer carrier recovery", func() bool {
		return wireTestLinkOperUp(t, "devB") && wireTestLinkOperUp(t, "devA")
	})
}

// testWireHeartbeatTimeout proves the crash path: a silent peer takes the link carrier-down
// within the session timeout. This scenario kills wire endpoint A and must run last.
func testWireHeartbeatTimeout(t *testing.T, h *wireTestHarness) {
	t.Helper()

	if err := h.wireA.Close(); err != nil {
		t.Fatalf("closing wire endpoint A: %v", err)
	}

	waitForWireCondition(
		t,
		fabricWireSessionTimeout+2*fabricWireHeartbeatInterval+time.Second,
		"carrier loss after heartbeat silence",
		func() bool {
			return !wireTestLinkOperUp(t, "devB")
		},
	)
}
