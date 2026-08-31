//go:build !linux

package directruntime

import "fmt"

type unsupportedTransportFilterOperations struct{}

func newTransportFilterOperations() TransportFilterOperations {
	return unsupportedTransportFilterOperations{}
}

func (unsupportedTransportFilterOperations) EnsureTransportFilterAccepts(
	TransportFilterSpec,
) error {
	return fmt.Errorf("transport filter assertion requires Linux")
}
