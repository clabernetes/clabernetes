//go:build linux

//nolint:testpackage // dense fixture-driven checks exercise one boundary end to end.
package directruntime

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	"github.com/google/nftables/userdata"
	"golang.org/x/sys/unix"
)

const transportFilterNetlinkChild = "C9S_TRANSPORT_FILTER_NETLINK_TEST_CHILD"

func TestTransportFilterAcceptsInIsolatedNamespace(t *testing.T) {
	if os.Getenv(transportFilterNetlinkChild) == "1" {
		testTransportFilterAccepts(t)

		return
	}

	unshare, err := exec.LookPath("unshare")
	if err != nil {
		t.Skip("unshare is unavailable")
	}

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	unshareArguments := []string{
		"-Urn",
		executable,
		"-test.run=^TestTransportFilterAcceptsInIsolatedNamespace$",
	}
	if os.Geteuid() == 0 {
		unshareArguments[0] = "-n"
	}

	command := exec.CommandContext( //nolint:gosec // The current test binary is executed in a new namespace.
		t.Context(),
		unshare,
		unshareArguments...,
	)

	command.Env = append(os.Environ(), transportFilterNetlinkChild+"=1")

	output, err := command.CombinedOutput()
	if err != nil {
		if strings.Contains(strings.ToLower(string(output)), "operation not permitted") {
			t.Skip(
				"the kernel restricts required netlink operations in an unprivileged user namespace",
			)
		}

		t.Fatalf("isolated transport filter test failed: %v\n%s", err, output)
	}
}

// deviceShapedFilter builds the ruleset shape iptables-nft leaves behind in a device-managed
// namespace: a filter table whose INPUT base chain carries a drop policy and a jump to a
// regular device chain, plus a NAT prerouting chain the sweep must never touch.
func deviceShapedFilter(t *testing.T, family nftables.TableFamily, tableName string) {
	t.Helper()

	conn := &nftables.Conn{}

	table := conn.AddTable(&nftables.Table{Family: family, Name: tableName})

	dropPolicy := nftables.ChainPolicyDrop

	input := conn.AddChain(&nftables.Chain{
		Name:     "INPUT",
		Table:    table,
		Type:     nftables.ChainTypeFilter,
		Hooknum:  nftables.ChainHookInput,
		Priority: nftables.ChainPriorityFilter,
		Policy:   &dropPolicy,
	})

	device := conn.AddChain(&nftables.Chain{
		Name:  "EOS_INPUT",
		Table: table,
	})

	conn.AddRule(&nftables.Rule{
		Table: table,
		Chain: input,
		Exprs: []expr.Any{
			&expr.Verdict{Kind: expr.VerdictJump, Chain: device.Name},
		},
	})

	if family != nftables.TableFamilyINet {
		conn.AddChain(&nftables.Chain{
			Name:     "PREROUTING",
			Table:    table,
			Type:     nftables.ChainTypeNAT,
			Hooknum:  nftables.ChainHookPrerouting,
			Priority: nftables.ChainPriorityNATDest,
		})
	}

	if err := conn.Flush(); err != nil {
		t.Fatalf("building device-shaped filter for family %d: %v", family, err)
	}
}

func chainByName(
	t *testing.T,
	family nftables.TableFamily,
	tableName string,
	chainName string,
) *nftables.Chain {
	t.Helper()

	conn := &nftables.Conn{}

	chains, err := conn.ListChainsOfTableFamily(family)
	if err != nil {
		t.Fatalf("listing chains: %v", err)
	}

	for _, chain := range chains {
		if chain.Table.Name == tableName && chain.Name == chainName {
			return chain
		}
	}

	return nil
}

func chainRules(t *testing.T, chain *nftables.Chain) []*nftables.Rule {
	t.Helper()

	if chain == nil {
		t.Fatal("chain is absent")
	}

	conn := &nftables.Conn{}

	rules, err := conn.GetRules(chain.Table, chain)
	if err != nil {
		t.Fatalf("listing rules of chain %q: %v", chain.Name, err)
	}

	return rules
}

func transportComments(rules []*nftables.Rule) []string {
	var comments []string

	for _, rule := range rules {
		comment, ok := userdata.GetString(rule.UserData, userdata.TypeComment)
		if ok && strings.HasPrefix(comment, transportFilterCommentPrefix) {
			comments = append(comments, comment)
		}
	}

	return comments
}

