package topology_test

import (
	"encoding/json"
	"fmt"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetescontrollerstopology "github.com/clabernetes/clabernetes/controllers/topology"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
	clabernetesutil "github.com/clabernetes/clabernetes/util"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const renderServiceFabricTestName = "servicefabric/render-service"

func TestResolveServiceFabric(t *testing.T) {
	cases := []struct {
		name               string
		ownedServices      *k8scorev1.ServiceList
		clabernetesConfigs map[string]*clabernetesutilcontainerlab.Config
		expectedCurrent    []string
		expectedMissing    []string
		expectedExtra      []*k8scorev1.Service
	}{
		{
			name:               "simple",
			ownedServices:      &k8scorev1.ServiceList{},
			clabernetesConfigs: nil,
			expectedCurrent:    nil,
			expectedMissing:    nil,
			expectedExtra:      []*k8scorev1.Service{},
		},
		{
			name:          "missing-nodes",
			ownedServices: &k8scorev1.ServiceList{},
			clabernetesConfigs: map[string]*clabernetesutilcontainerlab.Config{
				"node1": nil,
				"node2": nil,
			},
			expectedCurrent: nil,
			expectedMissing: []string{"node1", "node2"},
			expectedExtra:   []*k8scorev1.Service{},
		},
		{
			name: "extra-nodes",
			ownedServices: &k8scorev1.ServiceList{
				Items: []k8scorev1.Service{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "resolve-servicefabric-test",
							Namespace: "clabernetes",
							Labels: map[string]string{
								clabernetesconstants.LabelTopologyServiceType: clabernetesconstants.TopologyServiceTypeFabric,
								clabernetesconstants.LabelTopologyNode:        "node2",
							},
						},
					},
				},
			},
			clabernetesConfigs: map[string]*clabernetesutilcontainerlab.Config{
				"node1": nil,
			},
			expectedCurrent: nil,
			expectedMissing: nil,
			expectedExtra: []*k8scorev1.Service{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "resolve-servicefabric-test",
						Namespace: "clabernetes",
						Labels: map[string]string{
							clabernetesconstants.LabelTopologyServiceType: clabernetesconstants.TopologyServiceTypeFabric,
							clabernetesconstants.LabelTopologyNode:        "node2",
						},
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

				reconciler := clabernetescontrollerstopology.NewServiceFabricReconciler(
					&claberneteslogging.FakeInstance{},
					clabernetesconfig.GetFakeManager,
				)

				got, err := reconciler.Resolve(
					testCase.ownedServices,
					testCase.clabernetesConfigs,
					nil,
				)
				if err != nil {
					t.Fatal(err)
				}

				var gotCurrent []string

				for current := range got.Current {
					gotCurrent = append(gotCurrent, current)
				}

				if !clabernetesutil.StringSliceContainsAll(gotCurrent, testCase.expectedCurrent) {
					clabernetestesthelper.FailOutput(t, gotCurrent, testCase.expectedCurrent)
				}

				if !clabernetesutil.StringSliceContainsAll(got.Missing, testCase.expectedMissing) {
					clabernetestesthelper.FailOutput(t, got.Missing, testCase.expectedMissing)
				}

				clabernetestesthelper.MarshaledEqual(t, got.Extra, testCase.expectedExtra)
			})
	}
}

func TestRenderServiceFabric(t *testing.T) {
	cases := []struct {
		name           string
		owningTopology *clabernetesapisv1alpha1.Topology
		nodeName       string
	}{
		{
			name: "simple",
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "render-service-fabric-test",
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
			nodeName: "srl1",
		},
		{
			name: "simple-no-prefix",
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "render-service-fabric-test",
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
				Status: clabernetesapisv1alpha1.TopologyStatus{
					// to set naming for test purposes we need to update the *status* of the topo
					// since this is done very early in the rec
					RemoveTopologyPrefix: clabernetesutil.ToPointer(true),
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

				reconciler := clabernetescontrollerstopology.NewServiceFabricReconciler(
					&claberneteslogging.FakeInstance{},
					clabernetesconfig.GetFakeManager,
				)

				got := reconciler.Render(
					testCase.owningTopology,
					testCase.nodeName,
				)

				if *clabernetestesthelper.Update {
					clabernetestesthelper.WriteTestFixtureJSON(
						t,
						fmt.Sprintf(
							"golden/%s/%s.json",
							renderServiceFabricTestName,
							testCase.name,
						),
						got,
					)
				}

				var want k8scorev1.Service

				err := json.Unmarshal(
					clabernetestesthelper.ReadTestFixtureFile(
						t,
						fmt.Sprintf(
							"golden/%s/%s.json",
							renderServiceFabricTestName,
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
