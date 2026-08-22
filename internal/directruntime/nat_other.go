//go:build !linux

package directruntime

import "fmt"

type unsupportedNATOperations struct{}

func newNATOperations() NATOperations {
	return unsupportedNATOperations{}
}

func (unsupportedNATOperations) EnsureInterpositionNAT(InterpositionNATSpec) error {
	return fmt.Errorf("interposition translation requires Linux")
}

func (unsupportedNATOperations) DeleteInterpositionNAT() error {
	return fmt.Errorf("interposition translation requires Linux")
}
