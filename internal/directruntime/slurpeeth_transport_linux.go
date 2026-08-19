//go:build linux

//nolint:wsl_v5 // Protocol and process state transitions stay adjacent for auditability.
package directruntime

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/carlmontanari/slurpeeth/slurpeeth"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	"golang.org/x/sys/unix"
)

const (
	slurpeethDestinationQueueDepth = 64
	slurpeethInterfaceReadTimeout  = 100 * time.Millisecond
	slurpeethReconnectDelay        = 100 * time.Millisecond
	slurpeethDialTimeout           = 250 * time.Millisecond
	slurpeethWriteTimeout          = time.Second
	tpacketAuxdataSize             = 20
	ethernetHeaderPrefixSize       = 12
	defaultVLANTPID                = 0x8100
)

type directSlurpeethInterface struct {
	name        string
	sender      string
	file        int
	destination *unix.SockaddrLinklayer
}

type directSlurpeethDestination struct {
	key     int
	address string
	frames  chan []byte
}

type directSlurpeethSegment struct {
	id           uint16
	interfaces   []*directSlurpeethInterface
	destinations []*directSlurpeethDestination
}

type directSlurpeethDestinationState struct {
	key       int
	connected bool
}

type directSlurpeethTransport struct {
	listener     net.Listener
	segments     map[uint16]*directSlurpeethSegment
	destinations []*directSlurpeethDestination
	interfaces   []*directSlurpeethInterface
	errors       chan error
	states       chan directSlurpeethDestinationState

	context context.Context
	cancel  context.CancelFunc
	wait    sync.WaitGroup

	connectionMutex sync.Mutex
	connections     map[net.Conn]struct{}
	closeOnce       sync.Once
}

func newDirectSlurpeethTransport(
	config slurpeeth.Config,
	address string,
	port uint16,
) (*directSlurpeethTransport, error) {
	transport := &directSlurpeethTransport{
		segments:    make(map[uint16]*directSlurpeethSegment, len(config.Segments)),
		errors:      make(chan error, 1),
		states:      make(chan directSlurpeethDestinationState),
		connections: map[net.Conn]struct{}{},
	}
	interfaceNames := map[string]bool{}
	destinationKey := 0
	for _, configured := range config.Segments {
		if configured.ID == 0 || transport.segments[configured.ID] != nil {
			_ = transport.close()

			return nil, fmt.Errorf(
				"%w: segment ID is empty or duplicated",
				errDirectSlurpeeth,
			)
		}
		segment := &directSlurpeethSegment{id: configured.ID}
		for _, name := range configured.Interfaces {
			if !validLinuxInterfaceName(name) || interfaceNames[name] {
				_ = transport.close()

				return nil, fmt.Errorf(
					"%w: interface identity is invalid or duplicated",
					errDirectSlurpeeth,
				)
			}
			intf, err := openDirectSlurpeethInterface(configured.Name, name)
			if err != nil {
				_ = transport.close()

				return nil, err
			}
			interfaceNames[name] = true
			segment.interfaces = append(segment.interfaces, intf)
			transport.interfaces = append(transport.interfaces, intf)
		}
		for _, destination := range configured.Destinations {
			resolved := strings.TrimSuffix(strings.TrimPrefix(destination, "["), "]")
			if net.ParseIP(resolved) == nil {
				_ = transport.close()

				return nil, fmt.Errorf(
					"%w: destination is not a resolved IP address",
					errDirectSlurpeeth,
				)
			}
			worker := &directSlurpeethDestination{
				key: destinationKey,
				address: net.JoinHostPort(
					resolved,
					strconv.Itoa(int(port)),
				),
				frames: make(chan []byte, slurpeethDestinationQueueDepth),
			}
			destinationKey++
			segment.destinations = append(segment.destinations, worker)
			transport.destinations = append(transport.destinations, worker)
		}
		transport.segments[configured.ID] = segment
	}
	listener, err := (&net.ListenConfig{}).Listen(
		context.Background(),
		slurpeeth.TCP,
		net.JoinHostPort(address, strconv.Itoa(int(port))),
	)
	if err != nil {
		_ = transport.close()

		return nil, fmt.Errorf("binding direct slurpeeth listener: %w", err)
	}
	transport.listener = listener

	return transport, nil
}

