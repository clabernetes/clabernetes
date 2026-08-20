//nolint:err113,funcorder,funlen,gocognit,noinlineerr,perfsprint,wsl_v5 // Explicit RPC diagnostics.
package hostendpoint

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
	"k8s.io/klog/v2"
)

const (
	defaultSweepInterval    = 15 * time.Second
	requestTimeout          = 30 * time.Second
	staleSocketProbeTimeout = 100 * time.Millisecond
	socketDirectoryMode     = 0o750
	socketMode              = 0o600
	maximumReceivedFDBytes  = 8
)

// Operations is the daemon's only host-network mutation boundary.
type Operations interface {
	List(ctx context.Context) ([]OwnedEndpoint, error)
	Ensure(ctx context.Context, endpoint Endpoint, pod ObjectIdentity, namespaceFD int) error
	Delete(ctx context.Context, endpoint OwnedEndpoint) error
	ListFabric(ctx context.Context) ([]OwnedFabricObject, error)
	EnsureFabric(
		ctx context.Context,
		endpoint FabricEndpoint,
		pod ObjectIdentity,
		nodeAddress string,
		namespaceFD int,
	) (FabricStatus, error)
	ReconcileFabricTransports(
		ctx context.Context,
		endpoints []FabricEndpoint,
		nodeAddress string,
	) error
	DeleteFabric(ctx context.Context, object OwnedFabricObject) error
	ListManagement(ctx context.Context) ([]OwnedManagementObject, error)
	EnsureManagement(
		ctx context.Context,
		endpoint ManagementEndpoint,
		pod ObjectIdentity,
		namespaceFD int,
	) (ManagementStatus, error)
	DeleteManagement(ctx context.Context, object OwnedManagementObject) error
}

// Daemon reconciles one Kubernetes worker's c9s-owned host endpoint objects.
type Daemon struct {
	NodeName      string
	NodeAddress   string
	State         State
	Operations    Operations
	SweepInterval time.Duration
	mutex         sync.Mutex
}

// Reconcile validates one Pod's complete desired set, removes stale node state, and realizes the
// exact requested endpoints using the supplied Pod network-namespace descriptor. The returned
// statuses report per-Link fabric transport and management loop readiness.
func (d *Daemon) Reconcile(
	ctx context.Context,
	request ReconcileRequest,
	namespaceFD int,
) ([]FabricStatus, *ManagementStatus, error) {
	d.mutex.Lock()
	defer d.mutex.Unlock()

	return d.reconcile(ctx, request, namespaceFD)
}

