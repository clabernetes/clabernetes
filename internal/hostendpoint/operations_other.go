//go:build !linux

//nolint:err113,noinlineerr,perfsprint,wsl_v5 // Unsupported operations fail at their boundary.
package hostendpoint

import (
	"context"
	"fmt"
)

type unsupportedOperations struct{}

func newOperations() Operations {
	return unsupportedOperations{}
}

func (unsupportedOperations) List(context.Context) ([]OwnedEndpoint, error) {
	return nil, fmt.Errorf("host endpoints require Linux")
}

func (unsupportedOperations) Ensure(context.Context, Endpoint, ObjectIdentity, int) error {
	return fmt.Errorf("host endpoints require Linux")
}

func (unsupportedOperations) Delete(context.Context, OwnedEndpoint) error {
	return fmt.Errorf("host endpoints require Linux")
}
