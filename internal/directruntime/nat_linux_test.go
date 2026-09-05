//go:build linux

//nolint:gocyclo,testpackage // dense fixture-driven checks exercise one boundary end to end.
package directruntime

import (
	"encoding/json"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/google/nftables"
	"github.com/vishvananda/netlink"
)

const natNetlinkChild = "C9S_NAT_NETLINK_TEST_CHILD"

// natHarnessSpecEnvironmentVariable lets an operator-driven validation rig apply a spec through
// the real backend inside an arbitrary network namespace:
// C9S_NAT_HARNESS_SPEC='{"PodAddress":...}' nat.test -test.run TestInterpositionNATHarnessApply .
const natHarnessSpecEnvironmentVariable = "C9S_NAT_HARNESS_SPEC"

func validInterpositionNATSpec() InterpositionNATSpec {
	return InterpositionNATSpec{
		PodAddress:         "172.30.30.2",
		ManagementAddress:  "172.80.80.31",
		ManagementSubnet:   "172.80.80.0/24",
		GatewayAddress:     "172.80.80.1",
		TransportInterface: "c9s0",
		DeviceInterface:    "eth0",
		InboundPorts: []InterpositionPortMap{
			{Protocol: "tcp", PodPort: 2222, DevicePort: 22},
		},
	}
}

func TestParseInterpositionNATSpecRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	for name, mutate := range map[string]func(*InterpositionNATSpec){
		"empty pod address":          func(s *InterpositionNATSpec) { s.PodAddress = "" },
		"bad management address":     func(s *InterpositionNATSpec) { s.ManagementAddress = "not-an-ip" },
		"bad subnet":                 func(s *InterpositionNATSpec) { s.ManagementSubnet = "172.80.80.0" },
		"address outside subnet":     func(s *InterpositionNATSpec) { s.ManagementAddress = "172.81.0.1" },
		"bad gateway":                func(s *InterpositionNATSpec) { s.GatewayAddress = "not-an-ip" },
		"gateway outside subnet":     func(s *InterpositionNATSpec) { s.GatewayAddress = "172.81.0.1" },
		"gateway equals management":  func(s *InterpositionNATSpec) { s.GatewayAddress = "172.80.80.31" },
		"ipv6 address":               func(s *InterpositionNATSpec) { s.PodAddress = "fd00::1" },
		"empty transport interface":  func(s *InterpositionNATSpec) { s.TransportInterface = "" },
		"over-long device interface": func(s *InterpositionNATSpec) { s.DeviceInterface = "interface-name-far-too-long" },
		"bad protocol": func(s *InterpositionNATSpec) {
			s.InboundPorts = []InterpositionPortMap{{Protocol: "sctp", PodPort: 1, DevicePort: 1}}
		},
		"zero port": func(s *InterpositionNATSpec) {
			s.InboundPorts = []InterpositionPortMap{{Protocol: "tcp", PodPort: 0, DevicePort: 22}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			spec := validInterpositionNATSpec()
			mutate(&spec)

			if _, err := parseInterpositionNATSpec(spec); err == nil {
				t.Fatalf("expected spec rejection for %q", name)
			}
		})
	}
}

func TestParseInterpositionNATSpecAcceptsValidInput(t *testing.T) {
	t.Parallel()

	if _, err := parseInterpositionNATSpec(validInterpositionNATSpec()); err != nil {
		t.Fatalf("valid spec rejected: %v", err)
	}
}

func TestInterpositionNATProgramsOwnedTableInIsolatedNamespace(t *testing.T) {
	if os.Getenv(natNetlinkChild) == "1" {
		testInterpositionNATProgramsOwnedTable(t)

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
		"-test.run=^TestInterpositionNATProgramsOwnedTableInIsolatedNamespace$",
	}
	if os.Geteuid() == 0 {
		unshareArguments[0] = "-n"
	}

	command := exec.CommandContext( //nolint:gosec // The current test binary is executed in a new namespace.
		t.Context(),
		unshare,
		unshareArguments...,
	)

	command.Env = append(os.Environ(), natNetlinkChild+"=1")

	output, err := command.CombinedOutput()
	if err != nil {
		if strings.Contains(strings.ToLower(string(output)), "operation not permitted") {
			t.Skip(
				"the kernel restricts required netlink operations in an unprivileged user namespace",
			)
		}

		t.Fatalf("isolated NAT programming test failed: %v\n%s", err, output)
	}
}

