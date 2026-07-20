// notes:
//
// the topology is passed to the ReconcileResolve func so it can in turn be passed to
// the concrete resolvers, for some kinds this is only used for the meta, but for some like service
// expose and pvc relevant spec fields are passed
//
// we pass the clabernetesConfigs map but we only ever check the keys in it, so the test data can be
// just the "current" node names and nil for the data
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
	k8sappsv1 "k8s.io/api/apps/v1"
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	ctrlruntimeclientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const reconcileResolveTestName = "reconcileresolve"

func TestReconcileResolveDeployment(t *testing.T) {
	owningTopologyName := "reconcile-resolve-deployment-test"

	cases := []struct {
		name                      string
		loadObjects               []apimachineryruntime.Object
		owningTopology            *clabernetesapisv1alpha1.Topology
		currentClabernetesConfigs map[string]*clabernetesutilcontainerlab.Config
	}{
		{
			name: "simple-no-extra-or-missing",
			loadObjects: []apimachineryruntime.Object{
				&k8sappsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fmt.Sprintf("%s-srl1", owningTopologyName),
						Namespace: "clabernetes",
						Labels: map[string]string{
							clabernetesconstants.LabelTopologyOwner: owningTopologyName,
							clabernetesconstants.LabelTopologyNode:  "srl1",
						},
					},
				},
			},
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      owningTopologyName,
					Namespace: "clabernetes",
				},
			},
			currentClabernetesConfigs: map[string]*clabernetesutilcontainerlab.Config{
				"srl1": nil,
			},
		},
		{
			name: "simple-extra",
			loadObjects: []apimachineryruntime.Object{
				&k8sappsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fmt.Sprintf("%s-srl1", owningTopologyName),
						Namespace: "clabernetes",
						Labels: map[string]string{
							clabernetesconstants.LabelTopologyOwner: owningTopologyName,
							clabernetesconstants.LabelTopologyNode:  "srl1",
						},
					},
				},
				&k8sappsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fmt.Sprintf("%s-srl2", owningTopologyName),
						Namespace: "clabernetes",
						Labels: map[string]string{
							clabernetesconstants.LabelTopologyOwner: owningTopologyName,
							clabernetesconstants.LabelTopologyNode:  "srl2",
						},
					},
				},
			},
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      owningTopologyName,
					Namespace: "clabernetes",
				},
			},
			currentClabernetesConfigs: map[string]*clabernetesutilcontainerlab.Config{
				"srl1": nil,
			},
		},
		{
			name: "simple-missing",
			loadObjects: []apimachineryruntime.Object{
				&k8sappsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fmt.Sprintf("%s-srl1", owningTopologyName),
						Namespace: "clabernetes",
						Labels: map[string]string{
							clabernetesconstants.LabelTopologyOwner: owningTopologyName,
							clabernetesconstants.LabelTopologyNode:  "srl1",
						},
					},
				},
			},
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      owningTopologyName,
					Namespace: "clabernetes",
				},
			},
			currentClabernetesConfigs: map[string]*clabernetesutilcontainerlab.Config{
				"srl1": nil,
				"srl2": nil,
			},
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				fakeClient := ctrlruntimeclientfake.NewFakeClient(testCase.loadObjects...)

				r := clabernetescontrollerstopology.NewReconciler(
					&claberneteslogging.FakeInstance{},
					fakeClient,
					"clabernetes",
					"clabernetes",
					"containerd",
					clabernetesconfig.GetFakeManager,
				)

				got, err := clabernetescontrollerstopology.ReconcileResolve(
					t.Context(),
					r,
					&k8sappsv1.Deployment{},
					&k8sappsv1.DeploymentList{},
					clabernetesconstants.KubernetesDeployment,
					testCase.owningTopology,
					testCase.currentClabernetesConfigs,
					r.DeploymentReconciler.Resolve,
				)
				if err != nil {
					t.Fatal(err)
				}

				if *clabernetestesthelper.Update {
					clabernetestesthelper.WriteTestFixtureJSON(
						t,
						fmt.Sprintf(
							"golden/%s/deployment-%s.json",
							reconcileResolveTestName,
							testCase.name,
						),
						got,
					)
				}

				var want *clabernetesutil.ObjectDiffer[*k8sappsv1.Deployment]

				err = json.Unmarshal(
					clabernetestesthelper.ReadTestFixtureFile(
						t,
						fmt.Sprintf(
							"golden/%s/deployment-%s.json",
							reconcileResolveTestName,
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

func TestReconcileResolvePVC(t *testing.T) {
	owningTopologyName := "reconcile-resolve-pvc-test"

	cases := []struct {
		name                      string
		loadObjects               []apimachineryruntime.Object
		owningTopology            *clabernetesapisv1alpha1.Topology
		currentClabernetesConfigs map[string]*clabernetesutilcontainerlab.Config
	}{
		{
			name:        "simple-no-extra-or-missing-no-persistence",
			loadObjects: []apimachineryruntime.Object{},
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      owningTopologyName,
					Namespace: "clabernetes",
				},
				Spec: clabernetesapisv1alpha1.TopologySpec{
					Deployment: clabernetesapisv1alpha1.Deployment{
						Persistence: clabernetesapisv1alpha1.Persistence{
							Enabled: false,
						},
					},
				},
			},
			currentClabernetesConfigs: map[string]*clabernetesutilcontainerlab.Config{
				"srl1": nil,
			},
		},
		{
			name: "simple-no-extra-or-missing-with-persistence",
			loadObjects: []apimachineryruntime.Object{
				&k8scorev1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fmt.Sprintf("%s-srl1", owningTopologyName),
						Namespace: "clabernetes",
						Labels: map[string]string{
							clabernetesconstants.LabelTopologyOwner: owningTopologyName,
							clabernetesconstants.LabelTopologyNode:  "srl1",
						},
					},
				},
			},
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      owningTopologyName,
					Namespace: "clabernetes",
				},
				Spec: clabernetesapisv1alpha1.TopologySpec{
					Deployment: clabernetesapisv1alpha1.Deployment{
						Persistence: clabernetesapisv1alpha1.Persistence{
							Enabled: true,
						},
					},
				},
			},
			currentClabernetesConfigs: map[string]*clabernetesutilcontainerlab.Config{
				"srl1": nil,
			},
		},
		{
			name: "simple-extra-with-persistence-disabled",
			loadObjects: []apimachineryruntime.Object{
				&k8scorev1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fmt.Sprintf("%s-srl1", owningTopologyName),
						Namespace: "clabernetes",
						Labels: map[string]string{
							clabernetesconstants.LabelTopologyOwner: owningTopologyName,
							clabernetesconstants.LabelTopologyNode:  "srl1",
						},
					},
				},
			},
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      owningTopologyName,
					Namespace: "clabernetes",
				},
				Spec: clabernetesapisv1alpha1.TopologySpec{
					Deployment: clabernetesapisv1alpha1.Deployment{
						Persistence: clabernetesapisv1alpha1.Persistence{
							Enabled: false,
						},
					},
				},
			},
			currentClabernetesConfigs: map[string]*clabernetesutilcontainerlab.Config{
				"srl1": nil,
			},
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				fakeClient := ctrlruntimeclientfake.NewFakeClient(testCase.loadObjects...)

				r := clabernetescontrollerstopology.NewReconciler(
					&claberneteslogging.FakeInstance{},
					fakeClient,
					"clabernetes",
					"clabernetes",
					"containerd",
					clabernetesconfig.GetFakeManager,
				)

				got, err := clabernetescontrollerstopology.ReconcileResolve(
					t.Context(),
					r,
					&k8scorev1.PersistentVolumeClaim{},
					&k8scorev1.PersistentVolumeClaimList{},
					clabernetesconstants.KubernetesPVC,
					testCase.owningTopology,
					testCase.currentClabernetesConfigs,
					r.PersistentVolumeClaimReconciler.Resolve,
				)
				if err != nil {
					t.Fatal(err)
				}

				if *clabernetestesthelper.Update {
					clabernetestesthelper.WriteTestFixtureJSON(
						t,
						fmt.Sprintf(
							"golden/%s/pvc-%s.json",
							reconcileResolveTestName,
							testCase.name,
						),
						got,
					)
				}

				var want *clabernetesutil.ObjectDiffer[*k8scorev1.PersistentVolumeClaim]

				err = json.Unmarshal(
					clabernetestesthelper.ReadTestFixtureFile(
						t,
						fmt.Sprintf(
							"golden/%s/pvc-%s.json",
							reconcileResolveTestName,
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

func TestReconcileResolveServiceFabric(t *testing.T) {
	owningTopologyName := "reconcile-resolve-servicefabric-test"

	cases := []struct {
		name                      string
		loadObjects               []apimachineryruntime.Object
		owningTopology            *clabernetesapisv1alpha1.Topology
		currentClabernetesConfigs map[string]*clabernetesutilcontainerlab.Config
	}{
		{
			name: "simple-no-extra-or-missing",
			loadObjects: []apimachineryruntime.Object{
				&k8scorev1.Service{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fmt.Sprintf("%s-srl1", owningTopologyName),
						Namespace: "clabernetes",
						Labels: map[string]string{
							clabernetesconstants.LabelTopologyOwner:       owningTopologyName,
							clabernetesconstants.LabelTopologyNode:        "srl1",
							clabernetesconstants.LabelTopologyServiceType: clabernetesconstants.TopologyServiceTypeFabric,
						},
					},
				},
			},
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      owningTopologyName,
					Namespace: "clabernetes",
				},
			},
			currentClabernetesConfigs: map[string]*clabernetesutilcontainerlab.Config{
				"srl1": nil,
			},
		},
		{
			name: "simple-extra",
			loadObjects: []apimachineryruntime.Object{
				&k8scorev1.Service{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fmt.Sprintf("%s-srl1", owningTopologyName),
						Namespace: "clabernetes",
						Labels: map[string]string{
							clabernetesconstants.LabelTopologyOwner:       owningTopologyName,
							clabernetesconstants.LabelTopologyNode:        "srl1",
							clabernetesconstants.LabelTopologyServiceType: clabernetesconstants.TopologyServiceTypeFabric,
						},
					},
				},
				&k8scorev1.Service{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fmt.Sprintf("%s-srl2", owningTopologyName),
						Namespace: "clabernetes",
						Labels: map[string]string{
							clabernetesconstants.LabelTopologyOwner:       owningTopologyName,
							clabernetesconstants.LabelTopologyNode:        "srl2",
							clabernetesconstants.LabelTopologyServiceType: clabernetesconstants.TopologyServiceTypeFabric,
						},
					},
				},
			},
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      owningTopologyName,
					Namespace: "clabernetes",
				},
			},
			currentClabernetesConfigs: map[string]*clabernetesutilcontainerlab.Config{
				"srl1": nil,
			},
		},
		{
			name: "simple-missing",
			loadObjects: []apimachineryruntime.Object{
				&k8scorev1.Service{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fmt.Sprintf("%s-srl1", owningTopologyName),
						Namespace: "clabernetes",
						Labels: map[string]string{
							clabernetesconstants.LabelTopologyOwner:       owningTopologyName,
							clabernetesconstants.LabelTopologyNode:        "srl1",
							clabernetesconstants.LabelTopologyServiceType: clabernetesconstants.TopologyServiceTypeFabric,
						},
					},
				},
			},
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      owningTopologyName,
					Namespace: "clabernetes",
				},
			},
			currentClabernetesConfigs: map[string]*clabernetesutilcontainerlab.Config{
				"srl1": nil,
				"srl2": nil,
			},
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				fakeClient := ctrlruntimeclientfake.NewFakeClient(testCase.loadObjects...)

				r := clabernetescontrollerstopology.NewReconciler(
					&claberneteslogging.FakeInstance{},
					fakeClient,
					"clabernetes",
					"clabernetes",
					"containerd",
					clabernetesconfig.GetFakeManager,
				)

				got, err := clabernetescontrollerstopology.ReconcileResolve(
					t.Context(),
					r,
					&k8scorev1.Service{},
					&k8scorev1.ServiceList{},
					clabernetesconstants.KubernetesService,
					testCase.owningTopology,
					testCase.currentClabernetesConfigs,
					r.ServiceFabricReconciler.Resolve,
				)
				if err != nil {
					t.Fatal(err)
				}

				if *clabernetestesthelper.Update {
					clabernetestesthelper.WriteTestFixtureJSON(
						t,
						fmt.Sprintf(
							"golden/%s/servicefabric-%s.json",
							reconcileResolveTestName,
							testCase.name,
						),
						got,
					)
				}

				var want *clabernetesutil.ObjectDiffer[*k8scorev1.Service]

				err = json.Unmarshal(
					clabernetestesthelper.ReadTestFixtureFile(
						t,
						fmt.Sprintf(
							"golden/%s/servicefabric-%s.json",
							reconcileResolveTestName,
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

func TestReconcileResolveServiceExpose(t *testing.T) {
	owningTopologyName := "reconcile-resolve-serviceexpose-test"

	cases := []struct {
		name                      string
		loadObjects               []apimachineryruntime.Object
		owningTopology            *clabernetesapisv1alpha1.Topology
		currentClabernetesConfigs map[string]*clabernetesutilcontainerlab.Config
	}{
		{
			name: "simple-no-extra-or-missing",
			loadObjects: []apimachineryruntime.Object{
				&k8scorev1.Service{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fmt.Sprintf("%s-srl1", owningTopologyName),
						Namespace: "clabernetes",
						Labels: map[string]string{
							clabernetesconstants.LabelTopologyOwner:       owningTopologyName,
							clabernetesconstants.LabelTopologyNode:        "srl1",
							clabernetesconstants.LabelTopologyServiceType: clabernetesconstants.TopologyServiceTypeExpose,
						},
					},
				},
			},
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      owningTopologyName,
					Namespace: "clabernetes",
				},
			},
			currentClabernetesConfigs: map[string]*clabernetesutilcontainerlab.Config{
				"srl1": nil,
			},
		},
		{
			name: "simple-extra",
			loadObjects: []apimachineryruntime.Object{
				&k8scorev1.Service{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fmt.Sprintf("%s-srl1", owningTopologyName),
						Namespace: "clabernetes",
						Labels: map[string]string{
							clabernetesconstants.LabelTopologyOwner:       owningTopologyName,
							clabernetesconstants.LabelTopologyNode:        "srl1",
							clabernetesconstants.LabelTopologyServiceType: clabernetesconstants.TopologyServiceTypeExpose,
						},
					},
				},
				&k8scorev1.Service{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fmt.Sprintf("%s-srl2", owningTopologyName),
						Namespace: "clabernetes",
						Labels: map[string]string{
							clabernetesconstants.LabelTopologyOwner:       owningTopologyName,
							clabernetesconstants.LabelTopologyNode:        "srl2",
							clabernetesconstants.LabelTopologyServiceType: clabernetesconstants.TopologyServiceTypeExpose,
						},
					},
				},
			},
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      owningTopologyName,
					Namespace: "clabernetes",
				},
			},
			currentClabernetesConfigs: map[string]*clabernetesutilcontainerlab.Config{
				"srl1": nil,
			},
		},
		{
			name: "simple-missing",
			loadObjects: []apimachineryruntime.Object{
				&k8scorev1.Service{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fmt.Sprintf("%s-srl1", owningTopologyName),
						Namespace: "clabernetes",
						Labels: map[string]string{
							clabernetesconstants.LabelTopologyOwner:       owningTopologyName,
							clabernetesconstants.LabelTopologyNode:        "srl1",
							clabernetesconstants.LabelTopologyServiceType: clabernetesconstants.TopologyServiceTypeExpose,
						},
					},
				},
			},
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      owningTopologyName,
					Namespace: "clabernetes",
				},
			},
			currentClabernetesConfigs: map[string]*clabernetesutilcontainerlab.Config{
				"srl1": nil,
				"srl2": nil,
			},
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				fakeClient := ctrlruntimeclientfake.NewFakeClient(testCase.loadObjects...)

				r := clabernetescontrollerstopology.NewReconciler(
					&claberneteslogging.FakeInstance{},
					fakeClient,
					"clabernetes",
					"clabernetes",
					"containerd",
					clabernetesconfig.GetFakeManager,
				)

				got, err := clabernetescontrollerstopology.ReconcileResolve(
					t.Context(),
					r,
					&k8scorev1.Service{},
					&k8scorev1.ServiceList{},
					clabernetesconstants.KubernetesService,
					testCase.owningTopology,
					testCase.currentClabernetesConfigs,
					r.ServiceExposeReconciler.Resolve,
				)
				if err != nil {
					t.Fatal(err)
				}

				if *clabernetestesthelper.Update {
					clabernetestesthelper.WriteTestFixtureJSON(
						t,
						fmt.Sprintf(
							"golden/%s/serviceexpose-%s.json",
							reconcileResolveTestName,
							testCase.name,
						),
						got,
					)
				}

				var want *clabernetesutil.ObjectDiffer[*k8scorev1.Service]

				err = json.Unmarshal(
					clabernetestesthelper.ReadTestFixtureFile(
						t,
						fmt.Sprintf(
							"golden/%s/serviceexpose-%s.json",
							reconcileResolveTestName,
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
