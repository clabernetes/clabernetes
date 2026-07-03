package topology_test

import (
	"encoding/json"
	"fmt"
	"testing"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetescontrollerstopology "github.com/srl-labs/clabernetes/controllers/topology"
	clabernetestesthelper "github.com/srl-labs/clabernetes/testhelper"
	clabernetesutilcontainerlab "github.com/srl-labs/clabernetes/util/containerlab"
)

const reconcileDataSetStatusTestName = "reconciledatasetstatus"

func TestReconcileDataSetStatus(t *testing.T) {
	cases := []struct {
		name                 string
		reconcileData        *clabernetescontrollerstopology.ReconcileData
		owningTopologyStatus *clabernetesapisv1alpha1.TopologyStatus
	}{
		{
			name:                 "simple",
			reconcileData:        &clabernetescontrollerstopology.ReconcileData{},
			owningTopologyStatus: &clabernetesapisv1alpha1.TopologyStatus{},
		},
		{
			name: "simple-values",
			reconcileData: &clabernetescontrollerstopology.ReconcileData{
				Kind: "foo",
				ResolvedConfigs: map[string]*clabernetesutilcontainerlab.Config{
					"srl1": {},
					"srl2": {},
				},
				NodeStatuses: map[string]string{
					"srl1": "ready",
					"srl2": "notready",
				},
				TopologyState: clabernetesapisv1alpha1.TopologyStateDeploying,
			},
			owningTopologyStatus: &clabernetesapisv1alpha1.TopologyStatus{},
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				err := testCase.reconcileData.SetStatus(testCase.owningTopologyStatus)
				if err != nil {
					t.Fatal(err)
				}

				if *clabernetestesthelper.Update {
					clabernetestesthelper.WriteTestFixtureJSON(
						t,
						fmt.Sprintf(
							"golden/%s/%s.json",
							reconcileDataSetStatusTestName,
							testCase.name,
						),
						testCase.owningTopologyStatus,
					)
				}

				var want clabernetesapisv1alpha1.TopologyStatus

				err = json.Unmarshal(
					clabernetestesthelper.ReadTestFixtureFile(
						t,
						fmt.Sprintf(
							"golden/%s/%s.json",
							reconcileDataSetStatusTestName,
							testCase.name,
						),
					),
					&want,
				)
				if err != nil {
					t.Fatal(err)
				}

				clabernetestesthelper.MarshaledEqual(t, testCase.owningTopologyStatus, want)
			},
		)
	}
}
