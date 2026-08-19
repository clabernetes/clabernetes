//go:build linux

package directruntime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const (
	transportNamespaceMessage      = "c9s-transport-namespace-v1\n"
	transportNamespaceMaximumFiles = 256
	transportNamespaceTimeout      = 10 * time.Second
)

type linuxTransportNamespaceBroker struct {
	socketPath string
	podAddress string
	listener   *net.UnixListener
	updates    chan *os.File
	errors     chan error
	closeOnce  sync.Once
	closeErr   error
}

func startApplicationTransportNamespaceBroker(
	ctx context.Context,
	socketPath,
	podAddress string,
) (transportNamespaceBroker, error) {
	if ctx == nil {
		return nil, fmt.Errorf("transport namespace broker context is nil")
	}
	if err := validateTransportNamespaceIdentity(socketPath, podAddress); err != nil {
		return nil, err
	}
	cleanPath := filepath.Clean(socketPath)
	if err := os.MkdirAll(filepath.Dir(cleanPath), 0o755); err != nil {
		return nil, fmt.Errorf("creating transport namespace broker directory: %w", err)
	}
	if info, err := os.Lstat(cleanPath); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("transport namespace broker path is occupied by a non-socket")
		}
		if err = os.Remove(cleanPath); err != nil {
			return nil, fmt.Errorf("removing stale transport namespace broker socket: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspecting transport namespace broker socket: %w", err)
	}
	listener, err := net.ListenUnix(
		"unixpacket",
		&net.UnixAddr{Name: cleanPath, Net: "unixpacket"},
	)
	if err != nil {
		return nil, fmt.Errorf("listening for transport namespace descriptors: %w", err)
	}
	//nolint:gosec // Arbitrary application UIDs may publish only an independently verified fd.
	if err = os.Chmod(cleanPath, 0o666); err != nil {
		_ = listener.Close()
		_ = os.Remove(cleanPath)

		return nil, fmt.Errorf("setting transport namespace broker permissions: %w", err)
	}
	broker := &linuxTransportNamespaceBroker{
		socketPath: cleanPath,
		podAddress: podAddress,
		listener:   listener,
		updates:    make(chan *os.File, 1),
		errors:     make(chan error, 1),
	}
	go broker.serve(ctx)
	go func() {
		<-ctx.Done()
		_ = broker.Close()
	}()

	return broker, nil
}

func validateTransportNamespaceIdentity(socketPath, podAddress string) error {
	cleanPath := filepath.Clean(socketPath)
	address := net.ParseIP(strings.TrimSpace(podAddress))
	if !filepath.IsAbs(cleanPath) || cleanPath == string(filepath.Separator) ||
		filepath.Dir(cleanPath) == string(filepath.Separator) || address == nil ||
		address.IsUnspecified() || address.IsLoopback() || address.IsMulticast() {
		return fmt.Errorf("transport namespace identity is invalid")
	}

	return nil
}

func (b *linuxTransportNamespaceBroker) serve(ctx context.Context) {
	defer close(b.errors)
	for {
		connection, err := b.listener.AcceptUnix()
		if err != nil {
			if ctx.Err() == nil && !errors.Is(err, net.ErrClosed) {
				b.errors <- fmt.Errorf("accepting transport namespace descriptor: %w", err)
			}

			return
		}
		if err = b.acceptDescriptor(connection); err != nil {
			_, _ = connection.Write([]byte("rejected\n"))
		}
		_ = connection.Close()
	}
}

func (b *linuxTransportNamespaceBroker) acceptDescriptor(connection *net.UnixConn) error {
	_ = connection.SetDeadline(time.Now().Add(transportNamespaceTimeout))
	payload := make([]byte, len(transportNamespaceMessage)+1)
	control := make([]byte, unix.CmsgSpace(4))
	payloadBytes, controlBytes, _, _, err := connection.ReadMsgUnix(payload, control)
	if err != nil {
		return err
	}
	if string(payload[:payloadBytes]) != transportNamespaceMessage {
		return fmt.Errorf("transport namespace message is invalid")
	}
	descriptors, err := transportNamespaceDescriptors(control[:controlBytes])
	if err != nil {
		return err
	}
	for _, descriptor := range descriptors[1:] {
		_ = unix.Close(descriptor)
	}
	if len(descriptors) != 1 {
		if len(descriptors) != 0 {
			_ = unix.Close(descriptors[0])
		}

		return fmt.Errorf("transport namespace message must contain one descriptor")
	}
	file := os.NewFile(uintptr(descriptors[0]), "application-transport-network-namespace")
	if file == nil {
		_ = unix.Close(descriptors[0])

		return fmt.Errorf("transport namespace descriptor is unavailable")
	}
	accepted, err := networkNamespaceContainsAddress(file, b.podAddress)
	if err != nil || !accepted {
		_ = file.Close()
		if err != nil {
			return err
		}

		return fmt.Errorf("transport namespace does not contain the running Pod address")
	}
	select {
	case previous := <-b.updates:
		_ = previous.Close()
	default:
	}
	b.updates <- file
	_, err = connection.Write([]byte("accepted\n"))

	return err
}

func transportNamespaceDescriptors(control []byte) ([]int, error) {
	messages, err := unix.ParseSocketControlMessage(control)
	if err != nil {
		return nil, err
	}
	descriptors := []int{}
	for _, message := range messages {
		rights, rightsErr := unix.ParseUnixRights(&message)
		if rightsErr != nil {
			for _, descriptor := range descriptors {
				_ = unix.Close(descriptor)
			}

			return nil, rightsErr
		}
		descriptors = append(descriptors, rights...)
	}

	return descriptors, nil
}

