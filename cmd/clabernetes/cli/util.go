package cli

import (
	"fmt"

	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	"github.com/urfave/cli/v2"
)

// ShowVersion shows the clabernetes version information for clabernetes CLI tools.
func ShowVersion(_ *cli.Context) {
	fmt.Printf("\tversion: %s\n", clabernetesconstants.Version)                //nolint:forbidigo
	fmt.Printf("\tsource: %s\n", "https://github.com/clabernetes/clabernetes") //nolint:forbidigo
}
