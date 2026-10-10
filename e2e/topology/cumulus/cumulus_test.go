package cumulus_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
)

const (
	registrySecret = "regcred"
	deploymentWait = 12 * time.Minute
	sshWait        = 8 * time.Minute
	pollPeriod     = 10 * time.Second
)

func TestMain(m *testing.M) {
	clabernetestesthelper.Flags()

	os.Exit(m.Run())
}

func TestCumulusExampleBootsAcceptsSSHAndPings(t *testing.T) {
	if os.Getenv("CUMULUS_E2E") == "" {
		t.Skip("CUMULUS_E2E is not set")
	}

	namespace := clabernetestesthelper.NewTestNamespace("topology-cumulus")
	clabernetestesthelper.KubectlCreateNamespace(t, namespace)

	defer func() {
		if !*clabernetestesthelper.SkipCleanup {
			clabernetestesthelper.KubectlDeleteNamespace(t, namespace)
		}
	}()

	clabernetestesthelper.CreateGHCRPullSecret(t, namespace, registrySecret)
	manifest := filepath.Join("..", "..", "..", "examples", "basic", "cumulus-multitool.yaml")
	runKubectl(t, "apply", "--namespace", namespace, "-f", manifest)

	clabernetestesthelper.KubectlWaitForCreate(t, "deployment", namespace, "cumulus")
	clabernetestesthelper.KubectlWaitForCreate(t, "deployment", namespace, "multitool")
	runKubectl(t, "wait", "--namespace", namespace, "--for=condition=Available",
		"--timeout="+deploymentWait.String(), "deployment/cumulus", "deployment/multitool")

	clientContainer := clabernetestesthelper.DirectDeviceContainerName(t, namespace, "multitool")
	runKubectl(t, "exec", "--namespace", namespace, "deployment/multitool", "-c", clientContainer,
		"--", "ip", "link", "show", "eth1")
	runKubectl(t, "exec", "--namespace", namespace, "deployment/multitool", "-c", clientContainer,
		"--", "ip", "link", "show", "eth2")

	clabernetestesthelper.KubectlWaitForCreate(t, "service", namespace, "cumulus")
	serviceIP := strings.TrimSpace(string(runKubectl(
		t,
		"get",
		"service",
		"cumulus",
		"--namespace",
		namespace,
		"-o",
		"jsonpath={.spec.clusterIP}",
	)))
	if serviceIP == "" || serviceIP == "None" {
		t.Fatal("Cumulus Service has no ClusterIP")
	}

	assertSSHLogin(t, namespace, clientContainer, serviceIP)
	runKubectl(t, "wait", "--namespace", namespace, "--for=jsonpath={.status.readiness}=ready",
		"--timeout=2m", "node.c9s.run/cumulus")

	// The breakout lanes must exist inside the guest: the generated /config/ports.conf is what
	// makes the vrnetlab image present swp1s0..swp1s3 instead of the parent port.
	assertBreakoutInterface(t, namespace, clientContainer, serviceIP, "swp1s0")
	assertBreakoutInterface(t, namespace, clientContainer, serviceIP, "swp1s1")

	assertPing(t, namespace, clientContainer)

	// The second breakout lane must route: a ping toward the swp1s1 address crosses the guest's
	// own forwarding between the two lanes, which is the dataplane breakout layouts exist for.
	assertRoutedPing(t, namespace, clientContainer)
}

// assertPing proves the direct breakout dataplane: the multitool reaches the Cumulus lane
// address assigned to swp1s0.
func assertPing(t *testing.T, namespace, clientContainer string) {
	t.Helper()

	deadline := time.NewTimer(2 * time.Minute)
	defer deadline.Stop()

	var lastOutput []byte
	for {
		cmd := exec.CommandContext( //nolint:gosec // kubectl arguments are test-controlled.
			t.Context(), "kubectl", "exec", "--namespace", namespace,
			"deployment/multitool", "-c", clientContainer, "--",
			"ping", "-c", "1", "-W", "3", "192.0.2.0")
		output, err := cmd.CombinedOutput()
		if err == nil {
			return
		}
		lastOutput = output

		select {
		case <-t.Context().Done():
			t.Fatalf("Cumulus ping check canceled: %s", strings.TrimSpace(string(lastOutput)))
		case <-deadline.C:
			t.Fatalf("timed out pinging Cumulus at 192.0.2.0: %s",
				strings.TrimSpace(string(lastOutput)))
		case <-time.After(pollPeriod):
		}
	}
}

