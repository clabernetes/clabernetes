package srsim_test

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
	defaultSRSimImage   = "ghcr.io/clab-labs/nokia_srsim:26.7.R1"
	srsimRegistrySecret = "srsim-registry" //nolint:gosec // resource name, not a credential.
	deploymentWait      = 10 * time.Minute
	nodeReadyTimeout    = 12 * time.Minute
	datapathWait        = 5 * time.Minute
	datapathPollPeriod  = 10 * time.Second
)

func TestMain(m *testing.M) {
	clabernetestesthelper.Flags()

	os.Exit(m.Run())
}

func TestSRSimBootsAndReachesLinux(t *testing.T) {
	t.Parallel()

	// The fixtures authenticate through imagePull.pullSecrets, which only the direct runtime
	// realizes as Kubernetes imagePullSecrets consumed only by the kubelet.

	license := os.Getenv("SRSIM_LICENSE")
	if strings.TrimSpace(license) == "" {
		t.Skip("SRSIM_LICENSE is not set")
	}

	srsimImage := os.Getenv("SRSIM_IMAGE")
	if srsimImage == "" {
		srsimImage = defaultSRSimImage
	}

	testName := "topology-srsim"
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

	licensePath := filepath.Join(t.TempDir(), "license.txt")

	err := os.WriteFile( //nolint:gosec // path is inside t.TempDir.
		licensePath,
		[]byte(license),
		0o600,
	)
	if err != nil {
		t.Fatalf("write SR-SIM license: %v", err)
	}

	runKubectl(
		t,
		"create",
		"configmap",
		"srsim-license",
		"--namespace",
		namespace,
		"--from-file=license.txt="+licensePath,
	)

	createSRSimRegistrySecret(t, namespace)
	applyTopology(t, namespace, srsimImage)

	clabernetestesthelper.KubectlWaitForCreate(t, "deployment", namespace, "sros")
	clabernetestesthelper.KubectlWaitForCreate(t, "deployment", namespace, "l1")

	runKubectl(
		t,
		"wait",
		"--namespace",
		namespace,
		"--for=condition=Available",
		"--timeout="+deploymentWait.String(),
		"deployment/sros",
		"deployment/l1",
	)

	assertExpandedSRSimComponents(t, namespace)

	clabernetestesthelper.KubectlWaitForCreate(t, "nodes.c9s.run", namespace, "l1")
	clabernetestesthelper.KubectlWaitForCreate(t, "nodes.c9s.run", namespace, "sros")
	waitForNodeReady(t, namespace, "l1")
	waitForNodeReady(t, namespace, "sros")

	waitForDatapath(t, namespace)

	// SR-SIM sends around the qdisc of its own interface, so its endpoint's impairment is
	// programmed by l1's connectivity sidecar on the wire leg that delivers SR-SIM's packets to
	// l1. The 100ms delay therefore shows up in every round trip from l1.
	waitForImpairedDatapath(t, namespace)
}

