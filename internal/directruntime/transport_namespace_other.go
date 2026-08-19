//go:build !linux

package directruntime

import (
	"context"
	"fmt"
)

func startApplicationTransportNamespaceBroker(
	context.Context,
	string,
	string,
) (transportNamespaceBroker, error) {
	return nil, fmt.Errorf("application transport namespace handoff requires Linux")
}

func publishApplicationTransportNamespace(
	context.Context,
	string,
	string,
	string,
) error {
	return fmt.Errorf("application transport namespace handoff requires Linux")
}