func openDirectSlurpeethInterface(
	segmentName,
	interfaceName string,
) (*directSlurpeethInterface, error) {
	namedInterface, err := net.InterfaceByName(interfaceName)
	if err != nil {
		return nil, fmt.Errorf("resolving slurpeeth interface %q: %w", interfaceName, err)
	}
	file, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW, slurpeeth.EthPAll)
	if err != nil {
		return nil, fmt.Errorf("opening slurpeeth packet socket for %q: %w", interfaceName, err)
	}
	if err = unix.SetsockoptInt(
		file,
		unix.SOL_PACKET,
		slurpeeth.PacketAuxData,
		1,
	); err != nil {
		_ = unix.Close(file)

		return nil, fmt.Errorf("enabling packet metadata for %q: %w", interfaceName, err)
	}
	readTimeout := unix.NsecToTimeval(slurpeethInterfaceReadTimeout.Nanoseconds())
	if err = unix.SetsockoptTimeval(
		file,
		unix.SOL_SOCKET,
		unix.SO_RCVTIMEO,
		&readTimeout,
	); err != nil {
		_ = unix.Close(file)

		return nil, fmt.Errorf("setting packet read timeout for %q: %w", interfaceName, err)
	}
	destination := &unix.SockaddrLinklayer{Ifindex: namedInterface.Index}
	if err = unix.Bind(file, destination); err != nil {
		_ = unix.Close(file)

		return nil, fmt.Errorf("binding slurpeeth packet socket for %q: %w", interfaceName, err)
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(segmentName))
	_, _ = hash.Write([]byte(interfaceName))
	sender := hex.EncodeToString(hash.Sum(nil))[:10]

	return &directSlurpeethInterface{
		name: interfaceName, sender: sender, file: file, destination: destination,
	}, nil
}

func runDirectSlurpeethDaemon(
	config slurpeeth.Config,
	readyPath,
	digest string,
) error {
	transport, err := newDirectSlurpeethTransport(
		config,
		slurpeeth.Address,
		uint16(clabernetesconstants.SlurpeethServicePort),
	)
	if err != nil {
		return err
	}
	defer func() {
		_ = transport.close()
	}()
	ctx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()
	if err = os.Remove(readyPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing stale slurpeeth readiness: %w", err)
	}
	defer func() {
		_ = os.Remove(readyPath)
	}()

	return transport.run(ctx, func(ready bool) error {
		if !ready {
			if removeErr := os.Remove(readyPath); removeErr != nil &&
				!errors.Is(removeErr, os.ErrNotExist) {
				return fmt.Errorf("removing slurpeeth readiness: %w", removeErr)
			}

			return nil
		}
		if writeErr := writePrivateFileAtomically(readyPath, []byte(digest+"\n")); writeErr != nil {
			return fmt.Errorf("publishing slurpeeth readiness: %w", writeErr)
		}

		return nil
	})
}

func (t *directSlurpeethTransport) run(
	parent context.Context,
	setReady func(bool) error,
) error {
	// close invokes the retained cancellation function.
	t.context, t.cancel = context.WithCancel(parent) //nolint:gosec
	t.wait.Add(1)
	go t.acceptConnections()
	for _, segment := range t.segments {
		for _, intf := range segment.interfaces {
			t.wait.Add(1)
			go t.readInterface(segment, intf)
		}
	}
	for _, destination := range t.destinations {
		t.wait.Add(1)
		go t.writeDestination(destination)
	}
	connected := make(map[int]bool, len(t.destinations))
	ready := len(t.destinations) == 0
	if ready {
		if err := setReady(true); err != nil {
			return err
		}
	}
	for {
		select {
		case <-parent.Done():
			return nil
		case err := <-t.errors:
			return err
		case state := <-t.states:
			connected[state.key] = state.connected
			allConnected := len(connected) == len(t.destinations)
			if allConnected {
				for _, stateConnected := range connected {
					if !stateConnected {
						allConnected = false

						break
					}
				}
			}
			if allConnected == ready {
				continue
			}
			if err := setReady(allConnected); err != nil {
				return err
			}
			ready = allConnected
		}
	}
}

