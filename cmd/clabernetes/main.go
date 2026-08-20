package main

import (
	"os"

	clabernetescmdclabernetescli "github.com/clabernetes/clabernetes/cmd/clabernetes/cli"
	clabernetesinternaldirectruntime "github.com/clabernetes/clabernetes/internal/directruntime"
)

func main() {
	if len(os.Args) > 0 && clabernetesinternaldirectruntime.IsRuntimeCLIInvocation(os.Args[0]) {
		if err := clabernetesinternaldirectruntime.RunRuntimeCLIShim(os.Args); err != nil {
			panic(err)
		}

		return
	}

	err := clabernetescmdclabernetescli.Entrypoint().Run(os.Args)
	if err != nil {
		panic(err)
	}
}
