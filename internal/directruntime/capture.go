//nolint:err113,funlen,gocyclo // single-pass boundary logic with structured one-off diagnostics and protocol literals.
package directruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	"golang.org/x/sync/errgroup"
)

const (
	// PacketCaptureAuditSchemaVersion identifies the bounded JSON audit records written on stderr.
	PacketCaptureAuditSchemaVersion = "c9s.direct-packet-capture-audit/v1alpha1"
	// DefaultPacketCaptureSnapLength retains complete ordinary Ethernet frames while bounding
	// memory.
	DefaultPacketCaptureSnapLength = 256 << 10
	// PacketCaptureFormatPCAP is the classic libpcap stream; it carries exactly one interface.
	PacketCaptureFormatPCAP = "pcap"
	// PacketCaptureFormatPCAPNG is the pcapng stream with one Interface Description Block per
	// captured interface.
	PacketCaptureFormatPCAPNG = "pcapng"
	// PacketCaptureHostPeer names the peer of a Link endpoint on the worker host.
	PacketCaptureHostPeer = "host"

	minimumPacketCaptureSnapLength = 64
	maximumPacketCaptureSnapLength = 1 << 20
	maximumPacketCapturePackets    = 1_000_000
	maximumPacketCaptureDuration   = time.Hour
	packetCapturePollInterval      = 250 * time.Millisecond
	packetCaptureLinkTypeEthernet  = 1
)

// The connectivity sidecar mounts its plan, input, and connectivity revision at these fixed
// paths. The Pod renderer mounts them here and the capture command defaults to them, so callers
// name only the Node and its interfaces.
const (
	ConnectivityPlanRoot                 = "/var/run/clabernetes/plan"
	ConnectivityPlanPath                 = ConnectivityPlanRoot + "/plan.json"
	ConnectivityInputRoot                = "/var/run/clabernetes/input"
	ConnectivityInputPath                = ConnectivityInputRoot + "/input.json"
	ConnectivityRevisionRoot             = "/var/run/clabernetes/connectivity-revision"
	ConnectivityRevisionPath             = ConnectivityRevisionRoot + "/revision.json"
	fabricServiceSuffix                  = "-wire"
	packetCaptureDuplicateInterfaceError = "packet capture interface %q is requested more than once"
)

// FabricServiceName is the name of the Service that carries the link wires of a Node. Cross-Pod
// interfaces name their peer's wire Service as their peer transport.
func FabricServiceName(nodeName string) string {
	return nodeName + fabricServiceSuffix
}

// PacketCaptureSelector selects one logical Node of the Pod's plan by its UID or by its Node
// name. An empty selector selects every Node, which only an interface listing accepts.
type PacketCaptureSelector struct {
	NodeID   string
	NodeName string
}

// PacketCaptureOptions identifies one logical Node, its plan-owned direct interfaces, the
// output format, and finite capture bounds. PacketLimit and Duration may both be set; the
// first bound reached completes the capture. PacketLimit counts packets across all interfaces.
type PacketCaptureOptions struct {
	PacketCaptureSelector

	Interfaces  []string
	Format      string
	SnapLength  int
	PacketLimit int
	Duration    time.Duration
}

// PacketCaptureInterface is one capturable interface as --list-interfaces reports it.
// Interface is the planned name that --interface accepts and pcapng if_name carries; Alias is
// the spelling the Link used when the kind maps it. PeerInterface is the peer endpoint as the
// Link wrote it.
type PacketCaptureInterface struct {
	Node          string `json:"node"`
	Interface     string `json:"interface"`
	Alias         string `json:"alias,omitempty"`
	PeerNode      string `json:"peerNode"`
	PeerInterface string `json:"peerInterface"`
}

// PacketCaptureAuditInterface is one requested interface in an audit record. ID is empty for an
// interface the plan does not authorize.
type PacketCaptureAuditInterface struct {
	Name    string `json:"name"`
	ID      string `json:"id,omitempty"`
	Packets int    `json:"packets,omitempty"`
}

