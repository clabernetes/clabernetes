package ceos_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
	k8scorev1 "k8s.io/api/core/v1"
)

const (
	defaultCEOSImage   = "ghcr.io/clab-labs/ceos:4.33.1F"
	ceosRegistrySecret = "ceos-registry"
	deploymentWait     = 10 * time.Minute
	datapathWait       = 5 * time.Minute
	datapathPollPeriod = 10 * time.Second
)

func TestMain(m *testing.M) {
	clabernetestesthelper.Flags()

	os.Exit(m.Run())
}

// TestCEOSBootsAndReachesLinux checks that management is allocated, traffic crosses the fabric,
// and Link changes recreate the Pod automatically.
func TestCEOSBootsAndReachesLinux(t *testing.T) {
	t.Parallel()

	if os.Getenv("CEOS_E2E") == "" {
		t.Skip("CEOS_E2E is not set")
	}

	ceosImage := os.Getenv("CEOS_IMAGE")
	if ceosImage == "" {
		ceosImage = defaultCEOSImage
	}

	testName := "topology-ceos"
	namespace := clabernetestesthelper.NewTestNamespace(testName)
	clabernetestesthelper.KubectlCreateNamespace(t, namespace)

	defer func() {
		if !*clabernetestesthelper.SkipCleanup {
			t.Logf("deleting namespace %q used in test %q", namespace, testName)
			clabernetestesthelper.KubectlDeleteNamespace(t, namespace)
		}
	}()

	clabernetestesthelper.CreateGHCRPullSecret(t, namespace, ceosRegistrySecret)
	applyTopology(t, namespace, ceosImage)

	clabernetestesthelper.KubectlWaitForCreate(t, "deployment", namespace, "ceos1")
	clabernetestesthelper.KubectlWaitForCreate(t, "deployment", namespace, "l1")

	runKubectl(
		t,
		"wait",
		"--namespace",
		namespace,
		"--for=condition=Available",
		"--timeout="+deploymentWait.String(),
		"deployment/ceos1",
		"deployment/l1",
	)

	assertManagementAllocation(t, namespace)
	waitForDatapath(t, namespace)
	testLinkChanges(t, namespace)
}