//nolint:gocyclo // Host, fabric, and management endpoint families validate independently.
func (d *Daemon) reconcile(
	ctx context.Context,
	request ReconcileRequest,
	namespaceFD int,
) ([]FabricStatus, *ManagementStatus, error) {
	if ctx == nil || d.State == nil || d.Operations == nil || d.NodeName == "" || namespaceFD < 0 {
		return nil, nil, fmt.Errorf("host-endpoint daemon boundary is incomplete")
	}
	normalized, err := normalizeRequest(request)
	if err != nil {
		return nil, nil, err
	}
	expected, err := d.State.ExpectedForPod(ctx, d.NodeName, normalized.Pod)
	if err != nil {
		return nil, nil, fmt.Errorf("authorizing host-endpoint request: %w", err)
	}
	expectedFabric, err := d.expectedFabricForPod(ctx, normalized.Pod)
	if err != nil {
		return nil, nil, fmt.Errorf("authorizing fabric request: %w", err)
	}
	expectedManagement, err := d.expectedManagementForPod(ctx, normalized.Pod)
	if err != nil {
		return nil, nil, fmt.Errorf("authorizing management request: %w", err)
	}
	// The Pod-side interface name is plan-produced helper input confined to the requester's own
	// namespace; adopt it from the (already Linux-validated) request per Link identity so the
	// authoritative set can normalize and compare.
	requestedInterfaces := make(map[string]string, len(normalized.Fabric))
	for _, endpoint := range normalized.Fabric {
		requestedInterfaces[endpoint.Link.UID] = endpoint.PodInterface
	}
	for index := range expectedFabric {
		expectedFabric[index].PodInterface = requestedInterfaces[expectedFabric[index].Link.UID]
	}
	if len(expectedFabric) != len(normalized.Fabric) {
		return nil, nil, fmt.Errorf("fabric request differs from authoritative Kubernetes state")
	}
	if normalized.Management != nil {
		if expectedManagement == nil {
			return nil, nil, fmt.Errorf(
				"management request differs from authoritative Kubernetes state",
			)
		}
		// The Pod-side interface name is helper input confined to the requester's own namespace;
		// adopt it from the (already Linux-validated) request so the authoritative intent can
		// normalize and compare.
		expectedManagement.PodInterface = normalized.Management.PodInterface
		if expectedManagement.Node != normalized.Management.Node ||
			expectedManagement.PodAddress != normalized.Management.PodAddress {
			return nil, nil, fmt.Errorf(
				"management request differs from authoritative Kubernetes state",
			)
		}
	}
	// Authoritative management intent enters the normalized comparison only when the helper
	// requested it: its Pod-side interface name exists only inside the requesting namespace.
	comparableManagement := expectedManagement
	if normalized.Management == nil {
		comparableManagement = nil
	}
	expectedRequest, err := normalizeRequest(ReconcileRequest{
		SchemaVersion: SchemaVersion,
		Pod:           normalized.Pod,
		Endpoints:     expected,
		Fabric:        expectedFabric,
		Management:    comparableManagement,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("normalizing authoritative host-endpoint state: %w", err)
	}
	if !slices.Equal(expectedRequest.Endpoints, normalized.Endpoints) {
		return nil, nil, fmt.Errorf(
			"host-endpoint request differs from authoritative Kubernetes state",
		)
	}
	if !fabricEndpointsEqual(expectedRequest.Fabric, normalized.Fabric) {
		return nil, nil, fmt.Errorf("fabric request differs from authoritative Kubernetes state")
	}
	// Persist the owner node before mutation. An unannotated finalizer therefore proves that no
	// daemon-created host state can exist for that Link.
	for _, endpoint := range normalized.Endpoints {
		if err = d.State.MarkPending(ctx, d.NodeName, normalized.Pod, endpoint); err != nil {
			return nil, nil, fmt.Errorf("recording host-endpoint ownership: %w", err)
		}
	}
	if err = d.sweep(ctx); err != nil {
		return nil, nil, err
	}
	for _, endpoint := range normalized.Endpoints {
		if err = d.Operations.Ensure(ctx, endpoint, normalized.Pod, namespaceFD); err != nil {
			return nil, nil, fmt.Errorf("realizing host Link %q: %w", endpoint.Link.Name, err)
		}
	}
	statuses := make([]FabricStatus, 0, len(expectedRequest.Fabric))
	for _, endpoint := range expectedRequest.Fabric {
		status, fabricErr := d.Operations.EnsureFabric(
			ctx,
			endpoint,
			normalized.Pod,
			d.NodeAddress,
			namespaceFD,
		)
		if fabricErr != nil {
			return nil, nil, fmt.Errorf(
				"realizing fabric Link %q: %w",
				endpoint.Link.Name,
				fabricErr,
			)
		}
		statuses = append(statuses, status)
	}
	var managementStatus *ManagementStatus
	if normalized.Management != nil && expectedRequest.Management != nil {
		status, managementErr := d.Operations.EnsureManagement(
			ctx,
			*expectedRequest.Management,
			normalized.Pod,
			namespaceFD,
		)
		if managementErr != nil {
			return nil, nil, fmt.Errorf("realizing management loop: %w", managementErr)
		}
		managementStatus = &status
	}

	return statuses, managementStatus, d.finalizeAbsentLinks(ctx)
}

// expectedManagementForPod returns this Pod's authoritative management loop intent, or nil when
// the Pod is not a direct workload Pod on this worker.
func (d *Daemon) expectedManagementForPod(
	ctx context.Context,
	pod ObjectIdentity,
) (*ManagementEndpoint, error) {
	desired, err := d.State.DesiredManagementForNode(ctx, d.NodeName)
	if err != nil {
		return nil, err
	}
	for index := range desired {
		if desired[index].pod == pod {
			endpoint := desired[index]

			return &endpoint, nil
		}
	}

	return nil, nil //nolint:nilnil // no matching endpoint is a valid lookup result.
}

// expectedFabricForPod returns this Pod's authoritative fabric endpoints with their internal
// peer placement populated.
func (d *Daemon) expectedFabricForPod(
	ctx context.Context,
	pod ObjectIdentity,
) ([]FabricEndpoint, error) {
	desired, err := d.State.DesiredFabricForNode(ctx, d.NodeName)
	if err != nil {
		return nil, err
	}
	result := []FabricEndpoint{}
	for _, endpoint := range desired {
		if endpointFabricPod(endpoint) == pod {
			result = append(result, endpoint)
		}
	}

	return result, nil
}

// fabricEndpointsEqual compares only the wire-visible fields: the request never carries the
// daemon's internal Pod and peer placement.
func fabricEndpointsEqual(expected, requested []FabricEndpoint) bool {
	if len(expected) != len(requested) {
		return false
	}
	for index := range expected {
		left, right := expected[index], requested[index]
		if left.Link != right.Link || left.Node != right.Node ||
			left.PodInterface != right.PodInterface || left.TunnelID != right.TunnelID ||
			left.MTU != right.MTU {
			return false
		}
	}

	return true
}

// Sweep removes only parseable c9s host objects whose immutable ownership is no longer desired
// on this worker. It never uses interface name alone as deletion authority.
func (d *Daemon) Sweep(ctx context.Context) error {
	d.mutex.Lock()
	defer d.mutex.Unlock()

	return d.sweep(ctx)
}

func (d *Daemon) sweep(ctx context.Context) error {
	if ctx == nil || d.State == nil || d.Operations == nil || d.NodeName == "" {
		return fmt.Errorf("host-endpoint sweep boundary is incomplete")
	}
	desired, err := d.State.DesiredForNode(ctx, d.NodeName)
	if err != nil {
		return fmt.Errorf("reconstructing desired host endpoints: %w", err)
	}
	desiredOwners := make(map[Ownership]string, len(desired))
	for _, endpoint := range desired {
		pod := endpointPod(endpoint)
		if err = validateObjectIdentity(pod); err != nil {
			return fmt.Errorf("desired host endpoint has no Pod identity")
		}
		desiredOwners[ownershipFor(endpoint, pod)] = endpoint.HostInterface
	}
	existing, err := d.Operations.List(ctx)
	if err != nil {
		return fmt.Errorf("inventorying owned host endpoints: %w", err)
	}
	slices.SortFunc(existing, func(left, right OwnedEndpoint) int {
		return strings.Compare(left.HostInterface, right.HostInterface)
	})
	for _, endpoint := range existing {
		name, wanted := desiredOwners[endpoint.Ownership]
		if wanted && name == endpoint.HostInterface {
			continue
		}
		if err = d.Operations.Delete(ctx, endpoint); err != nil {
			return fmt.Errorf("sweeping stale host endpoint %q: %w", endpoint.HostInterface, err)
		}
	}
	if err = d.sweepFabric(ctx); err != nil {
		return err
	}
	if err = d.sweepManagement(ctx); err != nil {
		return err
	}

	return d.finalizeAbsentLinks(ctx)
}

// sweepManagement removes management loops whose owning Pod no longer exists on this worker.
func (d *Daemon) sweepManagement(ctx context.Context) error {
	desired, err := d.State.DesiredManagementForNode(ctx, d.NodeName)
	if err != nil {
		return fmt.Errorf("reconstructing desired management loops: %w", err)
	}
	desiredOwners := make(map[string]bool, len(desired))
	for _, endpoint := range desired {
		desiredOwners[endpoint.Node.UID+"\x00"+endpoint.pod.UID] = true
	}
	existing, err := d.Operations.ListManagement(ctx)
	if err != nil {
		return fmt.Errorf("inventorying owned management loops: %w", err)
	}
	for _, object := range existing {
		if desiredOwners[object.NodeUID+"\x00"+object.PodUID] {
			continue
		}
		if err = d.Operations.DeleteManagement(ctx, object); err != nil {
			return fmt.Errorf("sweeping stale management loop %q: %w", object.Name, err)
		}
	}

	return nil
}

// sweepFabric removes fabric objects whose ownership is no longer desired on this worker and
// re-realizes the host-side transports of every desired endpoint whose leg already exists, so
// peer moves converge without waiting for the owning helper's next request.
func (d *Daemon) sweepFabric(ctx context.Context) error {
	desired, err := d.State.DesiredFabricForNode(ctx, d.NodeName)
	if err != nil {
		return fmt.Errorf("reconstructing desired fabric endpoints: %w", err)
	}
	desiredOwners := make(map[Ownership]bool, len(desired))
	for _, endpoint := range desired {
		pod := endpointFabricPod(endpoint)
		if validateObjectIdentity(pod) != nil {
			return fmt.Errorf("desired fabric endpoint has no Pod identity")
		}
		desiredOwners[fabricOwnershipFor(endpoint, pod)] = true
	}
	existing, err := d.Operations.ListFabric(ctx)
	if err != nil {
		return fmt.Errorf("inventorying owned fabric objects: %w", err)
	}
	for _, object := range existing {
		if desiredOwners[object.Ownership] {
			continue
		}
		if err = d.Operations.DeleteFabric(ctx, object); err != nil {
			return fmt.Errorf("sweeping stale fabric object %q: %w", object.Name, err)
		}
	}

	return d.Operations.ReconcileFabricTransports(ctx, desired, d.NodeAddress)
}

func (d *Daemon) finalizeAbsentLinks(ctx context.Context) error {
	existing, err := d.Operations.List(ctx)
	if err != nil {
		return fmt.Errorf("inventorying host endpoints before finalization: %w", err)
	}
	presentLinkUIDs := map[string]bool{}
	for _, endpoint := range existing {
		presentLinkUIDs[endpoint.Ownership.LinkUID] = true
	}
	links, err := d.State.FinalizingLinks(ctx, d.NodeName)
	if err != nil {
		return err
	}
	for _, link := range links {
		if presentLinkUIDs[link.Identity.UID] {
			continue
		}
		if err = d.State.RemoveFinalizer(ctx, d.NodeName, link.Identity); err != nil {
			return fmt.Errorf("releasing host Link %q finalizer: %w", link.Identity.Name, err)
		}
	}

	return nil
}

// Serve runs the node-local Unix-packet RPC and periodic orphan sweep until ctx is canceled.
//
//nolint:gocyclo // Listener lifecycle and cleanup errors are handled independently.
func (d *Daemon) Serve(ctx context.Context, socketPath string) (returnErr error) {
	if ctx == nil {
		return fmt.Errorf("host-endpoint server context is nil")
	}
	serveContext, cancel := context.WithCancel(ctx)
	defer cancel()
	if socketPath == "" || !filepath.IsAbs(socketPath) ||
		filepath.Clean(socketPath) != socketPath {
		return fmt.Errorf("host-endpoint socket path is invalid")
	}
	if err := prepareSocketPath(serveContext, socketPath); err != nil {
		return err
	}
	address := &net.UnixAddr{Name: socketPath, Net: "unixpacket"}
	listener, err := net.ListenUnix("unixpacket", address)
	if err != nil {
		return fmt.Errorf("listening on host-endpoint socket: %w", err)
	}
	defer func() {
		closeErr := listener.Close()
		if errors.Is(closeErr, net.ErrClosed) {
			closeErr = nil
		}
		returnErr = errors.Join(returnErr, closeErr, removeSocket(socketPath))
	}()
	if err = os.Chmod(socketPath, socketMode); err != nil {
		return fmt.Errorf("setting host-endpoint socket permissions: %w", err)
	}
	if err = d.Sweep(serveContext); err != nil {
		return err
	}
	sweepInterval := d.SweepInterval
	if sweepInterval <= 0 {
		sweepInterval = defaultSweepInterval
	}
	go d.sweepLoop(serveContext, sweepInterval)
	go func() {
		<-serveContext.Done()
		_ = listener.Close()
	}()
	for {
		connection, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			if serveContext.Err() != nil {
				return nil
			}

			return fmt.Errorf("accepting host-endpoint request: %w", acceptErr)
		}
		deadline := time.Now().Add(requestTimeout)
		if deadlineErr := connection.SetDeadline(deadline); deadlineErr != nil {
			_ = connection.Close()

			return fmt.Errorf("setting host-endpoint request deadline: %w", deadlineErr)
		}
		handleErr := d.handleConnection(serveContext, connection)
		closeErr := connection.Close()
		if handleErr != nil {
			klog.Errorf("host-endpoint request failed: %s", handleErr)
		}
		if closeErr != nil {
			klog.Errorf("closing host-endpoint connection: %s", closeErr)
		}
	}
}