func (t *directSlurpeethTransport) acceptConnections() {
	defer t.wait.Done()
	for {
		connection, err := t.listener.Accept()
		if err != nil {
			if t.context.Err() == nil && !errors.Is(err, net.ErrClosed) {
				t.report(fmt.Errorf("accepting direct slurpeeth connection: %w", err))
			}

			return
		}
		t.connectionMutex.Lock()
		t.connections[connection] = struct{}{}
		t.connectionMutex.Unlock()
		t.wait.Add(1)
		go t.handleConnection(connection)
	}
}

func (t *directSlurpeethTransport) handleConnection(connection net.Conn) {
	defer t.wait.Done()
	defer func() {
		t.connectionMutex.Lock()
		delete(t.connections, connection)
		t.connectionMutex.Unlock()
		_ = connection.Close()
	}()
	for {
		message, err := readDirectSlurpeethMessage(connection)
		if err != nil {
			return
		}
		segment := t.segments[message.Header.ID]
		if segment == nil {
			continue
		}
		for _, intf := range segment.interfaces {
			if message.Header.Sender == intf.sender {
				continue
			}
			if err = unix.Sendto(intf.file, message.Body, 0, intf.destination); err != nil {
				if t.context.Err() == nil {
					t.report(fmt.Errorf(
						"writing direct slurpeeth frame to %q: %w",
						intf.name,
						err,
					))
				}

				return
			}
		}
	}
}

func readDirectSlurpeethMessage(connection net.Conn) (slurpeeth.Message, error) {
	headerBytes := make([]byte, slurpeeth.MessageHeaderSize)
	if _, err := io.ReadFull(connection, headerBytes); err != nil {
		return slurpeeth.Message{}, err
	}
	header, err := slurpeeth.NewHeaderFromRaw(headerBytes)
	if err != nil {
		return slurpeeth.Message{}, err
	}
	body := make([]byte, int(header.Size))
	if _, err = io.ReadFull(connection, body); err != nil {
		return slurpeeth.Message{}, err
	}

	return slurpeeth.Message{Header: header, Body: body}, nil
}

func (t *directSlurpeethTransport) readInterface(
	segment *directSlurpeethSegment,
	intf *directSlurpeethInterface,
) {
	defer t.wait.Done()
	for {
		frameBuffer := make([]byte, slurpeeth.ReadSize)
		auxiliaryBuffer := make([]byte, unix.CmsgSpace(slurpeeth.AuxReadSize))
		frameSize, auxiliarySize, _, _, err := unix.Recvmsg(
			intf.file,
			frameBuffer,
			auxiliaryBuffer,
			0,
		)
		if err != nil {
			if errors.Is(err, unix.EINTR) && t.context.Err() == nil {
				continue
			}
			if (errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK)) &&
				t.context.Err() == nil {
				continue
			}
			if t.context.Err() == nil {
				t.report(fmt.Errorf(
					"reading direct slurpeeth frame from %q: %w",
					intf.name,
					err,
				))
			}

			return
		}
		frame, err := restoreDirectSlurpeethVLAN(
			frameBuffer[:frameSize],
			auxiliaryBuffer[:auxiliarySize],
		)
		if err != nil {
			t.report(fmt.Errorf("restoring VLAN metadata for %q: %w", intf.name, err))

			return
		}
		if len(frame) > int(^uint16(0))-slurpeeth.MessageHeaderSize {
			t.report(fmt.Errorf(
				"%w: frame from %q exceeds the slurpeeth protocol size",
				errDirectSlurpeeth,
				intf.name,
			))

			return
		}
		message := slurpeeth.NewMessageFromBody(segment.id, intf.sender, frame)
		output := append([]byte(nil), message.Output()...)
		for _, destination := range segment.destinations {
			select {
			case destination.frames <- output:
			case <-t.context.Done():
				return
			}
		}
	}
}

