package directruntime

import (
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

// The fabric wire is the c9s-owned cross-Pod link transport: sidecar-to-sidecar UDP datagrams
// that segment whole Ethernet frames, carry link carrier-state, and detect peer loss. Control
// and data share one socket and one path, so the carrier signal cannot diverge from the
// datapath in endpoint or route, and the datagram semantics deliberately preserve loss: a
// frame missing any fragment is dropped whole, never retransmitted, so loss testing, BFD, and
// convergence measurements against emulated links stay meaningful.
const (
	// fabricWireVersion is the accepted wire format revision; datagrams carrying any other
	// version are dropped whole.
	fabricWireVersion = 1

	// fabricWireMsgData carries one fragment of one Ethernet frame.
	fabricWireMsgData = 1
	// fabricWireMsgLinkState advertises local interface carrier state, sent on change and
	// periodically so the state heals without acknowledgments.
	fabricWireMsgLinkState = 2
	// fabricWireMsgHeartbeat proves peer liveness for one Pod pair.
	fabricWireMsgHeartbeat = 3

	// fabricWireHeaderSize is version(1) | msgType(1) | generation(8).
	fabricWireHeaderSize = 10
	// fabricWireDataHeaderSize is linkID(4) | frameID(4) | fragIndex(1) | fragCount(1) |
	// totalLen(2), following the common header in data datagrams.
	fabricWireDataHeaderSize = 12
	// fabricWireLinkStateEntrySize is linkID(4) | operUp(1) per advertised interface.
	fabricWireLinkStateEntrySize = 5

	// fabricWireTransportOverhead is the underlay headroom one data fragment consumes: outer
	// IPv4(20) + UDP(8) + common header + data header.
	fabricWireTransportOverhead = 28 + fabricWireHeaderSize + fabricWireDataHeaderSize

	// fabricWireMinimumFragmentPayload floors the fragment payload so an unidentifiable or
	// implausibly small underlay still yields a functional wire.
	fabricWireMinimumFragmentPayload = 1200

	// fabricWireMaximumFrameSize bounds one reassembled frame; the totalLen field width is the
	// protocol ceiling.
	fabricWireMaximumFrameSize = 1<<16 - 1

	// fabricWireMaximumFragments bounds one frame's fragment count; the fragIndex/fragCount
	// field width is the protocol ceiling.
	fabricWireMaximumFragments = 1<<8 - 1
)

// Wire timing is centralized here and deliberately conservative: underlay jitter must never
// flap an emulated link.
const (
	// fabricWireHeartbeatInterval paces per-peer heartbeats and periodic link-state
	// re-advertisement. Both messages go to every peer each tick (in one sendmmsg batch), and
	// a link-state datagram refreshes the session exactly like a heartbeat -- the standalone
	// heartbeat is kept because it is fixed-size: link-state grows with the per-peer link
	// count (5 bytes per link, unchunked), so past roughly 230 links per Pod pair it can
	// exceed one underlay MTU and ride outer IP fragmentation, while the heartbeat stays a
	// single small datagram that keeps liveness robust. Chunking link-state is the upgrade
	// path if per-pair link counts ever make that ceiling real.
	fabricWireHeartbeatInterval = time.Second
	// fabricWireHeartbeatMissLimit is how many silent heartbeat intervals mark a peer session
	// dead, taking every link to that peer carrier-down.
	fabricWireHeartbeatMissLimit = 3
	// fabricWireSessionTimeout adds margin over the miss limit so scheduler jitter alone
	// cannot kill a session.
	fabricWireSessionTimeout = fabricWireHeartbeatMissLimit*fabricWireHeartbeatInterval +
		500*time.Millisecond
	// fabricWireReassemblyExpiry bounds how long an incomplete frame waits for its remaining
	// fragments before it is dropped whole.
	fabricWireReassemblyExpiry = 100 * time.Millisecond
	// fabricWireReassemblyFrameLimit caps in-flight frames per link, bounding reassembly
	// memory regardless of peer behavior.
	fabricWireReassemblyFrameLimit = 64
)

var (
	errFabricWireDatagram = errors.New("invalid fabric wire datagram")
	errFabricWireFrame    = errors.New("fabric wire frame is not representable")
)

// fabricWireHeader is the common prefix of every wire datagram. The generation identifies one
// sidecar process instance so datagrams from a replaced or restarted peer are never mixed into
// current reassembly state.
type fabricWireHeader struct {
	Version    byte
	MsgType    byte
	Generation uint64
}

// fabricWireDataHeader addresses one fragment of one frame on one link.
type fabricWireDataHeader struct {
	LinkID      uint32
	FrameID     uint32
	FragIndex   uint8
	FragCount   uint8
	TotalLength uint16
}

// fabricWireLinkState is one advertised interface carrier state.
type fabricWireLinkState struct {
	LinkID uint32
	OperUp bool
}

func putFabricWireHeader(dst []byte, msgType byte, generation uint64) {
	dst[0] = fabricWireVersion
	dst[1] = msgType
	binary.BigEndian.PutUint64(dst[2:], generation)
}

func parseFabricWireHeader(datagram []byte) (fabricWireHeader, []byte, error) {
	if len(datagram) < fabricWireHeaderSize {
		return fabricWireHeader{}, nil, fmt.Errorf("%w: short header", errFabricWireDatagram)
	}

	header := fabricWireHeader{
		Version:    datagram[0],
		MsgType:    datagram[1],
		Generation: binary.BigEndian.Uint64(datagram[2:]),
	}

	if header.Version != fabricWireVersion {
		return fabricWireHeader{}, nil, fmt.Errorf("%w: unknown version", errFabricWireDatagram)
	}

	return header, datagram[fabricWireHeaderSize:], nil
}

// encodeFabricWireFragment renders one complete data datagram for the payload slice of one
// frame fragment.
func encodeFabricWireFragment(
	generation uint64,
	header fabricWireDataHeader,
	payload []byte,
) []byte {
	datagram := make([]byte, fabricWireHeaderSize+fabricWireDataHeaderSize+len(payload))
	putFabricWireHeader(datagram, fabricWireMsgData, generation)

	body := datagram[fabricWireHeaderSize:]
	binary.BigEndian.PutUint32(body[0:], header.LinkID)
	binary.BigEndian.PutUint32(body[4:], header.FrameID)
	body[8] = header.FragIndex
	body[9] = header.FragCount
	binary.BigEndian.PutUint16(body[10:], header.TotalLength)
	copy(body[fabricWireDataHeaderSize:], payload)

	return datagram
}

func parseFabricWireDataHeader(body []byte) (fabricWireDataHeader, []byte, error) {
	if len(body) < fabricWireDataHeaderSize {
		return fabricWireDataHeader{}, nil, fmt.Errorf(
			"%w: short data header",
			errFabricWireDatagram,
		)
	}

	header := fabricWireDataHeader{
		LinkID:      binary.BigEndian.Uint32(body[0:]),
		FrameID:     binary.BigEndian.Uint32(body[4:]),
		FragIndex:   body[8],
		FragCount:   body[9],
		TotalLength: binary.BigEndian.Uint16(body[10:]),
	}

	payload := body[fabricWireDataHeaderSize:]

	if header.FragCount == 0 || header.FragIndex >= header.FragCount || len(payload) == 0 ||
		int(header.TotalLength) < len(payload) {
		return fabricWireDataHeader{}, nil, fmt.Errorf(
			"%w: inconsistent fragment geometry",
			errFabricWireDatagram,
		)
	}

	return header, payload, nil
}

// encodeFabricWireLinkStates renders one link-state datagram carrying every supplied entry.
func encodeFabricWireLinkStates(generation uint64, states []fabricWireLinkState) []byte {
	datagram := make(
		[]byte,
		fabricWireHeaderSize+len(states)*fabricWireLinkStateEntrySize,
	)
	putFabricWireHeader(datagram, fabricWireMsgLinkState, generation)

	body := datagram[fabricWireHeaderSize:]
	for index, state := range states {
		entry := body[index*fabricWireLinkStateEntrySize:]
		binary.BigEndian.PutUint32(entry, state.LinkID)

		if state.OperUp {
			entry[4] = 1
		}
	}

	return datagram
}

func parseFabricWireLinkStates(body []byte) ([]fabricWireLinkState, error) {
	if len(body) == 0 || len(body)%fabricWireLinkStateEntrySize != 0 {
		return nil, fmt.Errorf("%w: malformed link state body", errFabricWireDatagram)
	}

	states := make([]fabricWireLinkState, 0, len(body)/fabricWireLinkStateEntrySize)
	for offset := 0; offset < len(body); offset += fabricWireLinkStateEntrySize {
		states = append(states, fabricWireLinkState{
			LinkID: binary.BigEndian.Uint32(body[offset:]),
			OperUp: body[offset+4] == 1,
		})
	}

	return states, nil
}

func encodeFabricWireHeartbeat(generation uint64) []byte {
	datagram := make([]byte, fabricWireHeaderSize)
	putFabricWireHeader(datagram, fabricWireMsgHeartbeat, generation)

	return datagram
}

// splitFabricWireFrame slices one frame into fragment payload views without copying. The
// fragment payload size is fixed per frame so the fragments of one frame always agree on
// geometry.
func splitFabricWireFrame(frame []byte, payloadSize int) ([][]byte, error) {
	if payloadSize < 1 {
		return nil, fmt.Errorf("%w: fragment payload size is not positive", errFabricWireFrame)
	}

	if len(frame) == 0 || len(frame) > fabricWireMaximumFrameSize {
		return nil, fmt.Errorf("%w: frame size %d", errFabricWireFrame, len(frame))
	}

	count := (len(frame) + payloadSize - 1) / payloadSize
	if count > fabricWireMaximumFragments {
		return nil, fmt.Errorf("%w: frame needs %d fragments", errFabricWireFrame, count)
	}

	fragments := make([][]byte, 0, count)

	for offset := 0; offset < len(frame); offset += payloadSize {
		end := min(offset+payloadSize, len(frame))
		fragments = append(fragments, frame[offset:end])
	}

	return fragments, nil
}

// fabricWireDropCause classifies reassembly drops for the per-link counters.
type fabricWireDropCause int

const (
	fabricWireDropNone fabricWireDropCause = iota
	// fabricWireDropExpiry names a frame abandoned because a fragment gap outlived the
	// reassembly expiry.
	fabricWireDropExpiry
	// fabricWireDropMemoryCap names a frame evicted to keep in-flight reassembly bounded.
	fabricWireDropMemoryCap
	// fabricWireDropGeometry names fragments whose geometry disagrees with the pending frame.
	fabricWireDropGeometry
	// fabricWireDropOversize names a frame larger than the link can deliver.
	fabricWireDropOversize
)

// pendingWireFrame is one partially reassembled frame.
type pendingWireFrame struct {
	arrived   time.Time
	fragCount int
	totalLen  int
	received  int
	bytes     int
	fragments [][]byte
}

// wireReassembler reconstructs frames of one link from fragments. It is deliberately
// loss-preserving: a frame with any gap is dropped whole at expiry, duplicates are ignored,
// and memory stays bounded by evicting the oldest pending frame past the in-flight cap.
type wireReassembler struct {
	pending map[uint32]*pendingWireFrame
	drops   func(cause fabricWireDropCause)
}

func newWireReassembler(drops func(cause fabricWireDropCause)) *wireReassembler {
	if drops == nil {
		drops = func(fabricWireDropCause) {}
	}

	return &wireReassembler{pending: map[uint32]*pendingWireFrame{}, drops: drops}
}

// expire abandons every pending frame whose remaining fragments are past due.
func (r *wireReassembler) expire(now time.Time) {
	for frameID, frame := range r.pending {
		if now.Sub(frame.arrived) > fabricWireReassemblyExpiry {
			delete(r.pending, frameID)
			r.drops(fabricWireDropExpiry)
		}
	}
}

// evictOldest drops the longest-pending frame to admit a new one under the in-flight cap.
func (r *wireReassembler) evictOldest() {
	var (
		oldestID    uint32
		oldestFrame *pendingWireFrame
	)

	for frameID, frame := range r.pending {
		if oldestFrame == nil || frame.arrived.Before(oldestFrame.arrived) {
			oldestID = frameID
			oldestFrame = frame
		}
	}

	if oldestFrame != nil {
		delete(r.pending, oldestID)
		r.drops(fabricWireDropMemoryCap)
	}
}

// absorb integrates one fragment and returns the completed frame when it was the last missing
// piece. Fragments carrying geometry that disagrees with the pending frame drop the whole
// frame: the wire never guesses which half of a disagreement was honest.
func (r *wireReassembler) absorb(
	now time.Time,
	header fabricWireDataHeader,
	payload []byte,
) ([]byte, bool) {
	r.expire(now)

	frame, exists := r.pending[header.FrameID]
	if !exists {
		if len(r.pending) >= fabricWireReassemblyFrameLimit {
			r.evictOldest()
		}

		frame = &pendingWireFrame{
			arrived:   now,
			fragCount: int(header.FragCount),
			totalLen:  int(header.TotalLength),
			fragments: make([][]byte, header.FragCount),
		}
		r.pending[header.FrameID] = frame
	}

	if frame.fragCount != int(header.FragCount) || frame.totalLen != int(header.TotalLength) {
		delete(r.pending, header.FrameID)
		r.drops(fabricWireDropGeometry)

		return nil, false
	}

	if frame.fragments[header.FragIndex] != nil {
		// A duplicated fragment is ignored; the first arrival is authoritative.
		return nil, false
	}

	frame.fragments[header.FragIndex] = append([]byte(nil), payload...)
	frame.received++
	frame.bytes += len(payload)

	if frame.received < frame.fragCount {
		return nil, false
	}

	delete(r.pending, header.FrameID)

	if frame.bytes != frame.totalLen {
		r.drops(fabricWireDropGeometry)

		return nil, false
	}

	assembled := make([]byte, 0, frame.totalLen)
	for _, fragment := range frame.fragments {
		assembled = append(assembled, fragment...)
	}

	return assembled, true
}

// fabricWireFragmentPayload derives the per-fragment payload size from the local underlay MTU;
// a zero underlay means the underlay interface was not identifiable and the conservative floor
// applies. Mixed-MTU clusters need no coordination: each sender fragments to its own underlay
// and reassembly is size-agnostic.
func fabricWireFragmentPayload(underlayMTU int) int {
	payload := underlayMTU - fabricWireTransportOverhead
	if payload < fabricWireMinimumFragmentPayload {
		return fabricWireMinimumFragmentPayload
	}

	return payload
}
