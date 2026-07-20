package clicker

import (
	"os"

	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
)

func getCommand() []string {
	command := os.Getenv(clabernetesconstants.ClickerWorkerCommand)
	if command == "" {
		command = "/bin/sh"
	}

	return []string{command, "clicker"}
}

func getScript() string {
	script := os.Getenv(clabernetesconstants.ClickerWorkerScript)
	if script == "" {
		script = "echo 'hello, there'"
	}

	return script
}
