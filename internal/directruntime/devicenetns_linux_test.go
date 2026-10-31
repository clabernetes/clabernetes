//go:build linux

//nolint:testpackage // exercises the unexported isolated device namespace primitive directly.
package directruntime

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vishvananda/netns"
	"golang.org/x/sys/unix"
)

const deviceNetnsSysctlChild = "C9S_DEVICE_NETNS_SYSCTL_TEST_CHILD"

// TestNetlinkOperationsApplySysctlsInsideDeviceNetns verifies that a package-declared sysctl
// under isolation lands in the private device namespace and leaves the Pod namespace untouched.
// /proc/sys/net resolves against the calling thread's network namespace, so the assertion reads
// the value from inside the same namespace.
func TestNetlinkOperationsApplySysctlsInsideDeviceNetns(t *testing.T) {
	if os.Getenv(deviceNetnsSysctlChild) == "1" {
		testDeviceNetnsSysctlIsolation(t)

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
		"-test.run=^TestNetlinkOperationsApplySysctlsInsideDeviceNetns$",
	}
	if os.Geteuid() == 0 {
		unshareArguments[0] = "-n"
	}

	command := exec.CommandContext( //nolint:gosec // The current test binary is executed in a new namespace.
		t.Context(),
		unshare,
		unshareArguments...,
	)

	command.Env = append(os.Environ(), deviceNetnsSysctlChild+"=1")

	output, err := command.CombinedOutput()
	if err != nil {
		if strings.Contains(strings.ToLower(string(output)), "operation not permitted") {
			t.Skip(
				"the kernel restricts required netns operations in an unprivileged user namespace",
			)
		}

		t.Fatalf("isolated device netns sysctl test failed: %v\n%s", err, output)
	}
}

func testDeviceNetnsSysctlIsolation(t *testing.T) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "device.ns")
	if err := EnsureDeviceNetns(path); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = CleanupDeviceNetns(path) })

	device, err := openDeviceNetworkNamespace(path)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = device.Close() })

	operations := newLinkOperationsWithDeviceNetns(nil, device)

	for _, name := range []string{"net.ipv4.ip_forward", "net.ipv4.conf.all.rp_filter"} {
		podValue := readSysctl(t, netSysctlPath(name))

		// Both settings are booleans, so the isolated value always differs from the inherited
		// one and a namespace mix-up cannot pass unnoticed.
		want := "0"
		if podValue == "0" {
			want = "1"
		}

		if err := operations.EnsureSysctl(name, want); err != nil {
			t.Fatalf("applying %s in the device namespace: %v", name, err)
		}

		// Re-assertion is the steady state for every connectivity pass.
		if err := operations.EnsureSysctl(name, want); err != nil {
			t.Fatalf("re-applying %s in the device namespace: %v", name, err)
		}

		if got := readSysctlInDeviceNetns(t, device, name); got != want {
			t.Fatalf("device namespace %s = %q, want %q", name, got, want)
		}

		if got := readSysctl(t, netSysctlPath(name)); got != podValue {
			t.Fatalf("Pod namespace %s = %q, want the untouched %q", name, got, podValue)
		}
	}
}

// readSysctlInDeviceNetns reads one sysctl from the private namespace. The value is read after
// the calling thread returns to the Pod namespace, so a failing read cannot strand the caller's
// thread in the wrong namespace.
func readSysctlInDeviceNetns(t *testing.T, device *deviceNetworkNamespace, name string) string {
	t.Helper()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	current, err := netns.Get()
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = current.Close() }()

	if err = unix.Setns(int(device.ns), unix.CLONE_NEWNET); err != nil {
		t.Fatalf("entering the device namespace: %v", err)
	}

	raw, readErr := os.ReadFile(netSysctlPath(name))

	if err = unix.Setns(int(current), unix.CLONE_NEWNET); err != nil {
		t.Fatalf("restoring the Pod namespace: %v", err)
	}

	if readErr != nil {
		t.Fatalf("reading %s inside the device namespace: %v", name, readErr)
	}

	return strings.TrimSpace(string(raw))
}
