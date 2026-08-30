//go:build linux

//nolint:err113,gocyclo,mnd // single-pass boundary logic with structured one-off diagnostics and protocol literals.
package directruntime

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"runtime"
	"sync"
	"time"
	"unsafe"

	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const (
	// fabricWirePollInterval bounds how long a nonblocking receive loop sleeps before it
	// rechecks shutdown; it never delays delivery of available datagrams.
	fabricWirePollInterval = 100 * time.Millisecond
	// fabricWireSocketBuffer is requested for the shared UDP socket in both directions so
	// bursts of fragmented jumbo frames ride kernel queues instead of dropping early.
	fabricWireSocketBuffer = 4 << 20
	// fabricWireReceiveBatchSize bounds one recvmmsg batch on the shared UDP socket.
	fabricWireReceiveBatchSize = 16
	// fabricWireResolveInterval paces in-pump peer re-resolution while a session is down, so a
	// rescheduled peer Pod is found as soon as its DNS record moves.
	fabricWireResolveInterval = 2 * time.Second
	// fabricWireCountersInterval paces the periodic per-link drop diagnostics.
	fabricWireCountersInterval = 30 * time.Second
	// fabricWireFrameHeadroom is the non-MTU-counted Ethernet overhead one delivered frame may
	// carry over the interface MTU: header(14) + two 802.1Q tags(8). The reassembly bound is
	// deliberately not the tightest constraint -- the legs' own transmit path admits one
	// in-band tag above MTU (the kernel's, and most NICs', single-tag budget), and that
	// constraint should surface at the injection counter, not as a reassembly drop.
	fabricWireFrameHeadroom = 22
	// fabricWireCarrierSettleWindow suppresses local carrier-down observations right after the
	// wire raised a leg: linkwatch reports the pre-raise oper state asynchronously, and
	// advertising that echo as genuine local loss would force the peer down and oscillate. A
	// leg that is genuinely still down past the window is re-observed by the periodic sample.
	fabricWireCarrierSettleWindow = 500 * time.Millisecond
)

// fabricWireLinkSpec is one link's registration with the wire: the sidecar leg it captures
// from and injects into, the wire link identity shared with the peer endpoint, and the peer
// transport it sends to.
type fabricWireLinkSpec struct {
	LinkID        uint32
	Owner         string
	LegName       string
	PeerTransport string
	// PeerAddress is the currently resolved peer transport address; an invalid address means
	// the peer is not yet resolvable and the wire keeps the link carrier-down.
	PeerAddress netip.Addr
	// MTU bounds the frames the link delivers; larger reassembled frames are dropped as
	// oversize.
	MTU int
}

// fabricWireLinkCounters is the per-link observability state surfaced through periodic
// diagnostics; drops are classified by cause so wire behavior stays explainable.
type fabricWireLinkCounters struct {
	txFrames            uint64
	txFragments         uint64
	rxFrames            uint64
	rxFragments         uint64
	dropExpiry          uint64
	dropMemoryCap       uint64
	dropGeometry        uint64
	dropOversize        uint64
	dropStaleGeneration uint64
	dropForeignSource   uint64
	dropUnresolvedPeer  uint64
	dropSendFull        uint64
	dropInject          uint64
}

type fabricWireLink struct {
	linkID  uint32
	owner   string
	legName string
	// legIndex is the sidecar leg ifindex the capture socket is bound to; the leg is
	// sidecar-owned forever, so its oper state mirrors the device leg even after a device
	// adopts and moves the device-facing end.
	legIndex      int
	packetFD      int
	peerTransport string
	peerAddr      netip.Addr
	mtu           int

	// lastLocalUp is the advertised local carrier state: the sidecar leg oper state observed
	// while the wire is not holding the leg administratively down. While forcedDown it is
	// deliberately frozen so a wire-imposed down is never echoed back as local carrier loss.
	lastLocalUp bool
	// remoteOperUp is the peer's last advertised carrier state for this link in the current
	// peer generation.
	remoteOperUp bool
	// forcedDown records that the wire owns the leg's current admin-down: the peer end is
	// gone or down, so the device leg must show loss of carrier.
	forcedDown bool
	// settleUntil bounds the echo suppression after the wire raised this leg; down
	// observations before it are linkwatch echoes, not local state.
	settleUntil time.Time

	frameID     uint32
	reassembler *wireReassembler
	counters    fabricWireLinkCounters
	reported    fabricWireLinkCounters

	resolveInFlight bool
	lastResolve     time.Time

	stop chan struct{}
}

// fabricWireSession is the soft state for one adjacent Pod: liveness and the peer generation
// whose datagrams are current. Everything here is re-derivable from the wire itself.
type fabricWireSession struct {
	remote          netip.Addr
	generation      uint64
	generationKnown bool
	lastHeard       time.Time
	up              bool
}

// fabricWireConfig fixes one wire instance's identity at construction.
type fabricWireConfig struct {
	// LocalAddress is the Pod transport address the shared UDP socket binds.
	LocalAddress netip.Addr
	// FragmentPayload is the per-fragment payload size derived from the local underlay MTU.
	FragmentPayload int
}

// fabricWire is one sidecar's wire endpoint: the shared UDP socket, every registered link's
// capture pump, the peer sessions, and the carrier state machine. One instance exists per
// connectivity process in production; tests run several against distinct local addresses.
type fabricWire struct {
	config     fabricWireConfig
	generation uint64
	udpFD      int

	mu       sync.Mutex
	links    map[uint32]*fabricWireLink
	byLeg    map[int]uint32
	sessions map[netip.Addr]*fabricWireSession

	stop    chan struct{}
	closed  bool
	workers sync.WaitGroup
}

