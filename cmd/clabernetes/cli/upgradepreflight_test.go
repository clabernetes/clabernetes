//nolint:err113,testpackage // dense fixture-driven tests exercise one boundary end to end.
package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	urfavecli "github.com/urfave/cli/v2"
)

func TestUpgradePreflightCommandPassesKubeconfigAndOutput(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("incompatible")
	output := &bytes.Buffer{}
	command := upgradePreflightCommand(func(
		ctx context.Context,
		kubeconfig string,
		writer io.Writer,
	) error {
		if ctx == nil {
			t.Fatal("runner received nil context")
		}

		if kubeconfig != "/tmp/c9s-upgrade-kubeconfig" {
			t.Fatalf("kubeconfig = %q", kubeconfig)
		}

		if writer != output {
			t.Fatalf("writer = %T, want app writer", writer)
		}

		return wantErr
	})
	app := &urfavecli.App{
		Writer:   output,
		Commands: []*urfavecli.Command{command},
	}

	err := app.RunContext(
		context.Background(),
		[]string{"clabernetes", "upgrade-preflight", "--kubeconfig", "/tmp/c9s-upgrade-kubeconfig"},
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("command error = %v, want %v", err, wantErr)
	}
}

func TestEntrypointIncludesUpgradePreflight(t *testing.T) {
	t.Parallel()

	for _, command := range Entrypoint().Commands {
		if command.Name == "upgrade-preflight" {
			return
		}
	}

	t.Fatal("Entrypoint() does not expose upgrade-preflight")
}
