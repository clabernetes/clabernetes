//go:build linux

package directruntime

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	"github.com/google/nftables/userdata"
	"golang.org/x/sys/unix"
)

// transportFilterCommentPrefix marks the sidecar-owned accept rules inside foreign chains; the
// full comment carries the port ("c9s-transport-14789"). It is written in the iptables-nft
// comment encoding, so a device round-tripping its ruleset through iptables-save sees a plain
// `-m comment --comment` rule, and it doubles as the idempotency marker on re-assertion.
const transportFilterCommentPrefix = "c9s-transport-"

// sidecarTableNamePrefix identifies sidecar-owned tables the sweep must never touch; their
// chains carry no device policy.
const sidecarTableNamePrefix = "c9s-"

// transportFilterFamilies returns the families a device filter of the shared namespace can live
// in. iptables-nft programs ip and ip6; native nft rulesets may use inet. The bridge, arp, and
// netdev families never carry a local-input filter for UDP transports.
func transportFilterFamilies() []nftables.TableFamily {
	return []nftables.TableFamily{
		nftables.TableFamilyIPv4,
		nftables.TableFamilyIPv6,
		nftables.TableFamilyINet,
	}
}

type transportFilterOperations struct{}

func newTransportFilterOperations() TransportFilterOperations {
	return transportFilterOperations{}
}

// EnsureTransportFilterAccepts sweeps every foreign filter-type input and output base chain and
// inserts the missing per-port accepts at the chain head. Insertion is unconditional on chain
// policy: a permissive policy with an explicit drop rule severs the transports just as a drop
// policy does, and two attributed accept rules are harmless in a chain that would accept anyway.
func (transportFilterOperations) EnsureTransportFilterAccepts(spec TransportFilterSpec) error {
	if len(spec.UDPPorts) == 0 {
		return nil
	}

	for _, family := range transportFilterFamilies() {
		if err := ensureFamilyTransportAccepts(family, spec.UDPPorts); err != nil {
			return err
		}
	}

	return nil
}

// ensureFamilyTransportAccepts converges one table family. A kernel built without the family
// cannot host a device filter in it either, so an unsupported family is success, mirroring the
// mesh segment clamp's degradation contract.
func ensureFamilyTransportAccepts(family nftables.TableFamily, ports []uint16) error {
	conn := &nftables.Conn{}

	chains, err := conn.ListChainsOfTableFamily(family)
	if err != nil {
		if isMissingTransportFilterTargetError(err) {
			return nil
		}

		return fmt.Errorf("listing family %d filter chains: %w", family, err)
	}

	for _, chain := range chains {
		if !isForeignTransportFilterChain(chain) {
			continue
		}

		if err := ensureChainTransportAccepts(chain, ports); err != nil {
			return fmt.Errorf(
				"asserting transport accepts in %q chain %q: %w",
				chain.Table.Name,
				chain.Name,
				err,
			)
		}
	}

	return nil
}

// isForeignTransportFilterChain selects the chains a device filters local traffic with: filter-
// type base chains on the input or output hooks, outside the sidecar-owned tables. Non-base
// chains (a device's jump targets) are only reachable through their base chain and never need
// their own accept.
func isForeignTransportFilterChain(chain *nftables.Chain) bool {
	if chain.Hooknum == nil || chain.Type != nftables.ChainTypeFilter {
		return false
	}

	if *chain.Hooknum != *nftables.ChainHookInput && *chain.Hooknum != *nftables.ChainHookOutput {
		return false
	}

	if chain.Table == nil || strings.HasPrefix(chain.Table.Name, sidecarTableNamePrefix) {
		return false
	}

	return true
}

// ensureChainTransportAccepts inserts the missing per-port accepts at the head of one chain, in
// one kernel transaction per chain so a device flushing another table concurrently cannot fail
// the whole sweep. A chain that disappears between listing and commit is a device rewriting its
// filter; the next revision tick converges it.
func ensureChainTransportAccepts(chain *nftables.Chain, ports []uint16) error {
	conn := &nftables.Conn{}

	rules, err := conn.GetRules(chain.Table, chain)
	if err != nil {
		if isMissingTransportFilterTargetError(err) {
			return nil
		}

		return fmt.Errorf("listing chain rules: %w", err)
	}

	present := map[string]bool{}

	for _, rule := range rules {
		comment, ok := userdata.GetString(rule.UserData, userdata.TypeComment)
		if ok && strings.HasPrefix(comment, transportFilterCommentPrefix) {
			present[comment] = true
		}
	}

	inserted := false

	for _, port := range ports {
		comment := transportFilterCommentPrefix + strconv.Itoa(int(port))
		if present[comment] {
			continue
		}

		conn.InsertRule(&nftables.Rule{
			Table:    chain.Table,
			Chain:    chain,
			Exprs:    transportAcceptExpressions(port),
			UserData: userdata.AppendString(nil, userdata.TypeComment, comment),
		})

		inserted = true
	}

	if !inserted {
		return nil
	}

	if err := conn.Flush(); err != nil {
		if isMissingTransportFilterTargetError(err) {
			return nil
		}

		return fmt.Errorf("inserting transport accepts: %w", err)
	}

	return nil
}

// transportAcceptExpressions is the exact expression sequence iptables-nft generates for
// `-p udp --dport <port> -j ACCEPT`, so a device's iptables tooling can list, save, and restore
// the rule as one of its own. Meta L4PROTO carries the parsed transport protocol in every swept
// family, including ip6 extension chains.
func transportAcceptExpressions(port uint16) []expr.Any {
	return []expr.Any{
		&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{unix.IPPROTO_UDP}},
		&expr.Payload{
			DestRegister: 1,
			Base:         expr.PayloadBaseTransportHeader,
			Offset:       transportPortOffset,
			Len:          transportPortLength,
		},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: portBytes(port)},
		&expr.Counter{},
		&expr.Verdict{Kind: expr.VerdictAccept},
	}
}

// isMissingTransportFilterTargetError classifies the tolerated sweep outcomes: a family the
// kernel was built without (EAFNOSUPPORT/EOPNOTSUPP) and a chain or table a device removed
// between listing and commit (ENOENT).
func isMissingTransportFilterTargetError(err error) bool {
	return errors.Is(err, unix.ENOENT) ||
		errors.Is(err, unix.EOPNOTSUPP) ||
		errors.Is(err, unix.EAFNOSUPPORT)
}