// newFabricWireGeneration identifies this process instance on the wire. The high half orders
// instances by start time so a replacement Pod's datagrams displace a predecessor's; the low
// half disambiguates same-second restarts.
func newFabricWireGeneration() (uint64, error) {
	var nonce [4]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return 0, fmt.Errorf("deriving wire generation nonce: %w", err)
	}

	seconds := uint64(time.Now().Unix()) //nolint:gosec // wall time is non-negative.

	return seconds<<32 | uint64(binary.BigEndian.Uint32(nonce[:])), nil
}

func newFabricWire(config fabricWireConfig) (*fabricWire, error) {
	if !config.LocalAddress.Is4() {
		return nil, errors.New("fabric wire local address must be IPv4")
	}

	if config.FragmentPayload < 1 {
		return nil, errors.New("fabric wire fragment payload must be positive")
	}

	generation, err := newFabricWireGeneration()
	if err != nil {
		return nil, err
	}

	fd, err := unix.Socket(
		unix.AF_INET,
		unix.SOCK_DGRAM|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK,
		0,
	)
	if err != nil {
		return nil, fmt.Errorf("opening fabric wire socket: %w", err)
	}

	// Buffer growth is best effort: the kernel caps unprivileged requests, and the wire works
	// at default sizes with a higher burst-drop rate.
	_ = unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_RCVBUF, fabricWireSocketBuffer)
	_ = unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_SNDBUF, fabricWireSocketBuffer)

	if err = unix.Bind(fd, &unix.SockaddrInet4{
		Port: clabernetesconstants.FabricWireServicePort,
		Addr: config.LocalAddress.As4(),
	}); err != nil {
		_ = unix.Close(fd)

		return nil, fmt.Errorf("binding fabric wire socket: %w", err)
	}

	wire := &fabricWire{
		config:     config,
		generation: generation,
		udpFD:      fd,
		links:      map[uint32]*fabricWireLink{},
		byLeg:      map[int]uint32{},
		sessions:   map[netip.Addr]*fabricWireSession{},
		stop:       make(chan struct{}),
	}

	wire.workers.Add(3)

	go wire.receiveLoop()
	go wire.tickLoop()
	go wire.watchLinks()

	return wire, nil
}

// Close stops every pump goroutine and releases the wire's sockets. Registered sidecar legs
// keep their current admin state; a restarting sidecar re-derives everything from the plan.
func (w *fabricWire) Close() error {
	w.mu.Lock()

	if w.closed {
		w.mu.Unlock()

		return nil
	}

	w.closed = true

	close(w.stop)

	for _, link := range w.links {
		close(link.stop)
	}

	w.mu.Unlock()

	w.workers.Wait()

	return unix.Close(w.udpFD)
}

// EnsureLink converges one link registration idempotently: repeated calls with unchanged
// parameters do nothing, a moved sidecar leg re-binds the capture pump, and a re-resolved
// peer address re-homes the link's session.
func (w *fabricWire) EnsureLink(spec fabricWireLinkSpec) error {
	if spec.LinkID == 0 || spec.LegName == "" || spec.Owner == "" || spec.MTU < 1 {
		return errors.New("fabric wire link registration is incomplete")
	}

	leg, err := netlink.LinkByName(spec.LegName)
	if err != nil {
		return fmt.Errorf("reading fabric wire leg %q: %w", spec.LegName, err)
	}

	legIndex := leg.Attrs().Index
	legOperUp := leg.Attrs().OperState == netlink.OperUp

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return errors.New("fabric wire is closed")
	}

	link, exists := w.links[spec.LinkID]
	if !exists {
		packetFD, openErr := openFabricWireCapture(legIndex)
		if openErr != nil {
			return openErr
		}

		link = &fabricWireLink{
			linkID:      spec.LinkID,
			legIndex:    legIndex,
			packetFD:    packetFD,
			lastLocalUp: legOperUp,
			settleUntil: time.Now().Add(fabricWireCarrierSettleWindow),
			stop:        make(chan struct{}),
		}
		link.reassembler = newWireReassembler(func(cause fabricWireDropCause) {
			switch cause {
			case fabricWireDropNone:
			case fabricWireDropExpiry:
				link.counters.dropExpiry++
			case fabricWireDropMemoryCap:
				link.counters.dropMemoryCap++
			case fabricWireDropGeometry:
				link.counters.dropGeometry++
			case fabricWireDropOversize:
				link.counters.dropOversize++
			}
		})

		w.links[spec.LinkID] = link
		w.byLeg[legIndex] = spec.LinkID

		w.workers.Add(1)

		go w.captureLoop(link, packetFD, link.stop)
	} else if link.legIndex != legIndex {
		// The pair was recreated; move the capture pump to the new leg.
		delete(w.byLeg, link.legIndex)
		close(link.stop)

		packetFD, openErr := openFabricWireCapture(legIndex)
		if openErr != nil {
			return openErr
		}

		link.legIndex = legIndex
		link.packetFD = packetFD
		link.stop = make(chan struct{})
		link.forcedDown = false
		link.lastLocalUp = legOperUp
		link.settleUntil = time.Now().Add(fabricWireCarrierSettleWindow)

		w.byLeg[legIndex] = spec.LinkID

		w.workers.Add(1)

		go w.captureLoop(link, packetFD, link.stop)
	}

	link.owner = spec.Owner
	link.legName = spec.LegName
	link.peerTransport = spec.PeerTransport
	link.mtu = spec.MTU

	if spec.PeerAddress.IsValid() && spec.PeerAddress != link.peerAddr {
		link.peerAddr = spec.PeerAddress.Unmap()
		w.noteWiref("link %d peer transport is %s", link.linkID, link.peerAddr)
	}

	w.applyLinkCarrierLocked(link)

	return nil
}

