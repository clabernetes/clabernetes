//nolint:mnd // pcap and pcapng are fixed binary layouts made of protocol literals.
package directruntime

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
)

// pcap and pcapng block layout constants. pcapng is specified in
// https://www.ietf.org/archive/id/draft-ietf-opsawg-pcapng-03.html; both writers emit
// little-endian streams, which readers detect from the magic numbers.
const (
	pcapMagicMicroseconds   = 0xa1b2c3d4
	pcapngSectionHeaderType = 0x0a0d0d0a
	pcapngByteOrderMagic    = 0x1a2b3c4d
	pcapngInterfaceType     = 0x00000001
	pcapngEnhancedType      = 0x00000006
	pcapngOptionEnd         = 0
	pcapngOptionUserAppl    = 4
	pcapngOptionIfName      = 2
	pcapngOptionIfTSResol   = 9
	pcapngNanosecondResol   = 9
	pcapngUserApplication   = "clabernetes"
)

var (
	errPacketTimestampRange = errors.New("packet timestamp is outside the capture format range")
	errPCAPInterfaceCount   = errors.New("pcap carries exactly one interface")
)

// pcapStream writes the classic libpcap format: one global header and one record per packet.
type pcapStream struct {
	output io.Writer
}

func (s *pcapStream) WriteHeader(interfaces []string, snapLength int) error {
	if len(interfaces) != 1 {
		return errPCAPInterfaceCount
	}

	header := make([]byte, 24)
	binary.LittleEndian.PutUint32(header[0:], pcapMagicMicroseconds)
	binary.LittleEndian.PutUint16(header[4:], 2)
	binary.LittleEndian.PutUint16(header[6:], 4)
	snap := uint32(snapLength) //nolint:gosec // validated to at most 1 MiB.
	binary.LittleEndian.PutUint32(header[16:], snap)
	binary.LittleEndian.PutUint32(header[20:], packetCaptureLinkTypeEthernet)

	_, err := s.output.Write(header)

	return err
}

func (s *pcapStream) WritePacket(_ int, packet capturedPacket) error {
	seconds := packet.Timestamp.Unix()
	if seconds < 0 || seconds > math.MaxUint32 {
		return errPacketTimestampRange
	}

	record := make([]byte, 16, 16+len(packet.Data))
	binary.LittleEndian.PutUint32(record[0:], uint32(seconds))
	micros := uint32(packet.Timestamp.Nanosecond() / 1_000) //nolint:gosec // below one million.
	captured, original := packetLengths(packet)
	binary.LittleEndian.PutUint32(record[4:], micros)
	binary.LittleEndian.PutUint32(record[8:], captured)
	binary.LittleEndian.PutUint32(record[12:], original)
	record = append(record, packet.Data...)

	_, err := s.output.Write(record)

	return err
}

// pcapngStream writes one Section Header Block, one Interface Description Block per interface,
// and one Enhanced Packet Block per packet. Each block reaches the output in one Write.
type pcapngStream struct {
	output io.Writer
}

func (s *pcapngStream) WriteHeader(interfaces []string, snapLength int) error {
	section := make([]byte, 16)
	binary.LittleEndian.PutUint32(section[0:], pcapngByteOrderMagic)
	binary.LittleEndian.PutUint16(section[4:], 1)
	binary.LittleEndian.PutUint16(section[6:], 0)
	// The section length is unknown while streaming.
	binary.LittleEndian.PutUint64(section[8:], math.MaxUint64)
	section = appendPCAPNGOption(section, pcapngOptionUserAppl, []byte(pcapngUserApplication))
	section = appendPCAPNGOption(section, pcapngOptionEnd, nil)

	header := pcapngBlock(pcapngSectionHeaderType, section)

	snap := uint32(snapLength) //nolint:gosec // validated to at most 1 MiB.

	for _, name := range interfaces {
		description := make([]byte, 8)
		binary.LittleEndian.PutUint16(description[0:], packetCaptureLinkTypeEthernet)
		binary.LittleEndian.PutUint32(description[4:], snap)
		description = appendPCAPNGOption(description, pcapngOptionIfName, []byte(name))
		description = appendPCAPNGOption(
			description,
			pcapngOptionIfTSResol,
			[]byte{pcapngNanosecondResol},
		)
		description = appendPCAPNGOption(description, pcapngOptionEnd, nil)
		header = append(header, pcapngBlock(pcapngInterfaceType, description)...)
	}

	_, err := s.output.Write(header)

	return err
}

func (s *pcapngStream) WritePacket(interfaceIndex int, packet capturedPacket) error {
	nanoseconds := packet.Timestamp.UnixNano()
	if nanoseconds < 0 {
		return errPacketTimestampRange
	}

	body := make([]byte, 20, 20+len(packet.Data)+3)
	index := uint32(interfaceIndex) //nolint:gosec // bounded by the requested interfaces.
	timestamp := uint64(nanoseconds)
	captured, original := packetLengths(packet)
	binary.LittleEndian.PutUint32(body[0:], index)
	binary.LittleEndian.PutUint32(body[4:], uint32(timestamp>>32))
	binary.LittleEndian.PutUint32(body[8:], uint32(timestamp&math.MaxUint32))
	binary.LittleEndian.PutUint32(body[12:], captured)
	binary.LittleEndian.PutUint32(body[16:], original)
	body = append(body, packet.Data...)
	body = append(body, make([]byte, pcapngPadding(len(packet.Data)))...)

	_, err := s.output.Write(pcapngBlock(pcapngEnhancedType, body))

	return err
}

// pcapngBlock frames a 32-bit aligned block body with its type and both total-length fields.
func pcapngBlock(blockType uint32, body []byte) []byte {
	total := uint32(12 + len(body)) //nolint:gosec // block bodies are bounded by the snap length.
	block := make([]byte, 8, total)
	binary.LittleEndian.PutUint32(block[0:], blockType)
	binary.LittleEndian.PutUint32(block[4:], total)
	block = append(block, body...)

	return binary.LittleEndian.AppendUint32(block, total)
}

func appendPCAPNGOption(body []byte, code uint16, value []byte) []byte {
	body = binary.LittleEndian.AppendUint16(body, code)
	length := uint16(len(value)) //nolint:gosec // option values are short names.
	body = binary.LittleEndian.AppendUint16(body, length)
	body = append(body, value...)

	return append(body, make([]byte, pcapngPadding(len(value)))...)
}

// packetLengths returns the captured and original lengths of a packet as 32-bit fields.
// The capture loop bounds the data by the snap length and rejects original lengths
// that do not fit 32 bits.
func packetLengths(packet capturedPacket) (captured, original uint32) {
	captured = uint32(len(packet.Data))      //nolint:gosec // bounded by the snap length.
	original = uint32(packet.OriginalLength) //nolint:gosec // validated to fit 32 bits.

	return captured, original
}

func pcapngPadding(length int) int {
	return (4 - length%4) % 4
}
