package topology_test

import (
	"encoding/json"
	"fmt"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetescontrollerstopology "github.com/clabernetes/clabernetes/controllers/topology"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const renderConnectivityTestName = "connectivity/render-connectivity"

func TestRenderConnectivity(t *testing.T) {
	cases := []struct {
		name           string
		owningTopology *clabernetesapisv1alpha1.Topology
		tunnels        map[string][]*clabernetesapisv1alpha1.PointToPointTunnel
	}{
		{
			name: "simple-no-tunnels",
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "render-rolebinding-test",
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
`,
					},
				},
			},
		},
		{
			name: "simple",
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "render-rolebinding-test",
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
`,
					},
				},
			},
			tunnels: map[string][]*clabernetesapisv1alpha1.PointToPointTunnel{
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
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				reconciler := clabernetescontrollerstopology.NewConnectivityReconciler(
					&claberneteslogging.FakeInstance{},
					clabernetesconfig.GetFakeManager,
				)

				got := reconciler.Render(
					testCase.owningTopology,
					testCase.tunnels,
				)

				if *clabernetestesthelper.Update {
					clabernetestesthelper.WriteTestFixtureJSON(
						t,
						fmt.Sprintf(
							"golden/%s/%s.json",
							renderConnectivityTestName,
							testCase.name,
						),
						got,
					)
				}

				var want clabernetesapisv1alpha1.Connectivity

				err := json.Unmarshal(
					clabernetestesthelper.ReadTestFixtureFile(
						t,
						fmt.Sprintf(
							"golden/%s/%s.json",
							renderConnectivityTestName,
							testCase.name,
						),
					),
					&want,
				)
				if err != nil {
					t.Fatal(err)
				}

				clabernetestesthelper.MarshaledEqual(t, got, want)
			})
	}
}
