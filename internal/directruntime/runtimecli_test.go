package directruntime_test

import (
	"testing"

	clabernetesinternaldirectruntime "github.com/clabernetes/clabernetes/internal/directruntime"
)

func TestRuntimeCLIShimFailsClosedOutsideTheDeclaredExecSurface(t *testing.T) {
	t.Setenv(clabernetesinternaldirectruntime.RuntimeExecTargetEnvironmentVariable, "device-a")

	for name, arguments := range map[string][]string{
		"non-exec operation":  {"docker", "run", "device-a", "Cli"},
		"missing command":     {"docker", "exec", "-it", "device-a"},
		"foreign container":   {"docker", "exec", "-it", "device-b", "Cli"},
		"no operation at all": {"docker"},
	} {
		if err := clabernetesinternaldirectruntime.RunRuntimeCLIShim(arguments); err == nil {
			t.Fatalf("%s was accepted by the runtime CLI shim", name)
		}
	}
}

func TestRuntimeCLIShimRequiresADeclaredExecTarget(t *testing.T) {
	t.Setenv(clabernetesinternaldirectruntime.RuntimeExecTargetEnvironmentVariable, "")

	err := clabernetesinternaldirectruntime.RunRuntimeCLIShim(
		[]string{"docker", "exec", "-it", "device-a", "Cli"},
	)
	if err == nil {
		t.Fatal("exec without a declared target was accepted")
	}
}

func TestRuntimeCLIInvocationDetection(t *testing.T) {
	t.Parallel()

	if !clabernetesinternaldirectruntime.IsRuntimeCLIInvocation(
		"/var/lib/clabernetes/lifecycle-bin/docker",
	) ||
		!clabernetesinternaldirectruntime.IsRuntimeCLIInvocation("podman") ||
		clabernetesinternaldirectruntime.IsRuntimeCLIInvocation("/usr/local/bin/manager") {
		t.Fatal("runtime CLI shim name detection is wrong")
	}
}
