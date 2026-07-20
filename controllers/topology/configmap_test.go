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
	clabernetesutil "github.com/clabernetes/clabernetes/util"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
)

var defaultPorts = []string{
	"21022:22/tcp",
	"21023:23/tcp",
	"21161:161/udp",
	"33333:57400/tcp",
	"60000:21/tcp",
	"60001:80/tcp",
	"60002:443/tcp",
	"60003:830/tcp",
	"60004:5000/tcp",
	"60005:5900/tcp",
	"60006:6030/tcp",
	"60007:9339/tcp",
	"60008:9340/tcp",
	"60009:9559/tcp",
}

const renderConfigMapTestName = "configmap/render-config-map"

// TestRenderConfigMap ensures that we properly render the main tunnel/config configmap for a given
// c9s deployment (containerlab CR).
func TestRenderConfigMap(t *testing.T) {
	cases := []struct {
		name               string
		owningTopology     *clabernetesapisv1alpha1.Topology
		clabernetesConfigs map[string]*clabernetesutilcontainerlab.Config
		filesFromURL       map[string][]clabernetesapisv1alpha1.FileFromURL
		imagePullSecrets   string
	}{
		{
			name: "basic-two-node-with-links",
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-configmap",
					Namespace: "nowhere",
				},
			},
			clabernetesConfigs: map[string]*clabernetesutilcontainerlab.Config{
				"srl1": {
					Name:   "clabernetes-srl1",
					Prefix: clabernetesutil.ToPointer(""),
					Topology: &clabernetesutilcontainerlab.Topology{
						Defaults: &clabernetesutilcontainerlab.NodeDefinition{
							Ports: defaultPorts,
						},
						Nodes: map[string]*clabernetesutilcontainerlab.NodeDefinition{
							"srl1": {
								Kind: "srl",
							},
						},
						Links: []*clabernetesutilcontainerlab.LinkDefinition{
							{
								LinkConfig: clabernetesutilcontainerlab.LinkConfig{
									Endpoints: []string{
										"srl1:e1-1",
										"host:srl1-e1-1",
									},
								},
							},
						},
					},
					Debug: false,
				},
				"srl2": {
					Name:   "clabernetes-srl2",
					Prefix: clabernetesutil.ToPointer(""),
					Topology: &clabernetesutilcontainerlab.Topology{
						Defaults: &clabernetesutilcontainerlab.NodeDefinition{
							Ports: defaultPorts,
						},
						Nodes: map[string]*clabernetesutilcontainerlab.NodeDefinition{
							"srl2": {
								Kind: "srl",
							},
						},
						Links: []*clabernetesutilcontainerlab.LinkDefinition{
							{
								LinkConfig: clabernetesutilcontainerlab.LinkConfig{
									Endpoints: []string{
										"srl2:e1-1",
										"host:srl2-e1-1",
									},
								},
							},
						},
					},
					Debug: false,
				},
			},
			filesFromURL: map[string][]clabernetesapisv1alpha1.FileFromURL{},
		},
		{
			name: "basic-two-node-no-links",
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-configmap",
					Namespace: "nowhere",
				},
			},
			clabernetesConfigs: map[string]*clabernetesutilcontainerlab.Config{
				"srl1": {
					Name:   "clabernetes-srl1",
					Prefix: clabernetesutil.ToPointer(""),
					Topology: &clabernetesutilcontainerlab.Topology{
						Defaults: &clabernetesutilcontainerlab.NodeDefinition{
							Ports: defaultPorts,
						},
						Nodes: map[string]*clabernetesutilcontainerlab.NodeDefinition{
							"srl1": {
								Kind: "srl",
							},
						},
						Links: []*clabernetesutilcontainerlab.LinkDefinition{},
					},
					Debug: false,
				},
				"srl2": {
					Name:   "clabernetes-srl2",
					Prefix: clabernetesutil.ToPointer(""),
					Topology: &clabernetesutilcontainerlab.Topology{
						Defaults: &clabernetesutilcontainerlab.NodeDefinition{
							Ports: defaultPorts,
						},
						Nodes: map[string]*clabernetesutilcontainerlab.NodeDefinition{
							"srl2": {
								Kind: "srl",
							},
						},
						Links: []*clabernetesutilcontainerlab.LinkDefinition{},
					},
					Debug: false,
				},
			},
			filesFromURL: map[string][]clabernetesapisv1alpha1.FileFromURL{},
		},
		{
			name: "image-pull-secrets",
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-configmap",
					Namespace: "nowhere",
				},
			},
			clabernetesConfigs: map[string]*clabernetesutilcontainerlab.Config{
				"srl1": {
					Name:   "clabernetes-srl1",
					Prefix: clabernetesutil.ToPointer(""),
					Topology: &clabernetesutilcontainerlab.Topology{
						Defaults: &clabernetesutilcontainerlab.NodeDefinition{
							Ports: defaultPorts,
						},
						Nodes: map[string]*clabernetesutilcontainerlab.NodeDefinition{
							"srl1": {
								Kind: "srl",
							},
						},
						Links: []*clabernetesutilcontainerlab.LinkDefinition{},
					},
					Debug: false,
				},
			},
			filesFromURL:     map[string][]clabernetesapisv1alpha1.FileFromURL{},
			imagePullSecrets: "- some-secret\n-another-secret",
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				reconciler := clabernetescontrollerstopology.NewConfigMapReconciler(
					&claberneteslogging.FakeInstance{},
					clabernetesconfig.GetFakeManager,
				)

				got, err := reconciler.Render(
					testCase.owningTopology,
					testCase.clabernetesConfigs,
					testCase.filesFromURL,
					testCase.imagePullSecrets,
				)
				if err != nil {
					t.Fatal(err)
				}

				if *clabernetestesthelper.Update {
					clabernetestesthelper.WriteTestFixtureJSON(
						t,
						fmt.Sprintf("golden/%s/%s.json", renderConfigMapTestName, testCase.name),
						got,
					)
				}

				var want k8scorev1.ConfigMap

				err = json.Unmarshal(
					clabernetestesthelper.ReadTestFixtureFile(
						t,
						fmt.Sprintf("golden/%s/%s.json", renderConfigMapTestName, testCase.name),
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

func TestConfigMapConforms(t *testing.T) {
	cases := []struct {
		name     string
		existing *k8scorev1.ConfigMap
		rendered *k8scorev1.ConfigMap
		ownerUID apimachinerytypes.UID
		conforms bool
	}{
		{
			name: "simple",
			existing: &k8scorev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{
							UID: apimachinerytypes.UID("clabernetes-testing"),
						},
					},
				},
			},
			rendered: &k8scorev1.ConfigMap{},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: true,
		},
		{
			name: "bad-data-extra-stuff",
			existing: &k8scorev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{
							UID: apimachinerytypes.UID("clabernetes-testing"),
						},
					},
				},
				Data: map[string]string{
					"something": "not in the expected",
				},
			},
			rendered: &k8scorev1.ConfigMap{},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: false,
		},
		{
			name: "bad-data-missing-stuff",
			existing: &k8scorev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{
							UID: apimachinerytypes.UID("clabernetes-testing"),
						},
					},
				},
			},
			rendered: &k8scorev1.ConfigMap{
				Data: map[string]string{
					"something": "we expect expected",
				},
			},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: false,
		},

		// annotations

		{
			name: "missing-clabernetes-global-annotations",
			existing: &k8scorev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"somethingelse": "xyz",
					},
					OwnerReferences: []metav1.OwnerReference{
						{
							UID: apimachinerytypes.UID("clabernetes-testing"),
						},
					},
				},
			},
			rendered: &k8scorev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"user-provided-global-annotation": "expected-value",
					},
				},
			},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: false,
		},
		{
			name: "clabernetes-global-annotations-wrong-value",
			existing: &k8scorev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"user-provided-global-annotation": "xyz",
					},
					OwnerReferences: []metav1.OwnerReference{
						{
							UID: apimachinerytypes.UID("clabernetes-testing"),
						},
					},
				},
			},
			rendered: &k8scorev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"user-provided-global-annotation": "expected-value",
					},
				},
			},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: false,
		},
		{
			name: "extra-annotations-ok",
			existing: &k8scorev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"somethingelseentirely": "thisisok",
					},
					OwnerReferences: []metav1.OwnerReference{
						{
							UID: apimachinerytypes.UID("clabernetes-testing"),
						},
					},
				},
			},
			rendered: &k8scorev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{},
			},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: true,
		},

		// labels

		{
			name: "missing-clabernetes-global-annotations",
			existing: &k8scorev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"somethingelse": "xyz",
					},
					OwnerReferences: []metav1.OwnerReference{
						{
							UID: apimachinerytypes.UID("clabernetes-testing"),
						},
					},
				},
			},
			rendered: &k8scorev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"user-provided-global-label": "expected-value",
					},
				},
			},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: false,
		},
		{
			name: "clabernetes-global-labels-wrong-value",
			existing: &k8scorev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"user-provided-global-label": "xyz",
					},
					OwnerReferences: []metav1.OwnerReference{
						{
							UID: apimachinerytypes.UID("clabernetes-testing"),
						},
					},
				},
			},
			rendered: &k8scorev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"user-provided-global-label": "expected-value",
					},
				},
			},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: false,
		},
		{
			name: "extra-labels-ok",
			existing: &k8scorev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"somethingelseentirely": "thisisok",
					},
					OwnerReferences: []metav1.OwnerReference{
						{
							UID: apimachinerytypes.UID("clabernetes-testing"),
						},
					},
				},
			},
			rendered: &k8scorev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{},
			},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: true,
		},

		// owner

		{
			name: "bad-owner",
			existing: &k8scorev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{
							UID: apimachinerytypes.UID("evil-imposter"),
						},
					},
				},
			},
			rendered: &k8scorev1.ConfigMap{},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: false,
		},
		{
			name: "multiple-owner",
			existing: &k8scorev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{
							UID: apimachinerytypes.UID("evil-imposter"),
						},
						{
							UID: apimachinerytypes.UID("clabernetes-testing"),
						},
					},
				},
			},
			rendered: &k8scorev1.ConfigMap{},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: false,
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				reconciler := clabernetescontrollerstopology.NewConfigMapReconciler(
					&claberneteslogging.FakeInstance{},
					clabernetesconfig.GetFakeManager,
				)

				actual := reconciler.Conforms(
					testCase.existing,
					testCase.rendered,
					testCase.ownerUID,
				)
				if actual != testCase.conforms {
					clabernetestesthelper.FailOutput(t, testCase.existing, testCase.rendered)
				}
			})
	}
}
