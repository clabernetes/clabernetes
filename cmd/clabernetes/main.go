package main

import (
	"os"

	clabernetescmdclabernetescli "github.com/clabernetes/clabernetes/cmd/clabernetes/cli"
)

func main() {
	err := clabernetescmdclabernetescli.Entrypoint().Run(os.Args)
	if err != nil {
		panic(err)
	}
}