// SweepLinks stops and forgets links owned by this Pod whose owners left the desired plan,
// mirroring the netlink transport sweep for the wire's soft state.
func (w *fabricWire) SweepLinks(ownerPrefix string, keepOwners []string) {
	keep := make(map[string]bool, len(keepOwners))
	for _, owner := range keepOwners {
		keep[owner] = true
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	for linkID, link := range w.links {
		if keep[link.owner] || len(link.owner) < len(ownerPrefix) ||
			link.owner[:len(ownerPrefix)] != ownerPrefix {
			continue
		}

		delete(w.links, linkID)
		delete(w.byLeg, link.legIndex)
		close(link.stop)
	}

	for remote, session := range w.sessions {
		if !w.sessionReferencedLocked(remote) &&
			time.Since(session.lastHeard) > fabricWireSessionTimeout {
			delete(w.sessions, remote)
		}
	}
}

// ForcesLegDown reports whether the wire currently owns an administrative down on the given
// link's sidecar leg, so realization convergence never fights the carrier state machine.
func (w *fabricWire) ForcesLegDown(linkID uint32) (known, forced bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	link, exists := w.links[linkID]
	if !exists {
		return false, false
	}

	return true, link.forcedDown
}

func (w *fabricWire) sessionReferencedLocked(remote netip.Addr) bool {
	for _, link := range w.links {
		if link.peerAddr == remote {
			return true
		}
	}

	return false
}

// openFabricWireCapture opens the AF_PACKET socket carrying one link's frames in both
// directions: reads observe every frame the device sends into its leg, and writes inject
// reassembled peer frames back out of the sidecar leg.
func openFabricWireCapture(legIndex int) (int, error) {
	protocol := hostToNetworkShort(uint16(unix.ETH_P_ALL))

	fd, err := unix.Socket(
		unix.AF_PACKET,
		unix.SOCK_RAW|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK,
		int(protocol),
	)
	if err != nil {
		return -1, fmt.Errorf("opening fabric wire capture socket: %w", err)
	}

	if err = unix.Bind(fd, &unix.SockaddrLinklayer{
		Protocol: protocol,
		Ifindex:  legIndex,
	}); err != nil {
		_ = unix.Close(fd)

		return -1, fmt.Errorf("binding fabric wire capture socket: %w", err)
	}

	// The kernel RX path moves an outer 802.1Q/802.1ad tag into skb metadata before packet
	// taps run, so a plain read silently delivers tagged frames untagged. Auxdata carries the
	// stripped tag so the capture can put it back in-band.
	if err = unix.SetsockoptInt(fd, unix.SOL_PACKET, unix.PACKET_AUXDATA, 1); err != nil {
		_ = unix.Close(fd)

		return -1, fmt.Errorf("enabling fabric wire capture VLAN auxdata: %w", err)
	}

	return fd, nil
}

// vlanTagSize is one in-band 802.1Q tag: TPID(2) + TCI(2).
const vlanTagSize = 4

// vlanEthernetHeaderPrefix is the destination+source MAC prefix preceding the tag insertion
// point of an Ethernet header.
const vlanEthernetHeaderPrefix = 12

// capturedVLANTag extracts the kernel-stripped outer VLAN tag of one captured frame from its
// packet-socket control messages; ok reports that a tag must be reinserted.
func capturedVLANTag(oob []byte) (tpid, tci uint16, ok bool) {
	controls, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return 0, 0, false
	}

	for _, control := range controls {
		if control.Header.Level != unix.SOL_PACKET ||
			control.Header.Type != unix.PACKET_AUXDATA ||
			len(control.Data) < int(unsafe.Sizeof(unix.TpacketAuxdata{})) {
			continue
		}

		//nolint:gosec // fixed-layout kernel ABI struct.
		aux := (*unix.TpacketAuxdata)(unsafe.Pointer(&control.Data[0]))
		if aux.Status&unix.TP_STATUS_VLAN_VALID == 0 {
			continue
		}

		tpid = uint16(unix.ETH_P_8021Q)
		if aux.Status&unix.TP_STATUS_VLAN_TPID_VALID != 0 {
			tpid = aux.Vlan_tpid
		}

		return tpid, aux.Vlan_tci, true
	}

	return 0, 0, false
}

