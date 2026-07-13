package cli

import (
	clabernetesclabverter "github.com/srl-labs/clabernetes/clabverter"
	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	claberneteslogging "github.com/srl-labs/clabernetes/logging"
	"github.com/urfave/cli/v2"
)

const (
	topologyFile         = "topologyFile"
	topoSpecFile         = "topoSpecFile"
	outputDirectory      = "outputDirectory"
	destinationNamespace = "destinationNamespace"
	insecureRegistries   = "insecureRegistries"
	imagePullSecrets     = "imagePullSecrets"
	naming               = "naming"
	containerlabVersion  = "containerlabVersion"
	disableExpose        = "disableExpose"
	emitCRs              = "emitCRs"
	debug                = "debug"
	quiet                = "quiet"
	stdout               = "stdout"
)

// Entrypoint returns the clabernetes clabverter entrypoint.
func Entrypoint() *cli.App {
	cli.VersionPrinter = ShowVersion

	return &cli.App{
		Name:    clabernetesconstants.Clabverter,
		Version: clabernetesconstants.Version,
		Usage:   "run clabernetes clabverter -- clab to clabernetes manifest(s) converter",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name: topologyFile,
				Usage: "set the topology file to parse. If not set, clabverter will look for" +
					" a file named '*.clab.y*ml'",
				Required: false,
				Value:    "",
			},
			&cli.StringFlag{
				Name: topoSpecFile,
				Usage: "set the values file to parse that will be included in the topology" +
					" manifest spec",
				Required: false,
				Value:    "",
			},
			&cli.StringFlag{
				Name:     outputDirectory,
				Usage:    "set the output directory for the converted manifest(s)",
				Required: false,
				Value:    "converted",
			},
			&cli.StringFlag{
				Name:     destinationNamespace,
				Usage:    "set the namespace for the rendered manifest(s)",
				Required: false,
				Value:    "",
			},
			&cli.StringFlag{
				Name:     insecureRegistries,
				Usage:    "comma separated list of insecure registries",
				Required: false,
				Value:    "",
			},
			&cli.StringFlag{
				Name:     imagePullSecrets,
				Usage:    "comma separated list of registry secrets",
				Required: false,
				Value:    "",
			},
			&cli.BoolFlag{
				Name:     disableExpose,
				Usage:    "disable exposing nodes via Load Balancer service",
				Required: false,
				Value:    false,
			},
			&cli.BoolFlag{
				Name: emitCRs,
				Usage: "emit Node/Link/NodeProfile manifests directly instead of a Topology" +
					" manifest -- exactly what the in-cluster compiler would emit",
				Required: false,
				Value:    false,
			},
			&cli.StringFlag{
				Name:     naming,
				Usage:    "naming scheme to use for clabernetes resources",
				Required: false,
				Value:    "prefixed",
			},
			&cli.StringFlag{
				Name:     containerlabVersion,
				Usage:    "an explicit containerlab version to use (example: 0.51.1)",
				Required: false,
				Value:    "",
			},
			&cli.BoolFlag{
				Name:     debug,
				Usage:    "enable debug logging",
				Required: false,
				Value:    false,
			},
			&cli.BoolFlag{
				Name:     quiet,
				Usage:    "disable all output (other than stdout if enabled)",
				Required: false,
				Value:    false,
			},
			&cli.BoolFlag{
				Name: stdout,
				Usage: "print all rendered manifests to stdout, if set," +
					" output not written to disk",
				Required: false,
				Value:    false,
			},
		},
		Action: func(c *cli.Context) error {
			err := clabernetesclabverter.MustNewClabverter(
				c.String(topologyFile),
				c.String(topoSpecFile),
				c.String(outputDirectory),
				c.String(destinationNamespace),
				c.String(naming),
				c.String(containerlabVersion),
				c.String(insecureRegistries),
				c.String(imagePullSecrets),
				c.Bool(disableExpose),
				c.Bool(emitCRs),
				c.Bool(debug),
				c.Bool(quiet),
				c.Bool(stdout),
			).Clabvert()

			claberneteslogging.GetManager().Flush()

			return err
		},
	}
}
