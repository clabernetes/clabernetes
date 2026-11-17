package deviceplan_test

import (
	"context"
	"testing"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
)

func TestSRLinuxEndpointAddressesSurvivePlanningForPostDeploy(t *testing.T) {
	t.Parallel()

	registry := clabernetesinternaldeviceplan.NewContainerlabRegistry()
	compatibility, err := clabernetesinternaldeviceplan.CompatibilityForRegistry(
		registry,
		"test-linked-version",
	)
	if err != nil {
		t.Fatal(err)
	}

	input := singleNodeInput("nokia_srlinux", "example/srlinux:1")
	input.Compatibility = compatibility
	input.Management = []clabernetesinternaldeviceplan.ManagementInput{{
		NodeID: "node-a", InterfaceName: "mgmt0", IPv4: "192.0.2.10/24",
		IPv4Gateway: "192.0.2.1",
	}}
	input.Interfaces = []clabernetesinternaldeviceplan.InterfaceInput{{
		ID: "interface-a", NodeID: "node-a", Name: "e1-1", LinkID: "link-a",
		Connectivity: clabernetesinternaldeviceplan.ConnectivityWire, WireID: 1,
	}}
	adapter := clabernetesinternaldeviceplan.Adapter{
		Registry: registry,
		Revision: "link-address-test",
	}
	discovery, err := adapter.DiscoverImages(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	input.Certificates, adapter.CertificateRoot = materializeCertificateRequirements(
		t,
		discovery.Certificates,
	)

	baselinePlan, err := adapter.Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	assertImportedSRLPostDeploy(t, baselinePlan)
	if len(baselinePlan.Interfaces) != 1 || baselinePlan.Interfaces[0].IPv4 != "" ||
		baselinePlan.Interfaces[0].IPv6 != "" {
		t.Fatalf("address-free SR Linux interface plan = %#v", baselinePlan.Interfaces)
	}

	input.Interfaces[0].IPv4 = "198.51.100.0/31"
	input.Interfaces[0].IPv6 = "2001:db8:100::/127"
	addressedPlan, err := adapter.Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	assertImportedSRLPostDeploy(t, addressedPlan)
	if baselinePlan.InputDigest == addressedPlan.InputDigest {
		t.Fatal("endpoint addresses did not change the accepted plan input identity")
	}
	if len(addressedPlan.Interfaces) != 1 ||
		addressedPlan.Interfaces[0].IPv4 != input.Interfaces[0].IPv4 ||
		addressedPlan.Interfaces[0].IPv6 != input.Interfaces[0].IPv6 {
		t.Fatalf("planned interface addresses = %#v", addressedPlan.Interfaces)
	}
}

func assertImportedSRLPostDeploy(t *testing.T, plan *clabernetesinternaldeviceplan.Plan) {
	t.Helper()

	for _, action := range plan.Actions {
		if action.Kind == clabernetesinternaldeviceplan.ActionImportedPostDeploy {
			return
		}
	}

	t.Fatal("SR Linux plan has no imported post-deploy action for default interface configuration")
}
