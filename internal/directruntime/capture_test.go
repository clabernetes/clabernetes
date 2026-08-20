//nolint:err113,gocyclo,testpackage // dense fixture-driven tests exercise one boundary end to end.
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
	"strings"
	"testing"
	"time"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
)

type fakePacketCaptureSource struct {
	packets []capturedPacket
	closed  bool
}

func (s *fakePacketCaptureSource) ReadPacket(ctx context.Context) (capturedPacket, error) {
	if len(s.packets) == 0 {
		<-ctx.Done()

		return capturedPacket{}, ctx.Err()
	}

	packet := s.packets[0]
	s.packets = s.packets[1:]

	return packet, nil
}

func (s *fakePacketCaptureSource) Close() error {
	s.closed = true

	return nil
}

func TestRunPacketCaptureWritesPCAPAndSecretFreeAuditForOpaqueFutureKind(t *testing.T) {
	t.Parallel()

	plan := packetCaptureTestPlan()
	payloads := [][]byte{{0, 1, 2, 3}, []byte("packet-payload-must-not-enter-audit")}
	source := &fakePacketCaptureSource{packets: []capturedPacket{
		{Timestamp: time.Unix(100, 123_000), Data: payloads[0], OriginalLength: 4},
		{Timestamp: time.Unix(101, 456_000), Data: payloads[1], OriginalLength: 128},
	}}

	var (
		capture bytes.Buffer
		audit   bytes.Buffer
	)

	err := runPacketCapture(
		context.Background(),
		plan,
		PacketCaptureOptions{
			NodeID: "opaque-node-uid", InterfaceName: "package-a",
			SnapLength: 128, PacketLimit: 2,
		},
		&capture,
		&audit,
		func(name string, snapLength int) (packetCaptureSource, error) {
			if name != "package-a" || snapLength != 128 {
				t.Fatalf("packet source target = %q/%d", name, snapLength)
			}

			return source, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if !source.closed {
		t.Fatal("packet source was not closed")
	}

	raw := capture.Bytes()
	if len(raw) != 24+16+len(payloads[0])+16+len(payloads[1]) {
		t.Fatalf("pcap size = %d", len(raw))
	}

	if magic := binary.LittleEndian.Uint32(raw[:4]); magic != 0xa1b2c3d4 {
		t.Fatalf("pcap magic = %#x", magic)
	}

	if snapLength := binary.LittleEndian.Uint32(raw[16:20]); snapLength != 128 {
		t.Fatalf("pcap snap length = %d", snapLength)
	}

	firstCaptured := binary.LittleEndian.Uint32(raw[32:36])

	firstOriginal := binary.LittleEndian.Uint32(raw[36:40])
	if firstCaptured != 4 || firstOriginal != 4 ||
		!bytes.Equal(raw[40:44], payloads[0]) {
		t.Fatalf(
			"first pcap record = captured %d original %d data %v",
			firstCaptured,
			firstOriginal,
			raw[40:44],
		)
	}

	if strings.Contains(audit.String(), "packet-payload-must-not-enter-audit") {
		t.Fatalf("packet payload leaked into audit: %s", audit.String())
	}

	records := decodePacketCaptureAudit(t, audit.Bytes())
	if len(records) != 2 || records[0].Status != "Started" ||
		records[1].Status != "Succeeded" || records[1].Packets != 2 ||
		records[1].CapturedBytes != uint64(len(payloads[0])+len(payloads[1])) ||
		records[1].NodeID != "opaque-node-uid" || records[1].InterfaceID != "link-uid/a" ||
		records[1].PlanDigest == "" {
		t.Fatalf("packet capture audit = %#v", records)
	}
}

func TestRunPacketCaptureDeniesInterfaceOutsideRequestedLogicalNode(t *testing.T) {
	t.Parallel()

	plan := packetCaptureTestPlan()
	plan.Nodes = append(plan.Nodes, clabernetesinternaldeviceplan.NodePlan{
		ID: "other-node-uid", Name: "other", Kind: "another-opaque-kind",
		ContainerIDs: []string{
			"other-container",
		}, ReadinessContainerIDs: []string{"other-container"},
	})
	plan.Containers = append(plan.Containers, clabernetesinternaldeviceplan.ContainerPlan{
		ID: "other-container", NodeID: "other-node-uid", NamespaceOwnerID: "container-a",
		Image: "example/other:1", Required: true,
	})
	opened := false

	var audit bytes.Buffer

	err := runPacketCapture(
		context.Background(),
		plan,
		PacketCaptureOptions{
			NodeID: "other-node-uid", InterfaceName: "package-a", PacketLimit: 1,
		},
		io.Discard,
		&audit,
		func(string, int) (packetCaptureSource, error) {
			opened = true

			return nil, errors.New("must not open")
		},
	)
	if err == nil || !strings.Contains(err.Error(), "not uniquely planned") || opened {
		t.Fatalf("unauthorized capture = opened %t, error %v", opened, err)
	}

	records := decodePacketCaptureAudit(t, audit.Bytes())
	if len(records) != 1 || records[0].Status != "Denied" ||
		records[0].NodeID != "other-node-uid" || records[0].PlanDigest == "" {
		t.Fatalf("denied packet capture audit = %#v", records)
	}
}

func TestPacketCaptureRequiresFiniteBound(t *testing.T) {
	t.Parallel()

	_, err := NormalizePacketCaptureOptions(PacketCaptureOptions{
		NodeID: "node", InterfaceName: "eth1",
	})
	if err == nil || !strings.Contains(err.Error(), "requires a packet or duration limit") {
		t.Fatalf("NormalizePacketCaptureOptions() error = %v", err)
	}
}

func TestRunPacketCaptureCompletesAtDurationBound(t *testing.T) {
	t.Parallel()

	source := &fakePacketCaptureSource{}

	err := runPacketCapture(
		context.Background(),
		packetCaptureTestPlan(),
		PacketCaptureOptions{
			NodeID: "opaque-node-uid", InterfaceName: "package-a",
			Duration: 5 * time.Millisecond,
		},
		io.Discard,
		io.Discard,
		func(string, int) (packetCaptureSource, error) { return source, nil },
	)
	if err != nil || !source.closed {
		t.Fatalf("duration-bounded capture = closed %t, error %v", source.closed, err)
	}
}

func TestRunPacketCaptureAuthorizesLiveInterfaceFromValidatedRevision(t *testing.T) {
	t.Parallel()

	baseInput, basePlan, desiredInput, desiredPlan := packetCaptureRevisionPlans(t)

	revision, err := NewConnectivityRevision(baseInput, basePlan, desiredInput, desiredPlan)
	if err != nil {
		t.Fatal(err)
	}

	raw, err := revision.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}

	revisionPath := filepath.Join(t.TempDir(), "revision.json")
	if err = os.WriteFile(revisionPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	source := &fakePacketCaptureSource{packets: []capturedPacket{{
		Timestamp: time.Unix(100, 0), Data: []byte{1, 2, 3}, OriginalLength: 3,
	}}}
	opened := false

	err = runPacketCaptureWithRevision(
		context.Background(),
		baseInput,
		basePlan,
		revisionPath,
		PacketCaptureOptions{
			NodeID: "opaque-node-uid", InterfaceName: "package-a", PacketLimit: 1,
		},
		io.Discard,
		io.Discard,
		func(name string, _ int) (packetCaptureSource, error) {
			opened = name == "package-a"

			return source, nil
		},
	)
	if err != nil || !opened || !source.closed {
		t.Fatalf("revision capture = opened %t closed %t error %v", opened, source.closed, err)
	}
}

func decodePacketCaptureAudit(t *testing.T, raw []byte) []PacketCaptureAuditRecord {
	t.Helper()

	decoder := json.NewDecoder(bytes.NewReader(raw))
	records := []PacketCaptureAuditRecord{}

	for {
		var record PacketCaptureAuditRecord
		if err := decoder.Decode(&record); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			t.Fatal(err)
		}

		records = append(records, record)
	}

	return records
}

func packetCaptureTestPlan() clabernetesinternaldeviceplan.Plan {
	return clabernetesinternaldeviceplan.Plan{
		SchemaVersion: clabernetesinternaldeviceplan.SchemaVersion,
		Compatibility: clabernetesinternaldeviceplan.Compatibility{
			ContainerlabModule:  clabernetesinternaldeviceplan.ContainerlabModulePath,
			ContainerlabVersion: "v-test", PlanSchemaVersion: clabernetesinternaldeviceplan.SchemaVersion,
			RegistryDigest: "sha256:" + strings.Repeat("a", 64),
		},
		InputDigest: "sha256:" + strings.Repeat("b", 64),
		Planner: clabernetesinternaldeviceplan.PlannerIdentity{
			Name: "clabernetes", Revision: "capture-test",
		},
		Nodes: []clabernetesinternaldeviceplan.NodePlan{{
			ID: "opaque-node-uid", Name: "future-device", Kind: "future-package-kind",
			ContainerIDs: []string{"container-a"}, ReadinessContainerIDs: []string{"container-a"},
		}},
		Containers: []clabernetesinternaldeviceplan.ContainerPlan{{
			ID: "container-a", NodeID: "opaque-node-uid", NamespaceOwnerID: "container-a",
			Image: "example/device:1", Required: true,
		}},
		Interfaces: []clabernetesinternaldeviceplan.InterfacePlan{
			{
				ID: "link-uid/a", NodeID: "opaque-node-uid", NamespaceOwnerID: "container-a",
				Name: "package-a", LinkID: "link-uid", PeerNodeID: "opaque-node-uid",
				PeerInterface: "package-b", Connectivity: "loopback", MTU: 1500,
				LinkApplyMode: clabernetesinternaldeviceplan.LinkApplyLive, RequiredAtStart: true,
			},
			{
				ID: "link-uid/b", NodeID: "opaque-node-uid", NamespaceOwnerID: "container-a",
				Name: "package-b", LinkID: "link-uid", PeerNodeID: "opaque-node-uid",
				PeerInterface: "package-a", Connectivity: "loopback", MTU: 1500,
				LinkApplyMode: clabernetesinternaldeviceplan.LinkApplyLive, RequiredAtStart: true,
			},
		},
	}
}

func packetCaptureRevisionPlans(t *testing.T) (
	clabernetesinternaldeviceplan.Input,
	clabernetesinternaldeviceplan.Plan,
	clabernetesinternaldeviceplan.Input,
	clabernetesinternaldeviceplan.Plan,
) {
	t.Helper()

	desiredPlan := packetCaptureTestPlan()
	basePlan := desiredPlan
	basePlan.Interfaces = nil
	compatibility := desiredPlan.Compatibility
	baseInput := clabernetesinternaldeviceplan.Input{
		SchemaVersion: clabernetesinternaldeviceplan.SchemaVersion, TopologyName: "capture-test",
		Compatibility: compatibility,
		Nodes: []clabernetesinternaldeviceplan.NodeInput{{
			ID: "opaque-node-uid", Name: "future-device", Kind: "future-package-kind",
			Definition: []byte(`{"kind":"future-package-kind","image":"example/device:1"}`),
		}},
		Images: []clabernetesinternaldeviceplan.ImageInput{{
			NodeID: "opaque-node-uid", Role: "device", SourceReference: "example/device:1",
			DigestReference: "example/device@sha256:aaaaaaaa",
			Platform: clabernetesinternaldeviceplan.Platform{
				OS:           "linux",
				Architecture: "amd64",
			},
		}},
	}

	baseDigest, err := baseInput.Digest()
	if err != nil {
		t.Fatal(err)
	}

	basePlan.InputDigest = baseDigest
	desiredInput := baseInput
	desiredInput.Interfaces = []clabernetesinternaldeviceplan.InterfaceInput{
		{
			ID: "link-uid/a", NodeID: "opaque-node-uid", Name: "package-a",
			LinkID: "link-uid", PeerNodeID: "opaque-node-uid", PeerInterface: "package-b",
			Connectivity: "loopback", MTU: 1500,
		},
		{
			ID: "link-uid/b", NodeID: "opaque-node-uid", Name: "package-b",
			LinkID: "link-uid", PeerNodeID: "opaque-node-uid", PeerInterface: "package-a",
			Connectivity: "loopback", MTU: 1500,
		},
	}

	desiredDigest, err := desiredInput.Digest()
	if err != nil {
		t.Fatal(err)
	}

	desiredPlan.InputDigest = desiredDigest
	for _, intf := range desiredPlan.Interfaces {
		desiredPlan.Actions = append(desiredPlan.Actions, clabernetesinternaldeviceplan.Action{
			ID: "wait/" + intf.ID, Phase: clabernetesinternaldeviceplan.PhasePreStart,
			Target: clabernetesinternaldeviceplan.ActionTarget{
				NodeID: "opaque-node-uid", ContainerID: "container-a",
				NamespaceOwnerID: "container-a",
			},
			Kind: clabernetesinternaldeviceplan.ActionWaitInterface,
			WaitInterface: &clabernetesinternaldeviceplan.WaitInterfaceAction{
				InterfaceID: intf.ID, TimeoutSeconds: 30,
			},
		})
	}

	return baseInput, basePlan, desiredInput, desiredPlan
}