// captureLoop pumps one link's device-originated frames onto the wire. It owns the packet
// socket: the socket is closed here, after the link was already unpublished, so no injection
// path can race a reused descriptor. The stop channel is passed explicitly because a re-bound
// link replaces the field with a fresh channel for its successor pump.
func (w *fabricWire) captureLoop(link *fabricWireLink, packetFD int, stop <-chan struct{}) {
	defer w.workers.Done()
	defer func() { _ = unix.Close(packetFD) }()

	// Frames are read at a tag-sized offset so a kernel-stripped outer VLAN tag can be put
	// back in-band by shifting only the MAC prefix, never the payload.
	buffer := make([]byte, fabricWireMaximumFrameSize+1+vlanTagSize)
	oob := make([]byte, unix.CmsgSpace(int(unsafe.Sizeof(unix.TpacketAuxdata{}))))

	for {
		select {
		case <-stop:
			return
		default:
		}

		n, oobn, _, from, err := unix.Recvmsg(
			packetFD,
			buffer[vlanTagSize:],
			oob,
			unix.MSG_DONTWAIT,
		)
		if err != nil {
			if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) ||
				errors.Is(err, unix.EINTR) || errors.Is(err, unix.ENETDOWN) {
				pollFabricWireFD(packetFD)

				continue
			}

			return
		}

		source, isLinkLayer := from.(*unix.SockaddrLinklayer)
		if n == 0 || (isLinkLayer && source.Pkttype == unix.PACKET_OUTGOING) {
			continue
		}

		frame := buffer[vlanTagSize : vlanTagSize+n]

		if tpid, tci, tagged := capturedVLANTag(oob[:oobn]); tagged &&
			n >= vlanEthernetHeaderPrefix {
			copy(buffer[:vlanEthernetHeaderPrefix], frame[:vlanEthernetHeaderPrefix])
			binary.BigEndian.PutUint16(buffer[vlanEthernetHeaderPrefix:], tpid)
			binary.BigEndian.PutUint16(buffer[vlanEthernetHeaderPrefix+2:], tci)
			frame = buffer[:n+vlanTagSize]
		}

		w.transmitFrame(link, frame)
	}
}

// transmitFrame fragments one captured frame and sends every fragment back-to-back on the
// shared socket. A frame whose peer is unresolved is dropped, honestly, like a cable whose far
// end is unplugged.
func (w *fabricWire) transmitFrame(link *fabricWireLink, frame []byte) {
	w.mu.Lock()

	peer := link.peerAddr
	generation := w.generation
	payloadSize := w.config.FragmentPayload

	if !peer.IsValid() {
		link.counters.dropUnresolvedPeer++
		w.mu.Unlock()

		return
	}

	link.frameID++
	frameID := link.frameID
	w.mu.Unlock()

	fragments, err := splitFabricWireFrame(frame, payloadSize)
	if err != nil {
		w.mu.Lock()
		link.counters.dropOversize++
		w.mu.Unlock()

		return
	}

	datagrams := make([][]byte, len(fragments))
	for index, fragment := range fragments {
		datagrams[index] = encodeFabricWireFragment(generation, fabricWireDataHeader{
			LinkID:      link.linkID,
			FrameID:     frameID,
			FragIndex:   uint8(index),
			FragCount:   uint8(len(fragments)), //nolint:gosec // bounded by splitFabricWireFrame.
			TotalLength: uint16(len(frame)),    //nolint:gosec // bounded by splitFabricWireFrame.
		}, fragment)
	}

	sent, err := sendFabricWireDatagrams(w.udpFD, peer, datagrams)

	w.mu.Lock()
	link.counters.txFragments += uint64(sent) //nolint:gosec // non-negative count.

	if err != nil || sent < len(datagrams) {
		link.counters.dropSendFull++
	} else {
		link.counters.txFrames++
	}
	w.mu.Unlock()
}

// receiveLoop drains the shared UDP socket and dispatches control and data messages. One
// socket carries both, so the carrier signal can never disagree with the path it describes.
func (w *fabricWire) receiveLoop() {
	defer w.workers.Done()

	batch := newFabricWireReceiveBatch(fabricWireReceiveBatchSize)

	for {
		select {
		case <-w.stop:
			return
		default:
		}

		count, err := batch.receive(w.udpFD)
		if err != nil {
			if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) ||
				errors.Is(err, unix.EINTR) {
				pollFabricWireFD(w.udpFD)

				continue
			}

			return
		}

		now := time.Now()

		for index := range count {
			datagram, source := batch.message(index)
			w.handleDatagram(now, source, datagram)
		}
	}
}

func (w *fabricWire) handleDatagram(now time.Time, source netip.Addr, datagram []byte) {
	header, body, err := parseFabricWireHeader(datagram)
	if err != nil {
		return
	}

	switch header.MsgType {
	case fabricWireMsgData:
		dataHeader, payload, dataErr := parseFabricWireDataHeader(body)
		if dataErr != nil {
			return
		}

		w.handleData(now, source, header.Generation, dataHeader, payload)
	case fabricWireMsgLinkState:
		states, statesErr := parseFabricWireLinkStates(body)
		if statesErr != nil {
			return
		}

		w.handleLinkStates(now, source, header.Generation, states)
	case fabricWireMsgHeartbeat:
		w.mu.Lock()

		if w.refreshSessionLocked(now, source, header.Generation) != nil {
			w.applyPeerCarrierLocked(source)
		}

		w.mu.Unlock()
	}
}

func (w *fabricWire) handleData(
	now time.Time,
	source netip.Addr,
	generation uint64,
	header fabricWireDataHeader,
	payload []byte,
) {
	var (
		frame    []byte
		packetFD = -1
	)

	w.mu.Lock()

	link, exists := w.links[header.LinkID]
	if !exists {
		w.mu.Unlock()

		return
	}

	if link.peerAddr != source {
		link.counters.dropForeignSource++
		w.mu.Unlock()

		return
	}

	session := w.refreshSessionLocked(now, source, generation)
	if session == nil {
		link.counters.dropStaleGeneration++
		w.mu.Unlock()

		return
	}

	w.applyPeerCarrierLocked(source)

	link.counters.rxFragments++

	assembled, complete := link.reassembler.absorb(now, header, payload)
	if complete {
		if len(assembled) > link.mtu+fabricWireFrameHeadroom {
			link.counters.dropOversize++
		} else {
			frame = assembled
			packetFD = link.packetFD
			link.counters.rxFrames++
		}
	}

	if frame != nil {
		// The injection happens under the lock so the packet socket cannot be closed and
		// reused between lookup and write; the nonblocking send never parks the lock.
		if err := injectFabricWireFrame(packetFD, frame); err != nil {
			link.counters.dropInject++
		}
	}

	w.mu.Unlock()
}