// assertBreakoutInterface proves the guest presents the generated breakout lane names, using the
// Cumulus management Service for guest access.
func assertBreakoutInterface(t *testing.T, namespace, clientContainer, serviceIP, lane string) {
	t.Helper()

	const script = `helper=$(mktemp)
trap 'rm -f "$helper"' EXIT
cat > "$helper" <<'ASKPASS'
#!/bin/sh
printf '%s\n' 'Clab123!'
ASKPASS
chmod 700 "$helper"
DISPLAY=:0 SSH_ASKPASS_REQUIRE=force SSH_ASKPASS="$helper" timeout 15 ssh \
  -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
  -o ConnectTimeout=5 -o LogLevel=ERROR \
  -o PreferredAuthentications=password -o PubkeyAuthentication=no \
  "cumulus@$1" 'ip -br link show "$2"'
`
	deadline := time.NewTimer(sshWait)
	defer deadline.Stop()

	var lastOutput []byte
	for {
		cmd := exec.CommandContext( //nolint:gosec // kubectl arguments are test-controlled.
			t.Context(), "kubectl", "exec", "--namespace", namespace,
			"deployment/multitool", "-c", clientContainer,
			"--", "sh", "-c", script, "breakout-check", serviceIP, lane)
		output, err := cmd.CombinedOutput()
		if err == nil && strings.Contains(string(output), lane) {
			return
		}
		lastOutput = output

		select {
		case <-t.Context().Done():
			t.Fatalf("Cumulus breakout check canceled: %s", strings.TrimSpace(string(lastOutput)))
		case <-deadline.C:
			t.Fatalf("timed out waiting for Cumulus breakout interface %s: %s",
				lane, strings.TrimSpace(string(lastOutput)))
		case <-time.After(pollPeriod):
		}
	}
}

// assertRoutedPing proves the dataplane crosses the Cumulus guest between the two breakout
// lanes: the ping originates on the swp1s0-facing interface and must arrive at the swp1s1
// address the guest itself owns, so the guest's forwarding carries it.
func assertRoutedPing(t *testing.T, namespace, clientContainer string) {
	t.Helper()

	deadline := time.NewTimer(2 * time.Minute)
	defer deadline.Stop()

	var lastOutput []byte
	for {
		cmd := exec.CommandContext( //nolint:gosec // kubectl arguments are test-controlled.
			t.Context(), "kubectl", "exec", "--namespace", namespace,
			"deployment/multitool", "-c", clientContainer, "--",
			"ping", "-c", "1", "-I", "eth1", "-W", "3", "192.0.2.2")
		output, err := cmd.CombinedOutput()
		if err == nil {
			return
		}
		lastOutput = output

		select {
		case <-t.Context().Done():
			t.Fatalf(
				"Cumulus routed ping check canceled: %s",
				strings.TrimSpace(string(lastOutput)),
			)
		case <-deadline.C:
			t.Fatalf("timed out pinging the second breakout lane (192.0.2.2): %s",
				strings.TrimSpace(string(lastOutput)))
		case <-time.After(pollPeriod):
		}
	}
}

func assertSSHLogin(t *testing.T, namespace, clientContainer, serviceIP string) {
	t.Helper()

	const script = `helper=$(mktemp)
trap 'rm -f "$helper"' EXIT
cat > "$helper" <<'ASKPASS'
#!/bin/sh
printf '%s\n' 'Clab123!'
ASKPASS
chmod 700 "$helper"
DISPLAY=:0 SSH_ASKPASS_REQUIRE=force SSH_ASKPASS="$helper" timeout 15 ssh \
  -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
  -o ConnectTimeout=5 -o LogLevel=ERROR \
  -o PreferredAuthentications=password -o PubkeyAuthentication=no \
  "cumulus@$1" 'printf "c9s-cumulus-ssh-ok\n"'
`
	deadline := time.NewTimer(sshWait)
	defer deadline.Stop()

	var lastOutput []byte
	for {
		cmd := exec.CommandContext( //nolint:gosec // kubectl arguments are test-controlled.
			t.Context(), "kubectl", "exec", "--namespace", namespace,
			"deployment/multitool", "-c", clientContainer, "--",
			"sh", "-c", script, "ssh-check", serviceIP)
		output, err := cmd.CombinedOutput()
		if err == nil && strings.Contains(string(output), "c9s-cumulus-ssh-ok") {
			return
		}
		lastOutput = output

		select {
		case <-t.Context().Done():
			t.Fatalf("Cumulus SSH check canceled: %s", strings.TrimSpace(string(lastOutput)))
		case <-deadline.C:
			t.Fatalf("timed out authenticating to Cumulus SSH at %s: %s",
				serviceIP, strings.TrimSpace(string(lastOutput)))
		case <-time.After(pollPeriod):
		}
	}
}

func runKubectl(t *testing.T, args ...string) []byte {
	t.Helper()

	//nolint:gosec // kubectl arguments are test-controlled.
	cmd := exec.CommandContext(t.Context(), "kubectl", args...)

	return clabernetestesthelper.Execute(t, cmd)
}
