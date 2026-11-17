package directruntime_test

import (
	"strings"
	"testing"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	clabernetesinternaldirectruntime "github.com/clabernetes/clabernetes/internal/directruntime"
)

func TestEndpointAddressChangesRequireRecreate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                   string
		base                   func(*clabernetesinternaldeviceplan.Input)
		desired                func(*clabernetesinternaldeviceplan.Input)
		wantChanged            bool
		wantRecreate           bool
		truncatePlanInterfaces bool
	}{
		{
			name: "add IPv4",
			desired: func(input *clabernetesinternaldeviceplan.Input) {
				input.Interfaces[0].IPv4 = "198.51.100.0/31"
			},
			wantChanged: true, wantRecreate: true,
		},
		{
			name: "change IPv6",
			base: func(input *clabernetesinternaldeviceplan.Input) {
				input.Interfaces[0].IPv6 = "2001:db8::/127"
			},
			desired: func(input *clabernetesinternaldeviceplan.Input) {
				input.Interfaces[0].IPv6 = "2001:db8::2/127"
			},
			wantChanged: true, wantRecreate: true,
		},
		{
			name: "remove address",
			base: func(input *clabernetesinternaldeviceplan.Input) {
				input.Interfaces[1].IPv4 = "198.51.100.1/31"
			},
			wantChanged: true, wantRecreate: true,
		},
		{
			name: "remove addressed interface",
			base: func(input *clabernetesinternaldeviceplan.Input) {
				input.Interfaces[1].IPv4 = "198.51.100.1/31"
			},
			desired: func(input *clabernetesinternaldeviceplan.Input) {
				input.Interfaces = input.Interfaces[:1]
			},
			truncatePlanInterfaces: true,
			wantChanged:            true,
			wantRecreate:           true,
		},
		{
			name: "shared address changes while an unaddressed interface is removed",
			base: func(input *clabernetesinternaldeviceplan.Input) {
				input.Interfaces[0].IPv4 = "198.51.100.0/31"
			},
			desired: func(input *clabernetesinternaldeviceplan.Input) {
				input.Interfaces = input.Interfaces[:1]
				input.Interfaces[0].IPv4 = "198.51.100.2/31"
			},
			truncatePlanInterfaces: true,
			wantChanged:            true,
			wantRecreate:           true,
		},
		{
			name: "equivalent IPv6 spelling",
			base: func(input *clabernetesinternaldeviceplan.Input) {
				input.Interfaces[0].IPv6 = "2001:0db8:0:0::1/127"
			},
			desired: func(input *clabernetesinternaldeviceplan.Input) {
				input.Interfaces[0].IPv6 = "2001:db8::1/127"
			},
		},
		{name: "unchanged address-free"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			baseInput, basePlan := connectivityTestInputAndPlan(t)
			desiredInput, desiredPlan := connectivityTestInputAndPlan(t)
			setLoopbackLink(t, &baseInput, &basePlan, 1500)
			setLoopbackLink(t, &desiredInput, &desiredPlan, 1500)
			if test.base != nil {
				test.base(&baseInput)
			}
			if test.desired != nil {
				test.desired(&desiredInput)
			}

			syncAddressTestPlan(t, baseInput, &basePlan)
			syncAddressTestPlan(t, desiredInput, &desiredPlan)
			if test.truncatePlanInterfaces {
				desiredPlan.Interfaces = desiredPlan.Interfaces[:1]
				desiredPlan.Actions = nil
			}

			transition, err := clabernetesinternaldirectruntime.EvaluateConnectivityTransition(
				baseInput,
				basePlan,
				desiredInput,
				desiredPlan,
			)
			if err != nil {
				t.Fatal(err)
			}

			wantMode := clabernetesinternaldeviceplan.LinkApplyLive
			if test.wantRecreate {
				wantMode = clabernetesinternaldeviceplan.LinkApplyRecreate
			}
			if transition.Changed != test.wantChanged || transition.RequiredMode != wantMode {
				t.Fatalf("address transition = %#v, want changed %v mode %s",
					transition, test.wantChanged, wantMode)
			}
			if test.wantRecreate {
				if _, err = clabernetesinternaldirectruntime.NewConnectivityRevision(
					baseInput,
					basePlan,
					desiredInput,
					desiredPlan,
				); err == nil || !strings.Contains(strings.ToLower(err.Error()), "recreate") {
					t.Fatalf("NewConnectivityRevision() accepted address change: %v", err)
				}
			}
		})
	}
}

func TestAddressedInterfaceAdditionAndCombinedMTUChangeRequireRecreate(t *testing.T) {
	t.Parallel()

	baseInput, basePlan := connectivityTestInputAndPlan(t)
	desiredInput, desiredPlan := connectivityTestInputAndPlan(t)
	setLoopbackLink(t, &desiredInput, &desiredPlan, 9000)
	desiredInput.Interfaces[0].IPv4 = "198.51.100.0/31"
	syncAddressTestPlan(t, desiredInput, &desiredPlan)

	transition, err := clabernetesinternaldirectruntime.EvaluateConnectivityTransition(
		baseInput,
		basePlan,
		desiredInput,
		desiredPlan,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !transition.Changed ||
		transition.RequiredMode != clabernetesinternaldeviceplan.LinkApplyRecreate {
		t.Fatalf("addressed addition and MTU transition = %#v, want recreate", transition)
	}
}

func syncAddressTestPlan(
	t *testing.T,
	input clabernetesinternaldeviceplan.Input,
	plan *clabernetesinternaldeviceplan.Plan,
) {
	t.Helper()

	for index := range input.Interfaces {
		plan.Interfaces[index].IPv4 = input.Interfaces[index].IPv4
		plan.Interfaces[index].IPv6 = input.Interfaces[index].IPv6
	}

	digest, err := input.Digest()
	if err != nil {
		t.Fatal(err)
	}
	plan.InputDigest = digest
}
