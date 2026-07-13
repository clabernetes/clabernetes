package launcher //nolint:testpackage // tests cover the unexported link materializer

import (
	"reflect"
	"testing"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetesutilcontainerlab "github.com/srl-labs/clabernetes/util/containerlab"
)

func TestMaterializeTopologyLinks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		existingLinks []*clabernetesutilcontainerlab.LinkDefinition
		tunnels       []*clabernetesapisv1alpha1.PointToPointTunnel
		expectedLinks []*clabernetesutilcontainerlab.LinkDefinition
	}{
		{
			name:          "no-tunnels",
			existingLinks: nil,
			tunnels:       nil,
			expectedLinks: nil,
		},
		{
			name:          "simple-tunnel",
			existingLinks: nil,
			tunnels: []*clabernetesapisv1alpha1.PointToPointTunnel{
				{
					LocalNode:      "srl1",
					LocalInterface: "e1-1",
					RemoteNode:     "multitool",
				},
			},
			expectedLinks: []*clabernetesutilcontainerlab.LinkDefinition{
				{
					LinkConfig: clabernetesutilcontainerlab.LinkConfig{
						Endpoints: []string{"srl1:e1-1", "host:srl1-e1-1"},
					},
				},
			},
		},
		{
			name:          "tunnel-with-mtu",
			existingLinks: nil,
			tunnels: []*clabernetesapisv1alpha1.PointToPointTunnel{
				{
					LocalNode:      "srl1",
					LocalInterface: "e1-1",
					RemoteNode:     "multitool",
					MTU:            9212,
				},
			},
			expectedLinks: []*clabernetesutilcontainerlab.LinkDefinition{
				{
					LinkConfig: clabernetesutilcontainerlab.LinkConfig{
						Endpoints: []string{"srl1:e1-1", "host:srl1-e1-1"},
						MTU:       9212,
					},
				},
			},
		},
		{
			// configs rendered by older controllers still contain the synthetic host links --
			// those interfaces must not get a second stanza
			name: "stanza-already-rendered",
			existingLinks: []*clabernetesutilcontainerlab.LinkDefinition{
				{
					LinkConfig: clabernetesutilcontainerlab.LinkConfig{
						Endpoints: []string{"srl1:e1-1", "host:srl1-e1-1"},
					},
				},
			},
			tunnels: []*clabernetesapisv1alpha1.PointToPointTunnel{
				{
					LocalNode:      "srl1",
					LocalInterface: "e1-1",
					RemoteNode:     "multitool",
				},
			},
			expectedLinks: []*clabernetesutilcontainerlab.LinkDefinition{
				{
					LinkConfig: clabernetesutilcontainerlab.LinkConfig{
						Endpoints: []string{"srl1:e1-1", "host:srl1-e1-1"},
					},
				},
			},
		},
		{
			// genuine user defined host links stay as they are and never get doubled up
			name: "user-host-link-preserved",
			existingLinks: []*clabernetesutilcontainerlab.LinkDefinition{
				{
					LinkConfig: clabernetesutilcontainerlab.LinkConfig{
						Endpoints: []string{"srl1:e1-2", "host:eth1"},
					},
				},
			},
			tunnels: []*clabernetesapisv1alpha1.PointToPointTunnel{
				{
					LocalNode:      "srl1",
					LocalInterface: "e1-1",
					RemoteNode:     "multitool",
				},
			},
			expectedLinks: []*clabernetesutilcontainerlab.LinkDefinition{
				{
					LinkConfig: clabernetesutilcontainerlab.LinkConfig{
						Endpoints: []string{"srl1:e1-2", "host:eth1"},
					},
				},
				{
					LinkConfig: clabernetesutilcontainerlab.LinkConfig{
						Endpoints: []string{"srl1:e1-1", "host:srl1-e1-1"},
					},
				},
			},
		},
		{
			// grouped nodes: tunnels can terminate on secondary nodes of the launcher too
			name: "grouped-node-tunnels",
			existingLinks: []*clabernetesutilcontainerlab.LinkDefinition{
				{
					LinkConfig: clabernetesutilcontainerlab.LinkConfig{
						Endpoints: []string{"srl1a:e1-3", "srl1b:e1-3"},
					},
				},
			},
			tunnels: []*clabernetesapisv1alpha1.PointToPointTunnel{
				{
					LocalNode:      "srl1a",
					LocalInterface: "e1-1",
					RemoteNode:     "multitool",
				},
				{
					LocalNode:      "srl1b",
					LocalInterface: "e1-1",
					RemoteNode:     "multitool",
				},
			},
			expectedLinks: []*clabernetesutilcontainerlab.LinkDefinition{
				{
					LinkConfig: clabernetesutilcontainerlab.LinkConfig{
						Endpoints: []string{"srl1a:e1-3", "srl1b:e1-3"},
					},
				},
				{
					LinkConfig: clabernetesutilcontainerlab.LinkConfig{
						Endpoints: []string{"srl1a:e1-1", "host:srl1a-e1-1"},
					},
				},
				{
					LinkConfig: clabernetesutilcontainerlab.LinkConfig{
						Endpoints: []string{"srl1b:e1-1", "host:srl1b-e1-1"},
					},
				},
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			config := &clabernetesutilcontainerlab.Config{
				Topology: &clabernetesutilcontainerlab.Topology{
					Links: testCase.existingLinks,
				},
			}

			materializeTopologyLinks(config, testCase.tunnels)

			if !reflect.DeepEqual(config.Topology.Links, testCase.expectedLinks) {
				t.Fatalf(
					"materialized links do not match expectation\ngot: %+v\nwant: %+v",
					config.Topology.Links,
					testCase.expectedLinks,
				)
			}
		})
	}
}