func (w *fabricWire) handleLinkStates(
	now time.Time,
	source netip.Addr,
	generation uint64,
	states []fabricWireLinkState,
) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.refreshSessionLocked(now, source, generation) == nil {
		return
	}

	for _, state := range states {
		link, exists := w.links[state.LinkID]
		if !exists || link.peerAddr != source {
			continue
		}

		if link.remoteOperUp != state.OperUp {
			link.remoteOperUp = state.OperUp
			w.noteWiref(
				"link %d peer carrier is %s",
				link.linkID,
				fabricWireStateWord(state.OperUp),
			)
		}
	}

	w.applyPeerCarrierLocked(source)
}

// refreshSessionLocked accepts one datagram into its peer session, returning nil when the
// datagram belongs to a stale peer generation. A newer generation displaces the current one
// immediately; an unordered different generation is adopted only once the current holder has
// been silent past the session timeout, so a dead process's stragglers can never displace a
// live peer.
func (w *fabricWire) refreshSessionLocked(
	now time.Time,
	source netip.Addr,
	generation uint64,
) *fabricWireSession {
	session, exists := w.sessions[source]
	if !exists {
		if !w.sessionReferencedLocked(source) {
			return nil
		}

		session = &fabricWireSession{remote: source}
		w.sessions[source] = session
	}

	if session.generationKnown && session.generation != generation {
		if generation < session.generation &&
			now.Sub(session.lastHeard) <= fabricWireSessionTimeout {
			return nil
		}

		w.noteWiref("peer %s restarted; resetting link state", source)
		w.resetPeerLinksLocked(source)
	}

	if !session.generationKnown || session.generation != generation {
		session.generation = generation
		session.generationKnown = true
	}

	session.lastHeard = now

	if !session.up {
		session.up = true

		w.noteWiref("peer %s session is up", source)
	}

	return session
}

// resetPeerLinksLocked clears per-generation state for every link homed on the given peer:
// reassembly in progress and the advertised remote carrier, which the new generation
// re-advertises within one heartbeat interval.
func (w *fabricWire) resetPeerLinksLocked(source netip.Addr) {
	for _, link := range w.links {
		if link.peerAddr != source {
			continue
		}

		link.reassembler = newWireReassembler(link.reassembler.drops)
		link.remoteOperUp = false
	}
}

// applyPeerCarrierLocked re-evaluates the carrier state machine for every link homed on the
// given peer.
func (w *fabricWire) applyPeerCarrierLocked(source netip.Addr) {
	for _, link := range w.links {
		if link.peerAddr == source {
			w.applyLinkCarrierLocked(link)
		}
	}
}

// applyLinkCarrierLocked realizes carrier propagation exactly the way a cable would: while
// the peer end is dead or down and this end is alive, the sidecar leg is held
// administratively down so the device leg shows loss of carrier while staying admin-up. The
// device leg itself is never touched. Holding the leg down only while the local side is up
// means a link whose both devices shut their ports can still observe its own device leg
// return, so simultaneous down/up sequences on both ends always reconverge.
func (w *fabricWire) applyLinkCarrierLocked(link *fabricWireLink) {
	remoteUp := false

	if session, exists := w.sessions[link.peerAddr]; exists && session.up && link.remoteOperUp {
		remoteUp = true
	}

	desired := link.lastLocalUp && !remoteUp
	if desired == link.forcedDown {
		return
	}

	link.forcedDown = desired

	leg, err := netlink.LinkByIndex(link.legIndex)
	if err != nil {
		return
	}

	if desired {
		if err = netlink.LinkSetDown(leg); err == nil {
			w.noteWiref("link %d carrier is down", link.linkID)
		}

		return
	}

	if err = netlink.LinkSetUp(leg); err == nil {
		link.settleUntil = time.Now().Add(fabricWireCarrierSettleWindow)

		w.noteWiref("link %d carrier is restored", link.linkID)
	}
}

// watchLinks mirrors sidecar leg oper state into local carrier advertisements through a
// netlink subscription; the periodic tick re-samples as a lost-event backstop.
func (w *fabricWire) watchLinks() {
	defer w.workers.Done()

	updates := make(chan netlink.LinkUpdate, 64)
	done := make(chan struct{})

	defer close(done)

	if err := netlink.LinkSubscribeWithOptions(updates, done, netlink.LinkSubscribeOptions{
		ErrorCallback: func(error) {},
	}); err != nil {
		// The tick loop's sampling keeps carrier propagation alive without the subscription,
		// only slower.
		<-w.stop

		return
	}

	for {
		select {
		case <-w.stop:
			return
		case update, open := <-updates:
			if !open {
				<-w.stop

				return
			}

			if update.Link == nil {
				continue
			}

			attributes := update.Link.Attrs()

			w.mu.Lock()

			if linkID, exists := w.byLeg[attributes.Index]; exists {
				w.observeLegStateLocked(
					w.links[linkID],
					attributes.OperState == netlink.OperUp,
				)
			}

			w.mu.Unlock()
		}
	}
}

