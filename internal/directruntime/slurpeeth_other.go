//go:build !linux

package directruntime

import (
	"context"
	"fmt"
)

type unsupportedSlurpeethRuntime struct{}

func newSlurpeethRuntime(string) SlurpeethRuntime {
	return unsupportedSlurpeethRuntime{}
}

func (unsupportedSlurpeethRuntime) Reconcile(
	_ context.Context,
	segments []SlurpeethSegment,
) error {
	if len(segments) == 0 {
		return nil
	}

	return fmt.Errorf("direct slurpeeth connectivity requires Linux")
}

func (unsupportedSlurpeethRuntime) Ready() (bool, error) {
	return true, nil
}

func (unsupportedSlurpeethRuntime) Errors() <-chan error {
	return nil
}

func (unsupportedSlurpeethRuntime) Close() error {
	return nil
}

func RunSlurpeethDaemon(_, _ string) error {
	return fmt.Errorf("direct slurpeeth connectivity requires Linux")
}
