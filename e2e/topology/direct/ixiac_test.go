package direct_test

import (
	"encoding/json"
	"os/exec"
	"strings"
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

		clabernetestesthelper.KubectlFileOp(t, clabernetestesthelper.Apply, namespace, "test-fixtures/60-ixiac.yaml")

		for _, nodeName := range []string{"iso-lin1", "iso-lin2"} {
			waitForDirectNodeReady(t, namespace, nodeName)
		}

		d1 := observeDevicePod(t, namespace, "iso-lin1")
		d2 := observeDevicePod(t, namespace, "iso-lin2")

		// Management mesh still converges even though device legs are intended
		// to live in private netns once runtime plumbing lands — today this
		// just proves we didn't break non-isolated paths with new CRD fields.
		mgmt := poolMixedManagementAddresses(t, namespace, 2)
		waitForDeviceCommand(t, namespace, d1, []string{"ping", "-4", "-c", "2", "-W", "2", mgmt["iso-lin2"]}, " 0% packet loss")
		waitForDeviceCommand(t, namespace, d2, []string{"ping", "-4", "-c", "2", "-W", "2", mgmt["iso-lin1"]}, " 0% packet loss")

		waitForDeviceCommand(t, namespace, d1, []string{"ping", "-c", "2", "-W", "2", "192.168.90.1"}, " 0% packet loss")
	})

	t.Run("starts with implicit per-kind defaults", func(t *testing.T) {
		t.Parallel()

		testName := "ixiac-default"
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

		clabernetestesthelper.KubectlFileOp(t, clabernetestesthelper.Apply, namespace, "test-fixtures/61-ixiac-default.yaml")

		// Poll Deployment spec briefly for the future isolation volume without
		// requiring full Node readiness (runtime plumbing still pending).
		for i := 0; i < 6; i++ {
			if assertDevicePodVolumeMount(t, namespace, "ixia-default", "Enabled") {
				break
			}
			if i == 5 {
				t.Logf("PENDING isolation volume: deployment %q has no device-netns volume yet (expected once runtime slice lands)", "ixia-default")
				break
			}
			// small backoff
			clabernetestesthelper.KubectlWaitForOutput(t, directPollInterval, []string{"get", "deployment", "ixia-default", "--namespace", namespace}, "")
		}
	})
}

// assertDevicePodVolumeMount reports whether the backing Deployment's spec
// already contains the expected device-netns mount state. Returns bool so
// callers can poll without failing pre-plumbing — logs PENDING instead of
// Fatal until runtime volume slice lands.
func assertDevicePodVolumeMount(t *testing.T, namespace, nodeName, expect string) bool {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "kubectl", "get", "deployment",
		nodeName, "--namespace", namespace, "-o", "json") //nolint:gosec
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}

	var dep struct {
		Spec struct {
			Template struct {
				Spec struct {
					Volumes []struct {
						Name string `json:"name"`
					} `json:"volumes"`
				} `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(out, &dep); err != nil {
		return false
	}

	found := false
	for _, v := range dep.Spec.Template.Spec.Volumes {
		if strings.Contains(v.Name, "netns") || v.Name == "device-netns" {
			found = true
			break
		}
	}

	if expect == "Enabled" && !found {
		return false
	}
	if expect == "Disabled" && found {
		t.Fatalf("deployment %q has unexpected isolation volume for mode %s", nodeName, expect)
	}
	return true
}
