package direct_test

import (
	"context"
	"encoding/json"
	"net/netip"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	k8scorev1 "k8s.io/api/core/v1"
)

// poolMixedCheckManagement probes from the actual device containers, including SR Linux's
// management namespace. Short lab names check the peer directory; Service FQDNs check DNS.
func poolMixedCheckManagement(
	t *testing.T, namespace string, pods map[string]k8scorev1.Pod,
) map[string]string {
	t.Helper()
	addresses := poolMixedManagementAddresses(t, namespace, len(pods))
	names := make([]string, 0, len(pods))
	for name := range pods {
		names = append(names, name)
	}
	slices.Sort(names)
	client := observeDevicePod(t, namespace, "mt-srl")
	for _, name := range names {
		device := observeDevicePod(t, namespace, name)
		healthy := poolMixedCheckOutbound(t, namespace, client, device, name, addresses)
		// Check inbound management from another device, not just the address in Node status.
		if name != "mt-srl" {
			for _, target := range []string{addresses[name], name} {
				healthy = poolMixedManagementProbe(
					t,
					namespace,
					client,
					[]string{
						"ping",
						"-4",
						"-c",
						"2",
						"-W",
						"2",
						target,
					},
					" 0% packet loss",
				) && healthy
			}
		}
		if !strings.HasPrefix(name, "mt-") {
			for _, target := range []string{addresses[name], name + "." + namespace + ".svc.cluster.local"} {
				healthy = poolMixedManagementProbe(t, namespace, client, []string{
					"sh", "-c", `timeout 5 nc "$1" 22 </dev/null | head -n 1`, "ssh-banner", target,
				}, "SSH-2.0") && healthy
			}
		}
		if healthy {
			t.Logf(
				"%s: management %s, bidirectional reachability, peer name and Service DNS passed",
				name,
				addresses[name],
			)
		}
	}
	if _, added := pods["ceos-new"]; added {
		// The existing EOS Pod must learn a new peer even though its application replaced
		// the kubelet hosts mount during boot and lab membership does not restart it.
		poolMixedManagementProbe(t, namespace, observeDevicePod(t, namespace, "ceos"),
			[]string{"ping", "-4", "-c", "2", "-W", "2", "ceos-new"}, " 0% packet loss")
	}
	kubernetesIP := strings.TrimSpace(string(poolKubectl(t, "get", "service", "kubernetes",
		"-n", "default", "-o", "jsonpath={.spec.clusterIP}")))
	poolMixedManagementProbe(t, namespace, client,
		[]string{"dig", "+short", "kubernetes.default.svc.cluster.local", "A"}, kubernetesIP)

	return addresses
}

// SR-SIM 26.7.R1 does not populate native BOF DNS from the CPM's Linux resolver, including
// under standalone Containerlab. Configure this test's native resolver explicitly; retain
// management IP probes separately from DNS names, which resolve to Kubernetes Pod addresses.
func poolMixedConfigureSRSimDNS(t *testing.T, namespace string) {
	t.Helper()
	content, err := os.ReadFile("test-fixtures/planner-pool-mixed-sros-dns.cli")
	if err != nil {
		t.Fatal(err)
	}
	server := strings.TrimSpace(string(poolKubectl(t, "get", "service", "kube-dns",
		"-n", "kube-system", "-o", "jsonpath={.spec.clusterIP}")))
	if address, parseErr := netip.ParseAddr(server); parseErr != nil || !address.Is4() {
		t.Fatalf("mixed-vendor DNS test needs an IPv4 cluster DNS Service: %q", server)
	}
	command := strings.NewReplacer(
		"__DNS_SERVER__", server,
		"__DNS_DOMAIN__", namespace+".svc.cluster.local",
	).Replace(string(content))
	addresses := poolMixedManagementAddresses(t, namespace, 6)
	poolMixedSRSimProbe(t, namespace, observeDevicePod(t, namespace, "mt-srl"),
		addresses["sros"], command, "\n        primary-server "+server)
}

func poolMixedManagementAddresses(t *testing.T, namespace string, count int) map[string]string {
	t.Helper()
	var nodes clabernetesapisv1alpha1.NodeList
	if err := json.Unmarshal(poolKubectl(t, "get", "nodes.c9s.run", "-n", namespace, "-o", "json"), &nodes); err != nil {
		t.Fatal(err)
	}
	addresses, allocated := map[string]string{}, map[string]string{}
	for _, node := range nodes.Items {
		if node.Status.DirectManagement == nil {
			t.Fatalf("%s has no management allocation", node.Name)
		}
		prefix, err := netip.ParsePrefix(node.Status.DirectManagement.IPv4)
		if err != nil {
			t.Fatalf("%s has invalid management IPv4: %v", node.Name, err)
		}
		address := prefix.Addr().String()
		if prior := allocated[address]; prior != "" {
			t.Fatalf("%s and %s share management address %s", prior, node.Name, address)
		}
		allocated[address], addresses[node.Name] = node.Name, address
	}
	if len(addresses) != count {
		t.Fatalf("management inventory has %d Nodes for %d Pods", len(addresses), count)
	}

	return addresses
}

