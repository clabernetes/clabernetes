package vr_test

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
)

const (
	vrRegistrySecret   = "vr-registry" //nolint:gosec // resource name, not a credential.
	datapathWait       = 5 * time.Minute
	datapathPollPeriod = 10 * time.Second
)

// vmKindCase describes one restricted vrnetlab image conformance entry: the kind boots as a
// plain direct application container, applies its containerlab-staged startup-config through
// the image's own bootstrap, answers management SSH on the Pod address, and forwards the
// dataplane across the fabric to a linux peer.
type vmKindCase struct {
	name           string
	kind           string
	imageEnv       string
	defaultImage   string
	startupConfig  string
	consoleConfig  []string
	deploymentWait time.Duration
	datapathWait   time.Duration
	// managementSSH asserts an SSH banner on the Pod address. It mirrors what the image
	// actually serves under plain docker: some vrnetlab builds ship no working sshd, and the
	// harness records image behavior, never c9s behavior.
	managementSSH bool
}

func TestMain(m *testing.M) {
	clabernetestesthelper.Flags()

	os.Exit(m.Run())
}

// TestVMKindsBootAndReachLinux is the repeatable restricted-image harness for VM (vrnetlab)
// kinds: no kind-specific c9s behavior is involved anywhere — the topology, startup-config,
// and image are the only vendor inputs, exactly as on a containerlab host.
func TestVMKindsBootAndReachLinux(t *testing.T) {
	if os.Getenv("VR_E2E") == "" {
		t.Skip("VR_E2E is not set")
	}

	cases := []vmKindCase{
		{
			name:         "cisco-xrv",
			kind:         "cisco_xrv",
			imageEnv:     "VR_XRV_IMAGE",
			defaultImage: "ghcr.io/clab-labs/vr-xrv:6.3.1",
			startupConfig: `interface GigabitEthernet0/0/0/0
 ipv4 address 10.0.1.2 255.255.255.252
 no shutdown
!`,
			consoleConfig: []string{
				"",
				"configure",
				"interface GigabitEthernet0/0/0/0",
				"ipv4 address 10.0.1.2 255.255.255.252",
				"no shutdown",
				"commit",
				"end",
			},
			deploymentWait: 12 * time.Minute,
			datapathWait:   5 * time.Minute,
			managementSSH:  true,
		},
		{
			name:         "juniper-vqfx",
			kind:         "juniper_vqfx",
			imageEnv:     "VR_VQFX_IMAGE",
			defaultImage: "ghcr.io/clab-labs/vr-vqfx:20.2R1.10",
			startupConfig: `set interfaces xe-0/0/0 unit 0 family inet address 10.0.1.2/30
`,
			deploymentWait: 8 * time.Minute,
			// The vQFX PFE emulation attaches minutes after the RE reports startup complete.
			datapathWait: 12 * time.Minute,
		},
	}

	// VM images are memory-heavy; the cases run serially so the harness stays usable on
	// restricted single-host clusters.
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			runVMKindCase(t, testCase)
		})
	}
}

func runVMKindCase(t *testing.T, testCase vmKindCase) {
	t.Helper()

	image := os.Getenv(testCase.imageEnv)
	if image == "" {
		image = testCase.defaultImage
	}

	testName := "topology-vr-" + testCase.name
	namespace := clabernetestesthelper.NewTestNamespace(testName)
	clabernetestesthelper.KubectlCreateNamespace(t, namespace)

	defer func() {
		if !*clabernetestesthelper.SkipCleanup {
			t.Logf("deleting namespace %q used in test %q", namespace, testName)
			clabernetestesthelper.KubectlDeleteNamespace(t, namespace)
		}
	}()

	clabernetestesthelper.CreateGHCRPullSecret(t, namespace, vrRegistrySecret)
	applyTopology(t, namespace, testName, testCase, image)

	clabernetestesthelper.KubectlWaitForCreate(t, "deployment", namespace, "vm1")
	clabernetestesthelper.KubectlWaitForCreate(t, "deployment", namespace, "l1")

	runKubectl(
		t,
		"wait",
		"--namespace",
		namespace,
		"--for=condition=Available",
		"--timeout="+testCase.deploymentWait.String(),
		"deployment/vm1",
		"deployment/l1",
	)

	if testCase.managementSSH {
		assertManagementSSH(t, namespace)
	}

	applyConsoleConfig(t, namespace, testCase.consoleConfig)
	waitForDatapath(t, namespace, testCase.datapathWait)
}

