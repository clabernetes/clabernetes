package util

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
)

var onlyOneSignalHandler = make(chan struct{}) //nolint: gochecknoglobals

// SignalHandledContext returns a context that will be canceled if a SIGINT or SIGTERM is
// received.
func SignalHandledContext(
	logf func(f string, a ...any),
) (context.Context, context.CancelFunc) {
	// panics when called twice, this way there can only be one signal handled context
	close(onlyOneSignalHandler)

	ctx, cancel := context.WithCancel(context.Background())

	sigs := make(chan os.Signal, 2) //nolint:mnd

	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigs
		logf("received signal '%s', canceling context", sig)

		cancel()

		<-sigs
		logf("received signal '%s', exiting program", sig)

		os.Exit(clabernetesconstants.ExitCodeSigint)
	}()

	return ctx, cancel
}

// Panic tries to panic "nicely" but will send a second sigint to really kill the process if the
// first does not succeed within a second, and if for some reason that one does not kill it, we try
// a sigkill, and finally if that still didn't work, we will simply panic outright.
func Panic(msg string) {
	pid := syscall.Getpid()

	err := syscall.Kill(pid, syscall.SIGINT)
	if err != nil {
		panic(msg)
	}

	time.Sleep(time.Second)

	err = syscall.Kill(pid, syscall.SIGINT)
	if err != nil {
		panic(msg)
	}

	time.Sleep(time.Second)

	err = syscall.Kill(pid, syscall.SIGKILL)
	if err != nil {
		panic(msg)
	}

	panic(msg)
}
