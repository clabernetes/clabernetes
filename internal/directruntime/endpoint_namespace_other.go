//go:build !linux

package directruntime

import (
	"fmt"

	clabernetesdeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
)

func openEndpointNamespace(string) (EndpointNamespace, error) {
	return nil, &clabernetesdeviceplan.Error{
		Code:  clabernetesdeviceplan.ErrorUnsupported,
		Field: "runtime.networkNamespace", Behavior: "host-network-namespace",
		Message: "imported endpoint lifecycle requires Linux network namespaces",
	}
}

type unsupportedEndpointNamespace struct{}

func (*unsupportedEndpointNamespace) TargetPath() string { return "" }

func (*unsupportedEndpointNamespace) Execute(func() error) error {
	return fmt.Errorf("endpoint namespaces are unsupported")
}

func (*unsupportedEndpointNamespace) Close() error { return nil }
