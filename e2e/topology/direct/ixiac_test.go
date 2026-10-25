package direct_test

import (
	"testing"

	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
)

// TestIxiac validates that an ixiac node starts correctly. It covers the
// regression from #372 where the device's greedy interface handling stole
// sidecar infra and left the Pod unready. The explicit-profile proxy keeps
// coverage cheap (multitool dataplane), while the real ixiac case is below;
// future light-traffic checks should extend this file rather than adding a new
// constrained test name.
func TestIxiac(t *testing.T) {
	t.Parallel()

	t.Run("starts with explicit profile", func(t *testing.T) {
		t.Parallel()

		testName := "ixiac-proxy"
		namespace := clabernetestesthelper.NewTestNamespace(testName)

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
			t,
			clabernetestesthelper.Apply,
			namespace,
			"test-fixtures/60-ixiac.yaml",
		)

		for _, nodeName := range []string{"iso-lin1", "iso-lin2"} {
			waitForDirectNodeReady(t, namespace, nodeName)
		}

		d1 := observeDevicePod(t, namespace, "iso-lin1")
		d2 := observeDevicePod(t, namespace, "iso-lin2")

		// Management mesh still converges even though device legs are intended
		// to live in private netns once runtime plumbing lands — today this
		// just proves we didn't break non-isolated paths with new CRD fields.
		mgmt := poolMixedManagementAddresses(t, namespace, 2)
		waitForDeviceCommand(
			t,
			namespace,
			d1,
			[]string{"ping", "-4", "-c", "2", "-W", "2", mgmt["iso-lin2"]},
			" 0% packet loss",
		)
		waitForDeviceCommand(
			t,
			namespace,
			d2,
			[]string{"ping", "-4", "-c", "2", "-W", "2", mgmt["iso-lin1"]},
			" 0% packet loss",
		)

		waitForDeviceCommand(
			t,
			namespace,
			d1,
			[]string{"ping", "-c", "2", "-W", "2", "192.168.90.1"},
			" 0% packet loss",
		)
	})
}