func (d *Daemon) sweepLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := d.Sweep(ctx); err != nil && ctx.Err() == nil {
				klog.Errorf("host-endpoint orphan sweep failed: %s", err)
			}
		}
	}
}

func (d *Daemon) handleConnection(ctx context.Context, connection *net.UnixConn) error {
	payload := make([]byte, maximumMessage+1)
	control := make([]byte, unix.CmsgSpace(maximumReceivedFDBytes))
	read, controlRead, flags, _, err := connection.ReadMsgUnix(payload, control)
	if err != nil {
		return err
	}
	if read > maximumMessage || flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 {
		return writeResponse(
			connection,
			nil,
			nil,
			fmt.Errorf("host-endpoint request exceeds size limit"),
		)
	}
	fds, err := receivedFileDescriptors(control[:controlRead])
	if err != nil {
		return writeResponse(connection, nil, nil, err)
	}
	defer func() {
		for _, fd := range fds {
			_ = unix.Close(fd)
		}
	}()
	if len(fds) != 1 {
		return writeResponse(
			connection,
			nil,
			nil,
			fmt.Errorf("request requires one network-namespace handle"),
		)
	}
	request, err := decodeRequest(payload[:read])
	if err != nil {
		return writeResponse(connection, nil, nil, fmt.Errorf("decoding host-endpoint request"))
	}
	statuses, management, err := d.Reconcile(ctx, request, fds[0])

	return writeResponse(connection, statuses, management, err)
}

