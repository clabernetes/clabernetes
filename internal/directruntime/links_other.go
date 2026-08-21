//go:build !linux

package directruntime

import (
	"context"
	"fmt"
)

type unsupportedLinkOperations struct{}

func newLinkOperations(EndpointNamespace) LinkOperations {
	return unsupportedLinkOperations{}
}

func (unsupportedLinkOperations) EnsureSysctl(_, _ string) error {
	return fmt.Errorf("network-namespace sysctls require Linux")
}

func (unsupportedLinkOperations) ListVethInterfaces(string) ([]VethInterface, error) {
	return nil, fmt.Errorf("direct veth connectivity requires Linux")
}

func (unsupportedLinkOperations) EnsureVethPair(_, _ string, _ int, _ string) error {
	return fmt.Errorf("direct veth connectivity requires Linux")
}

func (unsupportedLinkOperations) DeleteVethPair(_, _ string) error {
	return fmt.Errorf("direct veth connectivity requires Linux")
}

func (unsupportedLinkOperations) ResolvePodTransportInterface(_ string) (string, error) {
	return "", fmt.Errorf("direct management interface discovery requires Linux")
}

func (unsupportedLinkOperations) EnsureManagementAddress(_, _, _ string) error {
	return fmt.Errorf("direct management addressing requires Linux")
}

func (unsupportedLinkOperations) EnsureManagementRoute(
	_, _, _, _ string,
	_, _ int,
	_ string,
) error {
	return fmt.Errorf("direct management routing requires Linux")
}

func (unsupportedLinkOperations) DisableTxChecksumOffload(_ string) error {
	return fmt.Errorf("checksum offload control requires Linux")
}

func (unsupportedLinkOperations) EnsureInterposition(InterpositionSpec) error {
	return fmt.Errorf("management interposition requires Linux")
}

func (unsupportedLinkOperations) EnsureFabricEndpoint(
	FabricEndpointSpec,
) (FabricEndpointResult, error) {
	return FabricEndpointResult{}, fmt.Errorf("fabric realization requires Linux")
}

func (unsupportedLinkOperations) EnsureHostInterface(HostInterfaceSpec) error {
	return fmt.Errorf("host Link realization requires Linux")
}

func (unsupportedLinkOperations) SweepTransportState(string, []string) error {
	return fmt.Errorf("transport sweep requires Linux")
}
