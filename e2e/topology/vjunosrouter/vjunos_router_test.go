package vjunosrouter_test

import (
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
)

const bootTimeout = 20 * time.Minute

const defaultImage = " ghcr.io/clab-labs/juniper_vjunos-router:boxen-25.2R1.9"

// Junos requires a PTY; SSH_ASKPASS keeps authentication noninteractive.
const sshScript = `helper=$(mktemp)
trap 'rm -f "$helper"' EXIT
cat > "$helper" <<'ASKPASS'
#!/bin/sh
printf '%s\n' 'admin@123'
ASKPASS
chmod 700 "$helper"
DISPLAY=:0 SSH_ASKPASS_REQUIRE=force SSH_ASKPASS="$helper" timeout 15 ssh -n -tt \
  -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
  -o ConnectTimeout=5 -o LogLevel=ERROR -o PubkeyAuthentication=no \
  "admin@$1" "$2"
`

func TestMain(m *testing.M) {
	clabernetestesthelper.Flags()
	os.Exit(m.Run())
}

func TestVJunosRouterMultitoolExample(t *testing.T) {
	image := routerImage(t)
	namespace := clabernetestesthelper.NewTestNamespace("example-vjunos-router")
	clabernetestesthelper.KubectlCreateNamespace(t, namespace)
	defer func() {
		if t.Failed() {
			clabernetestesthelper.DumpNamespaceDiagnostics(t, namespace)
		}
		if !*clabernetestesthelper.SkipCleanup {
			clabernetestesthelper.KubectlDeleteNamespace(t, namespace)
		}
	}()

	clabernetestesthelper.CreateGHCRPullSecret(t, namespace, "regcred")
	manifest, err := os.ReadFile(filepath.Join("..", "..", "..", "examples", "basic",
		"vjunos-router-multitool.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	apply(t, namespace, strings.ReplaceAll(string(manifest), defaultImage, image))
	for _, node := range []string{"juniper", "multitool"} {
		clabernetestesthelper.KubectlWaitForCreate(t, "nodes.c9s.run", namespace, node)
		runKubectl(t, "wait", "-n", namespace, "--for=jsonpath={.status.readiness}=ready",
			"--timeout=30m", "node.c9s.run/"+node)
	}
	container := clabernetestesthelper.DirectDeviceContainerName(t, namespace, "multitool")
	//nolint:prealloc // Each appended command needs its own argument slice.
	execArgs := []string{"exec", "-n", namespace, "deployment/multitool", "-c", container, "--"}
	clabernetestesthelper.KubectlWaitForOutput(t, 2*time.Minute,
		append(execArgs, "ip", "-4", "address", "show", "dev", "eth1"), "192.0.2.2/30")
	management := strings.TrimSpace(string(runKubectl(t, "get", "node.c9s.run/juniper",
		"-n", namespace, "-o", "jsonpath={.status.directManagement.ipv4}")))
	prefix, err := netip.ParsePrefix(management)
	if err != nil {
		t.Fatalf("invalid Juniper management address %q: %v", management, err)
	}
	sshArgs := slices.Clone(execArgs)
	sshArgs = append(sshArgs, "sh", "-c", sshScript, "ssh-check", prefix.Addr().String())
	clabernetestesthelper.KubectlWaitForOutput(t, 2*time.Minute, append(sshArgs,
		"show configuration interfaces ge-0/0/0 | display set | no-more"),
		"set interfaces ge-0/0/0 unit 0 family inet address 192.0.2.1/30")

	// Bind both ping sources to the data link, independent of management reachability.
	for _, args := range [][]string{
		append(execArgs, "ping", "-I", "eth1", "-c", "3", "-W", "3", "192.0.2.1"),
		append(sshArgs, "ping 192.0.2.2 source 192.0.2.1 count 3 rapid"),
	} {
		clabernetestesthelper.KubectlWaitForOutput(t, 10*time.Minute, args, " 0% packet loss")
		t.Log(string(runKubectl(t, args...)))
	}
}

// TestBoxenVJunosRouterLiveLinks verifies the complete router image, including its PFE.
// Like the example test, it requires an explicit manual opt-in.
func TestBoxenVJunosRouterLiveLinks(t *testing.T) {
	image := routerImage(t)
	namespace := clabernetestesthelper.NewTestNamespace("topology-vjunos-router")
	clabernetestesthelper.KubectlCreateNamespace(t, namespace)
	defer func() {
		if t.Failed() {
			clabernetestesthelper.DumpNamespaceDiagnostics(t, namespace)
		}
		if !*clabernetestesthelper.SkipCleanup {
			clabernetestesthelper.KubectlDeleteNamespace(t, namespace)
		}
	}()

	clabernetestesthelper.CreateGHCRPullSecret(t, namespace, "vjunos-registry")
	apply(t, namespace, fmt.Sprintf(`apiVersion: c9s.run/v1alpha1
kind: NodeProfile
metadata:
  name: router
spec:
  expose:
    disableAutoExpose: true
  imagePull:
    pullSecrets: [vjunos-registry]
  resources:
    requests:
      cpu: "4"
      memory: 6Gi
    limits:
      memory: 6Gi
---
apiVersion: c9s.run/v1alpha1
kind: Node
metadata:
  name: router
spec:
  kind: juniper_vjunosrouter
  image: %s
  profileRef:
    name: router
  healthcheck:
    test: [CMD, /boxen/boxen, health]
    interval: 5
    timeout: 5
    retries: 1
    start-period: 1200
  startup-config: |
    system {
        domain-name startup.boxen.example;
    }
    interfaces {
        ge-0/0/0 {
            unit 0 {
                family inet {
                    address 192.0.2.1/30;
                }
            }
        }
        ge-0/0/95 {
            unit 0 {
                family inet {
                    address 192.0.2.5/30;
                }
            }
        }
    }
---
apiVersion: c9s.run/v1alpha1
kind: Node
metadata:
  name: peer
spec:
  kind: linux
  image: ghcr.io/srl-labs/network-multitool:latest
`, image))

	for _, node := range []string{"router", "peer"} {
		clabernetestesthelper.KubectlWaitForCreate(t, "nodes.c9s.run", namespace, node)
		runKubectl(t, "wait", "-n", namespace, "--for=jsonpath={.status.readiness}=ready",
			"--timeout="+bootTimeout.String(), "node.c9s.run/"+node)
	}
	peerContainer := clabernetestesthelper.DirectDeviceContainerName(t, namespace, "peer")
	// Authenticate to Junos on each allocated management address and check the staged config.
	for _, family := range []string{"ipv4", "ipv6"} {
		value := strings.TrimSpace(string(runKubectl(t, "get", "node.c9s.run/router",
			"-n", namespace, "-o", "jsonpath={.status.directManagement."+family+"}")))
		if value == "" && family == "ipv6" {
			t.Log("cluster allocated only IPv4 management")

			continue
		}
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			t.Fatalf("invalid allocated management %s %q: %v", family, value, err)
		}
		clabernetestesthelper.KubectlWaitForOutput(t, 2*time.Minute, []string{
			"exec", "-n", namespace, "deployment/peer", "-c", peerContainer, "--",
			"sh", "-c", sshScript, "ssh-check", prefix.Addr().String(),
			"show configuration system domain-name | display set | no-more",
		}, "set system domain-name startup.boxen.example")
	}

	initial := map[string]string{}
	for _, node := range []string{"router", "peer"} {
		initial[node] = podIdentity(t, namespace, node)
	}
	const link = `apiVersion: c9s.run/v1alpha1
kind: Link
metadata:
  name: wire
spec:
  endpointA:
    nodeName: router
    interfaceName: ge-0/0/%d
  endpointB:
    nodeName: peer
    interfaceName: eth1
`
	// Exercise the first and last data ports. Both Links arrive after the VM is healthy;
	// replacing one must wire the existing taps without restarting the guest or its Pod.
	for attempt, port := range []struct {
		index       int
		peerAddress string
		target      string
	}{
		{0, "192.0.2.2/30", "192.0.2.1"},
		{95, "192.0.2.6/30", "192.0.2.5"},
	} {
		apply(t, namespace, fmt.Sprintf(link, port.index))
		clabernetestesthelper.KubectlWaitForOutput(t, 5*time.Minute, []string{
			"exec", "-n", namespace, "deployment/peer", "-c", peerContainer, "--", "sh", "-c",
			"ip link set eth1 up && ip address replace " + port.peerAddress + " dev eth1 && " +
				"ping -c 2 -W 3 " + port.target,
		}, " 0% packet loss")
		assertPodsUnchanged(t, namespace, initial)
		if attempt == 0 {
			runKubectl(t, "delete", "link.c9s.run/wire", "-n", namespace, "--wait=true")
			clabernetestesthelper.KubectlWaitForOutput(t, 2*time.Minute, []string{
				"exec", "-n", namespace, "deployment/peer", "-c", peerContainer, "--", "sh", "-c",
				"if ! ip link show eth1 >/dev/null 2>&1; then printf 'interface removed'; fi",
			}, "interface removed")
			assertPodsUnchanged(t, namespace, initial)
		}
	}
}

func routerImage(t *testing.T) string {
	t.Helper()
	if os.Getenv("VJUNOS_ROUTER_E2E") == "" {
		t.Skip("VJUNOS_ROUTER_E2E is not set")
	}
	if image := os.Getenv("VJUNOS_ROUTER_IMAGE"); image != "" {
		return image
	}

	return defaultImage
}

func podIdentity(t *testing.T, namespace, node string) string {
	t.Helper()
	identity := strings.TrimSpace(string(runKubectl(
		t,
		"get",
		"pods",
		"-n",
		namespace,
		"-l",
		"c9s.run/direct-workload="+node,
		"-o",
		`jsonpath={.items[0].metadata.uid}{"\n"}{range .items[0].status.containerStatuses[*]}{.name}{":"}{.restartCount}{"\n"}{end}`,
	)))
	if identity == "" {
		t.Fatalf("%s has no device Pod", node)
	}

	return identity
}

func assertPodsUnchanged(t *testing.T, namespace string, initial map[string]string) {
	t.Helper()
	for node, identity := range initial {
		if got := podIdentity(t, namespace, node); got != identity {
			t.Fatalf("live Link changed %s Pod identity or restart counts: %q -> %q",
				node, identity, got)
		}
	}
}

func apply(t *testing.T, namespace, manifest string) {
	t.Helper()
	//nolint:gosec // kubectl arguments are test-controlled.
	cmd := exec.CommandContext(t.Context(), "kubectl", "apply", "-n", namespace, "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	clabernetestesthelper.Execute(t, cmd)
}

func runKubectl(t *testing.T, args ...string) []byte {
	t.Helper()
	//nolint:gosec // kubectl arguments are test-controlled.
	cmd := exec.CommandContext(t.Context(), "kubectl", args...)

	return clabernetestesthelper.Execute(t, cmd)
}