// Use documentation subnets for data links: common 10.0.x.x Pod addresses can otherwise
// collide with a device address in the shared network namespace.
func applyTopology(t *testing.T, namespace, ceosImage string) {
	t.Helper()

	manifest := fmt.Sprintf(`apiVersion: c9s.run/v1alpha1
kind: Topology
metadata:
  name: topology-ceos
spec:
  statusProbes:
    enabled: true
  imagePull:
    pullSecrets:
      - %s
  definition:
    containerlab: |
      name: topology-ceos
      topology:
        nodes:
          l1:
            kind: linux
            image: ghcr.io/srl-labs/network-multitool:latest
            exec:
              - >-
                ash -c 'ip l set dev eth1 up && ip addr add dev eth1 192.0.2.1/30'
          ceos1:
            kind: arista_ceos
            image: %s
            startup-config: |
              interface Ethernet1
                 no switchport
                 ip address 192.0.2.2/30
              end
        links:
          - type: veth
            endpoints:
              - node: l1
                interface: eth1
              - node: ceos1
                interface: eth1
`, ceosRegistrySecret, ceosImage)

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

// assertManagementAllocation checks the Node's allocated management address in its network
// namespace. The management allocation is independent of the Pod's CNI address.
func assertManagementAllocation(t *testing.T, namespace string) {
	t.Helper()
	address := strings.TrimSpace(string(runKubectl(t,
		"get", "nodes.c9s.run", "ceos1", "--namespace", namespace,
		"-o", "jsonpath={.status.directManagement.ipv4}")))
	if address == "" {
		t.Fatal("ceos1 has no management allocation")
	}
	output := string(runKubectl(t, "exec", "--namespace", namespace, "deployment/ceos1", "-c",
		clabernetestesthelper.DirectDeviceContainerName(t, namespace, "ceos1"), "--",
		"ip", "-o", "-4", "address", "show"))
	if !strings.Contains(output, "inet "+address+" ") {
		t.Fatalf("management allocation %q is not realized: %s", address, output)
	}
}

func waitForDatapath(t *testing.T, namespace string) {
	t.Helper()
	waitForDatapathAddress(t, namespace, "192.0.2.2")
}

func waitForDatapathAddress(t *testing.T, namespace, address string) {
	t.Helper()

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
			"ping",
			"-c",
			"2",
			"-W",
			"3",
			address,
		)

		output, err := cmd.CombinedOutput()
		if err == nil {
			return
		}

		lastOutput = output

		select {
		case <-t.Context().Done():
			t.Fatalf("cEOS datapath check canceled: %s", strings.TrimSpace(string(lastOutput)))
		case <-deadline.C:
			t.Fatalf(
				"timed out waiting for cEOS datapath: %s",
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

// testLinkChanges verifies the application notices added/removed interfaces without an
// operator touching a Node or Deployment. Each change must replace the cEOS Pod.
func testLinkChanges(t *testing.T, namespace string) {
	t.Helper()
	before := ceosPod(t, namespace)
	manifest := `apiVersion: c9s.run/v1alpha1
kind: Link
metadata:
  name: ceos-extra
spec:
  endpointA:
    nodeName: l1
    interfaceName: eth2
  endpointB:
    nodeName: ceos1
    interfaceName: eth2
`
	cmd := exec.CommandContext( //nolint:gosec // Test-controlled kubectl arguments.
		t.Context(),
		"kubectl",
		"apply",
		"-n",
		namespace,
		"-f",
		"-",
	)
	cmd.Stdin = strings.NewReader(manifest)
	clabernetestesthelper.Execute(t, cmd)
	waitForCEOSRecreate(t, namespace, before)
	waitForCEOSInterface(t, namespace, true)

	runKubectl(
		t,
		"exec",
		"-n",
		namespace,
		"deployment/ceos1",
		"-c",
		clabernetestesthelper.DirectDeviceContainerName(t, namespace, "ceos1"),
		"--",
		"Cli",
		"-p",
		"15",
		"-c",
		"configure\ninterface Ethernet2\nno switchport\nend",
	)
	// EOS applies routed-port conversion asynchronously; early IP configuration is ignored.
	waitForCEOSOutput(
		t,
		namespace,
		"show interfaces Ethernet2 switchport",
		func(output string) bool {
			return strings.Contains(output, "Switchport: Disabled")
		},
	)
	runKubectl(
		t,
		"exec",
		"-n",
		namespace,
		"deployment/ceos1",
		"-c",
		clabernetestesthelper.DirectDeviceContainerName(t, namespace, "ceos1"),
		"--",
		"Cli",
		"-p",
		"15",
		"-c",
		"configure\ninterface Ethernet2\nip address 198.51.100.2/30\nno shutdown\nend",
	)
	// The Linux peer receives its connectivity revision independently of cEOS's Pod replacement.
	runKubectl(t, "exec", "-n", namespace, "deployment/l1", "-c",
		clabernetestesthelper.DirectDeviceContainerName(t, namespace, "l1"), "--",
		"sh", "-c", "for i in $(seq 1 60); do "+
			"if ip link set eth2 up && ip address replace 198.51.100.1/30 dev eth2; then exit 0; fi; "+
			"sleep 2; done; exit 1")
	waitForDatapathAddress(t, namespace, "198.51.100.2")
	waitForDatapath(t, namespace)
	before = ceosPod(t, namespace)
	runKubectl(t, "delete", "link", "ceos-extra", "-n", namespace)
	waitForCEOSRecreate(t, namespace, before)
	waitForCEOSInterface(t, namespace, false)
	waitForDatapath(t, namespace)
}

func ceosPod(t *testing.T, namespace string) *k8scorev1.Pod {
	t.Helper()
	var pods k8scorev1.PodList
	raw := runKubectl(t, "get", "pods", "-n", namespace,
		"-l", "c9s.run/direct-workload=ceos1", "-o", "json")
	if err := json.Unmarshal(raw, &pods); err != nil {
		t.Fatal(err)
	}
	for i := range pods.Items {
		if pods.Items[i].DeletionTimestamp == nil {
			return &pods.Items[i]
		}
	}

	return nil
}

func waitForCEOSRecreate(t *testing.T, namespace string, before *k8scorev1.Pod) {
	t.Helper()
	if before == nil {
		t.Fatal("cEOS Pod missing before link change")
	}
	container := clabernetestesthelper.DirectDeviceContainerName(t, namespace, "ceos1")
	deadline := time.Now().Add(datapathWait)
	for time.Now().Before(deadline) {
		current := ceosPod(t, namespace)
		if current != nil {
			for _, status := range current.Status.ContainerStatuses {
				if status.Name == container && status.Ready && status.State.Running != nil &&
					current.UID != before.UID {
					t.Logf(
						"cEOS recreated automatically: Pod %s -> %s",
						before.UID,
						current.UID,
					)

					return
				}
			}
		}
		select {
		case <-t.Context().Done():
			t.Fatal("canceled waiting for cEOS Pod replacement")
		case <-time.After(datapathPollPeriod):
		}
	}
	t.Fatal("cEOS Pod was not replaced automatically after link change")
}

func waitForCEOSInterface(t *testing.T, namespace string, present bool) {
	t.Helper()
	waitForCEOSOutput(t, namespace, "show interfaces status", func(output string) bool {
		return strings.Contains(output, "Et1") && strings.Contains(output, "Et2") == present
	})
}

func waitForCEOSOutput(t *testing.T, namespace, command string, check func(string) bool) {
	t.Helper()
	container := clabernetestesthelper.DirectDeviceContainerName(t, namespace, "ceos1")
	deadline := time.Now().Add(datapathWait)
	var output []byte
	for time.Now().Before(deadline) {
		cmd := exec.CommandContext( //nolint:gosec // Test-controlled kubectl arguments.
			t.Context(),
			"kubectl",
			"exec",
			"-n",
			namespace,
			"deployment/ceos1",
			"-c",
			container,
			"--",
			"Cli",
			"-c",
			command,
		)
		var err error
		output, err = cmd.CombinedOutput()
		if err == nil && check(string(output)) {
			return
		}
		select {
		case <-t.Context().Done():
			t.Fatal("canceled waiting for cEOS interface")
		case <-time.After(datapathPollPeriod):
		}
	}
	t.Fatalf("cEOS command %q did not reach expected state: %s", command, output)
}
