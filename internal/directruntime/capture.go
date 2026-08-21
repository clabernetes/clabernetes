//nolint:err113,funlen,gocognit,gocyclo,mnd,nestif // single-pass boundary logic with structured one-off diagnostics and protocol literals.
package directruntime

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strings"
	"time"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
)

const (
	// PacketCaptureAuditSchemaVersion identifies the bounded JSON audit records written on stderr.
	PacketCaptureAuditSchemaVersion = "c9s.direct-packet-capture-audit/v1alpha1"
	// DefaultPacketCaptureSnapLength retains complete ordinary Ethernet frames while bounding
	// memory.
	DefaultPacketCaptureSnapLength = 256 << 10
	maximumPacketCaptureSnapLength = 1 << 20
	maximumPacketCapturePackets    = 1_000_000
	maximumPacketCaptureDuration   = time.Hour
	packetCapturePollInterval      = 250 * time.Millisecond
	packetCaptureLinkTypeEthernet  = 1
)

// PacketCaptureOptions identifies one plan-owned direct interface and finite capture bounds.
// PacketLimit and Duration may both be set; the first bound reached completes the capture.
type PacketCaptureOptions struct {
	NodeID        string
	InterfaceName string
	SnapLength    int
	PacketLimit   int
	Duration      time.Duration
}

// PacketCaptureAuditRecord is a secret-free operation record suitable for stderr and Kubernetes
// Pod-exec audit correlation. Packet payload bytes are deliberately absent.
type PacketCaptureAuditRecord struct {
	SchemaVersion string    `json:"schemaVersion"`
	Time          time.Time `json:"time"`
	Operation     string    `json:"operation"`
	Status        string    `json:"status"`
	PlanDigest    string    `json:"planDigest,omitempty"`
	NodeID        string    `json:"nodeID,omitempty"`
	InterfaceID   string    `json:"interfaceID,omitempty"`
	InterfaceName string    `json:"interfaceName,omitempty"`
	SnapLength    int       `json:"snapLength,omitempty"`
	PacketLimit   int       `json:"packetLimit,omitempty"`
	Duration      string    `json:"duration,omitempty"`
	Packets       int       `json:"packets,omitempty"`
	CapturedBytes uint64    `json:"capturedBytes,omitempty"`
	Reason        string    `json:"reason,omitempty"`
}

type capturedPacket struct {
	Timestamp      time.Time
	Data           []byte
	OriginalLength int
}

type packetCaptureSource interface {
	ReadPacket(ctx context.Context) (capturedPacket, error)
	Close() error
}

type packetCaptureSourceFactory func(string, int) (packetCaptureSource, error)

// NormalizePacketCaptureOptions validates finite resource bounds and fills the default snap length.
func NormalizePacketCaptureOptions(options PacketCaptureOptions) (PacketCaptureOptions, error) {
	options.NodeID = strings.TrimSpace(options.NodeID)

	options.InterfaceName = strings.TrimSpace(options.InterfaceName)
	if options.NodeID == "" || options.InterfaceName == "" {
		return PacketCaptureOptions{}, errors.New("packet capture target identity is incomplete")
	}

	if options.SnapLength == 0 {
		options.SnapLength = DefaultPacketCaptureSnapLength
	}

	if options.SnapLength < 64 || options.SnapLength > maximumPacketCaptureSnapLength {
		return PacketCaptureOptions{},
			errors.New("packet capture snap length is outside the supported range")
	}

	if options.PacketLimit < 0 || options.PacketLimit > maximumPacketCapturePackets {
		return PacketCaptureOptions{},
			errors.New("packet capture packet limit is outside the supported range")
	}

	if options.Duration < 0 || options.Duration > maximumPacketCaptureDuration {
		return PacketCaptureOptions{},
			errors.New("packet capture duration is outside the supported range")
	}

	if options.PacketLimit == 0 && options.Duration == 0 {
		return PacketCaptureOptions{},
			errors.New("packet capture requires a packet or duration limit")
	}

	return options, nil
}

