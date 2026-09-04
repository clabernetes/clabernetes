//go:build linux

//nolint:testpackage // the clamp is an internal boundary exercised end to end.
package directruntime

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"
)

const segmentClampNetlinkChild = "C9S_SEGMENT_CLAMP_NETLINK_TEST_CHILD"

// TestMeshSegmentClampProgramsOwnedTableInIsolatedNamespace programs the real backend, so the
// kernel itself confirms the clamp is expressible: a rejected expression set fails here rather
// than silently leaving every management flow black-holed at runtime.
func TestMeshSegmentClampProgramsOwnedTableInIsolatedNamespace(t *testing.T) {
	if os.Getenv(segmentClampNetlinkChild) == "1" {
		testMeshSegmentClampProgramsOwnedTable(t)

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
		"-test.run=^TestMeshSegmentClampProgramsOwnedTableInIsolatedNamespace$",
	}
	if os.Geteuid() == 0 {
		unshareArguments[0] = "-n"
	}

	command := exec.CommandContext( //nolint:gosec // The current test binary is executed in a new namespace.
		t.Context(),
		unshare,
		unshareArguments...,
	)

	command.Env = append(os.Environ(), segmentClampNetlinkChild+"=1")

	output, err := command.CombinedOutput()
	if err != nil {
		if strings.Contains(strings.ToLower(string(output)), "operation not permitted") {
			t.Skip(
				"the kernel restricts required netlink operations in an unprivileged user namespace",
			)
		}

		t.Fatalf("isolated segment clamp programming test failed: %v\n%s", err, output)
	}
}

func testMeshSegmentClampProgramsOwnedTable(t *testing.T) {
	t.Helper()
	runtime.LockOSThread()

	defer runtime.UnlockOSThread()

	const meshMTU = 1430

	if err := ensureMeshSegmentClamp(meshMTU); err != nil {
		t.Fatalf("programming segment clamp: %v", err)
	}

	// Reconciling twice must be idempotent and leave exactly one table with the same rules.
	if err := ensureMeshSegmentClamp(meshMTU); err != nil {
		t.Fatalf("reprogramming segment clamp: %v", err)
	}

	conn := &nftables.Conn{}

	tables, err := conn.ListTablesOfFamily(nftables.TableFamilyINet)
	if err != nil {
		t.Fatalf("listing inet tables: %v", err)
	}

	var owned *nftables.Table

	for _, table := range tables {
		if table.Name == meshSegmentClampTableName {
			owned = table
		}
	}

	if owned == nil {
		t.Fatal("owned segment clamp table was not created")
	}

	chains, err := conn.ListChainsOfTableFamily(nftables.TableFamilyINet)
	if err != nil {
		t.Fatalf("listing inet chains: %v", err)
	}

	var forward *nftables.Chain

	for _, chain := range chains {
		if chain.Table.Name == meshSegmentClampTableName && chain.Name == "forward" {
			forward = chain
		}
	}

	if forward == nil {
		t.Fatal("segment clamp forward chain was not created")
	}

	rules, err := conn.GetRules(owned, forward)
	if err != nil {
		t.Fatalf("listing segment clamp rules: %v", err)
	}

	if len(rules) != 4 {
		t.Fatalf(
			"expected one clamp rule per mesh ingress interface and address family, got %d",
			len(rules),
		)
	}

	for _, rule := range rules {
		assertClampRuleShape(t, rule)
		assertClampRuleUsesGenericIngress(t, rule)
	}
}

// assertClampRuleShape pins the expression shape that was verified to actually rewrite a
// handshake on the wire. The inet family carries both address families, so the transport
// protocol has to come from the packet parse (meta l4proto) rather than a network-header offset:
// a rule that reads the protocol out of the header at an IPv4 offset is accepted by the kernel
// and then silently never matches IPv6.
func assertClampRuleShape(t *testing.T, rule *nftables.Rule) {
	t.Helper()

	var (
		matchesL4Proto   bool
		matchesFamily    bool
		readsMaxSegment  bool
		writesMaxSegment bool
	)

	for _, expression := range rule.Exprs {
		switch typed := expression.(type) {
		case *expr.Meta:
			if typed.Key == expr.MetaKeyL4PROTO {
				matchesL4Proto = true
			}

			if typed.Key == expr.MetaKeyNFPROTO {
				matchesFamily = true
			}
		case *expr.Payload:
			if typed.Base == expr.PayloadBaseNetworkHeader {
				t.Fatal("clamp rule reads the network header, whose layout differs per family")
			}
		case *expr.Exthdr:
			if typed.Type != tcpOptionMaxSegment {
				continue
			}

			if typed.SourceRegister != 0 {
				writesMaxSegment = true
			} else {
				readsMaxSegment = true
			}
		}
	}

	if !matchesL4Proto || !matchesFamily || !readsMaxSegment || !writesMaxSegment {
		t.Fatalf(
			"clamp rule is incomplete (l4proto %t, family %t, read %t, write %t)",
			matchesL4Proto, matchesFamily, readsMaxSegment, writesMaxSegment,
		)
	}
}

func assertClampRuleUsesGenericIngress(t *testing.T, rule *nftables.Rule) {
	t.Helper()

	for _, expression := range rule.Exprs {
		meta, ok := expression.(*expr.Meta)
		if !ok {
			continue
		}

		if meta.Key == expr.MetaKeyBRIIIFNAME {
			t.Fatal("clamp rule uses the optional bridge-specific meta module")
		}

		if meta.Key == expr.MetaKeyIIFNAME {
			return
		}
	}

	t.Fatal("clamp rule has no generic ingress interface match")
}

// TestMeshSegmentClampIsSkippedWithoutAMeshMTU keeps an unknown path size from programming a
// clamp of zero, which would make every handshake advertise nothing usable.
func TestMeshSegmentClampIsSkippedWithoutAMeshMTU(t *testing.T) {
	t.Parallel()

	if err := ensureMeshSegmentClamp(0); err != nil {
		t.Fatalf("ensureMeshSegmentClamp(0) error = %v, want no programming at all", err)
	}
}

func TestUnsupportedMeshSegmentClampErrorsAreOptional(t *testing.T) {
	t.Parallel()

	for _, err := range []error{
		unix.ENOENT,
		unix.EOPNOTSUPP,
		errors.Join(unix.EOPNOTSUPP, unix.ENOENT),
	} {
		if !isUnsupportedMeshSegmentClampError(err) {
			t.Fatalf("isUnsupportedMeshSegmentClampError(%v) = false, want true", err)
		}
	}

	if isUnsupportedMeshSegmentClampError(unix.EINVAL) {
		t.Fatal("isUnsupportedMeshSegmentClampError(EINVAL) = true, want false")
	}
}
