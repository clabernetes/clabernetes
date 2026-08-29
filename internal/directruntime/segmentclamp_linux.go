//go:build linux

package directruntime

import (
	"errors"
	"fmt"

	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"
)

// meshSegmentClampTableName is the sidecar-owned bridge-family nftables table holding the
// management mesh segment clamp. nftables tables are scoped to the Pod network namespace, so the
// name cannot collide across Pods.
const meshSegmentClampTableName = "c9s-mesh-clamp"

const (
	// tcpOptionMaxSegment is TCPOPT_MAXSEG, the option carrying the largest segment a peer is
	// willing to receive.
	tcpOptionMaxSegment = 2
	// tcpOptionMaxSegmentValueOffset and tcpOptionMaxSegmentValueLength locate the size inside
	// the option (kind and length precede it).
	tcpOptionMaxSegmentValueOffset = 2
	tcpOptionMaxSegmentValueLength = 2

	ipv4HeaderBytes = 20
	ipv6HeaderBytes = 40
	tcpHeaderBytes  = 20
	tcpFlagsOffset  = 13
	tcpFlagSYN      = 0x02
	etherTypeLength = 2
)

// ensureMeshSegmentClamp reconciles the segment clamp on the management mesh: every TCP handshake
// crossing the mesh bridge advertises at most the segment the mesh MTU can carry.
//
// Devices whose management port size is fixed by the application (SR OS presents a 1514 byte port
// that cannot be lowered) otherwise derive their segment size from their own interface and emit
// frames the mesh cannot carry. Nothing on the path answers with fragmentation-needed -- the drop
// happens on a veth, not a router -- so the flow becomes a black hole: the connection completes
// and then stalls on the first full-size segment, which is exactly what a TLS certificate or a
// NETCONF hello is. Clamping the advertised size makes both peers send segments that fit.
//
// The clamp only ever lowers an advertised size, and it is scoped to the mesh bridge so lab data
// plane traffic keeps whatever segment size the topology asked for.
func ensureMeshSegmentClamp(meshMTU int) error {
	if meshMTU <= 0 {
		return nil
	}

	conn := &nftables.Conn{}

	table := conn.AddTable(&nftables.Table{
		Family: nftables.TableFamilyBridge,
		Name:   meshSegmentClampTableName,
	})
	conn.FlushTable(table)

	chain := conn.AddChain(&nftables.Chain{
		Name:     "forward",
		Table:    table,
		Type:     nftables.ChainTypeFilter,
		Hooknum:  nftables.ChainHookForward,
		Priority: nftables.ChainPriorityFilter,
	})

	for _, ingressName := range []string{
		MeshDevicePortName,
		MeshGatewayPortName,
		MeshVTEPName,
	} {
		for _, family := range []struct {
			etherType   uint16
			headerBytes int
		}{
			{etherType: unix.ETH_P_IP, headerBytes: ipv4HeaderBytes},
			{etherType: unix.ETH_P_IPV6, headerBytes: ipv6HeaderBytes},
		} {
			maxSegment := meshMTU - family.headerBytes - tcpHeaderBytes
			if maxSegment <= 0 {
				continue
			}

			conn.AddRule(meshSegmentClampRule(
				table,
				chain,
				ingressName,
				family.etherType,
				uint16(maxSegment), //nolint:gosec // Bounded by the mesh MTU, which is a DNS-sized int.
			))
		}
	}

	if err := conn.Flush(); err != nil {
		// MSS clamping is an optimization for kernels that expose bridge-family nftables.
		// Older kernels may reject that optional family or expression with ENOENT or
		// EOPNOTSUPP; management connectivity must still use the realized mesh.
		if isUnsupportedMeshSegmentClampError(err) {
			return nil
		}

		return fmt.Errorf("programming management mesh segment clamp: %w", err)
	}

	return nil
}

func isUnsupportedMeshSegmentClampError(err error) bool {
	return errors.Is(err, unix.ENOENT) || errors.Is(err, unix.EOPNOTSUPP)
}

// meshSegmentClampRule builds the clamp for one address family: a TCP SYN crossing the mesh
// bridge whose advertised maximum segment exceeds what the mesh carries has that size replaced.
func meshSegmentClampRule(
	table *nftables.Table,
	chain *nftables.Chain,
	ingressName string,
	etherType uint16,
	maxSegment uint16,
) *nftables.Rule {
	size := binaryutil.BigEndian.PutUint16(maxSegment)

	return &nftables.Rule{
		Table: table,
		Chain: chain,
		Exprs: []expr.Any{
			// Match the fixed mesh ingress ports instead of the bridge-specific ibrname key:
			// Talos enables NF_TABLES_BRIDGE without NFT_BRIDGE_META.
			&expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
			&expr.Cmp{
				Op:       expr.CmpOpEq,
				Register: 1,
				Data:     interfaceName(ingressName),
			},
			&expr.Meta{Key: expr.MetaKeyPROTOCOL, Register: 1},
			&expr.Cmp{
				Op:       expr.CmpOpEq,
				Register: 1,
				Data:     binaryutil.BigEndian.PutUint16(etherType)[:etherTypeLength],
			},
			// The transport protocol comes from the packet parse rather than a header offset:
			// the bridge family carries the link header, so the network-header offsets differ
			// per address family and per IPv6 extension chain.
			&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{unix.IPPROTO_TCP}},
			&expr.Payload{
				DestRegister: 1,
				Base:         expr.PayloadBaseTransportHeader,
				Offset:       tcpFlagsOffset,
				Len:          1,
			},
			&expr.Bitwise{
				SourceRegister: 1,
				DestRegister:   1,
				Len:            1,
				Mask:           []byte{tcpFlagSYN},
				Xor:            []byte{0x00},
			},
			&expr.Cmp{Op: expr.CmpOpNeq, Register: 1, Data: []byte{0x00}},
			// A SYN without the option carries no size to clamp and stops matching here.
			&expr.Exthdr{
				DestRegister: 1,
				Type:         tcpOptionMaxSegment,
				Offset:       tcpOptionMaxSegmentValueOffset,
				Len:          tcpOptionMaxSegmentValueLength,
				Op:           expr.ExthdrOpTcpopt,
			},
			// Never raise a peer that already asked for less than the mesh carries.
			&expr.Cmp{Op: expr.CmpOpGt, Register: 1, Data: size},
			&expr.Immediate{Register: 1, Data: size},
			&expr.Exthdr{
				SourceRegister: 1,
				Type:           tcpOptionMaxSegment,
				Offset:         tcpOptionMaxSegmentValueOffset,
				Len:            tcpOptionMaxSegmentValueLength,
				Op:             expr.ExthdrOpTcpopt,
			},
		},
	}
}
