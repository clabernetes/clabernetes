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

func (unsupportedLaunchOperations) UpdateFile(
	_ string,
	_ func(current []byte) (updated []byte, write bool),
) error {
	return fmt.Errorf("in-place file updates require Linux")
}

func (unsupportedLaunchOperations) ReadFile(_ string) ([]byte, error) {
	return nil, fmt.Errorf("file reads require Linux")
}

func (unsupportedLaunchOperations) Hostname() (string, error) {
	return "", fmt.Errorf("application container hostnames require Linux")
}

func (unsupportedLaunchOperations) LimitOpenFiles(uint64) error {
	return fmt.Errorf("open-file limits require Linux")
}

func (unsupportedLaunchOperations) Exec(_ []string) error {
	return fmt.Errorf("direct application process replacement requires Linux")
}