func applyTopology(t *testing.T, namespace, srsimImage string) {
	t.Helper()

	manifest := fmt.Sprintf(`apiVersion: c9s.run/v1alpha1
kind: Topology
metadata:
  name: topology-srsim
spec:
  statusProbes:
    enabled: true
  imagePull:
    pullSecrets:
      - %s
  deployment:
    filesFromConfigMap:
      sros:
        - filePath: /opt/nokia/sros/license.txt
          configMapName: srsim-license
          configMapPath: license.txt
  definition:
    containerlab: |
      name: topology-srsim
      topology:
        nodes:
          l1:
            kind: linux
            image: ghcr.io/srl-labs/network-multitool:latest
            exec:
              - >-
                ash -c 'ip l set dev eth1 up && ip addr add dev eth1 198.51.100.1/30'
          sros:
            kind: nokia_srsim
            image: %s
            type: sr-1-92s
            license: /opt/nokia/sros/license.txt
            components:
              - slot: A
              - slot: 1
            startup-config: |
              /configure port 1/1/c23 connector breakout c4-100g
              /configure port 1/1/c23 admin-state enable
              /configure port 1/1/c23/4 ethernet mode hybrid
              /configure port 1/1/c23/4 admin-state enable
              /configure router "Base" interface "to-linux" port 1/1/c23/4:0
              /configure router "Base" interface "to-linux" ipv4 primary address 198.51.100.2
              /configure router "Base" interface "to-linux" ipv4 primary prefix-length 24
        links:
          - type: veth
            endpoints:
              - node: l1
                interface: eth1
              - node: sros
                interface: 1/1/c23/4
                netem:
                  delay: 100ms
`, srsimRegistrySecret, srsimImage)

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

func assertExpandedSRSimComponents(t *testing.T, namespace string) {
	t.Helper()

	// Direct runtime: each planned chassis component is a first-class application container in
	// the sros workload Pod, so the Pod must carry at least two device containers.
	output := runKubectl(
		t,
		"get",
		"pods",
		"--namespace",
		namespace,
		"--selector",
		"c9s.run/direct-workload=sros",
		"-o",
		`jsonpath={range .items[0].spec.containers[*]}{.name}{"\n"}{end}`,
	)
	deviceContainers := 0

	for name := range strings.SplitSeq(string(output), "\n") {
		if strings.HasPrefix(name, "node-") {
			deviceContainers++
		}
	}

	if deviceContainers < 2 {
		t.Fatalf("sros Pod has %d device containers, want at least 2: %s", deviceContainers, output)
	}
}

func createSRSimRegistrySecret(t *testing.T, namespace string) {
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
		srsimRegistrySecret,
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

func waitForNodeReady(t *testing.T, namespace, nodeName string) {
	t.Helper()

	cmd := exec.CommandContext( //nolint:gosec // kubectl arguments are test-controlled.
		t.Context(),
		"kubectl",
		"wait",
		"--for=jsonpath={.status.readiness}=ready",
		"--timeout="+nodeReadyTimeout.String(),
		"--namespace",
		namespace,
		"node.c9s.run/"+nodeName,
	)

	clabernetestesthelper.Execute(t, cmd)
}

func waitForDatapath(t *testing.T, namespace string) {
	t.Helper()

	waitForL1Command(
		t,
		namespace,
		"SR-SIM datapath",
		[]string{"ping", "-c", "2", "-W", "3", "198.51.100.2"},
		func(_ []byte, err error) bool { return err == nil },
	)
}

// waitForImpairedDatapath waits until every round trip to SR-SIM takes at least 90ms, the
// declared SR-SIM endpoint delay less a margin. busybox awk needs the comparison in parentheses.
func waitForImpairedDatapath(t *testing.T, namespace string) {
	t.Helper()

	waitForL1Command(
		t,
		namespace,
		"SR-SIM endpoint impairment",
		[]string{
			"sh", "-c",
			"ping -c 3 -W 3 198.51.100.2 | " +
				"awk -F' = ' '/min/ {split($2, rtt, \"/\"); print (rtt[1] >= 90)}'",
		},
		func(output []byte, err error) bool {
			return err == nil && strings.TrimSpace(string(output)) == "1"
		},
	)
}

// waitForL1Command runs command in the l1 device container until done accepts its result.
func waitForL1Command(
	t *testing.T,
	namespace string,
	description string,
	command []string,
	done func([]byte, error) bool,
) {
	t.Helper()

	deadline := time.NewTimer(datapathWait)
	defer deadline.Stop()

	var lastOutput []byte

	for {
		cmd := exec.CommandContext( //nolint:gosec // kubectl arguments are test-controlled.
			t.Context(),
			"kubectl",
			append(
				[]string{
					"exec",
					"--namespace",
					namespace,
					"deployment/l1",
					"-c",
					deviceContainerName(t, namespace, "l1"),
					"--",
				},
				command...,
			)...,
		)

		output, err := cmd.CombinedOutput()
		if done(output, err) {
			return
		}

		lastOutput = output

		select {
		case <-t.Context().Done():
			t.Fatalf("%s check canceled: %s", description, strings.TrimSpace(string(lastOutput)))
		case <-deadline.C:
			t.Fatalf(
				"timed out waiting for %s: %s",
				description,
				strings.TrimSpace(string(lastOutput)),
			)
		case <-time.After(datapathPollPeriod):
		}
	}
}

// deviceContainerName resolves the direct device application container of a workload Pod.
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
	for name := range strings.SplitSeq(string(output), "\n") {
		if strings.HasPrefix(name, "node-") {
			return name
		}
	}

	t.Fatalf("workload %q has no device application container: %s", workload, output)

	return ""
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
