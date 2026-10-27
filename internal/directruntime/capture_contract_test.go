//nolint:err113,gocyclo,testpackage // the capture client contract is exercised through unexported fakes.
package directruntime

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
)

// interleavingPacketSource yields between packets so concurrent interface readers interleave,
// then blocks until the capture ends. Each packet's first byte names its source.
type interleavingPacketSource struct {
	mu      sync.Mutex
	packets []capturedPacket
	closed  bool
}

func newInterleavingPacketSource(marker byte, count int) *interleavingPacketSource {
	source := &interleavingPacketSource{}
	for index := range count {
		source.packets = append(source.packets, capturedPacket{
			Timestamp: time.Unix(1_700_000_000, int64(index)*1_000+1),
			Data:      []byte{marker, byte(index), 0xaa, 0xbb, 0xcc},
		})
	}

	return source
}

func (s *interleavingPacketSource) ReadPacket(ctx context.Context) (capturedPacket, error) {
	runtime.Gosched()

	s.mu.Lock()
	if len(s.packets) == 0 {
		s.mu.Unlock()
		<-ctx.Done()

		return capturedPacket{}, ctx.Err()
	}

	packet := s.packets[0]
	s.packets = s.packets[1:]
	s.mu.Unlock()

	return packet, nil
}

func (s *interleavingPacketSource) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.closed = true

	return nil
}

func (s *interleavingPacketSource) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.closed
}

// packetCaptureGroupedPlan places a second logical Node in the Pod and gives the first Node a
// cross-Pod wire whose Link used an SR Linux interface alias, and the second a host endpoint.
func packetCaptureGroupedPlan() clabernetesinternaldeviceplan.Plan {
	plan := packetCaptureTestPlan()
	plan.Nodes = append(plan.Nodes, clabernetesinternaldeviceplan.NodePlan{
		ID: "grouped-node-uid", Name: "grouped-device", Kind: "another-opaque-kind",
		ContainerIDs: []string{"container-b"}, ReadinessContainerIDs: []string{"container-b"},
	})
	plan.Containers = append(plan.Containers, clabernetesinternaldeviceplan.ContainerPlan{
		ID: "container-b", NodeID: "grouped-node-uid", NamespaceOwnerID: "container-a",
		Image: "example/grouped:1", Required: true,
	})
	plan.Interfaces = append(plan.Interfaces,
		clabernetesinternaldeviceplan.InterfacePlan{
			ID: "wire-uid/a", NodeID: "opaque-node-uid", NamespaceOwnerID: "container-a",
			Name: "e1-1", Alias: "ethernet-1/1", LinkID: "wire-uid", PeerNodeID: "remote-uid",
			PeerInterface: "eth3", PeerTransport: FabricServiceName("remote-device"),
			Connectivity: clabernetesinternaldeviceplan.ConnectivityWire, WireID: 7, MTU: 1500,
			LinkApplyMode: clabernetesinternaldeviceplan.LinkApplyLive, RequiredAtStart: true,
		},
		clabernetesinternaldeviceplan.InterfacePlan{
			ID: "host-uid/a", NodeID: "grouped-node-uid", NamespaceOwnerID: "container-a",
			Name: "eth1", LinkID: "host-uid", PeerInterface: "veth-lab",
			Connectivity: clabernetesinternaldeviceplan.ConnectivityHost, MTU: 1500,
			LinkApplyMode: clabernetesinternaldeviceplan.LinkApplyLive, RequiredAtStart: true,
		},
	)

	return plan
}

