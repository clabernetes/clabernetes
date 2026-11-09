package testhelper

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// DefaultDeviceContainerAnnotation mirrors directpod.KubectlDefaultContainerAnnotation without
// making this public test helper depend on an internal package.
const DefaultDeviceContainerAnnotation = "kubectl.kubernetes.io/default-container"

// DirectDeviceContainerName resolves the primary direct device container of one logical Node's
// workload so tests can exec against the actual device process.
func DirectDeviceContainerName(t *testing.T, namespace, workload string) string {
	t.Helper()

	cmd := exec.CommandContext( //nolint:gosec // kubectl arguments are test-controlled.
		t.Context(),
		"kubectl",
		"get",
		"pods",
		"--namespace",
		namespace,
		"--selector",
		"c9s.run/direct-workload="+workload,
		"-o",
		fmt.Sprintf(
			"jsonpath={.items[0].metadata.annotations['%s']}",
			DefaultDeviceContainerAnnotation,
		),
	)

	containerName := strings.TrimSpace(string(Execute(t, cmd)))
	if containerName == "" {
		t.Fatalf("workload %q has no primary direct device container", workload)
	}

	return containerName
}
