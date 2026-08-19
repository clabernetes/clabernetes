//go:build !linux

package directruntime

import (
	"fmt"
	"time"
)

type unsupportedLaunchOperations struct{}

func newLaunchOperations() LaunchOperations {
	return unsupportedLaunchOperations{}
}

func (unsupportedLaunchOperations) Delay(duration time.Duration) error {
	time.Sleep(duration)

	return nil
}

func (unsupportedLaunchOperations) MountFilesystem(_, _, filesystem string, _ []string) error {
	return fmt.Errorf("filesystem operation %q requires Linux", filesystem)
}

func (unsupportedLaunchOperations) Exec(_ []string) error {
	return fmt.Errorf("direct application process replacement requires Linux")
}