func (b *linuxTransportNamespaceBroker) Updates() <-chan *os.File { return b.updates }

func (b *linuxTransportNamespaceBroker) Errors() <-chan error { return b.errors }

func (b *linuxTransportNamespaceBroker) Close() error {
	b.closeOnce.Do(func() {
		b.closeErr = b.listener.Close()
		if err := os.Remove(b.socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			b.closeErr = errors.Join(b.closeErr, err)
		}
		select {
		case pending := <-b.updates:
			b.closeErr = errors.Join(b.closeErr, pending.Close())
		default:
		}
	})

	return b.closeErr
}

func publishApplicationTransportNamespace(
	ctx context.Context,
	socketPath,
	podAddress,
	namespaceRoot string,
) error {
	if ctx == nil {
		return fmt.Errorf("transport namespace publisher context is nil")
	}
	if err := validateTransportNamespaceIdentity(socketPath, podAddress); err != nil {
		return err
	}
	namespace, err := findApplicationTransportNamespace(podAddress, namespaceRoot)
	if err != nil {
		return err
	}
	defer namespace.Close()
	connection, err := net.DialUnix(
		"unixpacket",
		nil,
		&net.UnixAddr{Name: filepath.Clean(socketPath), Net: "unixpacket"},
	)
	if err != nil {
		return fmt.Errorf("connecting to transport namespace broker: %w", err)
	}
	defer connection.Close()
	deadline := time.Now().Add(transportNamespaceTimeout)
	if contextDeadline, exists := ctx.Deadline(); exists && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	_ = connection.SetDeadline(deadline)
	if _, _, err = connection.WriteMsgUnix(
		[]byte(transportNamespaceMessage),
		unix.UnixRights(int(namespace.Fd())),
		nil,
	); err != nil {
		return fmt.Errorf("publishing transport namespace descriptor: %w", err)
	}
	response := make([]byte, 32)
	read, err := connection.Read(response)
	if err != nil {
		return fmt.Errorf("reading transport namespace acknowledgement: %w", err)
	}
	if string(response[:read]) != "accepted\n" {
		return fmt.Errorf("transport namespace descriptor was rejected")
	}

	return nil
}

func findApplicationTransportNamespace(podAddress, namespaceRoot string) (*os.File, error) {
	candidates := []*os.File{}
	current, err := os.Open("/proc/self/ns/net")
	if err != nil {
		return nil, fmt.Errorf("opening application network namespace: %w", err)
	}
	candidates = append(candidates, current)
	entries, readErr := os.ReadDir(namespaceRoot)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		_ = current.Close()

		return nil, fmt.Errorf("listing application network namespaces: %w", readErr)
	}
	if len(entries) > transportNamespaceMaximumFiles {
		_ = current.Close()

		return nil, fmt.Errorf("application network namespace inventory exceeds its bound")
	}
	slices.SortFunc(entries, func(left, right os.DirEntry) int {
		return strings.Compare(left.Name(), right.Name())
	})
	for _, entry := range entries {
		if entry.IsDir() || strings.Contains(entry.Name(), string(filepath.Separator)) {
			continue
		}
		descriptor, openErr := unix.Open(
			filepath.Join(namespaceRoot, entry.Name()),
			unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW,
			0,
		)
		if openErr != nil {
			continue
		}
		candidates = append(candidates, os.NewFile(uintptr(descriptor), entry.Name()))
	}
	defer func() {
		for _, candidate := range candidates {
			if candidate != nil {
				_ = candidate.Close()
			}
		}
	}()
	matches := []*os.File{}
	matchInfo := []os.FileInfo{}
	for _, candidate := range candidates {
		contains, containsErr := networkNamespaceContainsAddress(candidate, podAddress)
		if containsErr != nil || !contains {
			continue
		}
		info, statErr := candidate.Stat()
		if statErr != nil {
			continue
		}
		duplicate := false
		for _, existing := range matchInfo {
			if os.SameFile(info, existing) {
				duplicate = true

				break
			}
		}
		if !duplicate {
			matches = append(matches, candidate)
			matchInfo = append(matchInfo, info)
		}
	}
	if len(matches) != 1 {
		return nil, fmt.Errorf(
			"Pod transport address belongs to %d application network namespaces, want exactly one",
			len(matches),
		)
	}
	duplicate, err := unix.Dup(int(matches[0].Fd()))
	if err != nil {
		return nil, fmt.Errorf("retaining application transport namespace: %w", err)
	}

	return os.NewFile(uintptr(duplicate), "application-transport-network-namespace"), nil
}

func networkNamespaceContainsAddress(namespace *os.File, address string) (bool, error) {
	if namespace == nil {
		return false, fmt.Errorf("network namespace descriptor is nil")
	}
	target := net.ParseIP(strings.TrimSpace(address))
	if target == nil {
		return false, fmt.Errorf("network namespace address is invalid")
	}
	restore, err := os.Open("/proc/self/ns/net")
	if err != nil {
		return false, err
	}
	defer restore.Close()
	found := false
	err = executeEndpointNamespace(
		int(restore.Fd()),
		int(namespace.Fd()),
		func() error {
			interfaces, listErr := net.Interfaces()
			if listErr != nil {
				return listErr
			}
			for _, intf := range interfaces {
				addresses, addressErr := intf.Addrs()
				if addressErr != nil {
					return addressErr
				}
				for _, candidate := range addresses {
					raw, _, _ := strings.Cut(candidate.String(), "/")
					if parsed := net.ParseIP(raw); parsed != nil && parsed.Equal(target) {
						found = true

						return nil
					}
				}
			}

			return nil
		},
		unix.Setns,
		runtime.LockOSThread,
		runtime.UnlockOSThread,
	)

	return found, err
}
