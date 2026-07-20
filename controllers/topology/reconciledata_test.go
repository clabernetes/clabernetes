package topology_test

import (
	"encoding/json"
	"fmt"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetescontrollerstopology "github.com/clabernetes/clabernetes/controllers/topology"
	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
	clabernetesutil "github.com/clabernetes/clabernetes/util"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
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
				ResolvedHashes: clabernetesapisv1alpha1.ReconcileHashes{
					Config:           "confighash",
					ExposedPorts:     "porthash",
					ImagePullSecrets: "pullsecret",
				},
				ResolvedConfigs: map[string]*clabernetesutilcontainerlab.Config{
					"srl1": {},
					"srl2": {},
				},
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

func TestReconcileDataConfigMapHasChanges(t *testing.T) {
	cases := []struct {
		name          string
		reconcileData *clabernetescontrollerstopology.ReconcileData
		expected      bool
	}{
		{
			name: "simple",
			reconcileData: &clabernetescontrollerstopology.ReconcileData{
				PreviousHashes: clabernetesapisv1alpha1.ReconcileHashes{
					Config:           "a clab config, neat",
					ImagePullSecrets: "",
				},
				ResolvedHashes: clabernetesapisv1alpha1.ReconcileHashes{
					Config:           "a clab config, neat",
					ImagePullSecrets: "",
				},
				NodesNeedingReboot: clabernetesutil.NewStringSet(),
			},
			expected: false,
		},
		{
			name: "different-configs",
			reconcileData: &clabernetescontrollerstopology.ReconcileData{
				PreviousHashes: clabernetesapisv1alpha1.ReconcileHashes{
					Config:           "a clab config, neat",
					ImagePullSecrets: "",
				},
				ResolvedHashes: clabernetesapisv1alpha1.ReconcileHashes{
					Config:           "a not clab config, sad",
					ImagePullSecrets: "",
				},
				NodesNeedingReboot: clabernetesutil.NewStringSet(),
			},
			expected: true,
		},
		{
			name: "different-pull-secrets",
			reconcileData: &clabernetescontrollerstopology.ReconcileData{
				PreviousHashes: clabernetesapisv1alpha1.ReconcileHashes{
					Config:           "a clab config, neat",
					ImagePullSecrets: "some pull secret",
				},
				ResolvedHashes: clabernetesapisv1alpha1.ReconcileHashes{
					Config:           "a clab config, neat",
					ImagePullSecrets: "",
				},
				NodesNeedingReboot: clabernetesutil.NewStringSet(),
			},
			expected: true,
		},
		{
			name: "nodes-need-reboot",
			reconcileData: &clabernetescontrollerstopology.ReconcileData{
				PreviousHashes: clabernetesapisv1alpha1.ReconcileHashes{
					Config:           "a clab config, neat",
					ImagePullSecrets: "some pull secret",
				},
				ResolvedHashes: clabernetesapisv1alpha1.ReconcileHashes{
					Config:           "a clab config, neat",
					ImagePullSecrets: "",
				},
				NodesNeedingReboot: clabernetesutil.NewStringSetWithValues("foo", "bar"),
			},
			expected: true,
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				actual := testCase.reconcileData.ConfigMapHasChanges()
				if actual != testCase.expected {
					clabernetestesthelper.FailOutput(t, actual, testCase.expected)
				}
			},
		)
	}
}