//nolint:gocyclo // one straight-line verification pass.
func testInterpositionNATProgramsOwnedTable(t *testing.T) {
	t.Helper()
	runtime.LockOSThread()

	defer runtime.UnlockOSThread()

	operations := newNATOperations()

	spec := validInterpositionNATSpec()

	if err := operations.EnsureInterpositionNAT(spec); err != nil {
		t.Fatalf("programming translation table: %v", err)
	}

	// Reconciling twice must be idempotent and leave exactly one table with the same rules.
	if err := operations.EnsureInterpositionNAT(spec); err != nil {
		t.Fatalf("reprogramming translation table: %v", err)
	}

	conn := &nftables.Conn{}

	tables, err := conn.ListTablesOfFamily(nftables.TableFamilyIPv4)
	if err != nil {
		t.Fatalf("listing tables: %v", err)
	}

	var owned *nftables.Table

	for _, table := range tables {
		if table.Name == interpositionTableName {
			owned = table
		}
	}

	if owned == nil {
		t.Fatal("owned translation table was not created")
	}

	chains, err := conn.ListChainsOfTableFamily(nftables.TableFamilyIPv4)
	if err != nil {
		t.Fatalf("listing chains: %v", err)
	}

	ownedChains := map[string]*nftables.Chain{}

	for _, chain := range chains {
		if chain.Table.Name == interpositionTableName {
			ownedChains[chain.Name] = chain
		}
	}

	source, ok := ownedChains["srcnat"]
	if !ok || source.Priority == nil || *source.Priority != interpositionSourcePriority {
		t.Fatalf("srcnat chain missing or at wrong priority: %+v", source)
	}

	destination, ok := ownedChains["dstnat"]
	if !ok || destination.Priority == nil ||
		*destination.Priority != interpositionDestinationPriority {
		t.Fatalf("dstnat chain missing or at wrong priority: %+v", destination)
	}

	sourceRules, err := conn.GetRules(owned, source)
	if err != nil {
		t.Fatalf("listing srcnat rules: %v", err)
	}

	if len(sourceRules) != 3 {
		t.Fatalf("expected 3 srcnat rules (all shapes), got %d", len(sourceRules))
	}

	// With the management address held on the device leg by the pod kernel, inbound flows
	// still get the gateway as their source (the interposition hairpins the reply across the
	// pair); the table keeps the same three shapes.
	if err = netlink.LinkAdd(&netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: spec.DeviceInterface}}); err != nil {
		t.Fatalf("creating a device leg: %v", err)
	}

	held, _ := netlink.ParseAddr(spec.ManagementAddress + "/24")

	deviceLeg, _ := netlink.LinkByName(spec.DeviceInterface)
	if err = netlink.AddrAdd(deviceLeg, held); err != nil {
		t.Fatalf("holding the management address on the device leg: %v", err)
	}

	if err = operations.EnsureInterpositionNAT(spec); err != nil {
		t.Fatalf("reprogramming translation table with a kernel-held address: %v", err)
	}

	heldRules, err := conn.GetRules(owned, source)
	if err != nil || len(heldRules) != 3 {
		t.Fatalf(
			"expected 3 srcnat rules for a kernel-held address, got %d (%v)",
			len(heldRules),
			err,
		)
	}

	_ = netlink.LinkDel(deviceLeg)

	destinationRules, err := conn.GetRules(owned, destination)
	if err != nil {
		t.Fatalf("listing dstnat rules: %v", err)
	}

	if len(destinationRules) != 1 {
		t.Fatalf("expected 1 dstnat rule, got %d", len(destinationRules))
	}

	if err := operations.DeleteInterpositionNAT(); err != nil {
		t.Fatalf("deleting translation table: %v", err)
	}

	// Deleting again must be success on absence.
	if err := operations.DeleteInterpositionNAT(); err != nil {
		t.Fatalf("second delete is not idempotent: %v", err)
	}
}

// TestInterpositionNATHarnessApply is not a test of this repository's state: it is the
// entrypoint a validation rig uses to program the real backend inside a prepared network
// namespace. It does nothing unless the harness environment variable carries a spec.
func TestInterpositionNATHarnessApply(t *testing.T) {
	raw := os.Getenv(natHarnessSpecEnvironmentVariable)
	if raw == "" {
		t.Skip("no harness spec provided")
	}

	var spec InterpositionNATSpec

	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		t.Fatalf("decoding harness spec: %v", err)
	}

	if err := newNATOperations().EnsureInterpositionNAT(spec); err != nil {
		t.Fatalf("applying harness spec: %v", err)
	}
}
