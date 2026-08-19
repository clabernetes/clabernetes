package directruntime_test

import (
	"testing"

	clabernetesdirectruntime "github.com/clabernetes/clabernetes/internal/directruntime"
)

func TestRuntimeCLIShimFailsClosedOutsideTheDeclaredExecSurface(t *testing.T) {
	t.Setenv(clabernetesdirectruntime.RuntimeExecTargetEnvironmentVariable, "device-a")

	for name, arguments := range map[string][]string{
		"non-exec operation":  {"docker", "run", "device-a", "Cli"},
		"missing command":     {"docker", "exec", "-it", "device-a"},
		"foreign container":   {"docker", "exec", "-it", "device-b", "Cli"},
		"no operation at all": {"docker"},
	} {
		if err := clabernetesdirectruntime.RunRuntimeCLIShim(arguments); err == nil {
			t.Fatalf("%s was accepted by the runtime CLI shim", name)
		}
	}
}

func TestRuntimeCLIShimRequiresADeclaredExecTarget(t *testing.T) {
	t.Setenv(clabernetesdirectruntime.RuntimeExecTargetEnvironmentVariable, "")

	err := clabernetesdirectruntime.RunRuntimeCLIShim(
		[]string{"docker", "exec", "-it", "device-a", "Cli"},
	)
	if err == nil {
		t.Fatal("exec without a declared target was accepted")
	}
}

func TestRuntimeCLIInvocationDetection(t *testing.T) {
	t.Parallel()

	if !clabernetesdirectruntime.IsRuntimeCLIInvocation(
		"/var/lib/clabernetes/lifecycle-bin/docker",
	) ||
		!clabernetesdirectruntime.IsRuntimeCLIInvocation("podman") ||
		clabernetesdirectruntime.IsRuntimeCLIInvocation("/usr/local/bin/manager") {
		t.Fatal("runtime CLI shim name detection is wrong")
	}
}
