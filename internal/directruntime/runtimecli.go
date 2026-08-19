package directruntime

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	clabernetesdeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
)

// RuntimeExecTargetEnvironmentVariable names the only container the runtime-CLI shim may
// execute into: the plan-declared exec target of the running lifecycle boundary. The shim runs
// inside that container already, so realization is a local process exec and anything else fails
// closed.
const RuntimeExecTargetEnvironmentVariable = "C9S_RUNTIME_EXEC_TARGET"

// runtimeCLINames are the runtime binaries imported packages spawn to open CLI sessions
// (`<runtime> exec -it <container> <command...>`). The direct runtime publishes these names as
// links to its own binary so the sessions are realized application-locally.
var runtimeCLINames = []string{"docker", "podman"}

// IsRuntimeCLIInvocation reports whether this process was started through one of the published
// runtime-CLI shim names.
func IsRuntimeCLIInvocation(argument string) bool {
	base := filepath.Base(argument)
	for _, name := range runtimeCLINames {
		if base == name {
			return true
		}
	}

	return false
}

// RunRuntimeCLIShim realizes `<runtime> exec [flags] <container> <command...>` as a local
// process session: imported packages open CLI sessions through their container runtime's CLI,
// and inside a direct Pod the lifecycle boundary already executes in the target container. Only
// the plan-declared exec target is accepted; every other runtime-CLI operation fails closed.
// The command runs on its own pseudo-terminal with this process acting as the terminal — it
// answers terminal capability queries and forwards a query-free stream — because that is what
// `<runtime> exec -it` gives an interactive CLI, and screen-scraping callers cannot tolerate
// unanswered query sequences glued to the prompt.
func RunRuntimeCLIShim(arguments []string) error {
	if len(arguments) < 2 || arguments[1] != "exec" {
		return fmt.Errorf("runtime CLI shim realizes only application-local exec sessions")
	}
	rest := arguments[2:]
	for len(rest) > 0 && strings.HasPrefix(rest[0], "-") {
		rest = rest[1:]
	}
	if len(rest) < 2 {
		return fmt.Errorf("runtime CLI exec requires a container and a command")
	}
	container, command := rest[0], rest[1:]
	expected := os.Getenv(RuntimeExecTargetEnvironmentVariable)
	if expected == "" || container != expected {
		return fmt.Errorf(
			"runtime CLI exec targets a container other than the plan-declared exec target",
		)
	}
	binary, err := exec.LookPath(command[0])
	if err != nil {
		return fmt.Errorf("runtime CLI exec command is unavailable: %w", err)
	}

	return runTerminalSession(binary, command)
}

// prepareImportedRuntimeCLI publishes the runtime-CLI realization to imported package hooks
// executing in this process: the shim names resolve ahead of any image binaries, and the only
// accepted exec target is the boundary's plan-declared container.
func prepareImportedRuntimeCLI(plan clabernetesdeviceplan.Plan, containerID string) {
	runtimeID := ""
	for _, container := range plan.Containers {
		if container.ID == containerID {
			runtimeID = container.RuntimeID

			break
		}
	}
	if runtimeID != "" {
		os.Setenv(RuntimeExecTargetEnvironmentVariable, runtimeID)
	}
	// Any c9s binary this boundary spawns onto a screen-scraped pseudo-terminal must stay
	// terminal-silent: styling libraries probe TTYs with in-band capability queries, and those
	// bytes would pollute the session a package CLI reader is matching against. The session
	// command itself receives a real terminal identity from the shim.
	os.Setenv("TERM", "dumb")
	os.Setenv("NO_COLOR", "1")
	executable, err := os.Executable()
	if err != nil {
		return
	}
	directory := filepath.Dir(executable)
	path := os.Getenv("PATH")
	if path == "" || strings.HasPrefix(path, directory+string(os.PathListSeparator)) {
		return
	}
	os.Setenv("PATH", directory+string(os.PathListSeparator)+path)
}