func TestNormalizePacketCaptureOptionsRejectsMalformedRequests(t *testing.T) {
	t.Parallel()

	for name, testCase := range map[string]struct {
		options PacketCaptureOptions
		reason  string
	}{
		"no selector": {
			options: PacketCaptureOptions{Interfaces: []string{"eth1"}, PacketLimit: 1},
			reason:  "requires --nodeID or --nodeName",
		},
		"both selectors": {
			options: PacketCaptureOptions{
				PacketCaptureSelector: PacketCaptureSelector{NodeID: "uid", NodeName: "lin1"},
				Interfaces:            []string{"eth1"}, PacketLimit: 1,
			},
			reason: "not both",
		},
		"no interface": {
			options: PacketCaptureOptions{
				PacketCaptureSelector: PacketCaptureSelector{NodeName: "lin1"}, PacketLimit: 1,
			},
			reason: "requires an --interface",
		},
		"empty interface": {
			options: PacketCaptureOptions{
				PacketCaptureSelector: PacketCaptureSelector{NodeName: "lin1"},
				Interfaces:            []string{"eth1", " "}, Format: "pcapng", PacketLimit: 1,
			},
			reason: "interface name is empty",
		},
		"pcap with two interfaces": {
			options: PacketCaptureOptions{
				PacketCaptureSelector: PacketCaptureSelector{NodeName: "lin1"},
				Interfaces:            []string{"eth1", "eth2"}, PacketLimit: 1,
			},
			reason: "use --format pcapng",
		},
		"unknown format": {
			options: PacketCaptureOptions{
				PacketCaptureSelector: PacketCaptureSelector{NodeName: "lin1"},
				Interfaces:            []string{"eth1"}, Format: "erf", PacketLimit: 1,
			},
			reason: `format "erf"`,
		},
		"snap length below range": {
			options: PacketCaptureOptions{
				PacketCaptureSelector: PacketCaptureSelector{NodeName: "lin1"},
				Interfaces:            []string{"eth1"}, SnapLength: 63, PacketLimit: 1,
			},
			reason: "snap length",
		},
		"snap length above range": {
			options: PacketCaptureOptions{
				PacketCaptureSelector: PacketCaptureSelector{NodeName: "lin1"},
				Interfaces:            []string{"eth1"}, SnapLength: 1<<20 + 1, PacketLimit: 1,
			},
			reason: "snap length",
		},
		"too many packets": {
			options: PacketCaptureOptions{
				PacketCaptureSelector: PacketCaptureSelector{NodeName: "lin1"},
				Interfaces:            []string{"eth1"}, PacketLimit: 1_000_001,
			},
			reason: "packet limit",
		},
		"longer than an hour": {
			options: PacketCaptureOptions{
				PacketCaptureSelector: PacketCaptureSelector{NodeName: "lin1"},
				Interfaces:            []string{"eth1"}, Duration: time.Hour + time.Second,
			},
			reason: "duration",
		},
	} {
		_, err := NormalizePacketCaptureOptions(testCase.options)
		if err == nil || !strings.Contains(err.Error(), testCase.reason) {
			t.Errorf("%s: error = %v, want %q", name, err, testCase.reason)
		}
	}

	options, err := NormalizePacketCaptureOptions(PacketCaptureOptions{
		PacketCaptureSelector: PacketCaptureSelector{NodeName: " lin1 "},
		Interfaces:            []string{" eth1 "}, Duration: time.Hour,
	})
	if err != nil || options.Format != PacketCaptureFormatPCAP ||
		options.SnapLength != DefaultPacketCaptureSnapLength ||
		options.NodeName != "lin1" || options.Interfaces[0] != "eth1" {
		t.Fatalf("defaulted options = %#v, error %v", options, err)
	}
}

func TestResolvePacketCaptureRevisionPathTreatsAMissingDefaultAsNoRevision(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "absent", "revision.json")

	path, err := ResolvePacketCaptureRevisionPath(missing, false)
	if err != nil || path != "" {
		t.Fatalf("missing default revision = %q, %v", path, err)
	}

	path, err = ResolvePacketCaptureRevisionPath(missing, true)
	if err != nil || path != missing {
		t.Fatalf("explicit revision = %q, %v", path, err)
	}

	present := filepath.Join(t.TempDir(), "revision.json")
	if err = os.WriteFile(present, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	path, err = ResolvePacketCaptureRevisionPath(present, false)
	if err != nil || path != present {
		t.Fatalf("mounted default revision = %q, %v", path, err)
	}
}

