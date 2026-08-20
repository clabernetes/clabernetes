package cli_test

import (
	"testing"

	clabernetescli "github.com/clabernetes/clabernetes/cmd/clabernetes/cli"
)

// TestEntrypointShipsNoLauncherCommand is the CLI half of the nested-runtime removal negative
// verification: the shipped binary must not expose the retired launcher entrypoint.
func TestEntrypointShipsNoLauncherCommand(t *testing.T) {
	t.Parallel()

	for _, command := range clabernetescli.Entrypoint().Commands {
		if command.Name == "launch" {
			t.Fatal("the retired launcher command is still registered")
		}
	}
}
