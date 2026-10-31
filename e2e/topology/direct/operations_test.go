package direct_test

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
)

const (
	captureTrafficWindow            = 8 * time.Second
	captureStdinCloseExitBudget     = 5 * time.Second
	pcapHeaderLength                = 24
	pcapngSectionHeaderBlock        = 0x0a0d0d0a
	pcapngInterfaceDescriptionBlock = 0x00000001
	pcapngEnhancedPacketBlock       = 0x00000006
	pcapngIfNameOption              = 2
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

// TestDirectPacketCaptureOperation proves the packet capture client contract: the connectivity
// helper lists the Node's capturable interfaces, streams pcapng for two interfaces selected by
// Node name while dataplane traffic crosses both links, runs a second capture on the same Pod
// at the same time through the original explicit-path invocation, and accepts a follow-up
// capture the moment the first one ends. A capture with --stop-on-stdin-close ends within
// seconds of its client closing stdin, over both remotecommand transports.
func TestDirectPacketCaptureOperation(t *testing.T) {
	t.Parallel()

	testName := "topology-direct-capture"
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
		"test-fixtures/30-linux-capture.yaml",
	)

	for _, nodeName := range []string{"lin1", "lin2"} {
		waitForDirectNodeReady(t, namespace, nodeName)
	}

	device := observeDevicePod(t, namespace, "lin1")
	sidecar := connectivityContainerName(t, namespace, device)

	// The dataplane must be alive on both links before capturing so the window is guaranteed
	// traffic on each.
	for _, address := range []string{"192.168.1.1", "192.168.2.1"} {
		waitForDeviceCommand(
			t,
			namespace,
			device,
			[]string{"ping", "-c", "2", "-W", "2", address},
			" 0% packet loss",
		)
	}

	listed := capturableInterfaces(t, namespace, device, sidecar, "--nodeName", "lin1")
	if len(listed) != 2 ||
		listed[0] != (capturableInterface{"lin1", "eth1", "lin2", "eth1"}) ||
		listed[1] != (capturableInterface{"lin1", "eth2", "lin2", "eth2"}) {
		t.Fatalf("lin1 capturable interfaces = %#v", listed)
	}

	if podWide := capturableInterfaces(t, namespace, device, sidecar); len(podWide) != 2 {
		t.Fatalf("Pod-wide capturable interfaces = %#v", podWide)
	}

	nodeID, _ := planInterfaceTarget(t, namespace, device, "lin1")

	trafficDone := make(chan struct{})

	go func() {
		defer close(trafficDone)

		pings := make(chan struct{}, 2)

		for _, address := range []string{"192.168.1.1", "192.168.2.1"} {
			go func() {
				defer func() { pings <- struct{}{} }()

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
					"40",
					"-i",
					"0.5",
					address,
				)
				_ = trafficCmd.Run()
			}()
		}

		<-pings
		<-pings
	}()

	pcapngCapture := make(chan captureResult, 1)

	go func() {
		pcapngCapture <- runCapture(
			t, namespace, device, sidecar,
			"--nodeName", "lin1", "--interface", "eth1", "--interface", "eth2",
			"--format", "pcapng", "--duration", captureTrafficWindow.String(),
		)
	}()

	// The original invocation with explicit paths and the Node UID runs concurrently on the
	// same Pod and the same interface.
	pcap := runCapture(
		t, namespace, device, sidecar,
		"--plan", "/var/run/clabernetes/plan/plan.json",
		"--input", "/var/run/clabernetes/input/input.json",
		"--connectivityRevision", "/var/run/clabernetes/connectivity-revision/revision.json",
		"--nodeID", nodeID, "--interface", "eth1", "--duration", captureTrafficWindow.String(),
	)

	pcapng := <-pcapngCapture

	// A follow-up capture starts the moment the previous one ends, as clients chaining calls
	// past the one-hour bound do.
	chained := runCapture(
		t, namespace, device, sidecar,
		"--nodeName", "lin1", "--interface", "eth2", "--format", "pcapng",
		"--packetLimit", "2", "--duration", captureTrafficWindow.String(),
	)

	<-trafficDone

	verifyContractCaptures(t, pcap, pcapng, chained)

	for _, transport := range []captureTransport{
		{name: "websocket", websockets: "true", evidence: `protocol="v5.channel.k8s.io"`},
		{name: "spdy", websockets: "false", evidence: `verb="POST"`},
	} {
		verifyCaptureStopsWhenStdinCloses(t, namespace, device, sidecar, transport)
	}
}