func TestPacketCaptureTargetsSelectsGroupedNodeByName(t *testing.T) {
	t.Parallel()

	plan := packetCaptureGroupedPlan()

	node, targets, err := PacketCaptureTargets(
		plan,
		PacketCaptureSelector{NodeName: "grouped-device"},
		[]string{"eth1"},
	)
	if err != nil || node.ID != "grouped-node-uid" || len(targets) != 1 ||
		targets[0].ID != "host-uid/a" {
		t.Fatalf("grouped Node target = %#v %#v, %v", node, targets, err)
	}

	// The first Node's interfaces are not the grouped Node's to capture.
	if _, _, err = PacketCaptureTargets(
		plan,
		PacketCaptureSelector{NodeName: "grouped-device"},
		[]string{"package-a"},
	); err == nil || !strings.Contains(err.Error(), "not uniquely planned") {
		t.Fatalf("cross-Node interface error = %v", err)
	}

	if _, _, err = PacketCaptureTargets(
		plan,
		PacketCaptureSelector{NodeName: "absent-device"},
		[]string{"eth1"},
	); err == nil || !strings.Contains(err.Error(), "absent from the accepted plan") {
		t.Fatalf("unknown Node name error = %v", err)
	}

	node, targets, err = PacketCaptureTargets(
		plan,
		PacketCaptureSelector{NodeName: "future-device"},
		[]string{"ethernet-1/1", "package-b"},
	)
	if err != nil || node.ID != "opaque-node-uid" || len(targets) != 2 ||
		targets[0].Name != "e1-1" || targets[1].Name != "package-b" {
		t.Fatalf("alias target = %#v, %v", targets, err)
	}

	if _, _, err = PacketCaptureTargets(
		plan,
		PacketCaptureSelector{NodeName: "future-device"},
		[]string{"e1-1", "ethernet-1/1"},
	); err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("duplicate interface error = %v", err)
	}
}

func TestRunPacketCaptureDeniesWholeCallWhenOneInterfaceIsForeign(t *testing.T) {
	t.Parallel()

	opened := []string{}

	var (
		capture bytes.Buffer
		audit   bytes.Buffer
	)

	err := runPacketCapture(
		context.Background(),
		packetCaptureGroupedPlan(),
		PacketCaptureOptions{
			PacketCaptureSelector: PacketCaptureSelector{NodeName: "future-device"},
			Interfaces:            []string{"package-a", "eth1"},
			Format:                PacketCaptureFormatPCAPNG, PacketLimit: 1,
		},
		&capture,
		&audit,
		func(name string, _ int) (packetCaptureSource, error) {
			opened = append(opened, name)

			return &fakePacketCaptureSource{}, nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), `"eth1"`) {
		t.Fatalf("foreign interface error = %v", err)
	}

	if len(opened) != 0 || capture.Len() != 0 {
		t.Fatalf("denied capture opened %v and wrote %d bytes", opened, capture.Len())
	}

	records := decodePacketCaptureAudit(t, audit.Bytes())
	if len(records) != 1 || records[0].Status != "Denied" ||
		records[0].NodeID != "opaque-node-uid" || records[0].NodeName != "future-device" ||
		len(records[0].Interfaces) != 2 || records[0].Interfaces[0].Name != "package-a" ||
		records[0].Interfaces[1].Name != "eth1" || records[0].Format != "pcapng" {
		t.Fatalf("denied audit = %#v", records)
	}
}

func TestRunPacketCaptureFailsBeforeWritingWhenAnInterfaceCannotOpen(t *testing.T) {
	t.Parallel()

	first := &fakePacketCaptureSource{}

	var (
		capture bytes.Buffer
		audit   bytes.Buffer
	)

	err := runPacketCapture(
		context.Background(),
		packetCaptureTestPlan(),
		PacketCaptureOptions{
			PacketCaptureSelector: PacketCaptureSelector{NodeName: "future-device"},
			Interfaces:            []string{"package-a", "package-b"},
			Format:                PacketCaptureFormatPCAPNG, PacketLimit: 1,
		},
		&capture,
		&audit,
		func(name string, _ int) (packetCaptureSource, error) {
			if name == "package-b" {
				return nil, errors.New("no such interface")
			}

			return first, nil
		},
	)
	if err == nil || capture.Len() != 0 || !first.closed {
		t.Fatalf("open failure = %v, wrote %d bytes, closed %t", err, capture.Len(), first.closed)
	}

	records := decodePacketCaptureAudit(t, audit.Bytes())
	if len(records) != 2 || records[0].Status != "Started" || records[1].Status != "Failed" {
		t.Fatalf("open failure audit = %#v", records)
	}
}

