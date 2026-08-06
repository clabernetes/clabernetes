package topology_test

import (
	"encoding/json"
	"fmt"
	"testing"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/srl-labs/clabernetes/config"
	clabernetescontrollerstopology "github.com/srl-labs/clabernetes/controllers/topology"
	claberneteslogging "github.com/srl-labs/clabernetes/logging"
	clabernetestesthelper "github.com/srl-labs/clabernetes/testhelper"
	clabernetesutil "github.com/srl-labs/clabernetes/util"
	clabernetesutilcontainerlab "github.com/srl-labs/clabernetes/util/containerlab"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const definitionProcessTestName = "definition"

func TestDefinitionProcess(t *testing.T) {
	cases := []struct {
		name                 string
		inTopology           *clabernetesapisv1alpha1.Topology
		reconcileData        *clabernetescontrollerstopology.ReconcileData
		removeTopologyPrefix bool
	}{
		{
			name: "containerlab-host-and-links",
			inTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "process-containerlab-definition-host-and-links-test",
					Namespace: "clabernetes",
				},
				Spec: clabernetesapisv1alpha1.TopologySpec{
					Definition: clabernetesapisv1alpha1.Definition{
						Containerlab: `---
    name: test
    topology:
      nodes:
        srl1:
          kind: srl
          image: ghcr.io/nokia/srlinux
        srl2:
          kind: srl
          image: ghcr.io/nokia/srlinux
          healthcheck:
            test:
              - CMD-SHELL
              - cat /etc/os-release
            start-period: 3
            retries: 1
            interval: 5
            timeout: 2
      links:
        - endpoints: ["srl1:e1-1", "srl2:e1-1"]
        - endpoints: ["srl1:e3-3", "host:eth3-3"]
`,
					},
				},
			},
			reconcileData: &clabernetescontrollerstopology.ReconcileData{
				Kind:           "containerlab",
				ResolvedHashes: clabernetesapisv1alpha1.ReconcileHashes{},
				ResolvedConfigs: map[string]*clabernetesutilcontainerlab.Config{
					"srl1": {},
					"srl2": {
						Name: "clabernetes-srl2",
						Topology: &clabernetesutilcontainerlab.Topology{
							Defaults: &clabernetesutilcontainerlab.NodeDefinition{
								Ports: []string{},
							},
							Nodes: map[string]*clabernetesutilcontainerlab.NodeDefinition{
								"srl2": {
									Ports: []string{},
									Kind:  "srl",
									Image: "ghcr.io/nokia/srlinux",
									Healthcheck: &clabernetesutilcontainerlab.HealthcheckConfig{
										Test:        []string{"CMD-SHELL", "cat /etc/os-release"},
										StartPeriod: 3,
										Retries:     1,
										Interval:    5,
										Timeout:     2,
									},
								},
							},
						},
					},
				},
				ResolvedTunnels: map[string][]*clabernetesapisv1alpha1.PointToPointTunnel{
					"srl1": {},
					"srl2": {},
				},
			},
			removeTopologyPrefix: false,
		},
		{
			name: "containerlab-host-link",
			inTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "process-containerlab-definition-host-link-test",
					Namespace: "clabernetes",
				},
				Spec: clabernetesapisv1alpha1.TopologySpec{
					Definition: clabernetesapisv1alpha1.Definition{
						Containerlab: `---
    name: test
    topology:
      nodes:
        srl1:
          kind: srl
          image: ghcr.io/nokia/srlinux
      links:
        - endpoints: ["srl1:e1-1", "host:srl1-e1-1"]
`,
					},
				},
			},
			reconcileData: &clabernetescontrollerstopology.ReconcileData{
				Kind:           "containerlab",
				ResolvedHashes: clabernetesapisv1alpha1.ReconcileHashes{},
				ResolvedConfigs: map[string]*clabernetesutilcontainerlab.Config{
					"srl1": {},
				},
				ResolvedTunnels: map[string][]*clabernetesapisv1alpha1.PointToPointTunnel{},
			},
			removeTopologyPrefix: false,
		},
		{
			name: "containerlab-simple",
			inTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "process-containerlab-definition-test",
					Namespace: "clabernetes",
				},
				Spec: clabernetesapisv1alpha1.TopologySpec{
					Definition: clabernetesapisv1alpha1.Definition{
						Containerlab: `---
    name: test
    topology:
      nodes:
        srl1:
          kind: srl
          image: ghcr.io/nokia/srlinux
        srl2:
          kind: srl
          image: ghcr.io/nokia/srlinux
      links:
        - endpoints: ["srl1:e1-1", "srl2:e1-1"]
`,
					},
				},
			},
			reconcileData: &clabernetescontrollerstopology.ReconcileData{
				Kind:           "containerlab",
				ResolvedHashes: clabernetesapisv1alpha1.ReconcileHashes{},
				ResolvedConfigs: map[string]*clabernetesutilcontainerlab.Config{
					"srl1": {},
					"srl2": {},
				},
				ResolvedTunnels: map[string][]*clabernetesapisv1alpha1.PointToPointTunnel{
					"srl1": {},
					"srl2": {},
				},
			},
			removeTopologyPrefix: false,
		},
		{
			name: "containerlab-simple-remove-prefix",
			inTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "process-containerlab-definition-test",
					Namespace: "clabernetes",
				},
				Spec: clabernetesapisv1alpha1.TopologySpec{
					Definition: clabernetesapisv1alpha1.Definition{
						Containerlab: `---
    name: test
    topology:
      nodes:
        srl1:
          kind: srl
          image: ghcr.io/nokia/srlinux
        srl2:
          kind: srl
          image: ghcr.io/nokia/srlinux
      links:
        - endpoints: ["srl1:e1-1", "srl2:e1-1"]
`,
					},
				},
				Status: clabernetesapisv1alpha1.TopologyStatus{
					RemoveTopologyPrefix: clabernetesutil.ToPointer(true),
				},
			},
			reconcileData: &clabernetescontrollerstopology.ReconcileData{
				Kind:           "containerlab",
				ResolvedHashes: clabernetesapisv1alpha1.ReconcileHashes{},
				ResolvedConfigs: map[string]*clabernetesutilcontainerlab.Config{
					"srl1": {},
					"srl2": {},
				},
				ResolvedTunnels: map[string][]*clabernetesapisv1alpha1.PointToPointTunnel{
					"srl1": {},
					"srl2": {},
				},
			},
			removeTopologyPrefix: true,
		},
		// kne tests
		{
			name: "kne-simple",
			inTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "process-kne-definition-test",
					Namespace: "clabernetes",
				},
				Spec: clabernetesapisv1alpha1.TopologySpec{
					Definition: clabernetesapisv1alpha1.Definition{
						Kne: `      name: "2-srl-ixr6"
      nodes: {
        name: "srl1"
          vendor: NOKIA
          model: "ixr10"
          config:{
            file: "occonfig.cfg"
          }
          interfaces: {
            key: "e1-1"
              value: {
                name: "ethernet-1/1"
              }
          }
      }
      
      nodes: {
        name: "srl2"
          vendor: NOKIA
          model: "ixr10"
          config:{
            file: "occonfig.cfg"
          }
          interfaces: {
            key: "e1-1"
              value: {
                name: "ethernet-1/1"
              }
          }
      }
      
      links: {
        a_node: "srl1"
          a_int: "e1-1"
          z_node: "srl2"
          z_int: "e1-1"
      }
`,
					},
				},
			},
			reconcileData: &clabernetescontrollerstopology.ReconcileData{
				Kind:           "kne",
				ResolvedHashes: clabernetesapisv1alpha1.ReconcileHashes{},
				ResolvedConfigs: map[string]*clabernetesutilcontainerlab.Config{
					"srl1": {},
					"srl2": {},
				},
				ResolvedTunnels: map[string][]*clabernetesapisv1alpha1.PointToPointTunnel{
					"srl1": {},
					"srl2": {},
				},
			},
			removeTopologyPrefix: false,
		},
		{
			name: "kne-simple-remove-prefix",
			inTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "process-kne-definition-test",
					Namespace: "clabernetes",
				},
				Spec: clabernetesapisv1alpha1.TopologySpec{
					Definition: clabernetesapisv1alpha1.Definition{
						Kne: `      name: "2-srl-ixr6"
      nodes: {
        name: "srl1"
          vendor: NOKIA
          model: "ixr10"
          config:{
            file: "occonfig.cfg"
          }
          interfaces: {
            key: "e1-1"
              value: {
                name: "ethernet-1/1"
              }
          }
      }

      nodes: {
        name: "srl2"
          vendor: NOKIA
          model: "ixr10"
          config:{
            file: "occonfig.cfg"
          }
          interfaces: {
            key: "e1-1"
              value: {
                name: "ethernet-1/1"
              }
          }
      }

      links: {
        a_node: "srl1"
          a_int: "e1-1"
          z_node: "srl2"
          z_int: "e1-1"
      }
`,
					},
				},
				Status: clabernetesapisv1alpha1.TopologyStatus{
					RemoveTopologyPrefix: clabernetesutil.ToPointer(true),
				},
			},
			reconcileData: &clabernetescontrollerstopology.ReconcileData{
				Kind:           "kne",
				ResolvedHashes: clabernetesapisv1alpha1.ReconcileHashes{},
				ResolvedConfigs: map[string]*clabernetesutilcontainerlab.Config{
					"srl1": {},
					"srl2": {},
				},
				ResolvedTunnels: map[string][]*clabernetesapisv1alpha1.PointToPointTunnel{
					"srl1": {},
					"srl2": {},
				},
			},
			removeTopologyPrefix: false,
		},
		// distributed SR-SIM test (network-mode grouping)
		{
			name: "containerlab-network-mode-group",
			inTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "process-containerlab-definition-network-mode-test",
					Namespace: "clabernetes",
				},
				Spec: clabernetesapisv1alpha1.TopologySpec{
					Definition: clabernetesapisv1alpha1.Definition{
						Containerlab: `---
    name: srsim-distributed
    topology:
      kinds:
        nokia_srsim:
          image: nokia_srsim:25.7.R1
          license: /opt/nokia/sros/license.txt
      nodes:
        srsim-a:
          kind: nokia_srsim
          type: sr-7
          env:
            NOKIA_SROS_SLOT: A
        srsim-b:
          kind: nokia_srsim
          type: sr-7
          network-mode: container:srsim-a
          env:
            NOKIA_SROS_SLOT: B
        srsim-iom1:
          kind: nokia_srsim
          type: sr-7
          network-mode: container:srsim-a
          env:
            NOKIA_SROS_SLOT: "1"
        external-node:
          kind: srl
          image: ghcr.io/nokia/srlinux
      links:
        - endpoints: ["srsim-iom1:1/1/c1/1", "external-node:e1-1"]
`,
					},
				},
			},
			reconcileData: &clabernetescontrollerstopology.ReconcileData{
				Kind:           "containerlab",
				ResolvedHashes: clabernetesapisv1alpha1.ReconcileHashes{},
				ResolvedConfigs: map[string]*clabernetesutilcontainerlab.Config{
					// Only primary nodes and standalone nodes should have entries
					// srsim-a is primary (has srsim-b and srsim-iom1 as secondaries)
					// external-node is standalone
					"srsim-a":       {},
					"external-node": {},
				},
				ResolvedTunnels: map[string][]*clabernetesapisv1alpha1.PointToPointTunnel{
					// Tunnel from srsim-a group to external-node
					"srsim-a":       {},
					"external-node": {},
				},
			},
			removeTopologyPrefix: false,
		},
		{
			name: "containerlab-groups",
			inTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "process-containerlab-definition-groups-test",
					Namespace: "clabernetes",
				},
				Spec: clabernetesapisv1alpha1.TopologySpec{
					Definition: clabernetesapisv1alpha1.Definition{
						Containerlab: `---
    name: groups-test
    topology:
      kinds:
        nokia_srlinux:
          image: ghcr.io/nokia/srlinux
      groups:
        spine:
          kind: nokia_srlinux
          type: ixrd3l
        leaf:
          kind: nokia_srlinux
          type: ixrd2l
      nodes:
        spine1:
          group: spine
        leaf1:
          group: leaf
      links:
        - endpoints: ["spine1:e1-1", "leaf1:e1-1"]
`,
					},
				},
			},
			reconcileData: &clabernetescontrollerstopology.ReconcileData{
				Kind:           "containerlab",
				ResolvedHashes: clabernetesapisv1alpha1.ReconcileHashes{},
				ResolvedConfigs: map[string]*clabernetesutilcontainerlab.Config{
					"spine1": {},
					"leaf1":  {},
				},
				ResolvedTunnels: map[string][]*clabernetesapisv1alpha1.PointToPointTunnel{
					"spine1": {},
					"leaf1":  {},
				},
			},
			removeTopologyPrefix: false,
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				processor, err := clabernetescontrollerstopology.NewDefinitionProcessor(
					&claberneteslogging.FakeInstance{},
					testCase.inTopology,
					testCase.reconcileData,
					clabernetesconfig.GetFakeManager,
				)
				if err != nil {
					t.Fatal(err)
				}

				err = processor.Process()
				if err != nil {
					t.Fatal(err)
				}

				got := testCase.reconcileData

				if *clabernetestesthelper.Update {
					clabernetestesthelper.WriteTestFixtureJSON(
						t,
						fmt.Sprintf(
							"golden/%s/%s.json",
							definitionProcessTestName,
							testCase.name,
						),
						got,
					)
				}

				var want *clabernetescontrollerstopology.ReconcileData

				err = json.Unmarshal(
					clabernetestesthelper.ReadTestFixtureFile(
						t,
						fmt.Sprintf(
							"golden/%s/%s.json",
							definitionProcessTestName,
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
