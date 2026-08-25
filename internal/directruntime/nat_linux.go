//go:build linux

//nolint:gocyclo,mnd // single-pass boundary validation with protocol literals (header offsets, hook priorities).
package directruntime

import (
	"errors"
	"fmt"
	"net/netip"

	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"
)

// interpositionTableName is the sidecar-owned nftables table. nftables tables are scoped to the
// Pod network namespace, so the name cannot collide across Pods.
const interpositionTableName = "c9s-interposition"

const (
	// interpositionDestinationPriority hooks destination translation ahead of every x_tables NAT
	// PREROUTING chain (fixed at -100), so a device programming its own iptables NAT cannot
	// shadow the sidecar's declared-port translation.
	interpositionDestinationPriority = -110
	// interpositionSourcePriority hooks source translation ahead of every x_tables NAT
	// POSTROUTING chain (fixed at 100) for the same precedence guarantee on egress.
	interpositionSourcePriority = 90
)

const (
	ipv4SourceOffset      = 12
	ipv4DestinationOffset = 16
	ipv4AddressLength     = 4
	transportPortOffset   = 2
	transportPortLength   = 2
	interfaceNameLength   = 16
)

var errInterpositionNAT = errors.New("interposition NAT invariant failed")

type nftablesOperations struct{}

func newNATOperations() NATOperations {
	return nftablesOperations{}
}

// conntrackStatusDstNAT is IPS_DST_NAT from linux/netfilter/nf_conntrack_common.h: the flow's
// destination was rewritten by our dstnat chain.
const conntrackStatusDstNAT = 1 << 5

type parsedInterpositionNATSpec struct {
	podAddress        netip.Addr
	managementAddress netip.Addr
	managementSubnet  netip.Prefix
	gatewayAddress    netip.Addr
	transportName     string
	deviceName        string
	inbound           []InterpositionPortMap
}

func parseInterpositionNATSpec(spec InterpositionNATSpec) (parsedInterpositionNATSpec, error) {
	parsed := parsedInterpositionNATSpec{
		transportName: spec.TransportInterface,
		deviceName:    spec.DeviceInterface,
		inbound:       spec.InboundPorts,
	}

	if spec.TransportInterface == "" || spec.DeviceInterface == "" {
		return parsed, fmt.Errorf("%w: interface names are required", errInterpositionNAT)
	}

	if len(spec.TransportInterface) >= interfaceNameLength ||
		len(spec.DeviceInterface) >= interfaceNameLength {
		return parsed, fmt.Errorf("%w: interface name exceeds the Linux limit", errInterpositionNAT)
	}

	var err error

	parsed.podAddress, err = netip.ParseAddr(spec.PodAddress)
	if err != nil {
		return parsed, fmt.Errorf(
			"%w: pod address %q: %w",
			errInterpositionNAT,
			spec.PodAddress,
			err,
		)
	}

	parsed.managementAddress, err = netip.ParseAddr(spec.ManagementAddress)
	if err != nil {
		return parsed, fmt.Errorf(
			"%w: management address %q: %w", errInterpositionNAT, spec.ManagementAddress, err,
		)
	}

	parsed.managementSubnet, err = netip.ParsePrefix(spec.ManagementSubnet)
	if err != nil {
		return parsed, fmt.Errorf(
			"%w: management subnet %q: %w", errInterpositionNAT, spec.ManagementSubnet, err,
		)
	}

	if !parsed.podAddress.Is4() || !parsed.managementAddress.Is4() ||
		!parsed.managementSubnet.Addr().Is4() {
		return parsed, fmt.Errorf(
			"%w: only IPv4 translation is supported by this mode", errInterpositionNAT,
		)
	}

	if !parsed.managementSubnet.Contains(parsed.managementAddress) {
		return parsed, fmt.Errorf(
			"%w: management address %q is outside subnet %q",
			errInterpositionNAT, spec.ManagementAddress, spec.ManagementSubnet,
		)
	}

	parsed.gatewayAddress, err = netip.ParseAddr(spec.GatewayAddress)
	if err != nil {
		return parsed, fmt.Errorf(
			"%w: gateway address %q: %w", errInterpositionNAT, spec.GatewayAddress, err,
		)
	}

	if !parsed.gatewayAddress.Is4() ||
		!parsed.managementSubnet.Contains(parsed.gatewayAddress) ||
		parsed.gatewayAddress == parsed.managementAddress {
		return parsed, fmt.Errorf(
			"%w: gateway address %q is invalid for subnet %q",
			errInterpositionNAT, spec.GatewayAddress, spec.ManagementSubnet,
		)
	}

	for _, port := range spec.InboundPorts {
		if port.Protocol != "tcp" && port.Protocol != "udp" {
			return parsed, fmt.Errorf(
				"%w: inbound protocol %q is not tcp or udp", errInterpositionNAT, port.Protocol,
			)
		}

		if port.PodPort == 0 || port.DevicePort == 0 {
			return parsed, fmt.Errorf("%w: inbound ports must be non-zero", errInterpositionNAT)
		}
	}

	return parsed, nil
}

