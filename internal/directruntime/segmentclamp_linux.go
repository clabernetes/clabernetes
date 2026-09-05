//go:build linux

package directruntime

import (
	"errors"
	"fmt"
	"math"

	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"
)

// meshSegmentClampTableName is the sidecar-owned inet-family nftables table holding the
// sidecar conntrack zone assignment and the management mesh segment clamp. nftables tables are
// scoped to the Pod network namespace, so the name cannot collide across Pods.
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
)

// sidecarConntrackZone is the connection-tracking zone of the sidecar's own legs. A packet the
// sidecar routes between its legs and the device leg crosses netfilter twice in this namespace:
// once on the sidecar's leg and once more on the device leg. With one zone the first crossing
// confirms the connection, and the device's own translation on its leg (vrnetlab forwards
// management ports to its virtual machine with a DNAT bound to that leg) can no longer bind: the
// packet reaches the pod kernel untranslated. The bridged shape never had this problem because
// bridged frames skipped netfilter until the device leg. Giving the sidecar's legs and locally
// originated traffic their own zone restores that: the device leg sees every connection fresh,
// and the sidecar's translations and their replies stay consistent within its zone.
const sidecarConntrackZone = 1

// ensureMeshFilterTable reconciles the sidecar-owned inet table: the conntrack zone of the
// sidecar's legs, and the management segment clamp.
//
// The clamp makes every TCP handshake forwarded between the router leg and the mesh tunnel
// endpoint advertise at most the segment the mesh MTU can carry. Devices whose management port
// size is fixed by the application (SR OS presents a 1514 byte port that cannot be lowered)
// otherwise derive their segment size from their own interface and emit segments the mesh
// cannot carry. The routed path does answer an oversized DF packet with fragmentation-needed,
// but a stack that ignores it, or a flow whose first full-size segment is exactly what a TLS
// certificate or a NETCONF hello is, still stalls; clamping the advertised size makes both
// peers send segments that fit from the first byte. The clamp only ever lowers an advertised
// size, and it is scoped to the two mesh ingress interfaces so lab data plane traffic keeps
// whatever segment size the topology asked for.
func ensureMeshFilterTable(transportName, routerName string, meshMTU int) error {
	conn := &nftables.Conn{}

	table := conn.AddTable(&nftables.Table{
		Family: nftables.TableFamilyINet,
		Name:   meshSegmentClampTableName,
	})
	conn.FlushTable(table)

	rawPriority := nftables.ChainPriorityRaw

	zonePrerouting := conn.AddChain(&nftables.Chain{
		Name:     "zone-prerouting",
		Table:    table,
		Type:     nftables.ChainTypeFilter,
		Hooknum:  nftables.ChainHookPrerouting,
		Priority: rawPriority,
	})

	for _, ingressName := range []string{transportName, MeshVTEPName, routerName} {
		conn.AddRule(&nftables.Rule{
			Table: table,
			Chain: zonePrerouting,
			Exprs: append([]expr.Any{
				&expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
				&expr.Cmp{
					Op:       expr.CmpOpEq,
					Register: 1,
					Data:     interfaceName(ingressName),
				},
			}, conntrackZoneExpressions()...),
		})
	}

	zoneOutput := conn.AddChain(&nftables.Chain{
		Name:     "zone-output",
		Table:    table,
		Type:     nftables.ChainTypeFilter,
		Hooknum:  nftables.ChainHookOutput,
		Priority: rawPriority,
	})

	conn.AddRule(&nftables.Rule{
		Table: table,
		Chain: zoneOutput,
		Exprs: conntrackZoneExpressions(),
	})

	if meshMTU > 0 {
		chain := conn.AddChain(&nftables.Chain{
			Name:     "forward",
			Table:    table,
			Type:     nftables.ChainTypeFilter,
			Hooknum:  nftables.ChainHookForward,
			Priority: nftables.ChainPriorityFilter,
		})

		for _, ingressName := range []string{routerName, MeshVTEPName} {
			for _, family := range []struct {
				protocol    byte
				headerBytes int
			}{
				{protocol: unix.NFPROTO_IPV4, headerBytes: ipv4HeaderBytes},
				{protocol: unix.NFPROTO_IPV6, headerBytes: ipv6HeaderBytes},
			} {
				maxSegment := meshMTU - family.headerBytes - tcpHeaderBytes
				if maxSegment <= 0 {
					continue
				}

				if maxSegment > math.MaxUint16 {
					maxSegment = math.MaxUint16
				}

				maxSegmentValue := uint16(maxSegment)
				conn.AddRule(meshSegmentClampRule(
					table,
					chain,
					ingressName,
					family.protocol,
					maxSegmentValue,
				))
			}
		}
	}

	if err := conn.Flush(); err != nil {
		// The zone and the clamp are optimizations for kernels that expose the needed nftables
		// expressions. Older kernels may reject an optional expression with ENOENT or
		// EOPNOTSUPP; management connectivity must still use the realized mesh.
		if isUnsupportedMeshSegmentClampError(err) {
			return nil
		}

		return fmt.Errorf("programming management mesh filter table: %w", err)
	}

	return nil
}

// conntrackZoneExpressions assigns the sidecar conntrack zone to the packet.
func conntrackZoneExpressions() []expr.Any {
	return []expr.Any{
		&expr.Immediate{
			Register: 1,
			Data:     binaryutil.NativeEndian.PutUint16(sidecarConntrackZone),
		},
		&expr.Ct{Key: expr.CtKeyZONE, Register: 1, SourceRegister: true},
	}
}

func isUnsupportedMeshSegmentClampError(err error) bool {
	return errors.Is(err, unix.ENOENT) || errors.Is(err, unix.EOPNOTSUPP)
}

// meshSegmentClampRule builds the clamp for one ingress interface and address family: a TCP SYN
// forwarded from that interface whose advertised maximum segment exceeds what the mesh carries
// has that size replaced.
func meshSegmentClampRule(
	table *nftables.Table,
	chain *nftables.Chain,
	ingressName string,
	protocol byte,
	maxSegment uint16,
) *nftables.Rule {
	size := binaryutil.BigEndian.PutUint16(maxSegment)

	return &nftables.Rule{
		Table: table,
		Chain: chain,
		Exprs: []expr.Any{
			&expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
			&expr.Cmp{
				Op:       expr.CmpOpEq,
				Register: 1,
				Data:     interfaceName(ingressName),
			},
			&expr.Meta{Key: expr.MetaKeyNFPROTO, Register: 1},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{protocol}},
			// The transport protocol comes from the packet parse rather than a header offset:
			// the inet family carries both address families, whose network-header layouts
			// differ, and IPv6 may carry an extension chain ahead of TCP.
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
