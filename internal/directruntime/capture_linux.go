//go:build linux

package directruntime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"golang.org/x/sys/unix"
)

type linuxPacketCaptureSource struct {
	fd     int
	buffer []byte
}

func openPacketCaptureSource(interfaceName string, snapLength int) (packetCaptureSource, error) {
	intf, err := net.InterfaceByName(interfaceName)
	if err != nil {
		return nil, fmt.Errorf("resolving interface: %w", err)
	}

	protocol := hostToNetworkShort(uint16(unix.ETH_P_ALL))

	fd, err := unix.Socket(
		unix.AF_PACKET,
		unix.SOCK_RAW|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK,
		int(protocol),
	)
	if err != nil {
		return nil, fmt.Errorf("opening packet socket: %w", err)
	}

	if err = unix.Bind(fd, &unix.SockaddrLinklayer{
		Protocol: protocol,
		Ifindex:  intf.Index,
	}); err != nil {
		_ = unix.Close(fd)

		return nil, fmt.Errorf("binding packet socket: %w", err)
	}

	return &linuxPacketCaptureSource{fd: fd, buffer: make([]byte, snapLength)}, nil
}

func (s *linuxPacketCaptureSource) ReadPacket(ctx context.Context) (capturedPacket, error) {
	for {
		if err := ctx.Err(); err != nil {
			return capturedPacket{}, err
		}

		n, _, err := unix.Recvfrom(s.fd, s.buffer, unix.MSG_DONTWAIT|unix.MSG_TRUNC)
		if err == nil {
			capturedLength := min(n, len(s.buffer))

			return capturedPacket{
				Timestamp: time.Now(), Data: append([]byte(nil), s.buffer[:capturedLength]...),
				OriginalLength: n,
			}, nil
		}

		if !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EWOULDBLOCK) {
			return capturedPacket{}, err
		}

		poll := []unix.PollFd{
			//nolint:gosec // the value is bounded by validated plan input or a kernel interface width.
			{Fd: int32(s.fd), Events: unix.POLLIN},
		}
		if _, err = unix.Poll(poll, int(packetCapturePollInterval.Milliseconds())); err != nil &&
			!errors.Is(err, unix.EINTR) {
			return capturedPacket{}, err
		}
	}
}

func (s *linuxPacketCaptureSource) Close() error {
	return unix.Close(s.fd)
}

func hostToNetworkShort(value uint16) uint16 {
	return value<<8 | value>>8
}
