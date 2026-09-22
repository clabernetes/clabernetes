package direct_test

import (
	"testing"

	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
)

// TestLinuxWithoutManagement proves the Topology option reaches the realized Pod: the
// management mesh is absent while CNI addressing and the cross-Pod data link still work.
func TestLinuxWithoutManagement(t *testing.T) {
	t.Parallel()
	namespace := clabernetestesthelper.NewTestNamespace("direct-no-management")
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
		t, clabernetestesthelper.Apply, namespace, "test-fixtures/40-no-management.yaml",
	)
	for index, name := range []string{"lin1", "lin2"} {
		waitForDirectNodeReady(t, namespace, name)
		device := observeDevicePod(t, namespace, name)
		waitForDeviceCommand(t, namespace, device, []string{"sh", "-c", `
set -e
for interface in c9s0 c9sr0 c9sm0; do
  if ip link show dev "$interface" >/dev/null 2>&1; then exit 1; fi
done
ip -4 address show dev eth0 | grep 'inet '
echo no-management-ok
`}, "no-management-ok")
		peer := "192.168.1.1"
		if index == 1 {
			peer = "192.168.1.0"
		}
		waitForDeviceCommand(t, namespace, device,
			[]string{"ping", "-I", "eth1", "-c", "2", "-W", "2", peer}, " 0% packet loss")
	}
}
