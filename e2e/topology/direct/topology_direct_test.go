package direct_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
)

const (
	directNodeReadyTimeout = 12 * time.Minute
	directPollInterval     = 5 * time.Second
)

func TestMain(m *testing.M) {
	clabernetestesthelper.Flags()

	os.Exit(m.Run())
}

// TestNodeLinkDirect exercises the primary api without any Topology object against the direct
// device runtime: hand written Node, Link, and NodeProfile objects must yield device Pods
// whose application container is the actual device image, planning worker artifacts must be
// collected once their records are persisted, and a link rewire that the plan declares live
// must be applied without rolling the device Pods.
func TestNodeLinkDirect(t *testing.T) {
	t.Parallel()

	testName := "topology-direct"

	namespace := clabernetestesthelper.NewTestNamespace(testName)

	clabernetestesthelper.KubectlCreateNamespace(t, namespace)

	defer func() {
		if !*clabernetestesthelper.SkipCleanup {
			t.Logf("deleting namespace %q used in test %q", namespace, testName)
			clabernetestesthelper.KubectlDeleteNamespace(t, namespace)
		}
	}()

	clabernetestesthelper.KubectlFileOp(
		t,
		clabernetestesthelper.Apply,
		namespace,
		"test-fixtures/10-apply.yaml",
	)

	nodeNames := []string{"srl1", "srl2"}
	for _, nodeName := range nodeNames {
		waitForDirectNodeReady(t, namespace, nodeName)
	}

	initialPods := map[string]devicePodObservation{}

	for _, nodeName := range nodeNames {
		observation := observeDevicePod(t, namespace, nodeName)
		if !strings.Contains(observation.image, "srlinux") {
			t.Fatalf(
				"device container for %q runs %q, want the actual device image",
				nodeName,
				observation.image,
			)
		}

		initialPods[nodeName] = observation
	}

	// The embedded startup configuration must have been materialized, planned, prepared, and
	// committed by the imported package hooks: ethernet-1/1.0 carries the configured address
	// inside the running device.
	waitForDeviceCommand(
		t,
		namespace,
		initialPods["srl1"],
		[]string{"ip", "netns", "exec", "srbase-default", "ip", "-br", "addr", "show"},
		"192.168.0.0/31",
	)

	// Dataplane across the Link: fabric transports terminate in the worker host namespace, so
	// the wire works even though SR Linux takes ownership of the Pod's primary interface.
	waitForDeviceCommand(
		t,
		namespace,
		initialPods["srl1"],
		[]string{
			"ip",
			"netns",
			"exec",
			"srbase-default",
			"ping",
			"-c",
			"2",
			"-W",
			"2",
			"192.168.0.1",
		},
		" 0% packet loss",
	)

	waitForWorkerArtifactCollection(t, namespace)

	initialDigest := nodePlanDigest(t, namespace, "srl1")

	clabernetestesthelper.KubectlFileOp(
		t,
		clabernetestesthelper.Apply,
		namespace,
		"test-fixtures/20-apply.yaml",
	)

	waitForPlanDigestChange(t, namespace, "srl1", initialDigest)

	for _, nodeName := range nodeNames {
		waitForDirectNodeReady(t, namespace, nodeName)
	}

	for _, nodeName := range nodeNames {
		observation := observeDevicePod(t, namespace, nodeName)
		if observation.podName != initialPods[nodeName].podName {
			t.Fatalf(
				"link rewire rolled device Pod for %q: %q -> %q, want a live update",
				nodeName,
				initialPods[nodeName].podName,
				observation.podName,
			)
		}
	}

	waitForWorkerArtifactCollection(t, namespace)
}

type devicePodObservation struct {
	podName       string
	containerName string
	image         string
}

