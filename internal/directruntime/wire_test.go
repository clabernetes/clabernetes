//nolint:testpackage // exercises the unexported wire protocol directly.
package directruntime

import (
	"bytes"
	"testing"
	"time"
)

func TestFabricWireHeaderRoundTrips(t *testing.T) {
	datagram := encodeFabricWireHeartbeat(0xdeadbeefcafe)

	header, body, err := parseFabricWireHeader(datagram)
	if err != nil {
		t.Fatalf("parsing heartbeat header: %v", err)
	}

	if header.Version != fabricWireVersion || header.MsgType != fabricWireMsgHeartbeat ||
		header.Generation != 0xdeadbeefcafe || len(body) != 0 {
		t.Fatalf("heartbeat header = %+v with %d body bytes", header, len(body))
	}
}

func TestFabricWireHeaderRejectsShortAndForeignDatagrams(t *testing.T) {
	if _, _, err := parseFabricWireHeader([]byte{fabricWireVersion}); err == nil {
		t.Fatal("short datagram parsed")
	}

	bad := encodeFabricWireHeartbeat(1)
	bad[0] = fabricWireVersion + 1

	if _, _, err := parseFabricWireHeader(bad); err == nil {
		t.Fatal("foreign version parsed")
	}
}

func TestFabricWireDataRoundTrips(t *testing.T) {
	payload := []byte("one honest fragment")
	sent := fabricWireDataHeader{
		LinkID:      42,
		FrameID:     7,
		FragIndex:   1,
		FragCount:   3,
		TotalLength: 999,
	}

	datagram := encodeFabricWireFragment(11, sent, payload)

	header, body, err := parseFabricWireHeader(datagram)
	if err != nil || header.MsgType != fabricWireMsgData || header.Generation != 11 {
		t.Fatalf("data common header = %+v, err %v", header, err)
	}

	received, parsedPayload, err := parseFabricWireDataHeader(body)
	if err != nil {
		t.Fatalf("parsing data header: %v", err)
	}

	if received != sent || !bytes.Equal(parsedPayload, payload) {
		t.Fatalf("data header = %+v payload %q", received, parsedPayload)
	}
}

func TestFabricWireDataHeaderRejectsInconsistentGeometry(t *testing.T) {
	for name, mutate := range map[string]func(*fabricWireDataHeader, *[]byte){
		"zero fragment count": func(h *fabricWireDataHeader, _ *[]byte) { h.FragCount = 0 },
		"index beyond count":  func(h *fabricWireDataHeader, _ *[]byte) { h.FragIndex = 3 },
		"empty payload":       func(_ *fabricWireDataHeader, p *[]byte) { *p = nil },
		"total below payload": func(h *fabricWireDataHeader, _ *[]byte) { h.TotalLength = 1 },
	} {
		header := fabricWireDataHeader{
			LinkID: 1, FrameID: 1, FragIndex: 0, FragCount: 3, TotalLength: 30,
		}
		payload := []byte("0123456789")

		mutate(&header, &payload)

		datagram := encodeFabricWireFragment(1, header, payload)

		_, body, err := parseFabricWireHeader(datagram)
		if err != nil {
			t.Fatalf("%s: common header rejected: %v", name, err)
		}

		if _, _, err = parseFabricWireDataHeader(body); err == nil {
			t.Fatalf("%s: inconsistent data header parsed", name)
		}
	}
}

func TestFabricWireLinkStatesRoundTrip(t *testing.T) {
	sent := []fabricWireLinkState{
		{LinkID: 1, OperUp: true},
		{LinkID: 90210, OperUp: false},
	}

	datagram := encodeFabricWireLinkStates(3, sent)

	header, body, err := parseFabricWireHeader(datagram)
	if err != nil || header.MsgType != fabricWireMsgLinkState {
		t.Fatalf("link state header = %+v, err %v", header, err)
	}

	states, err := parseFabricWireLinkStates(body)
	if err != nil || len(states) != len(sent) {
		t.Fatalf("link states = %+v, err %v", states, err)
	}

	for index := range sent {
		if states[index] != sent[index] {
			t.Fatalf("link state %d = %+v, want %+v", index, states[index], sent[index])
		}
	}

	if _, err = parseFabricWireLinkStates(body[:len(body)-1]); err == nil {
		t.Fatal("ragged link state body parsed")
	}

	if _, err = parseFabricWireLinkStates(nil); err == nil {
		t.Fatal("empty link state body parsed")
	}
}

