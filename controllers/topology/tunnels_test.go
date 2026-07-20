package topology_test

import (
	"encoding/json"
	"fmt"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetescontrollerstopology "github.com/clabernetes/clabernetes/controllers/topology"
	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
)

const testAllocateTunnelIDsTestName = "tunnels/allocate-tunnel-ids"

// TestAllocateTunnelIds ensures that the tunnel clabernetes controllers VXLAN tunnel ID allocation
// process works as advertised. None of this is "hard" necessarily, but there are a lot of moving
// parts in play to ensure that we use the tunnel IDs consistently and also obviously don't stomp
// on any existing tunnel IDs.
func TestAllocateTunnelIds(t *testing.T) {
	cases := []struct {
		name             string
		previousTunnels  map[string][]*clabernetesapisv1alpha1.PointToPointTunnel
		processedTunnels map[string][]*clabernetesapisv1alpha1.PointToPointTunnel
	}{
		{
			name:            "simple",
			previousTunnels: map[string][]*clabernetesapisv1alpha1.PointToPointTunnel{},
			processedTunnels: map[string][]*clabernetesapisv1alpha1.PointToPointTunnel{
				"srl1": {
					{
						TunnelID:        0,
						LocalNode:       "srl1",
						Destination:     "topo-1-srl2.clabernetes.svc.cluster.local",
						RemoteNode:      "srl2",
						LocalInterface:  "e1-1",
						RemoteInterface: "e1-1",
					},
				},
				"srl2": {
					{
						TunnelID:        0,
						LocalNode:       "srl2",
						Destination:     "topo-1-srl1.clabernetes.svc.cluster.local",
						RemoteNode:      "srl1",
						LocalInterface:  "e1-1",
						RemoteInterface: "e1-1",
					},
				},
			},
		},
		{
			name: "simple-existing-status",
			previousTunnels: map[string][]*clabernetesapisv1alpha1.PointToPointTunnel{
				"srl1": {
					{
						TunnelID:        0,
						LocalNode:       "srl1",
						Destination:     "topo-1-srl2.clabernetes.svc.cluster.local",
						RemoteNode:      "srl2",
						LocalInterface:  "e1-1",
						RemoteInterface: "e1-1",
					},
				},
				"srl2": {
					{
						TunnelID:        0,
						LocalNode:       "srl2",
						Destination:     "topo-1-srl1.clabernetes.svc.cluster.local",
						RemoteNode:      "srl1",
						LocalInterface:  "e1-1",
						RemoteInterface: "e1-1",
					},
				},
			},
			processedTunnels: map[string][]*clabernetesapisv1alpha1.PointToPointTunnel{
				"srl1": {
					{
						TunnelID:        0,
						LocalNode:       "srl1",
						Destination:     "topo-1-srl2.clabernetes.svc.cluster.local",
						RemoteNode:      "srl2",
						LocalInterface:  "e1-1",
						RemoteInterface: "e1-1",
					},
				},
				"srl2": {
					{
						TunnelID:        0,
						LocalNode:       "srl2",
						Destination:     "topo-1-srl1.clabernetes.svc.cluster.local",
						RemoteNode:      "srl1",
						LocalInterface:  "e1-1",
						RemoteInterface: "e1-1",
					},
				},
			},
		},
		{
			name: "simple-already-allocated-ids",
			previousTunnels: map[string][]*clabernetesapisv1alpha1.PointToPointTunnel{
				"srl1": {
					{
						TunnelID:        1,
						LocalNode:       "srl1",
						Destination:     "topo-1-srl2.clabernetes.svc.cluster.local",
						RemoteNode:      "srl2",
						LocalInterface:  "e1-1",
						RemoteInterface: "e1-1",
					},
				},
				"srl2": {
					{
						TunnelID:        1,
						LocalNode:       "srl2",
						Destination:     "topo-1-srl1.clabernetes.svc.cluster.local",
						RemoteNode:      "srl1",
						LocalInterface:  "e1-1",
						RemoteInterface: "e1-1",
					},
				},
			},
			processedTunnels: map[string][]*clabernetesapisv1alpha1.PointToPointTunnel{
				"srl1": {
					{
						TunnelID:        0,
						LocalNode:       "srl1",
						Destination:     "topo-1-srl2.clabernetes.svc.cluster.local",
						RemoteNode:      "srl2",
						LocalInterface:  "e1-1",
						RemoteInterface: "e1-1",
					},
				},
				"srl2": {
					{
						TunnelID:        0,
						LocalNode:       "srl2",
						Destination:     "topo-1-srl1.clabernetes.svc.cluster.local",
						RemoteNode:      "srl1",
						LocalInterface:  "e1-1",
						RemoteInterface: "e1-1",
					},
				},
			},
		},
		{
			name: "simple-weirdly-allocated-ids",
			previousTunnels: map[string][]*clabernetesapisv1alpha1.PointToPointTunnel{
				"srl1": {
					{
						TunnelID:        0,
						LocalNode:       "srl1",
						Destination:     "topo-1-srl2.clabernetes.svc.cluster.local",
						RemoteNode:      "srl2",
						LocalInterface:  "e1-1",
						RemoteInterface: "e1-1",
					},
				},
				"srl2": {
					{
						TunnelID:        1,
						LocalNode:       "srl2",
						Destination:     "topo-1-srl1.clabernetes.svc.cluster.local",
						RemoteNode:      "srl1",
						LocalInterface:  "e1-1",
						RemoteInterface: "e1-1",
					},
				},
			},
			processedTunnels: map[string][]*clabernetesapisv1alpha1.PointToPointTunnel{
				"srl1": {
					{
						TunnelID:        0,
						LocalNode:       "srl1",
						Destination:     "topo-1-srl2.clabernetes.svc.cluster.local",
						RemoteNode:      "srl2",
						LocalInterface:  "e1-1",
						RemoteInterface: "e1-1",
					},
				},
				"srl2": {
					{
						TunnelID:        0,
						LocalNode:       "srl2",
						Destination:     "topo-1-srl1.clabernetes.svc.cluster.local",
						RemoteNode:      "srl1",
						LocalInterface:  "e1-1",
						RemoteInterface: "e1-1",
					},
				},
			},
		},
		{
			name:            "meshy-links",
			previousTunnels: map[string][]*clabernetesapisv1alpha1.PointToPointTunnel{},
			processedTunnels: map[string][]*clabernetesapisv1alpha1.PointToPointTunnel{
				"srl1": {
					{
						TunnelID:        0,
						LocalNode:       "srl1",
						Destination:     "topo-1-srl2.clabernetes.svc.cluster.local",
						RemoteNode:      "srl2",
						LocalInterface:  "e1-1",
						RemoteInterface: "e1-1",
					},
					{
						TunnelID:        0,
						LocalNode:       "srl1",
						Destination:     "topo-1-srl3.clabernetes.svc.cluster.local",
						RemoteNode:      "srl3",
						LocalInterface:  "e1-2",
						RemoteInterface: "e1-1",
					},
				},
				"srl2": {
					{
						TunnelID:        0,
						LocalNode:       "srl2",
						Destination:     "topo-1-srl1.clabernetes.svc.cluster.local",
						RemoteNode:      "srl1",
						LocalInterface:  "e1-1",
						RemoteInterface: "e1-1",
					},
					{
						TunnelID:        0,
						LocalNode:       "srl2",
						Destination:     "topo-1-srl3.clabernetes.svc.cluster.local",
						RemoteNode:      "srl3",
						LocalInterface:  "e1-2",
						RemoteInterface: "e1-2",
					},
					{
						TunnelID:        0,
						LocalNode:       "srl2",
						Destination:     "topo-1-srl4.clabernetes.svc.cluster.local",
						RemoteNode:      "srl4",
						LocalInterface:  "e1-3",
						RemoteInterface: "e1-1",
					},
				},
				"srl3": {
					{
						TunnelID:        0,
						LocalNode:       "srl3",
						Destination:     "topo-1-srl1.clabernetes.svc.cluster.local",
						RemoteNode:      "srl1",
						LocalInterface:  "e1-1",
						RemoteInterface: "e1-2",
					},
					{
						TunnelID:        0,
						LocalNode:       "srl3",
						Destination:     "topo-1-srl2.clabernetes.svc.cluster.local",
						RemoteNode:      "srl2",
						LocalInterface:  "e1-2",
						RemoteInterface: "e1-2",
					},
					{
						TunnelID:        0,
						LocalNode:       "srl3",
						Destination:     "topo-1-srl4.clabernetes.svc.cluster.local",
						RemoteNode:      "srl4",
						LocalInterface:  "e1-3",
						RemoteInterface: "e1-2",
					},
				},
				"srl4": {
					{
						TunnelID:        0,
						LocalNode:       "srl4",
						Destination:     "topo-1-srl2.clabernetes.svc.cluster.local",
						RemoteNode:      "srl2",
						LocalInterface:  "e1-1",
						RemoteInterface: "e1-3",
					},
					{
						TunnelID:        0,
						LocalNode:       "srl4",
						Destination:     "topo-1-srl3.clabernetes.svc.cluster.local",
						RemoteNode:      "srl3",
						LocalInterface:  "e1-2",
						RemoteInterface: "e1-3",
					},
				},
			},
		},
		{
			name: "updating-tunnels",
			previousTunnels: map[string][]*clabernetesapisv1alpha1.PointToPointTunnel{
				"srl1": {
					{
						TunnelID:        1,
						LocalNode:       "srl1",
						Destination:     "topo-1-srl2.clabernetes.svc.cluster.local",
						RemoteNode:      "srl2",
						LocalInterface:  "e1-1",
						RemoteInterface: "e1-1",
					},
					{
						TunnelID:        2,
						LocalNode:       "srl1",
						Destination:     "topo-1-srl2.clabernetes.svc.cluster.local",
						RemoteNode:      "srl2",
						LocalInterface:  "e1-2",
						RemoteInterface: "e1-2",
					},
					{
						TunnelID:        3,
						LocalNode:       "srl1",
						Destination:     "topo-1-srl2.clabernetes.svc.cluster.local",
						RemoteNode:      "srl2",
						LocalInterface:  "e1-3",
						RemoteInterface: "e1-3",
					},
				},
				"srl2": {
					{
						TunnelID:        1,
						LocalNode:       "srl2",
						Destination:     "topo-1-srl1.clabernetes.svc.cluster.local",
						RemoteNode:      "srl1",
						LocalInterface:  "e1-1",
						RemoteInterface: "e1-1",
					},
					{
						TunnelID:        2,
						LocalNode:       "srl2",
						Destination:     "topo-1-srl1.clabernetes.svc.cluster.local",
						RemoteNode:      "srl1",
						LocalInterface:  "e1-2",
						RemoteInterface: "e1-2",
					},
					{
						TunnelID:        3,
						LocalNode:       "srl2",
						Destination:     "topo-1-srl1.clabernetes.svc.cluster.local",
						RemoteNode:      "srl1",
						LocalInterface:  "e1-3",
						RemoteInterface: "e1-3",
					},
				},
			},
			processedTunnels: map[string][]*clabernetesapisv1alpha1.PointToPointTunnel{
				"srl1": {
					{
						TunnelID:        0,
						LocalNode:       "srl1",
						Destination:     "topo-1-srl2.clabernetes.svc.cluster.local",
						RemoteNode:      "srl2",
						LocalInterface:  "e1-1",
						RemoteInterface: "e1-3",
					},
					{
						TunnelID:        0,
						LocalNode:       "srl1",
						Destination:     "topo-1-srl2.clabernetes.svc.cluster.local",
						RemoteNode:      "srl2",
						LocalInterface:  "e1-2",
						RemoteInterface: "e1-1",
					},
					{
						TunnelID:        0,
						LocalNode:       "srl1",
						Destination:     "topo-1-srl2.clabernetes.svc.cluster.local",
						RemoteNode:      "srl2",
						LocalInterface:  "e1-3",
						RemoteInterface: "e1-2",
					},
				},
				"srl2": {
					{
						TunnelID:        0,
						LocalNode:       "srl2",
						Destination:     "topo-1-srl1.clabernetes.svc.cluster.local",
						RemoteNode:      "srl1",
						LocalInterface:  "e1-3",
						RemoteInterface: "e1-1",
					},
					{
						TunnelID:        0,
						LocalNode:       "srl2",
						Destination:     "topo-1-srl1.clabernetes.svc.cluster.local",
						RemoteNode:      "srl1",
						LocalInterface:  "e1-1",
						RemoteInterface: "e1-2",
					},
					{
						TunnelID:        0,
						LocalNode:       "srl2",
						Destination:     "topo-1-srl1.clabernetes.svc.cluster.local",
						RemoteNode:      "srl1",
						LocalInterface:  "e1-2",
						RemoteInterface: "e1-3",
					},
				},
			},
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				clabernetescontrollerstopology.AllocateTunnelIDs(
					testCase.previousTunnels,
					testCase.processedTunnels,
				)

				got := testCase.processedTunnels

				if *clabernetestesthelper.Update {
					clabernetestesthelper.WriteTestFixtureJSON(
						t,
						fmt.Sprintf(
							"golden/%s/%s.json",
							testAllocateTunnelIDsTestName,
							testCase.name,
						),
						got,
					)
				}

				var want map[string][]*clabernetesapisv1alpha1.PointToPointTunnel

				err := json.Unmarshal(
					clabernetestesthelper.ReadTestFixtureFile(
						t,
						fmt.Sprintf(
							"golden/%s/%s.json",
							testAllocateTunnelIDsTestName,
							testCase.name,
						),
					),
					&want,
				)
				if err != nil {
					t.Fatal(err)
				}

				clabernetestesthelper.MarshaledEqual(t, got, want)
			},
		)
	}
}
