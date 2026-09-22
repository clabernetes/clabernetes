//go:build !linux

package directruntime

import "os"

func cloneLifecycleBinary(_, _ *os.File, _ string) (bool, error) {
	return false, nil
}