// captureTransport selects kubectl's remotecommand transport and the -v=6 log line that proves
// it was used: the negotiated websocket subprotocol (v5 carries the stdin CLOSE signal), or the
// SPDY upgrade POST.
type captureTransport struct {
	name       string
	websockets string
	evidence   string
}

// verifyCaptureStopsWhenStdinCloses starts an hour-long capture with stdin attached, closes stdin
// once it runs, and requires the exec to end within the budget with a Succeeded record that names
// the client close. Kubernetes ends an exec only when its process exits, so kubectl returning is
// the process ending.
func verifyCaptureStopsWhenStdinCloses(
	t *testing.T,
	namespace string,
	device devicePodObservation,
	sidecar string,
	transport captureTransport,
) {
	t.Helper()

	cmd := exec.CommandContext( //nolint:gosec // kubectl arguments are test-controlled.
		t.Context(),
		"kubectl",
		"exec", "-v=6", "-i", "--namespace", namespace, device.podName, "-c", sidecar, "--",
		"/clabernetes/manager", "node-runtime", "packet-capture",
		"--nodeName", "lin1", "--interface", "eth1", "--format", "pcapng",
		"--duration", "1h", "--stop-on-stdin-close",
	)
	cmd.Env = append(os.Environ(), "KUBECTL_REMOTE_COMMAND_WEBSOCKETS="+transport.websockets)
	cmd.Stdout = io.Discard

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}

	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}

	var (
		mu     sync.Mutex
		stderr strings.Builder
	)

	started := make(chan struct{})
	stderrDone := make(chan struct{})

	go func() {
		defer close(stderrDone)

		scanner := bufio.NewScanner(stderrPipe)
		scanner.Buffer(make([]byte, 64<<10), 1<<20)

		once := sync.Once{}

		for scanner.Scan() {
			mu.Lock()
			stderr.WriteString(scanner.Text() + "\n")
			mu.Unlock()

			if strings.Contains(scanner.Text(), `"status":"Started"`) {
				once.Do(func() { close(started) })
			}
		}
	}()

	output := func() string {
		mu.Lock()
		defer mu.Unlock()

		return stderr.String()
	}

	select {
	case <-started:
	case <-time.After(time.Minute):
		_ = cmd.Process.Kill()

		t.Fatalf("%s: capture did not start: %s", transport.name, output())
	}

	// Let it capture for a moment, then go away.
	time.Sleep(2 * time.Second)

	closed := time.Now()
	_ = stdin.Close()

	done := make(chan error, 1)

	go func() {
		<-stderrDone

		done <- cmd.Wait()
	}()

	select {
	case err = <-done:
	case <-time.After(captureStdinCloseExitBudget):
		_ = cmd.Process.Kill()

		t.Fatalf("%s: capture still runs %s after stdin closed: %s",
			transport.name, captureStdinCloseExitBudget, output())
	}

	elapsed := time.Since(closed)
	log := output()

	if err != nil {
		t.Fatalf("%s: capture ended with %v: %s", transport.name, err, log)
	}

	if !strings.Contains(log, `"status":"Succeeded"`) ||
		!strings.Contains(log, `"reason":"client closed: stdin reached EOF"`) {
		t.Fatalf("%s: capture audit does not record the client close: %s", transport.name, log)
	}

	if !strings.Contains(log, transport.evidence) {
		t.Fatalf("%s: kubectl log lacks %s: %s", transport.name, transport.evidence, log)
	}

	t.Logf("%s: capture process ended %s after stdin closed", transport.name, elapsed)
}

// verifyContractCaptures checks the audit trail of every capture, the legacy pcap stream, the
// two-interface pcapng stream, and the chained follow-up stream.
func verifyContractCaptures(t *testing.T, pcap, pcapng, chained captureResult) {
	t.Helper()

	for name, result := range map[string]captureResult{
		"pcapng": pcapng, "pcap": pcap, "chained": chained,
	} {
		requireCaptureSucceeded(t, name, result)
	}

	// The helper writes little-endian pcap.
	if len(pcap.stdout) <= pcapHeaderLength ||
		!bytes.HasPrefix(pcap.stdout, []byte{0xd4, 0xc3, 0xb2, 0xa1}) {
		t.Fatalf("packet capture is not pcap with packet records: % x", pcap.stdout)
	}

	names, packets := parsePCAPNGStream(t, pcapng.stdout)
	if len(names) != 2 || names[0] != "eth1" || names[1] != "eth2" ||
		packets[0] == 0 || packets[1] == 0 {
		t.Fatalf("pcapng capture interfaces %v carried packets %v", names, packets)
	}

	if !strings.Contains(pcapng.stderr, `"name":"eth1"`) ||
		!strings.Contains(pcapng.stderr, `"name":"eth2"`) {
		t.Fatalf("pcapng capture audit does not list both interfaces: %s", pcapng.stderr)
	}

	if names, packets = parsePCAPNGStream(t, chained.stdout); len(names) != 1 ||
		packets[0] != 2 {
		t.Fatalf("chained capture interfaces %v carried packets %v", names, packets)
	}
}

