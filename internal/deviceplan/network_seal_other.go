//go:build !linux

package deviceplan

import "fmt"

func sealPlannerNetwork() error {
	return fmt.Errorf("planner network sealing requires Linux")
}