// observeLegStateLocked folds one observed sidecar leg oper state into the link. Observations
// while the wire itself holds the leg down are ignored, as are down observations inside the
// settle window right after the wire raised the leg: both are echoes of carrier propagation,
// not local state.
func (w *fabricWire) observeLegStateLocked(link *fabricWireLink, operUp bool) {
	if link == nil || link.forcedDown || link.lastLocalUp == operUp {
		return
	}

	if !operUp && time.Now().Before(link.settleUntil) {
		return
	}

	link.lastLocalUp = operUp
	w.noteWiref("link %d local carrier is %s", link.linkID, fabricWireStateWord(operUp))
	w.applyLinkCarrierLocked(link)
	w.advertiseLinkLocked(link)
}

// advertiseLinkLocked sends one immediate link-state advertisement for one link.
func (w *fabricWire) advertiseLinkLocked(link *fabricWireLink) {
	if !link.peerAddr.IsValid() {
		return
	}

	datagram := encodeFabricWireLinkStates(w.generation, []fabricWireLinkState{
		{LinkID: link.linkID, OperUp: link.lastLocalUp},
	})

	_, _ = sendFabricWireDatagrams(w.udpFD, link.peerAddr, [][]byte{datagram})
}

// tickLoop drives the wire's periodic behavior: heartbeats and state-based link-state
// re-advertisement, session liveness, leg state sampling, reassembly expiry, peer
// re-resolution, and drop diagnostics.
func (w *fabricWire) tickLoop() {
	defer w.workers.Done()

	ticker := time.NewTicker(fabricWireHeartbeatInterval)
	defer ticker.Stop()

	lastCounters := time.Now()

	for {
		select {
		case <-w.stop:
			return
		case <-ticker.C:
		}

		now := time.Now()

		w.tick(now)

		if now.Sub(lastCounters) >= fabricWireCountersInterval {
			lastCounters = now

			w.reportCounters()
		}
	}
}

func (w *fabricWire) tick(now time.Time) {
	type outbound struct {
		peer      netip.Addr
		datagrams [][]byte
	}

	var (
		sends    []outbound //nolint:prealloc // sized by the per-peer grouping below.
		resolves []*fabricWireLink
	)

	w.mu.Lock()

	// Sample leg oper state as the lost-netlink-event backstop.
	for _, link := range w.links {
		if link.forcedDown {
			continue
		}

		leg, err := netlink.LinkByIndex(link.legIndex)
		if err != nil {
			continue
		}

		w.observeLegStateLocked(link, leg.Attrs().OperState == netlink.OperUp)
	}

	// Expire sessions whose peer has been silent past the timeout.
	for _, session := range w.sessions {
		if session.up && now.Sub(session.lastHeard) > fabricWireSessionTimeout {
			session.up = false
			session.generationKnown = false
			w.noteWiref("peer %s session timed out", session.remote)
			w.applyPeerCarrierLocked(session.remote)
		}
	}

	// Group heartbeat plus full link-state advertisement per peer.
	states := map[netip.Addr][]fabricWireLinkState{}

	for _, link := range w.links {
		link.reassembler.expire(now)

		if !link.peerAddr.IsValid() {
			if link.peerTransport != "" && !link.resolveInFlight &&
				now.Sub(link.lastResolve) >= fabricWireResolveInterval {
				link.resolveInFlight = true
				link.lastResolve = now
				resolves = append(resolves, link)
			}

			continue
		}

		states[link.peerAddr] = append(states[link.peerAddr], fabricWireLinkState{
			LinkID: link.linkID,
			OperUp: link.lastLocalUp,
		})

		// A homed link whose session is silent re-resolves the peer transport so a
		// rescheduled peer Pod is found without waiting for plan re-assertion.
		session, exists := w.sessions[link.peerAddr]
		if (!exists || !session.up) && link.peerTransport != "" && !link.resolveInFlight &&
			now.Sub(link.lastResolve) >= fabricWireResolveInterval {
			link.resolveInFlight = true
			link.lastResolve = now
			resolves = append(resolves, link)
		}
	}

	for peer, peerStates := range states {
		sends = append(sends, outbound{
			peer: peer,
			datagrams: [][]byte{
				encodeFabricWireHeartbeat(w.generation),
				encodeFabricWireLinkStates(w.generation, peerStates),
			},
		})
	}

	w.mu.Unlock()

	for _, send := range sends {
		_, _ = sendFabricWireDatagrams(w.udpFD, send.peer, send.datagrams)
	}

	for _, link := range resolves {
		go w.resolvePeer(link)
	}
}

// resolvePeer refreshes one link's peer transport resolution off the tick goroutine so DNS
// latency never delays heartbeats.
func (w *fabricWire) resolvePeer(link *fabricWireLink) {
	defer func() {
		w.mu.Lock()
		link.resolveInFlight = false
		w.mu.Unlock()
	}()

	w.mu.Lock()
	transport := link.peerTransport
	local := w.config.LocalAddress
	w.mu.Unlock()

	if parsed, err := netip.ParseAddr(transport); err == nil {
		w.adoptPeerAddress(link, parsed.Unmap())

		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), fabricPeerResolveTimeout)
	defer cancel()

	addresses, err := podBoundResolver(local.String()).LookupNetIP(ctx, "ip4", transport)
	if err != nil || len(addresses) == 0 {
		return
	}

	w.adoptPeerAddress(link, addresses[0].Unmap())
}