// EnsureInterpositionNAT reconciles the owned table by atomically rebuilding it: the table is
// created (idempotent), flushed of rules, its two base chains asserted, and the exact rule set
// for the spec added, all in one kernel transaction.
func (nftablesOperations) EnsureInterpositionNAT(spec InterpositionNATSpec) error {
	parsed, err := parseInterpositionNATSpec(spec)
	if err != nil {
		return err
	}

	conn := &nftables.Conn{}

	table := conn.AddTable(&nftables.Table{
		Family: nftables.TableFamilyIPv4,
		Name:   interpositionTableName,
	})
	conn.FlushTable(table)

	destinationPriority := nftables.ChainPriority(interpositionDestinationPriority)
	sourcePriority := nftables.ChainPriority(interpositionSourcePriority)

	destinationChain := conn.AddChain(&nftables.Chain{
		Name:     "dstnat",
		Table:    table,
		Type:     nftables.ChainTypeNAT,
		Hooknum:  nftables.ChainHookPrerouting,
		Priority: &destinationPriority,
	})

	sourceChain := conn.AddChain(&nftables.Chain{
		Name:     "srcnat",
		Table:    table,
		Type:     nftables.ChainTypeNAT,
		Hooknum:  nftables.ChainHookPostrouting,
		Priority: &sourcePriority,
	})

	subnetNetwork := parsed.managementSubnet.Masked().Addr().As4()
	subnetMask := prefixMask4(parsed.managementSubnet.Bits())
	podAddress := parsed.podAddress.As4()
	managementAddress := parsed.managementAddress.As4()

	// Forwarded shape: management-sourced traffic leaving the preserved transport interface is
	// masqueraded to the Pod address.
	conn.AddRule(&nftables.Rule{
		Table: table,
		Chain: sourceChain,
		Exprs: []expr.Any{
			&expr.Meta{Key: expr.MetaKeyOIFNAME, Register: 1},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: interfaceName(parsed.transportName)},
			&expr.Payload{
				DestRegister: 1,
				Base:         expr.PayloadBaseNetworkHeader,
				Offset:       ipv4SourceOffset,
				Len:          ipv4AddressLength,
			},
			&expr.Bitwise{
				SourceRegister: 1,
				DestRegister:   1,
				Len:            ipv4AddressLength,
				Mask:           subnetMask,
				Xor:            make([]byte, ipv4AddressLength),
			},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: subnetNetwork[:]},
			&expr.Masq{},
		},
	})

	// Locally-originated hairpin shape: the NAT verdict for a same-namespace device's flow is
	// bound at its first POSTROUTING traversal, which happens on the device leg, so the source
	// translation must match there. Intra-subnet management traffic is never translated.
	conn.AddRule(&nftables.Rule{
		Table: table,
		Chain: sourceChain,
		Exprs: []expr.Any{
			&expr.Meta{Key: expr.MetaKeyOIFNAME, Register: 1},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: interfaceName(parsed.deviceName)},
			&expr.Payload{
				DestRegister: 1,
				Base:         expr.PayloadBaseNetworkHeader,
				Offset:       ipv4SourceOffset,
				Len:          ipv4AddressLength,
			},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: managementAddress[:]},
			&expr.Payload{
				DestRegister: 1,
				Base:         expr.PayloadBaseNetworkHeader,
				Offset:       ipv4DestinationOffset,
				Len:          ipv4AddressLength,
			},
			&expr.Bitwise{
				SourceRegister: 1,
				DestRegister:   1,
				Len:            ipv4AddressLength,
				Mask:           subnetMask,
				Xor:            make([]byte, ipv4AddressLength),
			},
			&expr.Cmp{Op: expr.CmpOpNeq, Register: 1, Data: subnetNetwork[:]},
			&expr.Immediate{Register: 1, Data: podAddress[:]},
			&expr.NAT{
				Type:       expr.NATTypeSourceNAT,
				Family:     unix.NFPROTO_IPV4,
				RegAddrMin: 1,
			},
		},
	})

	addInboundGatewaySourceRule(conn, table, sourceChain, parsed)

	for _, port := range parsed.inbound {
		protocol := byte(unix.IPPROTO_TCP)
		if port.Protocol == "udp" {
			protocol = unix.IPPROTO_UDP
		}

		conn.AddRule(&nftables.Rule{
			Table: table,
			Chain: destinationChain,
			Exprs: []expr.Any{
				&expr.Payload{
					DestRegister: 1,
					Base:         expr.PayloadBaseNetworkHeader,
					Offset:       ipv4DestinationOffset,
					Len:          ipv4AddressLength,
				},
				&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: podAddress[:]},
				&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
				&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{protocol}},
				&expr.Payload{
					DestRegister: 1,
					Base:         expr.PayloadBaseTransportHeader,
					Offset:       transportPortOffset,
					Len:          transportPortLength,
				},
				&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: portBytes(port.PodPort)},
				&expr.Immediate{Register: 1, Data: managementAddress[:]},
				&expr.Immediate{Register: 2, Data: portBytes(port.DevicePort)},
				&expr.NAT{
					Type:        expr.NATTypeDestNAT,
					Family:      unix.NFPROTO_IPV4,
					RegAddrMin:  1,
					RegProtoMin: 2,
				},
			},
		})
	}

	if err := conn.Flush(); err != nil {
		return fmt.Errorf("programming interposition translation table: %w", err)
	}

	return nil
}

