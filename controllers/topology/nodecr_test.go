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

const renderNodeTestName = "nodecr/render-node"

func TestRenderNode(t *testing.T) {
	cases := []struct {
		name           string
		owningTopology *clabernetesapisv1alpha1.Topology
		reconcileData  *clabernetescontrollerstopology.ReconcileData
		nodeName       string
	}{
		{
			name: "simple",
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "render-node-test",
					Namespace: "clabernetes",
				},
			},
			reconcileData: &clabernetescontrollerstopology.ReconcileData{
				ResolvedConfigs: map[string]*clabernetesutilcontainerlab.Config{
					"srl1": {
						Name:   "clabernetes-srl1",
						Prefix: clabernetesutil.ToPointer(""),
						Topology: &clabernetesutilcontainerlab.Topology{
							Defaults: &clabernetesutilcontainerlab.NodeDefinition{
								Ports: []string{},
							},
							Nodes: map[string]*clabernetesutilcontainerlab.NodeDefinition{
								"srl1": {
									Kind:  "srl",
									Image: "ghcr.io/nokia/srlinux",
								},
							},
						},
					},
				},
			},
			nodeName: "srl1",
		},
		{
			name: "extras",
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "render-node-test",
					Namespace: "clabernetes",
				},
				Spec: clabernetesapisv1alpha1.TopologySpec{
					Deployment: clabernetesapisv1alpha1.Deployment{
						FilesFromURL: map[string][]clabernetesapisv1alpha1.FileFromURL{
							"srl1": {
								{
									FilePath: "startup-config.cfg",
									URL:      "http://example.com/startup-config.cfg",
								},
							},
						},
					},
					ImagePull: clabernetesapisv1alpha1.ImagePull{
						PullSecrets: []string{"regcred"},
					},
				},
			},
			reconcileData: &clabernetescontrollerstopology.ReconcileData{
				ResolvedConfigs: map[string]*clabernetesutilcontainerlab.Config{
					"srl1": {
						Name:   "clabernetes-srl1",
						Prefix: clabernetesutil.ToPointer(""),
						Topology: &clabernetesutilcontainerlab.Topology{
							Defaults: &clabernetesutilcontainerlab.NodeDefinition{
								Ports: []string{},
							},
							Nodes: map[string]*clabernetesutilcontainerlab.NodeDefinition{
								"srl1": {
									Kind:  "srl",
									Image: "ghcr.io/nokia/srlinux",
								},
							},
						},
					},
				},
			},
			nodeName: "srl1",
		},
		{
			name: "no-prefix",
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "render-node-test",
					Namespace: "clabernetes",
				},
				Status: clabernetesapisv1alpha1.TopologyStatus{
					RemoveTopologyPrefix: clabernetesutil.ToPointer(true),
				},
			},
			reconcileData: &clabernetescontrollerstopology.ReconcileData{
				ResolvedConfigs: map[string]*clabernetesutilcontainerlab.Config{
					"srl1": {
						Name:   "clabernetes-srl1",
						Prefix: clabernetesutil.ToPointer(""),
						Topology: &clabernetesutilcontainerlab.Topology{
							Defaults: &clabernetesutilcontainerlab.NodeDefinition{
								Ports: []string{},
							},
							Nodes: map[string]*clabernetesutilcontainerlab.NodeDefinition{
								"srl1": {
									Kind:  "srl",
									Image: "ghcr.io/nokia/srlinux",
								},
							},
						},
					},
				},
			},
			nodeName: "srl1",
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				reconciler := clabernetescontrollerstopology.NewNodeReconciler(
					&claberneteslogging.FakeInstance{},
					clabernetesconfig.GetFakeManager,
				)

				got, err := reconciler.Render(
					testCase.owningTopology,
					testCase.reconcileData,
					testCase.nodeName,
				)
				if err != nil {
					t.Fatal(err)
				}

				if *clabernetestesthelper.Update {
					clabernetestesthelper.WriteTestFixtureJSON(
						t,
						fmt.Sprintf("golden/%s/%s.json", renderNodeTestName, testCase.name),
						got,
					)
				}

				var want clabernetesapisv1alpha1.Node

				err = json.Unmarshal(
					clabernetestesthelper.ReadTestFixtureFile(
						t,
						fmt.Sprintf("golden/%s/%s.json", renderNodeTestName, testCase.name),
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