func restoreDirectSlurpeethVLAN(frame, auxiliary []byte) ([]byte, error) {
	messages, err := unix.ParseSocketControlMessage(auxiliary)
	if err != nil {
		return nil, err
	}
	for _, message := range messages {
		if message.Header.Level != unix.SOL_PACKET ||
			message.Header.Type != slurpeeth.PacketAuxData {
			continue
		}
		if len(message.Data) < tpacketAuxdataSize {
			return nil, fmt.Errorf("%w: packet auxiliary data is truncated", errDirectSlurpeeth)
		}
		status := binary.NativeEndian.Uint32(message.Data[0:4])
		vlanTCI := binary.NativeEndian.Uint16(message.Data[16:18])
		if vlanTCI == 0 && status&unix.TP_STATUS_VLAN_VALID == 0 {
			continue
		}
		if len(frame) < ethernetHeaderPrefixSize {
			return nil, fmt.Errorf(
				"%w: VLAN frame is shorter than its Ethernet header",
				errDirectSlurpeeth,
			)
		}
		vlanTPID := binary.NativeEndian.Uint16(message.Data[18:20])
		if vlanTPID == 0 {
			vlanTPID = defaultVLANTPID
		}
		tag := make([]byte, slurpeeth.VlanTagSize)
		binary.BigEndian.PutUint16(tag[0:2], vlanTPID)
		binary.BigEndian.PutUint16(tag[2:4], vlanTCI)
		restored := make([]byte, 0, len(frame)+len(tag))
		restored = append(restored, frame[:ethernetHeaderPrefixSize]...)
		restored = append(restored, tag...)
		restored = append(restored, frame[ethernetHeaderPrefixSize:]...)

		return restored, nil
	}

	return frame, nil
}

func (t *directSlurpeethTransport) writeDestination(
	destination *directSlurpeethDestination,
) {
	defer t.wait.Done()
	dialer := net.Dialer{Timeout: slurpeethDialTimeout}
	var connection net.Conn
	var pending []byte
	connected := false
	defer func() {
		if connection != nil {
			_ = connection.Close()
		}
		if connected {
			t.sendDestinationState(destination.key, false)
		}
	}()
	for {
		if connection == nil {
			var err error
			connection, err = dialer.DialContext(t.context, slurpeeth.TCP, destination.address)
			if err != nil {
				if !waitForDirectSlurpeethRetry(t.context) {
					return
				}

				continue
			}
			connected = true
			t.sendDestinationState(destination.key, true)
		}
		if pending == nil {
			select {
			case pending = <-destination.frames:
			case <-t.context.Done():
				return
			}
		}
		_ = connection.SetWriteDeadline(time.Now().Add(slurpeethWriteTimeout))
		if err := writeDirectSlurpeethFrame(connection, pending); err != nil {
			_ = connection.Close()
			connection = nil
			if connected {
				connected = false
				t.sendDestinationState(destination.key, false)
			}
			if !waitForDirectSlurpeethRetry(t.context) {
				return
			}

			continue
		}
		pending = nil
	}
}

func writeDirectSlurpeethFrame(connection net.Conn, frame []byte) error {
	for len(frame) != 0 {
		written, err := connection.Write(frame)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrUnexpectedEOF
		}
		frame = frame[written:]
	}

	return nil
}

func waitForDirectSlurpeethRetry(ctx context.Context) bool {
	timer := time.NewTimer(slurpeethReconnectDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (t *directSlurpeethTransport) sendDestinationState(key int, connected bool) {
	select {
	case t.states <- directSlurpeethDestinationState{key: key, connected: connected}:
	case <-t.context.Done():
	}
}

func (t *directSlurpeethTransport) report(err error) {
	select {
	case t.errors <- err:
	default:
	}
}

func (t *directSlurpeethTransport) close() error {
	var closeErr error
	t.closeOnce.Do(func() {
		if t.cancel != nil {
			t.cancel()
		}
		if t.listener != nil {
			closeErr = errors.Join(closeErr, t.listener.Close())
		}
		t.connectionMutex.Lock()
		for connection := range t.connections {
			closeErr = errors.Join(closeErr, connection.Close())
		}
		t.connectionMutex.Unlock()
		// The packet-socket file descriptors close only after every reader and writer has
		// exited (their receive timeout observes the cancelled context); closing first would
		// let the kernel reuse an fd number while a goroutine is still using it.
		t.wait.Wait()
		for _, intf := range t.interfaces {
			closeErr = errors.Join(closeErr, unix.Close(intf.file))
		}
	})

	return closeErr
}