// addInboundGatewaySourceRule renders the inbound translated shape: a flow the dstnat chain
// pointed at the management address is also source-translated to the Pod-local gateway. The
// device then answers an on-subnet peer over its connected management route -- required for
// management stacks that never learn an off-subnet route (SR OS derives its routes from a
// Docker-shaped environment that a Pod does not present), and matching how containerlab's
// Docker port publishing presents the bridge address as the client.
func addInboundGatewaySourceRule(
	conn *nftables.Conn,
	table *nftables.Table,
	chain *nftables.Chain,
	parsed parsedInterpositionNATSpec,
) {
	managementAddress := parsed.managementAddress.As4()
	gatewayAddress := parsed.gatewayAddress.As4()

	conn.AddRule(&nftables.Rule{
		Table: table,
		Chain: chain,
		Exprs: []expr.Any{
			&expr.Payload{
				DestRegister: 1,
				Base:         expr.PayloadBaseNetworkHeader,
				Offset:       ipv4DestinationOffset,
				Len:          ipv4AddressLength,
			},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: managementAddress[:]},
			&expr.Ct{Register: 1, Key: expr.CtKeySTATUS},
			&expr.Bitwise{
				SourceRegister: 1,
				DestRegister:   1,
				Len:            ipv4AddressLength,
				Mask:           binaryutil.NativeEndian.PutUint32(conntrackStatusDstNAT),
				Xor:            make([]byte, ipv4AddressLength),
			},
			&expr.Cmp{Op: expr.CmpOpNeq, Register: 1, Data: make([]byte, ipv4AddressLength)},
			&expr.Immediate{Register: 1, Data: gatewayAddress[:]},
			&expr.NAT{
				Type:       expr.NATTypeSourceNAT,
				Family:     unix.NFPROTO_IPV4,
				RegAddrMin: 1,
			},
		},
	})
}

// DeleteInterpositionNAT removes the owned table; a missing table is success.
func (nftablesOperations) DeleteInterpositionNAT() error {
	conn := &nftables.Conn{}

	tables, err := conn.ListTablesOfFamily(nftables.TableFamilyIPv4)
	if err != nil {
		return fmt.Errorf("listing translation tables: %w", err)
	}

	for _, table := range tables {
		if table.Name != interpositionTableName {
			continue
		}

		conn.DelTable(table)

		if err := conn.Flush(); err != nil {
			return fmt.Errorf("removing interposition translation table: %w", err)
		}

		return nil
	}

	return nil
}

func interfaceName(name string) []byte {
	padded := make([]byte, interfaceNameLength)
	copy(padded, name)

	return padded
}

func prefixMask4(bits int) []byte {
	mask := make([]byte, ipv4AddressLength)
	for index := range mask {
		remaining := bits - index*8
		switch {
		case remaining >= 8:
			mask[index] = 0xff
		case remaining > 0:
			mask[index] = ^byte(0xff >> remaining)
		}
	}

	return mask
}

func portBytes(port uint16) []byte {
	return []byte{byte(port >> 8), byte(port & 0xff)}
}
