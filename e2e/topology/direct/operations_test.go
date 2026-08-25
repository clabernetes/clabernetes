package direct_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
)

const (
	captureTrafficWindow = 20 * time.Second
	pcapHeaderLength     = 24
)

// TestDirectSaveOperation proves the package-owned save lifecycle end to end: the same typed
// lifecycle entrypoint the workload already uses for PostStart runs the imported SaveConfig
// against the live device, with no kind knowledge anywhere in c9s.
func TestDirectSaveOperation(t *testing.T) {
	t.Parallel()

	testName := "topology-direct-save"
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

	waitForDirectNodeReady(t, namespace, "srl1")

	device := observeDevicePod(t, namespace, "srl1")
	saveCommand := lifecyclePhaseCommand(t, namespace, device, "Save")

	command := append([]string{
		"exec",
		"--namespace",
		namespace,
		device.podName,
		"-c",
		device.containerName,
		"--",
	}, saveCommand...)

	cmd := exec.CommandContext( //nolint:gosec // kubectl arguments are test-controlled.
		t.Context(),
		"kubectl",
		command...,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("save lifecycle failed: %v: %s", err, output)
	}

	if !strings.Contains(string(output), "saved") {
		t.Fatalf("save lifecycle reported no saved configuration: %s", output)
	}
}

// TestDirectPacketCaptureOperation proves plan-scoped packet capture: the connectivity helper
// streams pcap for a plan-owned interface while dataplane traffic crosses the fabric.
func TestDirectPacketCaptureOperation(t *testing.T) {
	t.Parallel()

	testName := "topology-direct-capture"
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

	// The dataplane must be alive before capturing so the window is guaranteed traffic.
	waitForDeviceCommand(
		t,
		namespace,
		device,
		[]string{"ping", "-c", "2", "-W", "2", "192.168.1.1"},
		" 0% packet loss",
	)

	nodeID, interfaceName := planInterfaceTarget(t, namespace, device, "lin1")

	trafficDone := make(chan struct{})

	go func() {
		defer close(trafficDone)

		trafficCmd := exec.CommandContext( //nolint:gosec // kubectl args are test-controlled.
			t.Context(),
			"kubectl",
			"exec",
			"--namespace",
			namespace,
			device.podName,
			"-c",
			device.containerName,
			"--",
			"ping",
			"-c",
			"10",
			"-i",
			"1",
			"192.168.1.1",
		)
		_ = trafficCmd.Run()
	}()

	captureCmd := exec.CommandContext( //nolint:gosec // kubectl arguments are test-controlled.
		t.Context(),
		"kubectl",
		"exec",
		"--namespace",
		namespace,
		device.podName,
		"-c",
		connectivityContainerName(t, namespace, device),
		"--",
		"/clabernetes/manager",
		"node-runtime",
		"packet-capture",
		"--plan",
		"/var/run/clabernetes/plan/plan.json",
		"--input",
		"/var/run/clabernetes/input/input.json",
		"--connectivityRevision",
		"/var/run/clabernetes/connectivity-revision/revision.json",
		"--nodeID",
		nodeID,
		"--interface",
		interfaceName,
		"--packetLimit",
		"4",
		"--duration",
		captureTrafficWindow.String(),
	)

	pcap, err := captureCmd.Output()

	<-trafficDone

	if err != nil {
		t.Fatalf("packet capture failed: %v", err)
	}

	if len(pcap) <= pcapHeaderLength {
		t.Fatalf("packet capture produced no packet records: %d bytes", len(pcap))
	}

	if !bytes.HasPrefix(pcap, []byte{0xd4, 0xc3, 0xb2, 0xa1}) &&
		!bytes.HasPrefix(pcap, []byte{0xa1, 0xb2, 0xc3, 0xd4}) {
		t.Fatalf("packet capture stream is not pcap: % x", pcap[:8])
	}
}