func TestSplitFabricWireFrame(t *testing.T) {
	frame := make([]byte, 2500)
	for index := range frame {
		frame[index] = byte(index)
	}

	fragments, err := splitFabricWireFrame(frame, 1000)
	if err != nil {
		t.Fatalf("splitting frame: %v", err)
	}

	if len(fragments) != 3 || len(fragments[0]) != 1000 || len(fragments[2]) != 500 {
		t.Fatalf("fragment geometry = %d fragments", len(fragments))
	}

	reassembled := append(append(append([]byte(nil), fragments[0]...), fragments[1]...),
		fragments[2]...)
	if !bytes.Equal(reassembled, frame) {
		t.Fatal("fragments do not reproduce the frame")
	}

	if _, err = splitFabricWireFrame(nil, 1000); err == nil {
		t.Fatal("empty frame split")
	}

	if _, err = splitFabricWireFrame(make([]byte, fabricWireMaximumFrameSize+1), 1000); err == nil {
		t.Fatal("oversize frame split")
	}

	if _, err = splitFabricWireFrame(frame, 0); err == nil {
		t.Fatal("zero payload size accepted")
	}

	if _, err = splitFabricWireFrame(make([]byte, 60000), 200); err == nil {
		t.Fatal("frame needing over 255 fragments split")
	}
}

func TestFabricWireFragmentPayloadDerivation(t *testing.T) {
	for underlay, want := range map[int]int{
		0:    fabricWireMinimumFragmentPayload,
		1200: fabricWireMinimumFragmentPayload,
		1480: 1480 - fabricWireTransportOverhead,
		9000: 9000 - fabricWireTransportOverhead,
	} {
		if got := fabricWireFragmentPayload(underlay); got != want {
			t.Fatalf("fragment payload for underlay %d = %d, want %d", underlay, got, want)
		}
	}
}

func reassemblyTestFrame(size int) []byte {
	frame := make([]byte, size)
	for index := range frame {
		frame[index] = byte(index * 7)
	}

	return frame
}

func absorbTestFragments(
	t *testing.T,
	reassembler *wireReassembler,
	now time.Time,
	frameID uint32,
	frame []byte,
	payloadSize int,
	order []int,
) ([]byte, bool) {
	t.Helper()

	fragments, err := splitFabricWireFrame(frame, payloadSize)
	if err != nil {
		t.Fatalf("splitting reassembly test frame: %v", err)
	}

	var (
		assembled []byte
		complete  bool
	)

	for _, index := range order {
		assembled, complete = reassembler.absorb(now, fabricWireDataHeader{
			LinkID:      1,
			FrameID:     frameID,
			FragIndex:   uint8(index),          //nolint:gosec // bounded by the test order.
			FragCount:   uint8(len(fragments)), //nolint:gosec // bounded by the split.
			TotalLength: uint16(len(frame)),    //nolint:gosec // bounded by the split.
		}, fragments[index])
	}

	return assembled, complete
}

func TestWireReassemblerCompletesOutOfOrderFrames(t *testing.T) {
	reassembler := newWireReassembler(nil)
	now := time.Now()
	frame := reassemblyTestFrame(2500)

	assembled, complete := absorbTestFragments(t, reassembler, now, 1, frame, 1000,
		[]int{2, 0, 1})
	if !complete || !bytes.Equal(assembled, frame) {
		t.Fatalf("out-of-order reassembly complete=%v equal=%v",
			complete, bytes.Equal(assembled, frame))
	}
}

func TestWireReassemblerIgnoresDuplicateFragments(t *testing.T) {
	reassembler := newWireReassembler(nil)
	now := time.Now()
	frame := reassemblyTestFrame(2500)

	assembled, complete := absorbTestFragments(t, reassembler, now, 1, frame, 1000,
		[]int{0, 0, 1, 1, 2})
	if !complete || !bytes.Equal(assembled, frame) {
		t.Fatal("duplicated fragments broke reassembly")
	}
}