// PacketCaptureAuditRecord is a secret-free operation record suitable for stderr and Kubernetes
// Pod-exec audit correlation. Packet payload bytes are deliberately absent. InterfaceID and
// InterfaceName are set only for single-interface captures; Interfaces lists every requested
// interface.
type PacketCaptureAuditRecord struct {
	SchemaVersion string                        `json:"schemaVersion"`
	Time          time.Time                     `json:"time"`
	Operation     string                        `json:"operation"`
	Status        string                        `json:"status"`
	PlanDigest    string                        `json:"planDigest,omitempty"`
	NodeID        string                        `json:"nodeID,omitempty"`
	NodeName      string                        `json:"nodeName,omitempty"`
	InterfaceID   string                        `json:"interfaceID,omitempty"`
	InterfaceName string                        `json:"interfaceName,omitempty"`
	Interfaces    []PacketCaptureAuditInterface `json:"interfaces,omitempty"`
	Format        string                        `json:"format,omitempty"`
	SnapLength    int                           `json:"snapLength,omitempty"`
	PacketLimit   int                           `json:"packetLimit,omitempty"`
	Duration      string                        `json:"duration,omitempty"`
	Packets       int                           `json:"packets,omitempty"`
	CapturedBytes uint64                        `json:"capturedBytes,omitempty"`
	Reason        string                        `json:"reason,omitempty"`
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

// packetCaptureStream writes one capture stream. Callers serialize calls.
type packetCaptureStream interface {
	WriteHeader(interfaces []string, snapLength int) error
	WritePacket(interfaceIndex int, packet capturedPacket) error
}

// NormalizePacketCaptureOptions validates the selector, the format, and finite resource bounds,
// and fills the default format and snap length.
func NormalizePacketCaptureOptions(options PacketCaptureOptions) (PacketCaptureOptions, error) {
	selector, err := normalizePacketCaptureSelector(options.PacketCaptureSelector)
	if err != nil {
		return PacketCaptureOptions{}, err
	}

	if selector.NodeID == "" && selector.NodeName == "" {
		return PacketCaptureOptions{},
			errors.New("packet capture requires --nodeID or --nodeName")
	}

	options.PacketCaptureSelector = selector

	interfaces := make([]string, 0, len(options.Interfaces))
	for _, name := range options.Interfaces {
		name = strings.TrimSpace(name)
		if name == "" {
			return PacketCaptureOptions{}, errors.New("packet capture interface name is empty")
		}

		interfaces = append(interfaces, name)
	}

	if len(interfaces) == 0 {
		return PacketCaptureOptions{}, errors.New("packet capture requires an --interface")
	}

	options.Interfaces = interfaces

	options.Format = strings.TrimSpace(options.Format)
	switch options.Format {
	case "":
		options.Format = PacketCaptureFormatPCAP
	case PacketCaptureFormatPCAP, PacketCaptureFormatPCAPNG:
	default:
		return PacketCaptureOptions{}, fmt.Errorf(
			"packet capture format %q is not %q or %q",
			options.Format,
			PacketCaptureFormatPCAP,
			PacketCaptureFormatPCAPNG,
		)
	}

	if options.Format == PacketCaptureFormatPCAP && len(options.Interfaces) != 1 {
		return PacketCaptureOptions{}, fmt.Errorf(
			"packet capture format %q carries exactly one interface; use --format %s",
			PacketCaptureFormatPCAP,
			PacketCaptureFormatPCAPNG,
		)
	}

	if options.SnapLength == 0 {
		options.SnapLength = DefaultPacketCaptureSnapLength
	}

	if options.SnapLength < minimumPacketCaptureSnapLength ||
		options.SnapLength > maximumPacketCaptureSnapLength {
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

func normalizePacketCaptureSelector(
	selector PacketCaptureSelector,
) (PacketCaptureSelector, error) {
	selector.NodeID = strings.TrimSpace(selector.NodeID)
	selector.NodeName = strings.TrimSpace(selector.NodeName)

	if selector.NodeID != "" && selector.NodeName != "" {
		return PacketCaptureSelector{},
			errors.New("packet capture accepts --nodeID or --nodeName, not both")
	}

	return selector, nil
}

// ResolvePacketCaptureRevisionPath returns the connectivity revision a capture applies. An
// explicit path is used as given. The default path is used when it is mounted; without a
// mounted revision the accepted plan is the effective plan, as it is for the sidecar.
func ResolvePacketCaptureRevisionPath(path string, explicit bool) (string, error) {
	if explicit {
		return path, nil
	}

	_, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}

	if err != nil {
		return "", fmt.Errorf("inspecting connectivity revision: %w", err)
	}

	return path, nil
}

// PacketCaptureNode resolves the selected logical Node in the accepted plan.
func PacketCaptureNode(
	plan clabernetesinternaldeviceplan.Plan,
	selector PacketCaptureSelector,
) (clabernetesinternaldeviceplan.NodePlan, error) {
	matches := []clabernetesinternaldeviceplan.NodePlan{}

	for _, node := range plan.Nodes {
		if (selector.NodeID != "" && node.ID == selector.NodeID) ||
			(selector.NodeName != "" && node.Name == selector.NodeName) {
			matches = append(matches, node)
		}
	}

	switch len(matches) {
	case 0:
		return clabernetesinternaldeviceplan.NodePlan{},
			errors.New("packet capture logical Node is absent from the accepted plan")
	case 1:
		return matches[0], nil
	default:
		return clabernetesinternaldeviceplan.NodePlan{},
			errors.New("packet capture logical Node is not unique in the accepted plan")
	}
}

// PacketCaptureTargets resolves interfaces owned by the selected logical Node in the accepted
// plan, by planned name or by the alias the Link used. Every interface must resolve to exactly
// one distinct planned interface. Kind and vendor identifiers remain opaque and never
// participate in selection.
func PacketCaptureTargets(
	plan clabernetesinternaldeviceplan.Plan,
	selector PacketCaptureSelector,
	interfaceNames []string,
) (clabernetesinternaldeviceplan.NodePlan, []clabernetesinternaldeviceplan.InterfacePlan, error) {
	normalized, err := clabernetesinternaldeviceplan.NormalizePlan(plan)
	if err != nil {
		return clabernetesinternaldeviceplan.NodePlan{}, nil, err
	}

	node, err := PacketCaptureNode(normalized, selector)
	if err != nil {
		return clabernetesinternaldeviceplan.NodePlan{}, nil, err
	}

	targets := make([]clabernetesinternaldeviceplan.InterfacePlan, 0, len(interfaceNames))
	seen := map[string]bool{}

	for _, interfaceName := range interfaceNames {
		matches := []clabernetesinternaldeviceplan.InterfacePlan{}

		for _, intf := range normalized.Interfaces {
			if intf.NodeID != node.ID {
				continue
			}

			if intf.Name == interfaceName || (intf.Alias != "" && intf.Alias == interfaceName) {
				matches = append(matches, intf)
			}
		}

		if len(matches) != 1 {
			return node, nil, fmt.Errorf(
				"packet capture interface %q is not uniquely planned for logical Node",
				interfaceName,
			)
		}

		if seen[matches[0].ID] {
			return node, nil, fmt.Errorf(packetCaptureDuplicateInterfaceError, interfaceName)
		}

		seen[matches[0].ID] = true
		targets = append(targets, matches[0])
	}

	return node, targets, nil
}

// PacketCaptureInterfaces lists the capturable interfaces of the selected logical Node, or of
// every Node in the plan for an empty selector, sorted by Node and interface.
func PacketCaptureInterfaces(
	plan clabernetesinternaldeviceplan.Plan,
	selector PacketCaptureSelector,
) ([]PacketCaptureInterface, error) {
	selector, err := normalizePacketCaptureSelector(selector)
	if err != nil {
		return nil, err
	}

	normalized, err := clabernetesinternaldeviceplan.NormalizePlan(plan)
	if err != nil {
		return nil, err
	}

	nodeNames := make(map[string]string, len(normalized.Nodes))
	for _, node := range normalized.Nodes {
		nodeNames[node.ID] = node.Name
	}

	selectedID := ""

	if selector.NodeID != "" || selector.NodeName != "" {
		node, nodeErr := PacketCaptureNode(normalized, selector)
		if nodeErr != nil {
			return nil, nodeErr
		}

		selectedID = node.ID
	}

	result := []PacketCaptureInterface{}

	for _, intf := range normalized.Interfaces {
		if selectedID != "" && intf.NodeID != selectedID {
			continue
		}

		result = append(result, PacketCaptureInterface{
			Node:          nodeNames[intf.NodeID],
			Interface:     intf.Name,
			Alias:         intf.Alias,
			PeerNode:      packetCapturePeerNode(intf, nodeNames),
			PeerInterface: intf.PeerInterface,
		})
	}

	slices.SortFunc(result, func(left, right PacketCaptureInterface) int {
		if order := strings.Compare(left.Node, right.Node); order != 0 {
			return order
		}

		return strings.Compare(left.Interface, right.Interface)
	})

	return result, nil
}

// packetCapturePeerNode names the peer of a planned interface: a Node of the same Pod by its
// plan name, a host endpoint as "host", and a cross-Pod peer by its wire Service name.
func packetCapturePeerNode(
	intf clabernetesinternaldeviceplan.InterfacePlan,
	nodeNames map[string]string,
) string {
	if name, ok := nodeNames[intf.PeerNodeID]; ok && intf.PeerNodeID != "" {
		return name
	}

	if intf.Connectivity == clabernetesinternaldeviceplan.ConnectivityHost {
		return PacketCaptureHostPeer
	}

	if intf.Connectivity == clabernetesinternaldeviceplan.ConnectivityWire {
		if name, ok := strings.CutSuffix(intf.PeerTransport, fabricServiceSuffix); ok &&
			name != "" {
			return name
		}
	}

	return ""
}

// ListPacketCaptureInterfacesWithRevision writes the capturable interfaces of the effective
// plan as one JSON array.
func ListPacketCaptureInterfacesWithRevision(
	input clabernetesinternaldeviceplan.Input,
	plan clabernetesinternaldeviceplan.Plan,
	revisionPath string,
	selector PacketCaptureSelector,
	output io.Writer,
) error {
	if output == nil {
		return errors.New("packet capture listing output is nil")
	}

	if err := clabernetesinternaldeviceplan.ValidatePlanInputIdentity(input, plan); err != nil {
		return err
	}

	_, effectivePlan, _, err := loadConnectivityRevision(input, plan, revisionPath)
	if err != nil {
		return err
	}

	interfaces, err := PacketCaptureInterfaces(effectivePlan, selector)
	if err != nil {
		return err
	}

	raw, err := json.Marshal(interfaces)
	if err != nil {
		return err
	}

	_, err = output.Write(append(raw, '\n'))

	return err
}

// RunPacketCaptureWithRevision reconstructs the exact effective connectivity plan from the
// immutable cold input/plan and its controller-validated projected revision before authorizing
// the interfaces. This permits Live Link additions without trusting an unverified interface
// name.
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

	record := PacketCaptureAuditRecord{
		PlanDigest: planDigest, NodeID: options.NodeID, NodeName: options.NodeName,
		Format: options.Format, SnapLength: options.SnapLength,
		PacketLimit: options.PacketLimit,
	}
	if options.Duration != 0 {
		record.Duration = options.Duration.String()
	}

	for _, name := range options.Interfaces {
		record.Interfaces = append(record.Interfaces, PacketCaptureAuditInterface{Name: name})
	}

	node, targets, err := PacketCaptureTargets(
		plan,
		options.PacketCaptureSelector,
		options.Interfaces,
	)
	if err != nil {
		if node.ID != "" {
			record.NodeID, record.NodeName = node.ID, node.Name
		}

		if len(options.Interfaces) == 1 {
			record.InterfaceName = options.Interfaces[0]
		}

		writePacketCaptureAudit(audit, withPacketCaptureStatus(record, "Denied", err.Error()))

		return err
	}

	record.NodeID, record.NodeName = node.ID, node.Name

	interfaceNames := make([]string, len(targets))
	for index, target := range targets {
		interfaceNames[index] = target.Name
		record.Interfaces[index] = PacketCaptureAuditInterface{Name: target.Name, ID: target.ID}
	}

	if len(targets) == 1 {
		record.InterfaceID, record.InterfaceName = targets[0].ID, targets[0].Name
	}

	writePacketCaptureAudit(audit, withPacketCaptureStatus(record, "Started", ""))

	sink := &packetCaptureSink{options: options, record: &record}

	defer func() {
		status, reason := "Succeeded", ""
		if returnErr != nil {
			status, reason = "Failed", boundedPacketCaptureReason(returnErr.Error())
		}

		sink.mu.Lock()
		final := record
		final.Interfaces = slices.Clone(record.Interfaces)
		sink.mu.Unlock()

		writePacketCaptureAudit(audit, withPacketCaptureStatus(final, status, reason))
	}()

	// Every interface opens before the first byte reaches the stream, so an interface that
	// vanished fails the call without a partial capture.
	sources := make([]packetCaptureSource, 0, len(targets))

	defer func() {
		for _, source := range sources {
			returnErr = errors.Join(returnErr, source.Close())
		}
	}()

	for _, name := range interfaceNames {
		source, openErr := openSource(name, options.SnapLength)
		if openErr != nil {
			return fmt.Errorf("opening plan-owned packet capture interface %q: %w", name, openErr)
		}

		sources = append(sources, source)
	}

	switch options.Format {
	case PacketCaptureFormatPCAPNG:
		sink.stream = &pcapngStream{output: output}
	default:
		sink.stream = &pcapStream{output: output}
	}

	if err = sink.stream.WriteHeader(interfaceNames, options.SnapLength); err != nil {
		return fmt.Errorf("writing packet capture header: %w", err)
	}

	captureCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	if options.Duration != 0 {
		var cancelTimeout context.CancelFunc

		captureCtx, cancelTimeout = context.WithTimeout(captureCtx, options.Duration)
		defer cancelTimeout()
	}

	sink.stop = cancel

	group, groupCtx := errgroup.WithContext(captureCtx)

	for index, source := range sources {
		group.Go(func() error {
			return sink.capture(groupCtx, index, interfaceNames[index], source)
		})
	}

	if err = group.Wait(); err != nil {
		return err
	}

	// The caller's cancellation is a failure; reaching the duration or packet bound is not.
	return ctx.Err()
}

// packetCaptureSink serializes packets from concurrent interface readers into one stream and
// enforces the packet limit across all interfaces.
type packetCaptureSink struct {
	mu      sync.Mutex
	options PacketCaptureOptions
	record  *PacketCaptureAuditRecord
	stream  packetCaptureStream
	stop    context.CancelFunc
}

func (s *packetCaptureSink) capture(
	ctx context.Context,
	index int,
	name string,
	source packetCaptureSource,
) error {
	for {
		packet, err := source.ReadPacket(ctx)
		if err != nil {
			// The capture ended (bound reached, caller cancelled, or another interface failed);
			// runPacketCapture reports which.
			if ctx.Err() != nil {
				return nil //nolint:nilerr // the read error is the expected wake-up from cancellation.
			}

			return fmt.Errorf("reading packet capture interface %q: %w", name, err)
		}

		if packet.OriginalLength == 0 {
			packet.OriginalLength = len(packet.Data)
		}

		if packet.OriginalLength < len(packet.Data) || packet.OriginalLength > math.MaxUint32 {
			return errors.New("captured packet length is invalid")
		}

		if len(packet.Data) > s.options.SnapLength {
			packet.Data = packet.Data[:s.options.SnapLength]
		}

		if packet.Timestamp.IsZero() {
			packet.Timestamp = time.Now()
		}

		done, err := s.write(index, packet)
		if err != nil || done {
			return err
		}
	}
}

func (s *packetCaptureSink) write(index int, packet capturedPacket) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	limit := s.options.PacketLimit
	if limit != 0 && s.record.Packets >= limit {
		return true, nil
	}

	if err := s.stream.WritePacket(index, packet); err != nil {
		return true, fmt.Errorf("writing captured packet: %w", err)
	}

	s.record.Packets++
	s.record.CapturedBytes += uint64(len(packet.Data))
	s.record.Interfaces[index].Packets++

	if limit != 0 && s.record.Packets >= limit {
		s.stop()

		return true, nil
	}

	return false, nil
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
	record.Reason = boundedPacketCaptureReason(reason)

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