func testTransportFilterAccepts(t *testing.T) {
	t.Helper()
	runtime.LockOSThread()

	defer runtime.UnlockOSThread()

	deviceShapedFilter(t, nftables.TableFamilyIPv4, "filter")
	deviceShapedFilter(t, nftables.TableFamilyIPv6, "filter")
	deviceShapedFilter(t, nftables.TableFamilyINet, "device")

	// The sidecar-owned table with a filter input chain must never be swept.
	{
		conn := &nftables.Conn{}
		owned := conn.AddTable(&nftables.Table{
			Family: nftables.TableFamilyIPv4,
			Name:   interpositionTableName,
		})
		conn.AddChain(&nftables.Chain{
			Name:     "input",
			Table:    owned,
			Type:     nftables.ChainTypeFilter,
			Hooknum:  nftables.ChainHookInput,
			Priority: nftables.ChainPriorityFilter,
		})

		if err := conn.Flush(); err != nil {
			t.Fatalf("building sidecar-owned table: %v", err)
		}
	}

	operations := newTransportFilterOperations()

	spec := TransportFilterSpec{UDPPorts: []uint16{14789, 14790}}

	if err := operations.EnsureTransportFilterAccepts(spec); err != nil {
		t.Fatalf("asserting transport accepts: %v", err)
	}

	// Both accepts sit at the head of every foreign input base chain, before the device jump.
	for _, target := range []struct {
		family nftables.TableFamily
		table  string
	}{
		{family: nftables.TableFamilyIPv4, table: "filter"},
		{family: nftables.TableFamilyIPv6, table: "filter"},
		{family: nftables.TableFamilyINet, table: "device"},
	} {
		rules := chainRules(t, chainByName(t, target.family, target.table, "INPUT"))
		if len(rules) != 3 {
			t.Fatalf(
				"family %d table %q INPUT: expected 2 accepts + 1 device rule, got %d rules",
				target.family, target.table, len(rules),
			)
		}

		head := transportComments(rules[:2])
		if len(head) != 2 {
			t.Fatalf(
				"family %d table %q INPUT: expected the accepts at the chain head, comments %v",
				target.family, target.table, head,
			)
		}
	}

	// The device jump target, the NAT chain, and the sidecar-owned table stay untouched.
	if rules := chainRules(
		t, chainByName(t, nftables.TableFamilyIPv4, "filter", "EOS_INPUT"),
	); len(rules) != 0 {
		t.Fatalf("device jump-target chain was mutated: %d rules", len(rules))
	}

	if rules := chainRules(
		t, chainByName(t, nftables.TableFamilyIPv4, "filter", "PREROUTING"),
	); len(rules) != 0 {
		t.Fatalf("device NAT chain was mutated: %d rules", len(rules))
	}

	if rules := chainRules(
		t, chainByName(t, nftables.TableFamilyIPv4, interpositionTableName, "input"),
	); len(rules) != 0 {
		t.Fatalf("sidecar-owned chain was swept: %d rules", len(rules))
	}

	// Re-asserting must not duplicate rules.
	if err := operations.EnsureTransportFilterAccepts(spec); err != nil {
		t.Fatalf("re-asserting transport accepts: %v", err)
	}

	if rules := chainRules(
		t, chainByName(t, nftables.TableFamilyIPv4, "filter", "INPUT"),
	); len(rules) != 3 {
		t.Fatalf("re-assertion is not idempotent: %d rules", len(rules))
	}

	// A device rewriting its filter flushes the rules; the next assertion converges them back.
	{
		conn := &nftables.Conn{}
		conn.FlushTable(&nftables.Table{Family: nftables.TableFamilyIPv4, Name: "filter"})

		if err := conn.Flush(); err != nil {
			t.Fatalf("simulating device filter rewrite: %v", err)
		}
	}

	if err := operations.EnsureTransportFilterAccepts(spec); err != nil {
		t.Fatalf("re-asserting after device rewrite: %v", err)
	}

	rules := chainRules(t, chainByName(t, nftables.TableFamilyIPv4, "filter", "INPUT"))
	if len(transportComments(rules)) != 2 {
		t.Fatalf("accepts were not re-asserted after a device rewrite: %d rules", len(rules))
	}

	// An empty spec is a no-op.
	if err := operations.EnsureTransportFilterAccepts(TransportFilterSpec{}); err != nil {
		t.Fatalf("empty spec must be a no-op: %v", err)
	}
}

const transportFilterLegacyChild = "C9S_TRANSPORT_FILTER_LEGACY_TEST_CHILD"

