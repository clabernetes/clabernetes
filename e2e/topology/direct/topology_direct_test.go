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

	// Dataplane over the vxlan Link: the startup configs address ethernet-1/1 on both ends, so
	// srl1 must reach srl2 across the tunnel from inside the actual device container.
	waitForDataplanePing(t, namespace, initialPods["srl1"], "192.168.0.1")

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

// waitForDataplanePing execs into the device container and pings the peer's link address from
// the device's default network instance until it answers or the deadline passes.
func waitForDataplanePing(
	t *testing.T,
	namespace string,
	device devicePodObservation,
	target string,
) {
	t.Helper()

	deadline := time.Now().Add(directNodeReadyTimeout)

	var lastOutput []byte
	for time.Now().Before(deadline) {
		cmd := exec.CommandContext( //nolint:gosec
			t.Context(),
			"kubectl",
			"exec",
			"--namespace",
			namespace,
			device.podName,
			"-c",
			device.containerName,
			"--",
			"ip",
			"netns",
			"exec",
			"srbase-default",
			"ping",
			"-c",
			"2",
			"-W",
			"2",
			target,
		)

		output, err := cmd.CombinedOutput()
		if err == nil {
			return
		}
		lastOutput = output

		time.Sleep(directPollInterval)
	}

	t.Fatalf(
		"device %q never reached %q across the link: %s",
		device.podName,
		target,
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