func TestRunPacketCaptureWritesPCAPNGForSeveralInterfaces(t *testing.T) {
	t.Parallel()

	sources := map[string]*interleavingPacketSource{
		"package-a": newInterleavingPacketSource('a', 25),
		"package-b": newInterleavingPacketSource('b', 25),
	}

	var (
		capture bytes.Buffer
		audit   bytes.Buffer
	)

	err := runPacketCapture(
		context.Background(),
		packetCaptureTestPlan(),
		PacketCaptureOptions{
			PacketCaptureSelector: PacketCaptureSelector{NodeName: "future-device"},
			Interfaces:            []string{"package-b", "package-a"},
			Format:                PacketCaptureFormatPCAPNG, SnapLength: 128,
			Duration: 100 * time.Millisecond,
		},
		&capture,
		&audit,
		func(name string, _ int) (packetCaptureSource, error) { return sources[name], nil },
	)
	if err != nil {
		t.Fatal(err)
	}

	for name, source := range sources {
		if !source.isClosed() {
			t.Fatalf("source %s was not closed", name)
		}
	}

	stream := parsePCAPNG(t, capture.Bytes())
	if !slices.Equal(stream.interfaceNames, []string{"package-b", "package-a"}) ||
		stream.snapLengths[0] != 128 || stream.snapLengths[1] != 128 {
		t.Fatalf("pcapng interfaces = %v snap %v", stream.interfaceNames, stream.snapLengths)
	}

	if len(stream.packets) != 50 {
		t.Fatalf("pcapng packets = %d", len(stream.packets))
	}

	// Each interface's packets stay in order and reference that interface's description.
	next := map[byte]byte{}

	for _, packet := range stream.packets {
		marker := packet.data[0]

		want := uint32(0)
		if marker == 'a' {
			want = 1
		}

		if packet.interfaceID != want || packet.data[1] != next[marker] {
			t.Fatalf("packet %v on interface %d out of order", packet.data, packet.interfaceID)
		}

		next[marker]++
	}

	records := decodePacketCaptureAudit(t, audit.Bytes())
	final := records[len(records)-1]

	if len(records) != 2 || final.Status != "Succeeded" || final.Packets != 50 ||
		len(final.Interfaces) != 2 || final.Interfaces[0].ID != "link-uid/b" ||
		final.Interfaces[0].Packets != 25 || final.Interfaces[1].Packets != 25 ||
		final.InterfaceName != "" {
		t.Fatalf("pcapng audit = %#v", records)
	}
}

func TestRunPacketCapturePacketLimitCountsAcrossInterfaces(t *testing.T) {
	t.Parallel()

	var capture bytes.Buffer

	err := runPacketCapture(
		context.Background(),
		packetCaptureTestPlan(),
		PacketCaptureOptions{
			PacketCaptureSelector: PacketCaptureSelector{NodeID: "opaque-node-uid"},
			Interfaces:            []string{"package-a", "package-b"},
			Format:                PacketCaptureFormatPCAPNG, PacketLimit: 7,
		},
		&capture,
		io.Discard,
		func(name string, _ int) (packetCaptureSource, error) {
			return newInterleavingPacketSource(name[len(name)-1], 100), nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if packets := len(parsePCAPNG(t, capture.Bytes()).packets); packets != 7 {
		t.Fatalf("packet limit across interfaces wrote %d packets", packets)
	}
}

func TestRunPacketCaptureFailsWhenCallerCancels(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())

	var audit bytes.Buffer

	errs := make(chan error, 1)

	go func() {
		errs <- runPacketCapture(
			ctx,
			packetCaptureTestPlan(),
			PacketCaptureOptions{
				PacketCaptureSelector: PacketCaptureSelector{NodeID: "opaque-node-uid"},
				Interfaces:            []string{"package-a"}, Duration: time.Hour,
			},
			io.Discard,
			&audit,
			func(string, int) (packetCaptureSource, error) {
				return &interleavingPacketSource{}, nil
			},
		)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	if err := <-errs; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled capture error = %v", err)
	}

	records := decodePacketCaptureAudit(t, audit.Bytes())
	if records[len(records)-1].Status != "Failed" {
		t.Fatalf("cancelled capture audit = %#v", records)
	}
}

// TestPacketCapturesOverlapAndChain proves captures share no state: several run at the same
// time on one plan and the same interfaces, and a new call starting as another ends succeeds.
// On a Pod each call is its own exec process with its own packet sockets.
func TestPacketCapturesOverlapAndChain(t *testing.T) {
	t.Parallel()

	plan := packetCaptureTestPlan()
	capture := func(packets int) ([]byte, error) {
		var output bytes.Buffer

		err := runPacketCapture(
			context.Background(),
			plan,
			PacketCaptureOptions{
				PacketCaptureSelector: PacketCaptureSelector{NodeName: "future-device"},
				Interfaces:            []string{"package-a", "package-b"},
				Format:                PacketCaptureFormatPCAPNG, PacketLimit: packets,
				Duration: time.Second,
			},
			&output,
			io.Discard,
			func(name string, _ int) (packetCaptureSource, error) {
				return newInterleavingPacketSource(name[len(name)-1], packets), nil
			},
		)

		return output.Bytes(), err
	}

	type result struct {
		raw []byte
		err error
	}

	var group sync.WaitGroup

	results := make(chan result, 8)

	for range 8 {
		group.Go(func() {
			raw, err := capture(20)
			results <- result{raw: raw, err: err}
		})
	}

	group.Wait()
	close(results)

	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}

		if packets := len(parsePCAPNG(t, result.raw).packets); packets != 20 {
			t.Fatalf("concurrent capture wrote %d packets", packets)
		}
	}

	// Chained calls: each starts the moment the previous one returns, and each is a complete
	// stream with its own section header.
	for range 5 {
		raw, err := capture(3)
		if err != nil {
			t.Fatal(err)
		}

		if stream := parsePCAPNG(t, raw); len(stream.packets) != 3 ||
			len(stream.interfaceNames) != 2 {
			t.Fatalf("chained capture = %d packets, %d interfaces",
				len(stream.packets), len(stream.interfaceNames))
		}
	}
}