func TestTransportFilterLegacyAcceptsInIsolatedNamespace(t *testing.T) {
	if os.Getenv(transportFilterLegacyChild) == "1" {
		testTransportFilterLegacyAccepts(t)

		return
	}

	unshare, err := exec.LookPath("unshare")
	if err != nil {
		t.Skip("unshare is unavailable")
	}

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	unshareArguments := []string{
		"-Urn",
		executable,
		"-test.run=^TestTransportFilterLegacyAcceptsInIsolatedNamespace$",
	}
	if os.Geteuid() == 0 {
		unshareArguments[0] = "-n"
	}

	command := exec.CommandContext( //nolint:gosec // The current test binary is executed in a new namespace.
		t.Context(),
		unshare,
		unshareArguments...,
	)

	// The sweep takes the xtables lock too: give the child the same lockfile path the
	// seeding uses, so seed and sweep serialize on one inode instead of the sweep
	// skip-ticking on the unmapped root's /run/xtables.lock.
	command.Env = append(
		os.Environ(),
		transportFilterLegacyChild+"=1",
		"XTABLES_LOCKFILE="+os.TempDir()+"/c9s-transport-filter-legacy-test.xtables.lock",
	)

	output, err := command.CombinedOutput()
	if err != nil {
		if strings.Contains(strings.ToLower(string(output)), "operation not permitted") {
			t.Skip(
				"the kernel restricts required legacy iptables operations in an unprivileged user namespace",
			)
		}

		t.Fatalf("isolated legacy transport filter test failed: %v\n%s", err, output)
	}
}

// seedLegacyDropPolicy seeds the kernel with a real legacy iptables filter table whose INPUT
// base chain carries a drop policy -- the exact shape an Arista cEOS programs through
// iptables-legacy in the Pod network namespace, which the netlink sweep cannot see. Seeding
// shells out to the iptables-legacy binary; without it, or on a kernel that cannot host a
// legacy filter table at all, the child skips rather than asserting against a filter that was
// never there.
func seedLegacyDropPolicy(t *testing.T) {
	t.Helper()

	binary, err := exec.LookPath("iptables-legacy")
	if err != nil {
		t.Skip("iptables-legacy is unavailable")
	}

	// The xtables lock must live where this namespace can take it: /run/xtables.lock belongs
	// to the host mount namespace, and a user namespace cannot create it there.
	environment := append(
		os.Environ(),
		"XTABLES_LOCKFILE="+os.TempDir()+"/c9s-transport-filter-legacy-test.xtables.lock",
	)

	seed := func(arguments ...string) (string, error) {
		command := exec.CommandContext( //nolint:gosec // Fixed iptables seed commands in an isolated namespace.
			t.Context(),
			binary,
			arguments...,
		)
		command.Env = environment

		output, err := command.CombinedOutput()

		return string(output), err
	}

	if output, err := seed("--version"); err != nil {
		t.Skipf("running %s: %v\n%s", binary, err, output)
	}

	if output, err := seed("-t", "filter", "-P", "INPUT", "DROP"); err != nil {
		lowered := strings.ToLower(output)
		if strings.Contains(lowered, "permission denied") ||
			strings.Contains(lowered, "no such file or directory") ||
			strings.Contains(lowered, "not supported") ||
			strings.Contains(lowered, "can't open table") {
			t.Skipf(
				"the kernel cannot host a legacy iptables filter table in a user namespace: %v\n%s",
				err,
				output,
			)
		}

		t.Fatalf("seeding the legacy filter table: %v\n%s", err, output)
	}

	output, err := seed("-S", "INPUT")
	if err != nil {
		t.Fatalf("reading back the seeded legacy INPUT chain: %v\n%s", err, output)
	}

	if !strings.Contains(output, "-P INPUT DROP") {
		t.Fatalf("the seeded legacy INPUT chain carries no drop policy: %s", output)
	}
}

func testTransportFilterLegacyAccepts(t *testing.T) {
	t.Helper()
	runtime.LockOSThread()

	defer runtime.UnlockOSThread()

	seedLegacyDropPolicy(t)

	operations := newTransportFilterOperations()

	spec := TransportFilterSpec{UDPPorts: []uint16{14790}, TCPPorts: []uint16{14791}}

	err := operations.EnsureTransportFilterAccepts(spec)
	if err != nil {
		t.Fatalf(
			"asserting transport accepts through the legacy filter: %v "+
				"(want udp/14790 and tcp/14791 accepted in the legacy filter table)",
			err,
		)
	}

	// The sweep must have converged the legacy filter table: both accepts present in INPUT.
	rules, err := listLegacyChainRules("filter", "INPUT")
	if err != nil {
		t.Fatalf(
			"reading legacy INPUT rules to assert the accepts: %v "+
				"(the sweep must converge the legacy table, not leave it unreadable)",
			err,
		)
	}

	if !legacyRulesAccept(rules, transportAccept{protocol: unix.IPPROTO_UDP, port: 14790}) {
		t.Fatalf("legacy filter INPUT does not accept udp/14790: %+v", rules)
	}

	if !legacyRulesAccept(rules, transportAccept{protocol: unix.IPPROTO_TCP, port: 14791}) {
		t.Fatalf("legacy filter INPUT does not accept tcp/14791: %+v", rules)
	}
}
