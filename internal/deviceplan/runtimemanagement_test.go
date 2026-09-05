//nolint:testpackage // exercises the unexported management network projection directly.
package deviceplan

import "testing"

func TestRuntimeManagementCarriesSubnetsAndGateways(t *testing.T) {
	t.Parallel()

	network := runtimeManagement(&ManagementInput{
		IPv4: "172.20.20.11/24", IPv4Gateway: "172.20.20.1",
		IPv6: "3fff:172:20:20::11/64", IPv6Gateway: "3fff:172:20:20::1",
	})

	if network.IPv4Subnet != "172.20.20.0/24" || network.IPv4Gw != "172.20.20.1" ||
		network.IPv6Subnet != "3fff:172:20:20::/64" || network.IPv6Gw != "3fff:172:20:20::1" {
		t.Fatalf("runtimeManagement() = %#v, want subnets derived from the addresses", network)
	}

	// Absent or prefix-less addresses yield no subnet rather than a bogus one.
	bare := runtimeManagement(&ManagementInput{IPv4: "172.20.20.11", IPv4Gateway: "172.20.20.1"})
	if bare.IPv4Subnet != "" || bare.IPv6Subnet != "" || bare.IPv4Gw != "172.20.20.1" {
		t.Fatalf("runtimeManagement(bare) = %#v", bare)
	}

	if empty := runtimeManagement(nil); empty == nil || empty.IPv4Subnet != "" {
		t.Fatalf("runtimeManagement(nil) = %#v", empty)
	}
}
