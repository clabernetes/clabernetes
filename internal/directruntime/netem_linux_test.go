//go:build linux

//nolint:gocyclo,testpackage // one netlink boundary is exercised end to end with unexported operations.
package directruntime

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	"github.com/vishvananda/netlink"
)

const netemNetlinkChild = "C9S_NETEM_NETLINK_TEST_CHILD"

// TestNetlinkOperationsConvergeNetem programs real qdiscs in a private network namespace: the
// test binary re-executes itself under unshare, like the management netlink test.
func TestNetlinkOperationsConvergeNetem(t *testing.T) {
	if os.Getenv(netemNetlinkChild) == "1" {
		testNetemConvergence(t)

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
		"-test.run=^TestNetlinkOperationsConvergeNetem$",
	}
	if os.Geteuid() == 0 {
		unshareArguments[0] = "-n"
	}

	command := exec.CommandContext( //nolint:gosec // The current test binary is executed in a new namespace.
		t.Context(),
		unshare,
		unshareArguments...,
	)

	command.Env = append(os.Environ(), netemNetlinkChild+"=1")

	output, err := command.CombinedOutput()
	if err != nil {
		lowered := strings.ToLower(string(output))
		if strings.Contains(lowered, "operation not permitted") ||
			strings.Contains(lowered, "no such file or directory") {
			t.Skip("the kernel cannot program netem qdiscs here (namespace or sch_netem module)")
		}

		t.Fatalf("netem netlink test failed: %v\n%s", err, output)
	}
}

func testNetemConvergence(t *testing.T) {
	t.Helper()

	attributes := netlink.NewLinkAttrs()
	attributes.Name = "netem-a"
	veth := netlink.NewVeth(attributes)
	veth.PeerName = "netem-b"

	if err := netlink.LinkAdd(veth); err != nil {
		t.Fatal(err)
	}

	operations := netlinkOperations{}

	// An interface the sidecar has not realized yet is left for a later pass.
	if err := operations.EnsureNetem("netem-missing", &clabernetesinternaldeviceplan.Netem{
		Delay: time.Millisecond,
	}); err != nil {
		t.Fatalf("EnsureNetem() on a missing interface = %v", err)
	}

	netem := &clabernetesinternaldeviceplan.Netem{
		Delay: 50 * time.Millisecond, Jitter: 5 * time.Millisecond,
		Loss: 1, Rate: 10000, Corruption: 0.5,
	}

	// Converging twice is a no-op the second time.
	for range 2 {
		if err := operations.EnsureNetem("netem-a", netem); err != nil {
			t.Fatal(err)
		}
	}

	current := rootNetem(t, "netem-a")
	if current == nil || current.Attrs().Handle != netlink.MakeHandle(netemHandleMajor, 0) ||
		current.Rate64 != 10000*1000/8 || current.Limit != netemLimit ||
		current.Latency == 0 || current.Jitter == 0 || current.Loss == 0 ||
		current.CorruptProb == 0 {
		t.Fatalf("root qdisc after EnsureNetem() = %+v", current)
	}

	// The longest delay the API admits must not overflow the 32-bit kernel ticks.
	longest := &clabernetesinternaldeviceplan.Netem{
		Delay: clabernetesinternaldeviceplan.MaxNetemDelay,
	}
	if err := operations.EnsureNetem("netem-a", longest); err != nil {
		t.Fatal(err)
	}

	desired := desiredNetemQdisc(0, longest)
	if current = rootNetem(t, "netem-a"); current == nil || current.Latency != desired.Latency ||
		current.Jitter != 0 || current.Loss != 0 || current.Rate64 != 0 {
		t.Fatalf("root qdisc after updating the impairment = %+v", current)
	}

	ticksPerMicrosecond := float64(desired.Latency) /
		float64(clabernetesinternaldeviceplan.MaxNetemDelay.Microseconds())
	if ticksPerMicrosecond < 1 {
		t.Fatalf("the longest delay wrapped around to %d ticks", desired.Latency)
	}

	// Without a declared impairment the sidecar removes its own.
	if err := operations.EnsureNetem("netem-a", nil); err != nil {
		t.Fatal(err)
	}

	if current = rootNetem(t, "netem-a"); current != nil {
		t.Fatalf("the sidecar's impairment survived its removal: %+v", current)
	}

	// An impairment the sidecar did not install is not its to remove.
	foreign := netlink.NewNetem(
		netlink.QdiscAttrs{
			LinkIndex: linkIndex(t, "netem-b"),
			Handle:    netlink.MakeHandle(1, 0),
			Parent:    netlink.HANDLE_ROOT,
		},
		netlink.NetemQdiscAttrs{Latency: 1000},
	)
	if err := netlink.QdiscAdd(foreign); err != nil {
		t.Fatal(err)
	}

	if err := operations.EnsureNetem("netem-b", nil); err != nil {
		t.Fatal(err)
	}

	if current = rootNetem(t, "netem-b"); current == nil ||
		current.Attrs().Handle != netlink.MakeHandle(1, 0) {
		t.Fatalf("the sidecar removed a qdisc it does not own: %+v", current)
	}
}

func rootNetem(t *testing.T, name string) *netlink.Netem {
	t.Helper()

	link, err := netlink.LinkByName(name)
	if err != nil {
		t.Fatal(err)
	}

	qdiscs, err := netlink.QdiscList(link)
	if err != nil {
		t.Fatal(err)
	}

	for _, qdisc := range qdiscs {
		if netem, ok := qdisc.(*netlink.Netem); ok &&
			netem.Attrs().Parent == netlink.HANDLE_ROOT {
			return netem
		}
	}

	return nil
}

func linkIndex(t *testing.T, name string) int {
	t.Helper()

	link, err := netlink.LinkByName(name)
	if err != nil {
		t.Fatal(err)
	}

	return link.Attrs().Index
}