func requireCaptureSucceeded(t *testing.T, name string, result captureResult) {
	t.Helper()

	if result.err != nil {
		t.Fatalf("%s capture failed: %v: %s", name, result.err, result.stderr)
	}

	if !strings.Contains(result.stderr, `"status":"Started"`) ||
		!strings.Contains(result.stderr, `"status":"Succeeded"`) {
		t.Fatalf("%s capture audit lacks Started and Succeeded: %s", name, result.stderr)
	}
}

type capturableInterface struct {
	Node          string `json:"node"`
	Interface     string `json:"interface"`
	PeerNode      string `json:"peerNode"`
	PeerInterface string `json:"peerInterface"`
}

type captureResult struct {
	stdout []byte
	stderr string
	err    error
}

func captureCommand(
	t *testing.T,
	namespace string,
	device devicePodObservation,
	sidecar string,
	arguments ...string,
) *exec.Cmd {
	t.Helper()

	return exec.CommandContext( //nolint:gosec // kubectl arguments are test-controlled.
		t.Context(),
		"kubectl",
		append([]string{
			"exec", "--namespace", namespace, device.podName, "-c", sidecar, "--",
			"/clabernetes/manager", "node-runtime", "packet-capture",
		}, arguments...)...,
	)
}

func runCapture(
	t *testing.T,
	namespace string,
	device devicePodObservation,
	sidecar string,
	arguments ...string,
) captureResult {
	t.Helper()

	var stdout, stderr bytes.Buffer

	cmd := captureCommand(t, namespace, device, sidecar, arguments...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	err := cmd.Run()

	return captureResult{stdout: stdout.Bytes(), stderr: stderr.String(), err: err}
}

func capturableInterfaces(
	t *testing.T,
	namespace string,
	device devicePodObservation,
	sidecar string,
	arguments ...string,
) []capturableInterface {
	t.Helper()

	raw := clabernetestesthelper.Execute(
		t,
		captureCommand(
			t,
			namespace,
			device,
			sidecar,
			append(arguments, "--list-interfaces")...,
		),
	)

	var interfaces []capturableInterface

	err := json.Unmarshal(raw, &interfaces)
	if err != nil {
		t.Fatalf("decoding capturable interfaces: %v: %s", err, raw)
	}

	return interfaces
}

// parsePCAPNGStream walks a little-endian single-section pcapng stream and returns the
// interface names and the packet count per interface.
func parsePCAPNGStream(t *testing.T, raw []byte) ([]string, []int) {
	t.Helper()

	names := []string{}
	packets := []int{}

	if len(raw) < 12 || binary.LittleEndian.Uint32(raw) != pcapngSectionHeaderBlock {
		t.Fatalf("capture stream is not little-endian pcapng: % x", raw[:min(len(raw), 12)])
	}

	for len(raw) > 0 {
		if len(raw) < 12 {
			t.Fatalf("truncated pcapng block: % x", raw)
		}

		blockType := binary.LittleEndian.Uint32(raw)
		total := int(binary.LittleEndian.Uint32(raw[4:]))

		if total < 12 || total%4 != 0 || total > len(raw) {
			t.Fatalf("invalid pcapng block length %d", total)
		}

		body := raw[8 : total-4]

		switch blockType {
		case pcapngInterfaceDescriptionBlock:
			names = append(names, pcapngInterfaceName(body[8:]))
			packets = append(packets, 0)
		case pcapngEnhancedPacketBlock:
			interfaceID := int(binary.LittleEndian.Uint32(body))
			if interfaceID >= len(packets) {
				t.Fatalf("packet references undescribed interface %d", interfaceID)
			}

			packets[interfaceID]++
		case pcapngSectionHeaderBlock:
			if len(names) != 0 {
				t.Fatal("pcapng stream repeats its section header")
			}
		}

		raw = raw[total:]
	}

	return names, packets
}

func pcapngInterfaceName(options []byte) string {
	for len(options) >= 4 {
		code := binary.LittleEndian.Uint16(options)
		length := int(binary.LittleEndian.Uint16(options[2:]))

		if code == 0 || len(options) < 4+length {
			break
		}

		if code == pcapngIfNameOption {
			return string(options[4 : 4+length])
		}

		options = options[4+(length+3)/4*4:]
	}

	return ""
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