// TestLinuxDataplaneDirect proves generic dataplane across a direct cross-Pod Link: two
// linux-kind devices address their link interfaces through imported exec intent and must reach
// each other across the fabric wire from inside the actual device containers, at the full
// advertised interface MTU, with carrier propagating across the wire for both the graceful
// interface-down case and the endpoint-loss case.
//
//nolint:funlen // one sequential dataplane story against a single deployed lab.
func TestLinuxDataplaneDirect(t *testing.T) {
	t.Parallel()

	testName := "topology-direct-linux"

	namespace := clabernetestesthelper.NewTestNamespace(testName)

	clabernetestesthelper.KubectlCreateNamespace(t, namespace)

	defer func() {
		if !*clabernetestesthelper.SkipCleanup {
			t.Logf("deleting namespace %q used in test %q", namespace, testName)
			clabernetestesthelper.KubectlDeleteNamespace(t, namespace)
		}
	}()

	clabernetestesthelper.KubectlFileOp(
		t,
		clabernetestesthelper.Apply,
		namespace,
		"test-fixtures/30-linux-dataplane.yaml",
	)

	for _, nodeName := range []string{"lin1", "lin2"} {
		waitForDirectNodeReady(t, namespace, nodeName)
	}

	device := observeDevicePod(t, namespace, "lin1")
	waitForDeviceCommand(
		t,
		namespace,
		device,
		[]string{"ping", "-c", "2", "-W", "2", "192.168.1.1"},
		" 0% packet loss",
	)

	// The advertised interface MTU must be carryable end to end: a don't-fragment ping filling
	// the advertised MTU exactly would black-hole if the transport carried less than the
	// device-facing interface claims. With the wire fragmenting to the underlay, this proves
	// the containerlab-default 9500 crossing the kind cluster's 1500-byte Pod network.
	linkMTU := deviceInterfaceMTU(t, namespace, device, "eth1")
	waitForDeviceCommand(
		t,
		namespace,
		device,
		[]string{
			"ping",
			"-M", "do",
			"-s", strconv.Itoa(linkMTU - 28),
			"-c", "2",
			"-W", "2",
			"192.168.1.1",
		},
		" 0% packet loss",
	)

	peer := observeDevicePod(t, namespace, "lin2")

	// Graceful carrier propagation: taking one device leg down must show as loss of carrier on
	// the peer device's interface, which itself stays administratively up -- and recovery must
	// follow the same path.
	deviceCommand(t, namespace, device, []string{"ip", "link", "set", "eth1", "down"})

	waitForDeviceCommand(
		t,
		namespace,
		peer,
		[]string{"ip", "link", "show", "eth1"},
		"NO-CARRIER",
	)

	deviceCommand(t, namespace, device, []string{"ip", "link", "set", "eth1", "up"})

	waitForDeviceCommand(
		t,
		namespace,
		peer,
		[]string{"ip", "link", "show", "eth1"},
		"state UP",
	)

	waitForDeviceCommand(
		t,
		namespace,
		peer,
		[]string{"ping", "-c", "2", "-W", "2", "192.168.1.0"},
		" 0% packet loss",
	)

	// Crash carrier propagation: killing one endpoint Pod outright must take the peer's
	// interface carrier-down within the wire's liveness bound. The carrier loss is transient
	// -- the replacement Pod restores it within seconds -- so a sub-second watcher inside the
	// peer container must already be sampling when the Pod dies; polled execs would race the
	// recovery.
	carrierWatch := startDeviceCarrierWatch(t, namespace, peer, "eth1")

	clabernetestesthelper.Execute(t, exec.CommandContext(
		t.Context(),
		"kubectl",
		"delete", "pod", "--namespace", namespace, device.podName, "--wait=false",
	))

	if output := carrierWatch(); !strings.Contains(output, "WIRE-CARRIER-LOST") {
		t.Fatalf("peer never observed carrier loss after endpoint Pod death: %s", output)
	}

	waitForDirectNodeReady(t, namespace, "lin1")

	waitForDeviceCommand(
		t,
		namespace,
		peer,
		[]string{"ip", "link", "show", "eth1"},
		"state UP",
	)

	waitForDeviceCommand(
		t,
		namespace,
		peer,
		[]string{"ping", "-c", "2", "-W", "2", "192.168.1.0"},
		" 0% packet loss",
	)

	waitForWorkerArtifactCollection(t, namespace)
}

