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
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const renderServiceAccountTestName = "serviceaccount/render-serviceaccount"

func TestRenderServiceAccount(t *testing.T) {
	cases := []struct {
		name                   string
		owningTopology         *clabernetesapisv1alpha1.Topology
		existingServiceAccount *k8scorev1.ServiceAccount
	}{
		{
			name: "simple",
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "render-serviceaccount-test",
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
			name: "simple-existing-serviceaccount",
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "render-serviceaccount-test",
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
			existingServiceAccount: &k8scorev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name: "existing-serviceaccount",
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "apps/v1",
							Kind:       "Deployment",
							Name:       "dummy",
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

				reconciler := clabernetescontrollerstopology.NewServiceAccountReconciler(
					&claberneteslogging.FakeInstance{},
					nil,
					clabernetesconfig.GetFakeManager,
				)

				got := reconciler.Render(
					testCase.owningTopology,
					testCase.existingServiceAccount,
				)

				if *clabernetestesthelper.Update {
					clabernetestesthelper.WriteTestFixtureJSON(
						t,
						fmt.Sprintf(
							"golden/%s/%s.json",
							renderServiceAccountTestName,
							testCase.name,
						),
						got,
					)
				}

				var want k8scorev1.ServiceAccount

				err := json.Unmarshal(
					clabernetestesthelper.ReadTestFixtureFile(
						t,
						fmt.Sprintf(
							"golden/%s/%s.json",
							renderServiceAccountTestName,
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
