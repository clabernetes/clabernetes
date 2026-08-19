package direct_test

import (
	"encoding/json"
	"os"
	"os/exec"
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
// device runtime: hand written Node, Link, and LauncherProfile objects must yield device Pods
// whose application container is the actual device image, planning worker artifacts must be
// collected once their records are persisted, and a link rewire that the plan declares live
// must be applied without rolling the device Pods.
func TestNodeLinkDirect(t *testing.T) {
	t.Parallel()

	clabernetestesthelper.SkipUnlessDeviceRuntimeMode(t, "direct")

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
	// inside the running device. Dataplane across the vxlan Link is asserted by the linux-kind
	// test below: SR Linux takes ownership of the Pod's primary interface, which currently
	// leaves the in-Pod VXLAN underlay without kernel routes -- a recorded generic gap whose
	// fix is host-namespace fabric termination, not a kind-specific repair.
	waitForDeviceCommand(
		t,
		namespace,
		initialPods["srl1"],
		[]string{"ip", "netns", "exec", "srbase-default", "ip", "-br", "addr", "show"},
		"192.168.0.0/31",
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

// TestLinuxDataplaneDirect proves generic dataplane across a direct vxlan Link: two linux-kind
// devices address their link interfaces through imported exec intent and must reach each other
// across the tunnel from inside the actual device containers.
func TestLinuxDataplaneDirect(t *testing.T) {
	t.Parallel()

	clabernetestesthelper.SkipUnlessDeviceRuntimeMode(t, "direct")

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

	waitForWorkerArtifactCollection(t, namespace)
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