func applyTopology(
	t *testing.T,
	namespace,
	testName string,
	testCase vmKindCase,
	image string,
) {
	t.Helper()

	indentedConfig := "              " + strings.ReplaceAll(
		strings.TrimRight(testCase.startupConfig, "\n"),
		"\n",
		"\n              ",
	)

	manifest := fmt.Sprintf(`apiVersion: c9s.run/v1alpha1
kind: Topology
metadata:
  name: %s
spec:
  naming: non-prefixed
  statusProbes:
    enabled: true
  imagePull:
    pullSecrets:
      - %s
  definition:
    containerlab: |
      name: %s
      topology:
        nodes:
          l1:
            kind: linux
            image: ghcr.io/srl-labs/network-multitool:latest
            exec:
              - >-
                ash -c 'ip l set dev eth1 up && ip addr add dev eth1 10.0.1.1/30'
          vm1:
            kind: %s
            image: %s
            startup-config: |
%s
        links:
          - type: veth
            endpoints:
              - node: l1
                interface: eth1
              - node: vm1
                interface: eth1
`, testName, vrRegistrySecret, testName, testCase.kind, image, indentedConfig)

	cmd := exec.CommandContext( //nolint:gosec // kubectl arguments are test-controlled.
		t.Context(),
		"kubectl",
		"apply",
		"--namespace",
		namespace,
		"-f",
		"-",
	)
	cmd.Stdin = strings.NewReader(manifest)
	clabernetestesthelper.Execute(t, cmd)
}

// assertManagementSSH proves cross-Pod management reachability: vrnetlab forwards the VM's
// management plane onto the Pod address, so the linux peer must read an SSH banner from it.
func assertManagementSSH(t *testing.T, namespace string) {
	t.Helper()

	podIP := strings.TrimSpace(string(runKubectl(
		t,
		"get",
		"pods",
		"--namespace",
		namespace,
		"--selector",
		"c9s.run/direct-workload=vm1",
		"-o",
		"jsonpath={.items[0].status.podIP}",
	)))
	if podIP == "" {
		t.Fatal("vm1 Pod has no address")
	}

	deadline := time.NewTimer(datapathWait)
	defer deadline.Stop()

	var lastOutput []byte

	for {
		cmd := exec.CommandContext( //nolint:gosec // kubectl arguments are test-controlled.
			t.Context(),
			"kubectl",
			"exec",
			"--namespace",
			namespace,
			"deployment/l1",
			"-c",
			clabernetestesthelper.DirectDeviceContainerName(t, namespace, "l1"),
			"--",
			"sh",
			"-c",
			"echo | nc -w 5 "+podIP+" 22",
		)

		output, err := cmd.CombinedOutput()
		if err == nil && strings.Contains(string(output), "SSH-") {
			return
		}

		lastOutput = output

		select {
		case <-t.Context().Done():
			t.Fatalf("management SSH check canceled: %s", strings.TrimSpace(string(lastOutput)))
		case <-deadline.C:
			t.Fatalf(
				"timed out waiting for management SSH banner on %s: %s",
				podIP,
				strings.TrimSpace(string(lastOutput)),
			)
		case <-time.After(datapathPollPeriod):
		}
	}
}

// applyConsoleConfig drives the VM's released serial console exactly as an operator would:
// vrnetlab images without startup-config bootstrap support (matching their behavior under plain
// docker) receive their data-interface addressing over the console, so the harness proves the
// same dataplane with zero c9s kind knowledge.
func applyConsoleConfig(t *testing.T, namespace string, commands []string) {
	t.Helper()

	if len(commands) == 0 {
		return
	}

	script := `
import telnetlib, time, sys
tn = telnetlib.Telnet("127.0.0.1", 5000, 30)
for line in sys.argv[1:]:
    tn.write(line.encode() + b"\r\n")
    time.sleep(3)
time.sleep(3)
out = tn.read_very_eager()
sys.stdout.write(out.decode(errors="replace"))
`

	command := append([]string{
		"exec",
		"--namespace",
		namespace,
		"deployment/vm1",
		"-c",
		clabernetestesthelper.DirectDeviceContainerName(t, namespace, "vm1"),
		"--",
		"python3",
		"-c",
		script,
	}, commands...)

	output := runKubectl(t, command...)
	t.Logf("console configuration applied:\n%s", string(output))
}

func waitForDatapath(t *testing.T, namespace string, wait time.Duration) {
	t.Helper()

	deadline := time.NewTimer(wait)
	defer deadline.Stop()

	var lastOutput []byte

	for {
		cmd := exec.CommandContext( //nolint:gosec // kubectl arguments are test-controlled.
			t.Context(),
			"kubectl",
			"exec",
			"--namespace",
			namespace,
			"deployment/l1",
			"-c",
			clabernetestesthelper.DirectDeviceContainerName(t, namespace, "l1"),
			"--",
			"ping",
			"-c",
			"2",
			"-W",
			"3",
			"10.0.1.2",
		)

		output, err := cmd.CombinedOutput()
		if err == nil {
			return
		}

		lastOutput = output

		select {
		case <-t.Context().Done():
			t.Fatalf("VM datapath check canceled: %s", strings.TrimSpace(string(lastOutput)))
		case <-deadline.C:
			t.Fatalf(
				"timed out waiting for VM datapath: %s",
				strings.TrimSpace(string(lastOutput)),
			)
		case <-time.After(datapathPollPeriod):
		}
	}
}

func runKubectl(t *testing.T, args ...string) []byte {
	t.Helper()

	cmd := exec.CommandContext( //nolint:gosec // kubectl arguments are test-controlled.
		t.Context(),
		"kubectl",
		args...,
	)

	return clabernetestesthelper.Execute(t, cmd)
}
