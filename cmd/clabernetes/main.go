package main

import (
	"fmt"
	"os"

	clabernetescmdclabernetescli "github.com/clabernetes/clabernetes/cmd/clabernetes/cli"
	clabernetesinternaldirectruntime "github.com/clabernetes/clabernetes/internal/directruntime"
)

func main() {
	if len(os.Args) > 0 && clabernetesinternaldirectruntime.IsRuntimeCLIInvocation(os.Args[0]) {
		if err := clabernetesinternaldirectruntime.RunRuntimeCLIShim(os.Args); err != nil {
			fail(err)
		}

		return
	}

	err := clabernetescmdclabernetescli.Entrypoint().Run(os.Args)
	if err != nil {
		fail(err)
	}
}

// fail reports the error as the process's last line rather than panicking: lifecycle hooks and
// probes surface this stream in Kubernetes events, where a panic trace buries the actual cause.
func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
