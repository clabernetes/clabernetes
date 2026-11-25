package direct_test

import (
	"os/exec"
	"strings"
	"testing"

	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
)

// TestDirectLinkNetem proves link impairments end to end: the peer's connectivity sidecar
// programs a wire endpoint's netem on the sidecar leg that delivers the endpoint's packets, never
// on a device interface, applies a changed impairment live without replacing or restarting either
// device Pod, moves it with the cable when the Link is recabled, removes it with the declaration,
// and the API server rejects an impairment the dataplane cannot program.
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

	for _, nodeName := range []string{"lin1", "lin2", "lin3"} {
		waitForDirectNodeReady(t, namespace, nodeName)
	}

	sender := observeDevicePod(t, namespace, "lin1")
	receiver := observeDevicePod(t, namespace, "lin2")

	// The device containers share their Pod's network namespace, so each lists the sidecar's
	// own netem qdiscs, or none.
	sidecarNetem := []string{"sh", "-c", "tc qdisc show | grep 'netem c9:' || echo none"}

	// lin1's impairment sits on lin2's sidecar leg, where the wire delivers lin1's packets, and
	// lin1's own interface is left alone.
	waitForDeviceCommand(t, namespace, receiver, sidecarNetem, "qdisc netem c9: dev c9ss")
	waitForDeviceCommand(t, namespace, receiver, sidecarNetem, "delay 50ms  5ms")
	waitForDeviceCommand(t, namespace, sender, sidecarNetem, "none")

	// The delay applies to the packets lin1 sends, so every round trip takes at least 40ms, the
	// delay less its jitter with some margin. busybox awk needs the comparison in parentheses.
	waitForDeviceCommand(
		t,
		namespace,
		sender,
		[]string{
			"sh", "-c",
			"ping -c 3 -W 2 192.168.1.1 | awk -F' = ' '/min/ {split($2, rtt, \"/\"); print (rtt[1] >= 40)}'",
		},
		"1",
	)

	podUIDs := map[string]string{}
	for _, device := range []devicePodObservation{sender, receiver} {
		podUIDs[device.podName] = devicePodUID(t, namespace, device.podName)
	}

	kubectl(
		t,
		"patch", "link.c9s.run", "lin1-eth1-lin2-eth1", "--namespace", namespace,
		"--type", "merge", "-p", `{"spec":{"endpointA":{"netem":{"delay":"200ms"}}}}`,
	)

	waitForDeviceCommand(t, namespace, receiver, sidecarNetem, "delay 200ms  5ms")

	// A changed impairment is applied live: both Pods run on with no restarted container.
	for nodeName, device := range map[string]devicePodObservation{
		"lin1": sender, "lin2": receiver,
	} {
		if current := observeDevicePod(t, namespace, nodeName); current.podName != device.podName ||
			devicePodUID(t, namespace, current.podName) != podUIDs[device.podName] {
			t.Fatalf("changing the impairment replaced the device Pod %q", device.podName)
		}

		if restarts := kubectl(
			t,
			"get", "pod", device.podName, "--namespace", namespace,
			"-o", "jsonpath={.status.containerStatuses[*].restartCount}",
		); strings.Trim(restarts, "0 ") != "" {
			t.Fatalf("changing the impairment restarted containers: restart counts %q", restarts)
		}
	}

	// The impairment follows the cable: recabling endpoint B from lin2 to lin3 moves lin1's
	// impairment from lin2's sidecar leg to lin3's.
	recabled := observeDevicePod(t, namespace, "lin3")

	kubectl(
		t,
		"patch", "link.c9s.run", "lin1-eth1-lin2-eth1", "--namespace", namespace,
		"--type", "merge", "-p", `{"spec":{"endpointB":{"nodeName":"lin3"}}}`,
	)

	waitForDeviceCommand(t, namespace, recabled, sidecarNetem, "delay 200ms  5ms")
	waitForDeviceCommand(t, namespace, receiver, sidecarNetem, "none")

	// lin3 takes over lin2's address once its end of the cable exists, and the round trip
	// carries the changed delay. Flushing the neighbor entry drops lin2's stale MAC.
	kubectl(
		t,
		"exec", "--namespace", namespace, recabled.podName, "-c", recabled.containerName,
		"--", "ip", "address", "add", "192.168.1.1/31", "dev", "eth1",
	)
	waitForDeviceCommand(
		t,
		namespace,
		sender,
		[]string{
			"sh", "-c",
			"ip neigh flush dev eth1; ping -c 3 -W 2 192.168.1.1 | " +
				"awk -F' = ' '/min/ {split($2, rtt, \"/\"); print (rtt[1] >= 190)}'",
		},
		"1",
	)

	kubectl(
		t,
		"patch", "link.c9s.run", "lin1-eth1-lin2-eth1", "--namespace", namespace,
		"--type", "json", "-p", `[{"op":"remove","path":"/spec/endpointA/netem"}]`,
	)

	// Without the declaration the sidecar removes its qdisc.
	waitForDeviceCommand(t, namespace, recabled, sidecarNetem, "none")

	// Admission rejects an impairment the dataplane cannot program.
	for _, test := range []struct {
		name    string
		payload string
	}{
		{
			name:    "jitter without delay",
			payload: `{"spec":{"endpointA":{"netem":{"jitter":"5ms"}}}}`,
		},
		{
			name:    "jitter with zero delay",
			payload: `{"spec":{"endpointA":{"netem":{"delay":"0s","jitter":"5ms"}}}}`,
		},
	} {
		invalid := exec.CommandContext( //nolint:gosec // kubectl arguments are test-controlled.
			t.Context(),
			"kubectl", "patch", "link.c9s.run", "lin1-eth1-lin2-eth1", "--namespace", namespace,
			"--type", "merge", "-p", test.payload,
		)

		output, err := invalid.CombinedOutput()
		if err == nil ||
			!strings.Contains(string(output), "jitter cannot be set without setting delay") {
			t.Fatalf("%s was not rejected: %v\n%s", test.name, err, output)
		}
	}
}

func devicePodUID(t *testing.T, namespace, podName string) string {
	t.Helper()

	return kubectl(
		t,
		"get", "pod", podName, "--namespace", namespace, "-o", "jsonpath={.metadata.uid}",
	)
}