// startDeviceCarrierWatch starts a sub-second carrier sampler inside the device container and
// returns a function that waits for its verdict: WIRE-CARRIER-LOST as soon as the interface
// loses carrier, or WIRE-CARRIER-HELD when it never does within the watch window.
func startDeviceCarrierWatch(
	t *testing.T,
	namespace string,
	device devicePodObservation,
	interfaceName string,
) func() string {
	t.Helper()

	script := "for i in $(seq 1 1200); do " +
		"[ \"$(cat /sys/class/net/" + interfaceName + "/carrier 2>/dev/null)\" = \"0\" ] && " +
		"echo WIRE-CARRIER-LOST && exit 0; sleep 0.1; done; echo WIRE-CARRIER-HELD"

	cmd := exec.CommandContext( //nolint:gosec
		t.Context(),
		"kubectl",
		"exec", "--namespace", namespace, device.podName, "-c", device.containerName,
		"--",
		"sh", "-c", script,
	)

	var output strings.Builder

	cmd.Stdout = &output
	cmd.Stderr = &output

	if err := cmd.Start(); err != nil {
		t.Fatalf("starting carrier watch in %q: %v", device.podName, err)
	}

	return func() string {
		if err := cmd.Wait(); err != nil {
			t.Logf("carrier watch in %q exited with %v", device.podName, err)
		}

		return output.String()
	}
}

// deviceCommand execs one command inside the device container and fails the test on error.
func deviceCommand(
	t *testing.T,
	namespace string,
	device devicePodObservation,
	command []string,
) {
	t.Helper()

	arguments := append(
		[]string{
			"exec", "--namespace", namespace, device.podName, "-c", device.containerName,
			"--",
		},
		command...,
	)

	clabernetestesthelper.Execute(
		t,
		exec.CommandContext(t.Context(), "kubectl", arguments...), //nolint:gosec
	)
}

// deviceInterfaceMTU reads one interface MTU from inside the device container.
func deviceInterfaceMTU(
	t *testing.T,
	namespace string,
	device devicePodObservation,
	interfaceName string,
) int {
	t.Helper()

	cmd := exec.CommandContext( //nolint:gosec
		t.Context(),
		"kubectl",
		"exec", "--namespace", namespace, device.podName, "-c", device.containerName,
		"--",
		"cat", "/sys/class/net/"+interfaceName+"/mtu",
	)

	output := clabernetestesthelper.Execute(t, cmd)

	mtu, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil || mtu <= 0 {
		t.Fatalf(
			"device %q interface %q MTU is unreadable: %q",
			device.podName,
			interfaceName,
			output,
		)
	}

	return mtu
}

// waitForDeviceCommand execs a command inside the device container until its combined output
// contains the expected substring or the deadline passes.
func waitForDeviceCommand(
	t *testing.T,
	namespace string,
	device devicePodObservation,
	command []string,
	expect string,
) {
	t.Helper()

	deadline := time.Now().Add(directNodeReadyTimeout)

	var lastOutput []byte

	for time.Now().Before(deadline) {
		arguments := append(
			[]string{
				"exec", "--namespace", namespace, device.podName, "-c", device.containerName,
				"--",
			},
			command...,
		)
		cmd := exec.CommandContext(t.Context(), "kubectl", arguments...) //nolint:gosec

		output, err := cmd.CombinedOutput()
		if err == nil && strings.Contains(string(output), expect) {
			return
		}

		lastOutput = output

		time.Sleep(directPollInterval)
	}

	t.Fatalf(
		"device %q never produced %q from %v: %s",
		device.podName,
		expect,
		command,
		strings.TrimSpace(string(lastOutput)),
	)
}

