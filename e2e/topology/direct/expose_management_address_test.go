package direct_test

import (
	"os/exec"
	"strings"
	"testing"

	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
)

// TestExposeRequestsManagementAddress proves useNodeMgmtIpv4Address requests the management
// address each device is configured with, for a pinned and for an allocated address, so the
// LoadBalancer IP and the device's management port carry the same address. It asserts the
// request only; whether a provider honors it depends on the cluster.
func TestExposeRequestsManagementAddress(t *testing.T) {
	t.Parallel()
	namespace := clabernetestesthelper.NewTestNamespace("direct-expose-mgmt")
	clabernetestesthelper.KubectlCreateNamespace(t, namespace)
	defer func() {
		if t.Failed() {
			clabernetestesthelper.DumpNamespaceDiagnostics(t, namespace)
		}
		if !*clabernetestesthelper.SkipCleanup {
			clabernetestesthelper.KubectlDeleteNamespace(t, namespace)
		}
	}()
	clabernetestesthelper.KubectlFileOp(
		t, clabernetestesthelper.Apply, namespace, "test-fixtures/50-expose-management-address.yaml",
	)

	for name, pinned := range map[string]string{"pinned": "198.18.57.10", "allocated": ""} {
		waitForDirectNodeReady(t, namespace, name)

		managementCIDR := kubectlJSONPath(
			t, namespace, "node.c9s.run", name, "{.status.directManagement.ipv4}",
		)
		address, _, _ := strings.Cut(managementCIDR, "/")
		if address == "" || !strings.HasPrefix(address, "198.18.57.") ||
			(pinned != "" && address != pinned) {
			t.Fatalf("node %q management address = %q", name, managementCIDR)
		}

		requested := kubectlJSONPath(t, namespace, "service", name, "{.spec.loadBalancerIP}")
		if requested != address {
			t.Fatalf(
				"node %q expose Service requests %q, want its management address %q",
				name,
				requested,
				address,
			)
		}

		device := observeDevicePod(t, namespace, name)
		waitForDeviceCommand(t, namespace, device,
			[]string{"ip", "-4", "-o", "address", "show", "dev", "eth0"}, " "+managementCIDR+" ")
	}
}

func kubectlJSONPath(t *testing.T, namespace, kind, name, path string) string {
	t.Helper()

	cmd := exec.CommandContext( //nolint:gosec
		t.Context(),
		"kubectl",
		"get",
		kind,
		name,
		"--namespace",
		namespace,
		"-o",
		"jsonpath="+path,
	)

	return strings.TrimSpace(string(clabernetestesthelper.Execute(t, cmd)))
}
