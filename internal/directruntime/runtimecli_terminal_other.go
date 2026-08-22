//go:build !linux

package directruntime

import "fmt"

func runTerminalSession(string, []string) error {
	return fmt.Errorf("runtime CLI sessions require Linux")
}