func waitForDirectNodeReady(t *testing.T, namespace, nodeName string) {
	t.Helper()

	cmd := exec.CommandContext( //nolint:gosec
		t.Context(),
		"kubectl",
		"wait",
		"--for=jsonpath={.status.readiness}=ready",
		"--timeout="+directNodeReadyTimeout.String(),
		"--namespace",
		namespace,
		"node.c9s.run/"+nodeName,
	)

	clabernetestesthelper.Execute(t, cmd)
}

func nodePlanDigest(t *testing.T, namespace, nodeName string) string {
	t.Helper()

	cmd := exec.CommandContext( //nolint:gosec
		t.Context(),
		"kubectl",
		"get",
		"node.c9s.run",
		nodeName,
		"--namespace",
		namespace,
		"-o",
		"jsonpath={.status.planDigest}",
	)

	return strings.TrimSpace(string(clabernetestesthelper.Execute(t, cmd)))
}

func waitForPlanDigestChange(t *testing.T, namespace, nodeName, previousDigest string) {
	t.Helper()

	deadline := time.Now().Add(directNodeReadyTimeout)
	for time.Now().Before(deadline) {
		if digest := nodePlanDigest(t, namespace, nodeName); digest != "" &&
			digest != previousDigest {
			return
		}

		time.Sleep(directPollInterval)
	}

	t.Fatalf("plan digest for %q never moved off %q", nodeName, previousDigest)
}

// observeDevicePod returns the single running device Pod for a Node along with its application
// container image, failing when the Pod is not exactly one or not fully ready.
func observeDevicePod(t *testing.T, namespace, nodeName string) devicePodObservation {
	t.Helper()

	pods := listPods(t, namespace, "c9s.run/direct-workload="+nodeName)
	if len(pods.Items) != 1 {
		t.Fatalf("device Pods for %q = %d, want exactly 1", nodeName, len(pods.Items))
	}

	pod := pods.Items[0]
	for _, status := range pod.Status.ContainerStatuses {
		if !status.Ready {
			t.Fatalf("device Pod %q container %q is not ready", pod.Metadata.Name, status.Name)
		}
	}

	image := ""
	containerName := ""

	for _, container := range pod.Spec.Containers {
		if strings.HasPrefix(container.Name, "device-") {
			image = container.Image
			containerName = container.Name
		}
	}

	if image == "" {
		t.Fatalf("device Pod %q has no device application container", pod.Metadata.Name)
	}

	return devicePodObservation{
		podName:       pod.Metadata.Name,
		containerName: containerName,
		image:         image,
	}
}

// waitForWorkerArtifactCollection asserts that completed planning and image-discovery worker
// Pods are removed once their records are persisted rather than accumulating forever.
func waitForWorkerArtifactCollection(t *testing.T, namespace string) {
	t.Helper()

	deadline := time.Now().Add(directNodeReadyTimeout)

	var remaining int

	for time.Now().Before(deadline) {
		pods := listPods(
			t,
			namespace,
			"app.kubernetes.io/name=clabernetes-device-planner",
		)

		remaining = len(pods.Items)
		if remaining == 0 {
			return
		}

		time.Sleep(directPollInterval)
	}

	t.Fatalf("planning worker Pods were never collected: %d remain", remaining)
}

type podList struct {
	Items []struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Spec struct {
			Containers []struct {
				Name  string `json:"name"`
				Image string `json:"image"`
			} `json:"containers"`
		} `json:"spec"`
		Status struct {
			ContainerStatuses []struct {
				Name  string `json:"name"`
				Ready bool   `json:"ready"`
			} `json:"containerStatuses"`
		} `json:"status"`
	} `json:"items"`
}

func listPods(t *testing.T, namespace, selector string) podList {
	t.Helper()

	cmd := exec.CommandContext( //nolint:gosec
		t.Context(),
		"kubectl",
		"get",
		"pods",
		"--namespace",
		namespace,
		"--selector",
		selector,
		"-o",
		"json",
	)

	output := clabernetestesthelper.Execute(t, cmd)

	var pods podList
	if err := json.Unmarshal(output, &pods); err != nil {
		t.Fatalf("failed decoding Pod list: %s", err)
	}

	return pods
}
