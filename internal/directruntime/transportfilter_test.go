//nolint:testpackage // exercises unexported reconcile gating directly.
package directruntime

import (
	"testing"

	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
)

type recordingTransportFilterOperations struct {
	specs []TransportFilterSpec
}

func (r *recordingTransportFilterOperations) EnsureTransportFilterAccepts(
	spec TransportFilterSpec,
) error {
	r.specs = append(r.specs, spec)

	return nil
}

func meshManagementPlan() clabernetesinternaldeviceplan.ManagementPlan {
	return clabernetesinternaldeviceplan.ManagementPlan{
		ID:                "node",
		InterfaceSelector: clabernetesinternaldeviceplan.ManagementInterfaceInterposed,
		Interposition: &clabernetesinternaldeviceplan.ManagementInterposition{
			DeviceInterface: "eth0",
			Mesh: &clabernetesinternaldeviceplan.ManagementMesh{
				TunnelID:   100,
				GatewayMAC: "02:00:00:00:00:01",
			},
		},
	}
}

func wireInterfacePlan() clabernetesinternaldeviceplan.InterfacePlan {
	return clabernetesinternaldeviceplan.InterfacePlan{
		ID:           "node-e1-1",
		Connectivity: clabernetesinternaldeviceplan.ConnectivityWire,
	}
}

func TestReconcileTransportFilterDerivesPortsFromPlan(t *testing.T) {
	t.Parallel()

	for name, tt := range map[string]struct {
		plan  clabernetesinternaldeviceplan.Plan
		ports []uint16
	}{
		"no transports": {
			plan:  clabernetesinternaldeviceplan.Plan{},
			ports: nil,
		},
		"mesh only": {
			plan: clabernetesinternaldeviceplan.Plan{
				Management: []clabernetesinternaldeviceplan.ManagementPlan{
					meshManagementPlan(),
				},
			},
			ports: []uint16{clabernetesconstants.ManagementMeshVXLANPort},
		},
		"wires only": {
			plan: clabernetesinternaldeviceplan.Plan{
				Interfaces: []clabernetesinternaldeviceplan.InterfacePlan{
					wireInterfacePlan(),
				},
			},
			ports: []uint16{clabernetesconstants.FabricWireServicePort},
		},
		"mesh and wires": {
			plan: clabernetesinternaldeviceplan.Plan{
				Management: []clabernetesinternaldeviceplan.ManagementPlan{
					meshManagementPlan(),
				},
				Interfaces: []clabernetesinternaldeviceplan.InterfacePlan{
					wireInterfacePlan(),
				},
			},
			ports: []uint16{
				clabernetesconstants.ManagementMeshVXLANPort,
				clabernetesconstants.FabricWireServicePort,
			},
		},
		"local endpoints carry no wire port": {
			plan: clabernetesinternaldeviceplan.Plan{
				Interfaces: []clabernetesinternaldeviceplan.InterfacePlan{
					{
						ID:           "node-e1-1",
						Connectivity: clabernetesinternaldeviceplan.ConnectivitySamePod,
					},
				},
			},
			ports: nil,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			operations := &recordingTransportFilterOperations{}

			err := reconcileTransportFilter(tt.plan, ConnectivityOptions{
				FilterOperations: operations,
			})
			if err != nil {
				t.Fatalf("reconciling transport filter: %v", err)
			}

			if tt.ports == nil {
				if len(operations.specs) != 0 {
					t.Fatalf("expected no filter assertion, got %+v", operations.specs)
				}

				return
			}

			if len(operations.specs) != 1 {
				t.Fatalf("expected exactly one filter assertion, got %+v", operations.specs)
			}

			got := operations.specs[0].UDPPorts
			if len(got) != len(tt.ports) {
				t.Fatalf("expected ports %v, got %v", tt.ports, got)
			}

			for index, port := range tt.ports {
				if got[index] != port {
					t.Fatalf("expected ports %v, got %v", tt.ports, got)
				}
			}
		})
	}
}

// TestReconcileTransportFilterKeepsTheReadinessEndpointReachable pins the TCP accept for the
// kubelet's readiness probe: a device's input policy must not turn the Pod unready, and a Pod
// without a Pod address (nothing to probe) asks for no TCP accept at all.
func TestReconcileTransportFilterKeepsTheReadinessEndpointReachable(t *testing.T) {
	t.Parallel()

	operations := &recordingTransportFilterOperations{}

	if err := reconcileTransportFilter(clabernetesinternaldeviceplan.Plan{}, ConnectivityOptions{
		FilterOperations: operations,
		PodAddress:       "10.244.0.12",
	}); err != nil {
		t.Fatalf("reconciling transport filter: %v", err)
	}

	if len(operations.specs) != 1 || len(operations.specs[0].UDPPorts) != 0 ||
		len(operations.specs[0].TCPPorts) != 1 ||
		operations.specs[0].TCPPorts[0] != clabernetesconstants.ConnectivityReadinessPort {
		t.Fatalf("readiness accept = %+v", operations.specs)
	}
}

func TestReconcileTransportFilterRequiresOperationsWhenPortsExist(t *testing.T) {
	t.Parallel()

	plan := clabernetesinternaldeviceplan.Plan{
		Interfaces: []clabernetesinternaldeviceplan.InterfacePlan{wireInterfacePlan()},
	}

	if err := reconcileTransportFilter(plan, ConnectivityOptions{}); err == nil {
		t.Fatal("expected missing filter operations to fail")
	}

	// A plan without transports must not require the seam at all.
	if err := reconcileTransportFilter(
		clabernetesinternaldeviceplan.Plan{},
		ConnectivityOptions{},
	); err != nil {
		t.Fatalf("transport-free plan must not need filter operations: %v", err)
	}
}