// PacketCaptureTarget resolves only an interface owned by the requested logical Node in the
// accepted plan. Kind and vendor identifiers remain opaque and never participate in selection.
func PacketCaptureTarget(
	plan clabernetesinternaldeviceplan.Plan,
	nodeID,
	interfaceName string,
) (clabernetesinternaldeviceplan.InterfacePlan, error) {
	normalized, err := clabernetesinternaldeviceplan.NormalizePlan(plan)
	if err != nil {
		return clabernetesinternaldeviceplan.InterfacePlan{}, err
	}

	nodeExists := slices.ContainsFunc(
		normalized.Nodes,
		func(node clabernetesinternaldeviceplan.NodePlan) bool {
			return node.ID == nodeID
		},
	)
	if !nodeExists {
		return clabernetesinternaldeviceplan.InterfacePlan{},
			errors.New("packet capture logical Node is absent from the accepted plan")
	}

	targets := []clabernetesinternaldeviceplan.InterfacePlan{}

	for _, intf := range normalized.Interfaces {
		if intf.NodeID == nodeID && intf.Name == interfaceName {
			targets = append(targets, intf)
		}
	}

	if len(targets) != 1 {
		return clabernetesinternaldeviceplan.InterfacePlan{}, fmt.Errorf(
			"packet capture interface %q is not uniquely planned for logical Node",
			interfaceName,
		)
	}

	return targets[0], nil
}

// RunPacketCaptureWithRevision reconstructs the exact effective connectivity plan from the
// immutable cold input/plan and its controller-validated projected revision before authorizing the
// interface. This permits Live Link additions without trusting an unverified interface name.
func RunPacketCaptureWithRevision(
	ctx context.Context,
	input clabernetesinternaldeviceplan.Input,
	plan clabernetesinternaldeviceplan.Plan,
	revisionPath string,
	options PacketCaptureOptions,
	output,
	audit io.Writer,
) error {
	return runPacketCaptureWithRevision(
		ctx,
		input,
		plan,
		revisionPath,
		options,
		output,
		audit,
		openPacketCaptureSource,
	)
}

func runPacketCaptureWithRevision(
	ctx context.Context,
	input clabernetesinternaldeviceplan.Input,
	plan clabernetesinternaldeviceplan.Plan,
	revisionPath string,
	options PacketCaptureOptions,
	output,
	audit io.Writer,
	openSource packetCaptureSourceFactory,
) error {
	if err := clabernetesinternaldeviceplan.ValidatePlanInputIdentity(input, plan); err != nil {
		return err
	}

	_, effectivePlan, _, err := loadConnectivityRevision(input, plan, revisionPath)
	if err != nil {
		return err
	}

	return runPacketCapture(ctx, effectivePlan, options, output, audit, openSource)
}

func runPacketCapture(
	ctx context.Context,
	plan clabernetesinternaldeviceplan.Plan,
	options PacketCaptureOptions,
	output,
	audit io.Writer,
	openSource packetCaptureSourceFactory,
) (returnErr error) {
	if ctx == nil {
		return errors.New("packet capture context is nil")
	}

	if output == nil || openSource == nil {
		return errors.New("packet capture output boundary is incomplete")
	}

	options, err := NormalizePacketCaptureOptions(options)
	if err != nil {
		return err
	}

	planDigest, err := plan.Digest()
	if err != nil {
		return err
	}

	target, err := PacketCaptureTarget(plan, options.NodeID, options.InterfaceName)
	if err != nil {
		writePacketCaptureAudit(audit, PacketCaptureAuditRecord{
			PlanDigest: planDigest, NodeID: options.NodeID,
			InterfaceName: options.InterfaceName, Status: "Denied", Reason: err.Error(),
		})

		return err
	}

	record := PacketCaptureAuditRecord{
		PlanDigest: planDigest, NodeID: options.NodeID, InterfaceID: target.ID,
		InterfaceName: options.InterfaceName, SnapLength: options.SnapLength,
		PacketLimit: options.PacketLimit,
	}
	if options.Duration != 0 {
		record.Duration = options.Duration.String()
	}

	writePacketCaptureAudit(audit, withPacketCaptureStatus(record, "Started", ""))

	defer func() {
		status, reason := "Succeeded", ""
		if returnErr != nil {
			status, reason = "Failed", boundedPacketCaptureReason(returnErr.Error())
		}

		writePacketCaptureAudit(audit, withPacketCaptureStatus(record, status, reason))
	}()

	source, err := openSource(options.InterfaceName, options.SnapLength)
	if err != nil {
		return fmt.Errorf("opening plan-owned packet capture interface: %w", err)
	}

	defer func() {
		returnErr = errors.Join(returnErr, source.Close())
	}()

	if err = writePCAPGlobalHeader(output, options.SnapLength); err != nil {
		return fmt.Errorf("writing packet capture header: %w", err)
	}

	captureCtx := ctx

	cancel := func() {}
	if options.Duration != 0 {
		captureCtx, cancel = context.WithTimeout(ctx, options.Duration)
	}
	defer cancel()

	for options.PacketLimit == 0 || record.Packets < options.PacketLimit {
		packet, readErr := source.ReadPacket(captureCtx)
		if readErr != nil {
			if captureCtx.Err() != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}

				if options.Duration != 0 && captureCtx.Err() == context.DeadlineExceeded {
					break
				}
			}

			return fmt.Errorf("reading packet capture interface: %w", readErr)
		}

		if packet.OriginalLength == 0 {
			packet.OriginalLength = len(packet.Data)
		}

		if packet.OriginalLength < len(packet.Data) || packet.OriginalLength > math.MaxUint32 {
			return errors.New("captured packet length is invalid")
		}

		if len(packet.Data) > options.SnapLength {
			packet.Data = packet.Data[:options.SnapLength]
		}

		if packet.Timestamp.IsZero() {
			packet.Timestamp = time.Now()
		}

		if err = writePCAPPacket(output, packet); err != nil {
			return fmt.Errorf("writing captured packet: %w", err)
		}

		record.Packets++
		record.CapturedBytes += uint64(len(packet.Data))
	}

	return nil
}