func receivedFileDescriptors(control []byte) ([]int, error) {
	messages, err := unix.ParseSocketControlMessage(control)
	if err != nil {
		return nil, fmt.Errorf("parsing host-endpoint namespace handle: %w", err)
	}
	result := []int{}
	for _, message := range messages {
		fds, rightsErr := unix.ParseUnixRights(&message)
		if rightsErr != nil {
			return nil, fmt.Errorf("parsing host-endpoint descriptor rights: %w", rightsErr)
		}
		result = append(result, fds...)
	}

	return result, nil
}

func writeResponse(
	connection *net.UnixConn,
	statuses []FabricStatus,
	management *ManagementStatus,
	requestErr error,
) error {
	response := Response{Fabric: statuses, Management: management}
	if requestErr != nil {
		response.Error = requestErr.Error()
	}
	payload, err := json.Marshal(response)
	if err != nil {
		return err
	}
	if _, err = connection.Write(payload); err != nil {
		return err
	}

	return requestErr
}

func decodeRequest(payload []byte) (ReconcileRequest, error) {
	request := ReconcileRequest{}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return ReconcileRequest{}, err
	}
	if err := requireJSONEnd(decoder); err != nil {
		return ReconcileRequest{}, err
	}

	return request, nil
}

func decodeResponse(payload []byte) (Response, error) {
	response := Response{}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return Response{}, err
	}
	if err := requireJSONEnd(decoder); err != nil {
		return Response{}, err
	}

	return response, nil
}

