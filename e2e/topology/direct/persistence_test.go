package direct_test

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
)

// TestDirectPersistenceSavedConfigSurvival proves the persistence contract end to end: a
// configuration change saved through the package-owned Save lifecycle survives Pod replacement
// on a persistent artifact volume, and the device-state reset annotation re-seeds the Node back
// to its declared startup configuration.
func TestDirectPersistenceSavedConfigSurvival(t *testing.T) {
	t.Parallel()

	testName := "topology-direct-persistence"
	namespace := clabernetestesthelper.NewTestNamespace(testName)
	clabernetestesthelper.KubectlCreateNamespace(t, namespace)

	defer func() {
		if !*clabernetestesthelper.SkipCleanup {
			t.Logf("deleting namespace %q used in test %q", namespace, testName)
			clabernetestesthelper.KubectlDeleteNamespace(t, namespace)
		}
	}()

	clabernetestesthelper.KubectlFileOp(
		t,
		clabernetestesthelper.Apply,
		namespace,
		"test-fixtures/40-persistence.yaml",
	)

	waitForDirectNodeReady(t, namespace, "srl1")

	device := observeDevicePod(t, namespace, "srl1")

	waitForDeviceCommand(
		t,
		namespace,
		device,
		[]string{"sr_cli", "info from running /system information location"},
		"seeded-by-startup-config",
	)

	// change the running configuration the way a user would, then save through the same typed
	// lifecycle entrypoint the workload already uses
	changeCommand := []string{
		"bash", "-c",
		`printf 'enter candidate\nset / system information location saved-by-user\n` +
			`commit now\nquit\n' | sr_cli`,
	}
	waitForDeviceCommand(t, namespace, device, changeCommand, "committed")

	saveCommand := lifecyclePhaseCommand(t, namespace, device, "Save")

	saveArguments := append(
		[]string{
			"exec",
			"--namespace",
			namespace,
			device.podName,
			"-c",
			device.containerName,
			"--",
		},
		saveCommand...,
	)

	saveOutput, err := exec.CommandContext( //nolint:gosec // kubectl args are test-controlled.
		t.Context(),
		"kubectl",
		saveArguments...,
	).CombinedOutput()
	if err != nil {
		t.Fatalf("save lifecycle failed: %v: %s", err, saveOutput)
	}

	// persistence is enabled, so the ephemeral-volume warning must NOT appear
	if strings.Contains(string(saveOutput), "will not survive Pod replacement") {
		t.Fatalf("persistent save produced the ephemeral warning: %s", saveOutput)
	}

	// replace the Pod and prove the saved configuration is what the device boots from
	clabernetestesthelper.Execute(t, exec.CommandContext( //nolint:gosec
		t.Context(),
		"kubectl",
		"delete", "pod", "--namespace", namespace, device.podName, "--wait=true",
	))

	waitForDirectNodeReady(t, namespace, "srl1")

	replacement := observeDevicePod(t, namespace, "srl1")
	if replacement.podName == device.podName {
		t.Fatalf("device Pod %q was not replaced", device.podName)
	}

	waitForDeviceCommand(
		t,
		namespace,
		replacement,
		[]string{"sr_cli", "info from running /system information location"},
		"saved-by-user",
	)

	// the device-state reset annotation must re-seed the declared startup configuration
	token := fmt.Sprintf("e2e-%d", time.Now().Unix())
	clabernetestesthelper.Execute(t, exec.CommandContext( //nolint:gosec
		t.Context(),
		"kubectl",
		"annotate", "--namespace", namespace, "node.c9s.run", "srl1",
		"c9s.run/device-state-reset="+token, "--overwrite",
	))

	waitForDevicePodReplacement(t, namespace, "srl1", replacement.podName)
	waitForDirectNodeReady(t, namespace, "srl1")

	reseeded := observeDevicePod(t, namespace, "srl1")

	waitForDeviceCommand(
		t,
		namespace,
		reseeded,
		[]string{"sr_cli", "info from running /system information location"},
		"seeded-by-startup-config",
	)
}