func writePCAPGlobalHeader(output io.Writer, snapLength int) error {
	return binary.Write(output, binary.LittleEndian, struct {
		Magic       uint32
		Major       uint16
		Minor       uint16
		Timezone    int32
		SigFigs     uint32
		SnapLength  uint32
		NetworkType uint32
	}{
		Magic: 0xa1b2c3d4, Major: 2, Minor: 4, SnapLength: uint32(snapLength), //nolint:gosec // the value is bounded by validated plan input or a kernel interface width.
		NetworkType: packetCaptureLinkTypeEthernet,
	})
}

func writePCAPPacket(output io.Writer, packet capturedPacket) error {
	seconds := packet.Timestamp.Unix()
	if seconds < 0 || seconds > math.MaxUint32 {
		return errors.New("packet timestamp is outside the pcap range")
	}

	header := struct {
		Seconds        uint32
		Microseconds   uint32
		CapturedLength uint32
		OriginalLength uint32
	}{
		Seconds: uint32(
			seconds,
		), Microseconds: uint32(packet.Timestamp.Nanosecond() / 1_000), //nolint:gosec // the value is bounded by validated plan input or a kernel interface width.
		//nolint:gosec // the value is bounded by validated plan input or a kernel interface width.
		CapturedLength: uint32(
			len(packet.Data),
		), OriginalLength: uint32(packet.OriginalLength), //nolint:gosec // the value is bounded by validated plan input or a kernel interface width.
	}
	if err := binary.Write(output, binary.LittleEndian, header); err != nil {
		return err
	}

	_, err := output.Write(packet.Data)

	return err
}

func withPacketCaptureStatus(
	record PacketCaptureAuditRecord,
	status,
	reason string,
) PacketCaptureAuditRecord {
	record.SchemaVersion = PacketCaptureAuditSchemaVersion
	record.Time = time.Now().UTC()
	record.Operation = "PacketCapture"
	record.Status = status
	record.Reason = reason

	return record
}

func writePacketCaptureAudit(output io.Writer, record PacketCaptureAuditRecord) {
	if output == nil {
		return
	}

	if record.SchemaVersion == "" {
		record = withPacketCaptureStatus(record, record.Status, record.Reason)
	}

	//nolint:errchkjson // The trailer is best-effort diagnostics on an already-failing stream.
	_ = json.NewEncoder(output).Encode(record)
}

func boundedPacketCaptureReason(value string) string {
	const maximumReasonBytes = 512
	if len(value) <= maximumReasonBytes {
		return value
	}

	return value[:maximumReasonBytes]
}