func (w *fabricWire) adoptPeerAddress(link *fabricWireLink, address netip.Addr) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !address.IsValid() || link.peerAddr == address {
		return
	}

	link.peerAddr = address
	link.remoteOperUp = false
	w.noteWiref("link %d peer transport moved to %s", link.linkID, address)
	w.applyLinkCarrierLocked(link)
}

// reportCounters surfaces per-link drop deltas through the connectivity diagnostic stream so
// wire loss stays observable without any new API.
func (w *fabricWire) reportCounters() {
	w.mu.Lock()
	defer w.mu.Unlock()

	for _, link := range w.links {
		current := link.counters
		previous := link.reported

		drops := (current.dropExpiry - previous.dropExpiry) +
			(current.dropMemoryCap - previous.dropMemoryCap) +
			(current.dropGeometry - previous.dropGeometry) +
			(current.dropOversize - previous.dropOversize) +
			(current.dropStaleGeneration - previous.dropStaleGeneration) +
			(current.dropForeignSource - previous.dropForeignSource) +
			(current.dropUnresolvedPeer - previous.dropUnresolvedPeer) +
			(current.dropSendFull - previous.dropSendFull) +
			(current.dropInject - previous.dropInject)
		if drops == 0 {
			continue
		}

		link.reported = current

		fmt.Fprintf(
			os.Stderr,
			"connectivity: wire link %d dropped %d in %s: expiry=%d cap=%d geometry=%d "+
				"oversize=%d stale-generation=%d foreign-source=%d unresolved-peer=%d "+
				"send-full=%d inject=%d (tx %d/%d rx %d/%d frames/fragments total)\n",
			link.linkID,
			drops,
			fabricWireCountersInterval,
			current.dropExpiry-previous.dropExpiry,
			current.dropMemoryCap-previous.dropMemoryCap,
			current.dropGeometry-previous.dropGeometry,
			current.dropOversize-previous.dropOversize,
			current.dropStaleGeneration-previous.dropStaleGeneration,
			current.dropForeignSource-previous.dropForeignSource,
			current.dropUnresolvedPeer-previous.dropUnresolvedPeer,
			current.dropSendFull-previous.dropSendFull,
			current.dropInject-previous.dropInject,
			current.txFrames,
			current.txFragments,
			current.rxFrames,
			current.rxFragments,
		)
	}
}

func (w *fabricWire) noteWiref(format string, values ...any) {
	fmt.Fprintf(os.Stderr, "connectivity: wire "+format+"\n", values...)
}

func fabricWireStateWord(up bool) string {
	if up {
		return "up"
	}

	return "down"
}

// pollFabricWireFD parks one nonblocking receive loop until readability or the poll interval,
// whichever is first.
func pollFabricWireFD(fd int) {
	poll := []unix.PollFd{
		//nolint:gosec // kernel-issued descriptor.
		{Fd: int32(fd), Events: unix.POLLIN},
	}

	_, _ = unix.Poll(poll, int(fabricWirePollInterval.Milliseconds()))
}

// injectFabricWireFrame writes one reassembled frame out of the sidecar leg. ENETDOWN while
// the leg is held down is exactly a dead cable and is not an error.
func injectFabricWireFrame(packetFD int, frame []byte) error {
	_, err := unix.Write(packetFD, frame)
	if err != nil && errors.Is(err, unix.ENETDOWN) {
		return nil
	}

	return err
}

// fabricWireMmsgHdr is the kernel's struct mmsghdr for 64-bit Linux. The layout (trailing
// 4-byte pad after the message length) assumes a 64-bit ABI -- every published c9s image is
// amd64 or arm64 -- and would need its own build tags before any 32-bit port.
type fabricWireMmsgHdr struct {
	hdr    unix.Msghdr
	length uint32
	_      [4]byte
}

func fabricWireSockaddr(address netip.Addr) unix.RawSockaddrInet4 {
	raw := unix.RawSockaddrInet4{Family: unix.AF_INET, Addr: address.As4()}

	port := (*[2]byte)(unsafe.Pointer(&raw.Port)) //nolint:gosec // fixed-layout kernel ABI struct.
	port[0] = byte(clabernetesconstants.FabricWireServicePort >> 8)
	port[1] = byte(clabernetesconstants.FabricWireServicePort & 0xff)

	return raw
}

// sendFabricWireDatagrams sends every datagram to one peer with one sendmmsg batch,
// returning how many the kernel accepted. A full socket drops the tail: wire loss stays
// loss.
func sendFabricWireDatagrams(fd int, peer netip.Addr, datagrams [][]byte) (int, error) {
	if len(datagrams) == 0 {
		return 0, nil
	}

	destination := fabricWireSockaddr(peer)

	vectors := make([]unix.Iovec, len(datagrams))
	headers := make([]fabricWireMmsgHdr, len(datagrams))

	//nolint:gosec // fixed-layout kernel ABI struct.
	name := (*byte)(unsafe.Pointer(&destination))

	for index, datagram := range datagrams {
		vectors[index].Base = &datagram[0]
		vectors[index].SetLen(len(datagram))

		headers[index].hdr.Name = name
		headers[index].hdr.Namelen = unix.SizeofSockaddrInet4
		headers[index].hdr.Iov = &vectors[index]
		headers[index].hdr.SetIovlen(1)
	}

	sent := 0

	for sent < len(headers) {
		//nolint:gosec // fixed-layout kernel ABI struct.
		vector := uintptr(unsafe.Pointer(&headers[sent]))

		n, _, errno := unix.Syscall6(
			unix.SYS_SENDMMSG,
			uintptr(fd),
			vector,
			uintptr(len(headers)-sent),
			unix.MSG_DONTWAIT,
			0,
			0,
		)
		if errno != 0 {
			if errno == unix.EINTR {
				continue
			}

			runtime.KeepAlive(datagrams)
			runtime.KeepAlive(vectors)
			runtime.KeepAlive(&destination)

			return sent, errno
		}

		sent += int(n)

		if n == 0 {
			break
		}
	}

	runtime.KeepAlive(datagrams)
	runtime.KeepAlive(vectors)
	runtime.KeepAlive(&destination)

	return sent, nil
}