// lifecyclePhaseCommand derives a typed lifecycle invocation from the workload's own PostStart
// hook so operations always exercise exactly the shipped argument surface.
func lifecyclePhaseCommand(
	t *testing.T,
	namespace string,
	device devicePodObservation,
	phase string,
) []string {
	t.Helper()

	cmd := exec.CommandContext( //nolint:gosec // kubectl arguments are test-controlled.
		t.Context(),
		"kubectl",
		"get",
		"pod",
		"--namespace",
		namespace,
		device.podName,
		"-o",
		fmt.Sprintf(
			`jsonpath={.spec.containers[?(@.name==%q)].lifecycle.postStart.exec.command}`,
			device.containerName,
		),
	)

	raw := clabernetestesthelper.Execute(t, cmd)

	var command []string

	err := json.Unmarshal(raw, &command)
	if err != nil {
		t.Fatalf("decoding PostStart command: %v: %s", err, raw)
	}

	for index, argument := range command {
		if argument == "--phase" && index+1 < len(command) {
			command[index+1] = phase

			return command
		}
	}

	t.Fatalf("PostStart command has no --phase argument: %v", command)

	return nil
}

// planInterfaceTarget resolves the plan node ID and one plan-owned interface name for a logical
// node by reading the immutable input projected into the workload. The read happens in the
// device container (the connectivity helper image carries no shell tools) at the input path the
// workload's own lifecycle hook declares.
func planInterfaceTarget(
	t *testing.T,
	namespace string,
	device devicePodObservation,
	nodeName string,
) (string, string) {
	t.Helper()

	inputPath := ""

	postStart := lifecyclePhaseCommand(t, namespace, device, "PostStart")
	for index, argument := range postStart {
		if argument == "--input" && index+1 < len(postStart) {
			inputPath = postStart[index+1]

			break
		}
	}

	if inputPath == "" {
		t.Fatalf("PostStart command declares no --input path: %v", postStart)
	}

	cmd := exec.CommandContext( //nolint:gosec // kubectl arguments are test-controlled.
		t.Context(),
		"kubectl",
		"exec",
		"--namespace",
		namespace,
		device.podName,
		"-c",
		device.containerName,
		"--",
		"cat",
		inputPath,
	)

	raw := clabernetestesthelper.Execute(t, cmd)

	var input struct {
		Nodes []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"nodes"`
		Interfaces []struct {
			NodeID string `json:"nodeID"`
			Name   string `json:"name"`
		} `json:"interfaces"`
	}

	err := json.Unmarshal(raw, &input)
	if err != nil {
		t.Fatalf("decoding plan input: %v", err)
	}

	nodeID := ""

	for _, node := range input.Nodes {
		if node.Name == nodeName {
			nodeID = node.ID

			break
		}
	}

	if nodeID == "" {
		t.Fatalf("plan input has no node named %q", nodeName)
	}

	for _, planInterface := range input.Interfaces {
		if planInterface.NodeID == nodeID {
			return nodeID, planInterface.Name
		}
	}

	t.Fatalf("plan input has no interface for node %q", nodeName)

	return "", ""
}

func connectivityContainerName(
	t *testing.T,
	namespace string,
	device devicePodObservation,
) string {
	t.Helper()

	// The connectivity helper is a native sidecar, so it lives in initContainers.
	cmd := exec.CommandContext( //nolint:gosec // kubectl arguments are test-controlled.
		t.Context(),
		"kubectl",
		"get",
		"pod",
		"--namespace",
		namespace,
		device.podName,
		"-o",
		`jsonpath={range .spec.initContainers[*]}{.name}{"\n"}{end}`+
			`{range .spec.containers[*]}{.name}{"\n"}{end}`,
	)

	for name := range strings.SplitSeq(string(clabernetestesthelper.Execute(t, cmd)), "\n") {
		if name == "clabwire" {
			return name
		}
	}

	t.Fatalf("pod %q has no connectivity helper container", device.podName)

	return ""
}