func TestPacketCaptureInterfacesListsPeers(t *testing.T) {
	t.Parallel()

	plan := packetCaptureGroupedPlan()

	all, err := PacketCaptureInterfaces(plan, PacketCaptureSelector{})
	if err != nil {
		t.Fatal(err)
	}

	want := []PacketCaptureInterface{
		{
			Node: "future-device", Interface: "e1-1", Alias: "ethernet-1/1",
			PeerNode: "remote-device", PeerInterface: "eth3",
		},
		{
			Node: "future-device", Interface: "package-a",
			PeerNode: "future-device", PeerInterface: "package-b",
		},
		{
			Node: "future-device", Interface: "package-b",
			PeerNode: "future-device", PeerInterface: "package-a",
		},
		{
			Node: "grouped-device", Interface: "eth1",
			PeerNode: PacketCaptureHostPeer, PeerInterface: "veth-lab",
		},
	}
	if !slices.Equal(all, want) {
		t.Fatalf("all interfaces = %#v", all)
	}

	grouped, err := PacketCaptureInterfaces(plan, PacketCaptureSelector{NodeID: "grouped-node-uid"})
	if err != nil || !slices.Equal(grouped, want[3:]) {
		t.Fatalf("grouped Node interfaces = %#v, %v", grouped, err)
	}

	raw, err := json.Marshal(grouped)
	if err != nil {
		t.Fatal(err)
	}

	if string(raw) != `[{"node":"grouped-device","interface":"eth1","peerNode":"host",`+
		`"peerInterface":"veth-lab"}]` {
		t.Fatalf("interface listing JSON = %s", raw)
	}

	if _, err = PacketCaptureInterfaces(
		plan,
		PacketCaptureSelector{NodeName: "absent-device"},
	); err == nil {
		t.Fatal("listing an absent Node succeeded")
	}
}

