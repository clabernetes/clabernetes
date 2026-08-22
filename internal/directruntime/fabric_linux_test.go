//go:build linux

//nolint:testpackage // exercises the unexported linux realization directly.
package directruntime

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/vishvananda/netlink"
)

const fabricSweepNetlinkChild = "C9S_FABRIC_SWEEP_NETLINK_TEST_CHILD"

func TestSweepTransportStateDeletesBothVethEndsInIsolatedNamespace(t *testing.T) {
	if os.Getenv(fabricSweepNetlinkChild) == "1" {
		testSweepTransportStateDeletesBothVethEnds(t)
		testStaleTransportLinkDeleteIsIdempotent(t)

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
		"-test.run=^TestSweepTransportStateDeletesBothVethEndsInIsolatedNamespace$",
	}
	if os.Geteuid() == 0 {
		unshareArguments[0] = "-n"
	}

	command := exec.CommandContext( //nolint:gosec // The current test binary is executed in a new namespace.
		t.Context(),
		unshare,
		unshareArguments...,
	)

	command.Env = append(os.Environ(), fabricSweepNetlinkChild+"=1")

	output, err := command.CombinedOutput()
	if err != nil {
		if strings.Contains(strings.ToLower(string(output)), "operation not permitted") {
			t.Skip(
				"the kernel restricts required netlink operations in an unprivileged user namespace",
			)
		}

		t.Fatalf("isolated fabric sweep test failed: %v\n%s", err, output)
	}
}

func testStaleTransportLinkDeleteIsIdempotent(t *testing.T) {
	t.Helper()

	attributes := netlink.NewLinkAttrs()

	attributes.Name = "deleted-left"
	if err := netlink.LinkAdd(&netlink.Veth{
		LinkAttrs: attributes,
		PeerName:  "stale-right",
	}); err != nil {
		t.Fatalf("creating transport pair for stale-delete test: %v", err)
	}

	left, err := netlink.LinkByName("deleted-left")
	if err != nil {
		t.Fatal(err)
	}

	right, err := netlink.LinkByName("stale-right")
	if err != nil {
		t.Fatal(err)
	}

	if err = netlink.LinkDel(left); err != nil {
		t.Fatalf("deleting the first transport end: %v", err)
	}

	if err = deleteStaleTransportLink(right); err != nil {
		t.Fatalf("deleting an already-removed transport peer: %v", err)
	}
}

func testSweepTransportStateDeletesBothVethEnds(t *testing.T) {
	t.Helper()

	const (
		leftName    = "stale-left"
		rightName   = "stale-right"
		ownerPrefix = "c9s:direct:v1:test:transport:"
		owner       = ownerPrefix + "stale"
	)

	attributes := netlink.NewLinkAttrs()

	attributes.Name = leftName
	attributes.Alias = owner

	if err := netlink.LinkAdd(&netlink.Veth{
		LinkAttrs: attributes,
		PeerName:  rightName,
	}); err != nil {
		t.Fatalf("creating stale transport pair: %v", err)
	}

	right, err := netlink.LinkByName(rightName)
	if err != nil {
		t.Fatal(err)
	}

	if err = netlink.LinkSetAlias(right, owner); err != nil {
		t.Fatalf("marking stale peer ownership: %v", err)
	}

	if err = (netlinkOperations{}).SweepTransportState(ownerPrefix, nil); err != nil {
		t.Fatalf("sweeping stale transport pair: %v", err)
	}

	for _, name := range []string{leftName, rightName} {
		if _, lookupErr := netlink.LinkByName(name); !errors.As(
			lookupErr,
			&netlink.LinkNotFoundError{},
		) {
			t.Fatalf("stale transport link %q still exists or lookup failed unexpectedly: %v", name, lookupErr)
		}
	}
}
