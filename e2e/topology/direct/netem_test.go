package direct_test

import (
	"os/exec"
	"strings"
	"testing"

	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
)

// TestDirectLinkNetem proves link impairments end to end: the connectivity sidecar programs a
// Link endpoint's netem on the device interface, applies a changed impairment live without
// replacing or restarting the device Pod, removes its impairment with the declaration, and the
// API server rejects an impairment the dataplane cannot program.
func TestDirectLinkNetem(t *testing.T) {
	t.Parallel()

	testName := "topology-direct-netem"

	namespace := clabernetestesthelper.NewTestNamespace(testName)

	clabernetestesthelper.KubectlCreateNamespace(t, namespace)

	defer func() {
		if t.Failed() {
			clabernetestesthelper.DumpNamespaceDiagnostics(t, namespace)
		}

		if !*clabernetestesthelper.SkipCleanup {
			t.Logf("deleting namespace %q used in test %q", namespace, testName)
			clabernetestesthelper.KubectlDeleteNamespace(t, namespace)
		}
	}()

	clabernetestesthelper.KubectlFileOp(
		t,
		clabernetestesthelper.Apply,
		namespace,
		"test-fixtures/30-linux-netem.yaml",
	)

	for _, nodeName := range []string{"lin1", "lin2"} {
		waitForDirectNodeReady(t, namespace, nodeName)
	}

	device := observeDevicePod(t, namespace, "lin1")
	qdisc := []string{"tc", "qdisc", "show", "dev", "eth1"}

	// The device sees the impairment on its own interface, installed under the sidecar's handle.
	waitForDeviceCommand(t, namespace, device, qdisc, "qdisc netem c9: root")
	waitForDeviceCommand(t, namespace, device, qdisc, "delay 50ms  5ms")

	// The delay applies to the packets lin1 sends, so every round trip takes at least 40ms, the
	// delay less its jitter with some margin. busybox awk needs the comparison in parentheses.
	waitForDeviceCommand(
		t,
		namespace,
		device,
		[]string{
			"sh", "-c",
			"ping -c 3 -W 2 192.168.1.1 | awk -F' = ' '/min/ {split($2, rtt, \"/\"); print (rtt[1] >= 40)}'",
		},
		"1",
	)

	podUID := devicePodUID(t, namespace, device.podName)

	kubectl(
		t,
		"patch", "link.c9s.run", "lin1-eth1-lin2-eth1", "--namespace", namespace,
		"--type", "merge", "-p", `{"spec":{"endpointA":{"netem":{"delay":"200ms"}}}}`,
	)

	waitForDeviceCommand(t, namespace, device, qdisc, "delay 200ms  5ms")

	// A changed impairment is applied live: the same Pod runs with no restarted container.
	if current := observeDevicePod(t, namespace, "lin1"); current.podName != device.podName ||
		devicePodUID(t, namespace, current.podName) != podUID {
		t.Fatalf("changing the impairment replaced the device Pod %q", device.podName)
	}

	if restarts := kubectl(
		t,
		"get", "pod", device.podName, "--namespace", namespace,
		"-o", "jsonpath={.status.containerStatuses[*].restartCount}",
	); strings.Trim(restarts, "0 ") != "" {
		t.Fatalf("changing the impairment restarted containers: restart counts %q", restarts)
	}

	kubectl(
		t,
		"patch", "link.c9s.run", "lin1-eth1-lin2-eth1", "--namespace", namespace,
		"--type", "json", "-p", `[{"op":"remove","path":"/spec/endpointA/netem"}]`,
	)

	// Without the declaration the sidecar removes its qdisc and the veth default returns.
	waitForDeviceCommand(t, namespace, device, qdisc, "qdisc noqueue 0: root")

	// Admission rejects an impairment the dataplane cannot program.
	invalid := exec.CommandContext( //nolint:gosec // kubectl arguments are test-controlled.
		t.Context(),
		"kubectl", "patch", "link.c9s.run", "lin1-eth1-lin2-eth1", "--namespace", namespace,
		"--type", "merge", "-p", `{"spec":{"endpointA":{"netem":{"jitter":"5ms"}}}}`,
	)

	output, err := invalid.CombinedOutput()
	if err == nil ||
		!strings.Contains(string(output), "jitter cannot be set without setting delay") {
		t.Fatalf("jitter without delay was not rejected: %v\n%s", err, output)
	}
}

func devicePodUID(t *testing.T, namespace, podName string) string {
	t.Helper()

	return kubectl(
		t,
		"get", "pod", podName, "--namespace", namespace, "-o", "jsonpath={.metadata.uid}",
	)
}