func TestPCAPNGStreamBytes(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer

	stream := &pcapngStream{output: &output}
	if err := stream.WriteHeader([]string{"e1-1", "eth10"}, 256); err != nil {
		t.Fatal(err)
	}

	timestamp := time.Unix(0, 0x0000_0102_0304_0506)
	if err := stream.WritePacket(1, capturedPacket{
		Timestamp: timestamp, Data: []byte{1, 2, 3, 4, 5}, OriginalLength: 60,
	}); err != nil {
		t.Fatal(err)
	}

	le32 := func(value uint32) []byte { return binary.LittleEndian.AppendUint32(nil, value) }
	le16 := func(value uint16) []byte { return binary.LittleEndian.AppendUint16(nil, value) }
	join := func(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

	want := join(
		// Section Header Block: 16-byte body, shb_userappl "clabernetes" padded to 12, end.
		le32(0x0a0d0d0a), le32(48), le32(0x1a2b3c4d), le16(1), le16(0),
		bytes.Repeat([]byte{0xff}, 8),
		le16(4), le16(11), []byte("clabernetes\x00"),
		le16(0), le16(0),
		le32(48),
		// Interface Description Block for e1-1: if_name, if_tsresol = 9, end.
		le32(1), le32(40), le16(1), le16(0), le32(256),
		le16(2), le16(4), []byte("e1-1"),
		le16(9), le16(1), []byte{9, 0, 0, 0},
		le16(0), le16(0),
		le32(40),
		// Interface Description Block for eth10, whose name pads to 8 bytes.
		le32(1), le32(44), le16(1), le16(0), le32(256),
		le16(2), le16(5), []byte("eth10\x00\x00\x00"),
		le16(9), le16(1), []byte{9, 0, 0, 0},
		le16(0), le16(0),
		le32(44),
		// Enhanced Packet Block on interface 1 with a nanosecond timestamp, data padded to 8.
		le32(6), le32(40), le32(1), le32(0x0102), le32(0x03040506), le32(5), le32(60),
		[]byte{1, 2, 3, 4, 5, 0, 0, 0},
		le32(40),
	)

	if !bytes.Equal(output.Bytes(), want) {
		t.Fatalf("pcapng bytes =\n% x\nwant\n% x", output.Bytes(), want)
	}

	if err := (&pcapngStream{output: io.Discard}).WritePacket(0, capturedPacket{
		Timestamp: time.Unix(-1, 0),
	}); err == nil {
		t.Fatal("a timestamp before the epoch was accepted")
	}
}

func TestPacketCapturePeerNodeFollowsTheWireServiceName(t *testing.T) {
	t.Parallel()

	intf := clabernetesinternaldeviceplan.InterfacePlan{
		Connectivity:  clabernetesinternaldeviceplan.ConnectivityWire,
		PeerNodeID:    "remote-uid",
		PeerTransport: FabricServiceName("srl2"),
	}
	if peer := packetCapturePeerNode(intf, map[string]string{}); peer != "srl2" {
		t.Fatalf("wire peer = %q", peer)
	}

	intf.PeerTransport = "10.0.0.1"
	if peer := packetCapturePeerNode(intf, map[string]string{}); peer != "" {
		t.Fatalf("unnamed wire peer = %q", peer)
	}
}

type parsedPCAPNGPacket struct {
	interfaceID uint32
	data        []byte
}

type parsedPCAPNG struct {
	interfaceNames []string
	snapLengths    []uint32
	packets        []parsedPCAPNGPacket
}

// parsePCAPNG walks a little-endian pcapng stream with one section and checks every block's
// framing, option layout, and interface reference.
func parsePCAPNG(t *testing.T, raw []byte) parsedPCAPNG {
	t.Helper()

	result := parsedPCAPNG{}
	sections := 0

	for len(raw) > 0 {
		if len(raw) < 12 {
			t.Fatalf("truncated pcapng block: % x", raw)
		}

		blockType := binary.LittleEndian.Uint32(raw[0:])
		total := binary.LittleEndian.Uint32(raw[4:])

		if total%4 != 0 || int(total) > len(raw) ||
			binary.LittleEndian.Uint32(raw[total-4:]) != total {
			t.Fatalf("pcapng block framing is invalid: type %#x total %d", blockType, total)
		}

		body := raw[8 : total-4]

		switch blockType {
		case pcapngSectionHeaderType:
			sections++

			if binary.LittleEndian.Uint32(body) != pcapngByteOrderMagic {
				t.Fatal("pcapng byte-order magic is invalid")
			}
		case pcapngInterfaceType:
			if binary.LittleEndian.Uint16(body) != packetCaptureLinkTypeEthernet {
				t.Fatal("pcapng interface link type is not Ethernet")
			}

			result.snapLengths = append(result.snapLengths, binary.LittleEndian.Uint32(body[4:]))
			result.interfaceNames = append(result.interfaceNames, pcapngOptionValue(t, body[8:]))
		case pcapngEnhancedType:
			interfaceID := binary.LittleEndian.Uint32(body)
			captured := binary.LittleEndian.Uint32(body[12:])

			if int(interfaceID) >= len(result.interfaceNames) {
				t.Fatalf("packet references undescribed interface %d", interfaceID)
			}

			result.packets = append(result.packets, parsedPCAPNGPacket{
				interfaceID: interfaceID,
				data:        slices.Clone(body[20 : 20+captured]),
			})
		default:
			t.Fatalf("unexpected pcapng block type %#x", blockType)
		}

		raw = raw[total:]
	}

	if sections != 1 {
		t.Fatalf("pcapng stream has %d sections", sections)
	}

	return result
}

func pcapngOptionValue(t *testing.T, options []byte) string {
	t.Helper()

	for len(options) >= 4 {
		code := binary.LittleEndian.Uint16(options)
		length := int(binary.LittleEndian.Uint16(options[2:]))

		if code == pcapngOptionEnd {
			break
		}

		if code == pcapngOptionIfName {
			return string(options[4 : 4+length])
		}

		options = options[4+length+pcapngPadding(length):]
	}

	t.Fatal("pcapng interface has no if_name")

	return ""
}
