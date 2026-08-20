package ceos_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
)

const (
	defaultCEOSImage   = "ghcr.io/clab-labs/ceos:4.33.1F"
	ceosRegistrySecret = "ceos-registry" //nolint:gosec // resource name, not a credential.
	deploymentWait     = 10 * time.Minute
	datapathWait       = 5 * time.Minute
	datapathPollPeriod = 10 * time.Second
)

func TestMain(m *testing.M) {
	clabernetestesthelper.Flags()

	os.Exit(m.Run())
}

// TestCEOSBootsAndReachesLinux exercises a systemd-based NOS in direct mode with no
// startup-config at all: the imported kind's own template must render the runtime management
// identity, the interface fixups must map eth1, and the dataplane must cross the fabric.
func TestCEOSBootsAndReachesLinux(t *testing.T) {
	t.Parallel()

	clabernetestesthelper.SkipUnlessDeviceRuntimeMode(t, "direct")

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

	createRegistrySecret(t, namespace)
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

	assertRenderedManagement(t, namespace)
	waitForDatapath(t, namespace)
}

func applyTopology(t *testing.T, namespace, ceosImage string) {
	t.Helper()

	manifest := fmt.Sprintf(`apiVersion: c9s.run/v1alpha1
kind: Topology
metadata:
  name: topology-ceos
spec:
  naming: non-prefixed
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
                ash -c 'ip l set dev eth1 up && ip addr add dev eth1 10.0.1.1/30'
          ceos1:
            kind: arista_ceos
            image: %s
            startup-config: |
              interface Ethernet1
                 no switchport
                 ip address 10.0.1.2/30
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

// assertRenderedManagement proves the two-render preparation protocol delivered the runtime
// management identity through the imported kind's own hook path: Management0 carries the Pod
// address with its real prefix.
func assertRenderedManagement(t *testing.T, namespace string) {
	t.Helper()

	podIP := strings.TrimSpace(string(runKubectl(
		t,
		"get",
		"pods",
		"--namespace",
		namespace,
		"--selector",
		"c9s.run/direct-workload=ceos1",
		"-o",
		"jsonpath={.items[0].status.podIP}",
	)))
	if podIP == "" {
		t.Fatal("ceos1 Pod has no address")
	}
	output := string(runKubectl(
		t,
		"exec",
		"--namespace",
		namespace,
		"deployment/ceos1",
		"-c",
		deviceContainerName(t, namespace, "ceos1"),
		"--",
		"Cli",
		"-c",
		"show ip interface brief",
	))
	if !strings.Contains(output, podIP+"/") {
		t.Fatalf("management interface does not carry the Pod identity %q: %s", podIP, output)
	}
}

func waitForDatapath(t *testing.T, namespace string) {
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
			deviceContainerName(t, namespace, "l1"),
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

func deviceContainerName(t *testing.T, namespace, workload string) string {
	t.Helper()

	output := runKubectl(
		t,
		"get",
		"pods",
		"--namespace",
		namespace,
		"--selector",
		"c9s.run/direct-workload="+workload,
		"-o",
		`jsonpath={range .items[0].spec.containers[*]}{.name}{"\n"}{end}`,
	)
	for _, name := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(name, "device-") {
			return name
		}
	}

	t.Fatalf("workload %q has no direct device container: %s", workload, output)

	return ""
}

func createRegistrySecret(t *testing.T, namespace string) {
	t.Helper()

	configPath := dockerConfigPath(t)

	configData, err := os.ReadFile(configPath) //nolint:gosec // path is the runner Docker config.
	if err != nil {
		t.Fatalf("read Docker config %q: %v", configPath, err)
	}

	var config struct {
		Auths map[string]json.RawMessage `json:"auths"`
	}

	err = json.Unmarshal(configData, &config)
	if err != nil {
		t.Fatalf("decode Docker config %q: %v", configPath, err)
	}

	ghcrAuth, ok := config.Auths["ghcr.io"]
	if !ok {
		t.Fatalf("Docker config %q has no ghcr.io credentials", configPath)
	}

	minimalConfig, err := json.Marshal(struct {
		Auths map[string]json.RawMessage `json:"auths"`
	}{
		Auths: map[string]json.RawMessage{"ghcr.io": ghcrAuth},
	})
	if err != nil {
		t.Fatalf("encode GHCR Docker config: %v", err)
	}

	minimalConfigPath := filepath.Join(t.TempDir(), "config.json")

	err = os.WriteFile(
		minimalConfigPath,
		minimalConfig,
		0o600,
	)
	if err != nil {
		t.Fatalf("write GHCR Docker config: %v", err)
	}

	runKubectl(
		t,
		"create",
		"secret",
		"generic",
		ceosRegistrySecret,
		"--namespace",
		namespace,
		"--type=kubernetes.io/dockerconfigjson",
		"--from-file=.dockerconfigjson="+minimalConfigPath,
	)
}

func dockerConfigPath(t *testing.T) string {
	t.Helper()

	if dockerConfigDir := os.Getenv("DOCKER_CONFIG"); dockerConfigDir != "" {
		return filepath.Join(dockerConfigDir, "config.json")
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("resolve home directory for Docker config: %v", err)
	}

	return filepath.Join(homeDir, ".docker", "config.json")
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
