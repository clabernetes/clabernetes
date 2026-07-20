package cli

import (
	clabernetesclicker "github.com/clabernetes/clabernetes/clicker"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	claberneteslauncher "github.com/clabernetes/clabernetes/launcher"
	clabernetesmanager "github.com/clabernetes/clabernetes/manager"
	"github.com/urfave/cli/v2"
)

const (
	// indicates the manager command is being run in init/initialization mode (as in, in an init
	// container).
	cliInitializer = "initializer"

	// indicates the clicker invocation should target all nodes; if unset we only target nodes that
	// do *not* have the LabelClickerNodeConfigured label set. Note that this is applied after the
	// selector (if present), so it's not technically "all" nodes, its all nodes that were selected.
	clickerOverrideNodes = "overrideNodes"

	// indicates the node selector filter that should be applied to the nodes clicker targets.
	clickerNodeSelector = "nodeSelector"

	// indicates that the clicker job should *not* cleanup the "worker" pods it creates. useful for
	// troubleshooting so we can see logs and such, without this the pods get cleaned up way too
	// quickly to investigate!
	clickerSkipPodCleanup = "skipPodCleanup"

	// indicates that the clicker job should *not* cleanup the configmap it creates.
	clickerSkipConfigMapCleanup = "skipConfigMapCleanup"
)

// Entrypoint returns the clabernetes manager entrypoint, kicking off one of the clabernetes
// processes.
func Entrypoint() *cli.App {
	cli.VersionPrinter = ShowVersion

	return &cli.App{
		Name:    "clabernetes",
		Version: clabernetesconstants.Version,
		Usage:   "run clabernetes manager",
		Commands: []*cli.Command{
			{
				Name:  "run",
				Usage: "run the manager",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:     cliInitializer,
						Usage:    "indicate if this instance should run initialization",
						Required: false,
						Value:    false,
					},
				},
				Action: func(c *cli.Context) error {
					clabernetesmanager.StartClabernetes(
						c.Bool(cliInitializer),
					)

					return nil
				},
			},
			{
				Name:  "launch",
				Usage: "run the launcher",
				Flags: []cli.Flag{},
				Action: func(_ *cli.Context) error {
					claberneteslauncher.StartClabernetes()

					return nil
				},
			},
			{
				Name:  "clicker",
				Usage: "run the node clicker",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name: clickerOverrideNodes,
						Usage: "indicates if the clicker should be re-ran on all nodes" +
							" even if they already have a clabernetes clicker label",
						Required: false,
						Value:    false,
					},
					&cli.StringFlag{
						Name: clickerNodeSelector,
						Usage: "node selector to target which nodes clicker should" +
							" execute on",
						Required: false,
						Value:    "",
					},
					&cli.BoolFlag{
						Name: clickerSkipConfigMapCleanup,
						Usage: "indicates if the clicker should skip cleaning up the configmap" +
							" it creates",
						Required: false,
						Value:    false,
					},
					&cli.BoolFlag{
						Name: clickerSkipPodCleanup,
						Usage: "indicates if the clicker should skip cleaning up the worker pods" +
							" it creates",
						Required: false,
						Value:    false,
					},
				},
				Action: func(c *cli.Context) error {
					clabernetesclicker.StartClabernetes(
						&clabernetesclicker.Args{
							OverrideNodes:        c.Bool(clickerOverrideNodes),
							NodeSelector:         c.String(clickerNodeSelector),
							SkipConfigMapCleanup: c.Bool(clickerSkipConfigMapCleanup),
							SkipPodsCleanup:      c.Bool(clickerSkipPodCleanup),
						},
					)

					return nil
				},
			},
		},
	}
}