func TestWireReassemblerDropsGappedFrameOnExpiry(t *testing.T) {
	drops := map[fabricWireDropCause]int{}
	reassembler := newWireReassembler(func(cause fabricWireDropCause) { drops[cause]++ })
	now := time.Now()
	frame := reassemblyTestFrame(2500)

	if _, complete := absorbTestFragments(t, reassembler, now, 1, frame, 1000,
		[]int{0, 2}); complete {
		t.Fatal("gapped frame completed")
	}

	reassembler.expire(now.Add(fabricWireReassemblyExpiry + time.Millisecond))

	if drops[fabricWireDropExpiry] != 1 || len(reassembler.pending) != 0 {
		t.Fatalf("expiry drops = %d, pending = %d",
			drops[fabricWireDropExpiry], len(reassembler.pending))
	}

	// The late fragment restarts an empty pending frame rather than resurrecting state.
	if _, complete := absorbTestFragments(t, reassembler, now.Add(time.Second), 1, frame,
		1000, []int{1}); complete {
		t.Fatal("stale fragment completed a frame")
	}
}

func TestWireReassemblerDropsGeometryDisagreement(t *testing.T) {
	drops := map[fabricWireDropCause]int{}
	reassembler := newWireReassembler(func(cause fabricWireDropCause) { drops[cause]++ })
	now := time.Now()

	if _, complete := reassembler.absorb(now, fabricWireDataHeader{
		LinkID: 1, FrameID: 9, FragIndex: 0, FragCount: 3, TotalLength: 3000,
	}, make([]byte, 1000)); complete {
		t.Fatal("first fragment completed a frame")
	}

	if _, complete := reassembler.absorb(now, fabricWireDataHeader{
		LinkID: 1, FrameID: 9, FragIndex: 1, FragCount: 2, TotalLength: 3000,
	}, make([]byte, 1000)); complete {
		t.Fatal("disagreeing fragment completed a frame")
	}

	if drops[fabricWireDropGeometry] != 1 || len(reassembler.pending) != 0 {
		t.Fatalf("geometry drops = %d, pending = %d",
			drops[fabricWireDropGeometry], len(reassembler.pending))
	}
}

func TestWireReassemblerDropsShortFrameAtCompletion(t *testing.T) {
	drops := map[fabricWireDropCause]int{}
	reassembler := newWireReassembler(func(cause fabricWireDropCause) { drops[cause]++ })
	now := time.Now()

	for index := range 2 {
		if _, complete := reassembler.absorb(now, fabricWireDataHeader{
			LinkID: 1, FrameID: 9,
			FragIndex: uint8(index),
			FragCount: 2, TotalLength: 3000,
		}, make([]byte, 1000)); complete {
			t.Fatal("short frame completed")
		}
	}

	if drops[fabricWireDropGeometry] != 1 {
		t.Fatalf("short-frame drops = %d, want 1", drops[fabricWireDropGeometry])
	}
}

func TestWireReassemblerEvictsOldestPastFrameLimit(t *testing.T) {
	drops := map[fabricWireDropCause]int{}
	reassembler := newWireReassembler(func(cause fabricWireDropCause) { drops[cause]++ })
	now := time.Now()

	for index := range fabricWireReassemblyFrameLimit + 1 {
		reassembler.absorb(now.Add(time.Duration(index)*time.Microsecond), fabricWireDataHeader{
			LinkID:    1,
			FrameID:   uint32(index),
			FragIndex: 0, FragCount: 2, TotalLength: 2000,
		}, make([]byte, 1000))
	}

	if drops[fabricWireDropMemoryCap] != 1 ||
		len(reassembler.pending) != fabricWireReassemblyFrameLimit {
		t.Fatalf("memory-cap drops = %d, pending = %d",
			drops[fabricWireDropMemoryCap], len(reassembler.pending))
	}

	if _, evictedStillPending := reassembler.pending[0]; evictedStillPending {
		t.Fatal("oldest pending frame was not the eviction victim")
	}
}