// fabricWireReceiveBatch owns the reusable buffers for one recvmmsg batch on the shared
// socket.
type fabricWireReceiveBatch struct {
	buffers   [][]byte
	vectors   []unix.Iovec
	headers   []fabricWireMmsgHdr
	addresses []unix.RawSockaddrInet4
}

func newFabricWireReceiveBatch(size int) *fabricWireReceiveBatch {
	batch := &fabricWireReceiveBatch{
		buffers:   make([][]byte, size),
		vectors:   make([]unix.Iovec, size),
		headers:   make([]fabricWireMmsgHdr, size),
		addresses: make([]unix.RawSockaddrInet4, size),
	}

	for index := range size {
		batch.buffers[index] = make([]byte, fabricWireMaximumFrameSize+1)
	}

	return batch
}

// receive fills the batch with available datagrams, returning how many arrived.
func (b *fabricWireReceiveBatch) receive(fd int) (int, error) {
	for index := range b.headers {
		b.vectors[index].Base = &b.buffers[index][0]
		b.vectors[index].SetLen(len(b.buffers[index]))

		//nolint:gosec // fixed-layout kernel ABI struct.
		b.headers[index].hdr.Name = (*byte)(unsafe.Pointer(&b.addresses[index]))
		b.headers[index].hdr.Namelen = unix.SizeofSockaddrInet4
		b.headers[index].hdr.Iov = &b.vectors[index]
		b.headers[index].hdr.SetIovlen(1)
		b.headers[index].hdr.Flags = 0
		b.headers[index].length = 0
	}

	//nolint:gosec // fixed-layout kernel ABI struct.
	vector := uintptr(unsafe.Pointer(&b.headers[0]))

	for {
		n, _, errno := unix.Syscall6(
			unix.SYS_RECVMMSG,
			uintptr(fd),
			vector,
			uintptr(len(b.headers)),
			unix.MSG_DONTWAIT,
			0,
			0,
		)
		if errno != 0 {
			if errno == unix.EINTR {
				continue
			}

			return 0, errno
		}

		runtime.KeepAlive(b.buffers)
		runtime.KeepAlive(b.vectors)
		runtime.KeepAlive(b.addresses)

		return int(n), nil
	}
}

// One wire endpoint exists per connectivity process in production: the sidecar owns exactly
// one Pod address and one UDP port, and a restart re-derives the wire from the plan like every
// other realization.
//
//nolint:gochecknoglobals // deliberate one-per-process transport endpoint.
var (
	podFabricWireMutex sync.Mutex
	podFabricWire      *fabricWire
)

// ensurePodFabricWire lazily starts the process-wide wire endpoint on the Pod address; the
// fragment payload is fixed from the underlay observed at first use because the Pod underlay
// never changes within one Pod lifetime.
func ensurePodFabricWire(localAddress netip.Addr, underlayMTU int) (*fabricWire, error) {
	podFabricWireMutex.Lock()
	defer podFabricWireMutex.Unlock()

	if podFabricWire != nil {
		if podFabricWire.config.LocalAddress != localAddress.Unmap() {
			return nil, errors.New("fabric wire is already bound to another Pod address")
		}

		return podFabricWire, nil
	}

	wire, err := newFabricWire(fabricWireConfig{
		LocalAddress:    localAddress.Unmap(),
		FragmentPayload: fabricWireFragmentPayload(underlayMTU),
	})
	if err != nil {
		return nil, err
	}

	podFabricWire = wire

	return wire, nil
}

// podFabricWireHoldsLegDown reports whether the process-wide wire currently owns an
// administrative down on the given link's sidecar leg.
func podFabricWireHoldsLegDown(wireID int) bool {
	podFabricWireMutex.Lock()
	wire := podFabricWire
	podFabricWireMutex.Unlock()

	if wire == nil || wireID <= 0 {
		return false
	}

	known, forced := wire.ForcesLegDown(uint32(wireID)) //nolint:gosec // positive bound checked.

	return known && forced
}

// sweepPodFabricWireLinks stops wire pumps whose owners left the desired plan; a wire that was
// never started has nothing to sweep.
func sweepPodFabricWireLinks(ownerPrefix string, keepOwners []string) {
	podFabricWireMutex.Lock()
	wire := podFabricWire
	podFabricWireMutex.Unlock()

	if wire != nil {
		wire.SweepLinks(ownerPrefix, keepOwners)
	}
}

// message returns one received datagram view and its source address.
func (b *fabricWireReceiveBatch) message(index int) ([]byte, netip.Addr) {
	length := min(int(b.headers[index].length), len(b.buffers[index]))

	address := b.addresses[index]

	var source netip.Addr
	if address.Family == unix.AF_INET {
		source = netip.AddrFrom4(address.Addr)
	}

	return b.buffers[index][:length], source
}
