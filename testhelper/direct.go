package testhelper

import (
	"os/exec"
	"strings"
	"testing"
)

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
		`jsonpath={range .items[0].spec.containers[*]}{.name}{"\n"}{end}`,
	)

	output := Execute(t, cmd)
	for name := range strings.SplitSeq(string(output), "\n") {
		if strings.HasPrefix(name, "node-") {
			return name
		}
	}

	t.Fatalf("workload %q has no direct device container: %s", workload, output)

	return ""
}
