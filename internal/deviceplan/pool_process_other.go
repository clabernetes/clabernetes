//go:build !linux

//nolint:err113 // These commands are available only in Linux planner containers.
package deviceplan

import (
	"context"
	"errors"
	"io"
	"os"
	"time"
)

// RunPoolIdle requires the Linux container process model.
func RunPoolIdle(context.Context) error {
	return errors.New("planner pool workers require Linux")
}

// RunPoolProcess requires the Linux process and filesystem isolation used by planner Pods.
func RunPoolProcess(context.Context, *os.File, io.Writer, io.Writer, string, time.Duration) error {
	return errors.New("planner pool workers require Linux")
}