func poolMixedCheckOutbound(
	t *testing.T, namespace string, client, device devicePodObservation,
	name string, addresses map[string]string,
) bool {
	t.Helper()
	var healthy bool
	if name == "sros" {
		healthy = poolMixedSRSimProbe(t, namespace, client, addresses[name],
			"show router management interface", addresses[name]+"/")
	} else {
		healthy = poolMixedManagementProbe(t, namespace, device,
			poolMixedManagementCommand(name, "ip", "-o", "-4", "address", "show"),
			"inet "+addresses[name]+"/")
	}
	peer := "mt-srl"
	if name == peer {
		peer = "mt-ceos"
	}
	peerName := peer
	if name == "sros" {
		peerName += "-dns" // Native SR OS uses the explicit headless DNS alias.
	}
	for _, target := range []string{addresses[peer], peerName, peer + "-wire." + namespace + ".svc.cluster.local"} {
		if name == "sros" {
			healthy = poolMixedSRSimProbe(
				t,
				namespace,
				client,
				addresses[name],
				"ping "+target+" router-instance management count 2",
				" 0.00% packet loss",
			) && healthy
		} else {
			healthy = poolMixedManagementProbe(t, namespace, device,
				poolMixedManagementCommand(name, "ping", "-4", "-c", "2", "-W", "2", target),
				" 0% packet loss") && healthy
		}
	}

	return healthy
}

// Keep independent checks running after one fails, so a resolver failure does not hide
// data-link or membership regressions. Three bounded attempts allow normal convergence.
func poolMixedManagementProbe(
	t *testing.T, namespace string, device devicePodObservation, command []string, expect string,
) bool {
	t.Helper()

	return poolMixedManagementProbeNamed(
		t,
		namespace,
		device,
		command,
		expect,
		strings.Join(command, " "),
	)
}

func poolMixedManagementProbeNamed(
	t *testing.T,
	namespace string,
	device devicePodObservation,
	command []string,
	expect, label string,
) bool {
	t.Helper()
	arguments := append(
		[]string{"exec", "-n", namespace, device.podName, "-c", device.containerName, "--"},
		command...)
	var output []byte
	var err error
	for attempt := range 3 {
		if attempt > 0 {
			time.Sleep(2 * time.Second)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		//nolint:gosec // Test-controlled kubectl arguments.
		output, err = exec.CommandContext(ctx, "kubectl", arguments...).CombinedOutput()
		cancel()
		if err == nil && strings.Contains(string(output), expect) {
			t.Logf("management probe passed: %s %s", device.podName, label)

			return true
		}
	}
	t.Errorf("management probe %s %s failed: %v: %s", device.podName, label, err, output)

	return false
}

// SR-SIM's management address belongs to SR OS, not the component containers' Linux IP
// stack. Probe the native CLI over SSH using the image's documented default credentials.
func poolMixedSRSimProbe(
	t *testing.T, namespace string, client devicePodObservation, address, command, expect string,
) bool {
	t.Helper()
	const script = `helper=$(mktemp)
trap 'rm -f "$helper"' EXIT
cat > "$helper" <<'ASKPASS'
#!/bin/sh
printf '%s\n' 'NokiaSros1!'
ASKPASS
chmod 700 "$helper"
printf 'environment more false\n%s\nlogout\n' "$2" | DISPLAY=:0 SSH_ASKPASS_REQUIRE=force SSH_ASKPASS="$helper" timeout 12 ssh -tt -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=5 -o LogLevel=ERROR "admin@$1"
`

	return poolMixedManagementProbeNamed(t, namespace, client,
		[]string{"sh", "-c", script, "sros-cli", address, command}, expect, "SR OS CLI: "+command)
}

func poolMixedManagementCommand(node string, command ...string) []string {
	if node == "srl" {
		return append([]string{"ip", "netns", "exec", "srbase-mgmt"}, command...)
	}

	return command
}

func poolMixedAssertManagementStable(t *testing.T, before, after map[string]string) {
	t.Helper()
	for name, address := range before {
		if after[name] != address {
			t.Fatalf("management address for %s changed: %s -> %s", name, address, after[name])
		}
	}
}

func poolMixedConfigureAddedNodePort(t *testing.T, namespace string) {
	t.Helper()
	srl := observeDevicePod(t, namespace, "srl")
	waitForDeviceCommand(t, namespace, srl, []string{
		"bash", "-c",
		`printf 'enter candidate\nset / interface ethernet-1/6 admin-state enable\n` +
			`set / interface ethernet-1/6 subinterface 0 ipv4 admin-state enable\n` +
			`set / interface ethernet-1/6 subinterface 0 ipv4 address 10.210.14.1/30\n` +
			`set / network-instance default interface ethernet-1/6.0\ncommit now\nquit\n' | sr_cli`,
	}, "committed")
}