func requireJSONEnd(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("JSON contains trailing data")
		}

		return err
	}

	return nil
}

func prepareSocketPath(ctx context.Context, socketPath string) error {
	directory := filepath.Dir(socketPath)
	if err := os.MkdirAll(directory, socketDirectoryMode); err != nil {
		return fmt.Errorf("creating host-endpoint socket directory: %w", err)
	}
	info, err := os.Lstat(socketPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("host-endpoint socket path contains a non-socket object")
	}
	dialer := &net.Dialer{Timeout: staleSocketProbeTimeout}
	connection, dialErr := dialer.DialContext(ctx, "unixpacket", socketPath)
	if dialErr == nil {
		_ = connection.Close()

		return fmt.Errorf("host-endpoint socket is already served by another process")
	}

	return removeSocket(socketPath)
}

func removeSocket(socketPath string) error {
	err := os.Remove(socketPath)
	if os.IsNotExist(err) {
		return nil
	}

	return err
}

// Client sends one complete Pod reconciliation request and its current network namespace handle.
type Client struct {
	SocketPath string
}

// Reconcile performs one bounded node-local RPC and returns the daemon's per-Link fabric
// transport statuses and the Pod's management loop status.
//
//nolint:gocyclo // Each guard validates one bounded transport invariant.
func (c Client) Reconcile(
	ctx context.Context,
	request ReconcileRequest,
	networkNamespacePath string,
) ([]FabricStatus, *ManagementStatus, error) {
	if ctx == nil {
		return nil, nil, fmt.Errorf("host-endpoint client context is nil")
	}
	normalized, err := normalizeRequest(request)
	if err != nil {
		return nil, nil, err
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return nil, nil, err
	}
	if len(payload) > maximumMessage {
		return nil, nil, fmt.Errorf("host-endpoint request exceeds size limit")
	}
	namespace, err := os.Open(networkNamespacePath) //nolint:gosec // Explicit self netns path.
	if err != nil {
		return nil, nil, fmt.Errorf("opening Pod network namespace: %w", err)
	}
	defer func() { _ = namespace.Close() }()
	socketPath := c.SocketPath
	if socketPath == "" {
		socketPath = DefaultSocketPath
	}
	connection, err := net.DialUnix(
		"unixpacket",
		nil,
		&net.UnixAddr{Name: socketPath, Net: "unixpacket"},
	)
	if err != nil {
		return nil, nil, fmt.Errorf("connecting to host-endpoint daemon: %w", err)
	}
	defer func() { _ = connection.Close() }()
	deadline := time.Now().Add(requestTimeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err = connection.SetDeadline(deadline); err != nil {
		return nil, nil, err
	}
	namespaceFD := namespace.Fd()
	if namespaceFD > uintptr(math.MaxInt) {
		return nil, nil, fmt.Errorf(
			"Pod network-namespace descriptor is outside the supported range",
		)
	}
	rights := unix.UnixRights(int(namespaceFD))
	written, controlWritten, err := connection.WriteMsgUnix(payload, rights, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("sending host-endpoint request: %w", err)
	}
	if written != len(payload) || controlWritten != len(rights) {
		return nil, nil, fmt.Errorf("sending host-endpoint request was incomplete")
	}
	responseRaw := make([]byte, maximumMessage+1)
	read, err := connection.Read(responseRaw)
	if err != nil {
		return nil, nil, fmt.Errorf("reading host-endpoint response: %w", err)
	}
	response, decodeErr := decodeResponse(responseRaw[:read])
	if read > maximumMessage || decodeErr != nil {
		return nil, nil, fmt.Errorf("host-endpoint response is invalid")
	}
	if response.Error != "" {
		return nil, nil, fmt.Errorf("host-endpoint daemon rejected request: %s", response.Error)
	}

	return response.Fabric, response.Management, nil
}
