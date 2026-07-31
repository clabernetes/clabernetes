package topology_test

import (
	"encoding/json"
	"fmt"
	"testing"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/srl-labs/clabernetes/config"
	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	clabernetescontrollerstopology "github.com/srl-labs/clabernetes/controllers/topology"
	claberneteslogging "github.com/srl-labs/clabernetes/logging"
	clabernetestesthelper "github.com/srl-labs/clabernetes/testhelper"
	clabernetesutil "github.com/srl-labs/clabernetes/util"
	clabernetesutilcontainerlab "github.com/srl-labs/clabernetes/util/containerlab"
	k8sappsv1 "k8s.io/api/apps/v1"
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
)

const renderDeploymentTestName = "deployment/render-deployment"

func TestResolveDeployment(t *testing.T) {
	cases := []struct {
		name                 string
		ownedDeployments     *k8sappsv1.DeploymentList
		criKind              string
		imagePullThroughMode string
		clabernetesConfigs   map[string]*clabernetesutilcontainerlab.Config
		expectedCurrent      []string
		expectedMissing      []string
		expectedExtra        []*k8sappsv1.Deployment
	}{
		{
			name:               "simple",
			ownedDeployments:   &k8sappsv1.DeploymentList{},
			clabernetesConfigs: nil,
			expectedCurrent:    nil,
			expectedMissing:    nil,
			expectedExtra:      []*k8sappsv1.Deployment{},
		},
		{
			name:             "missing-nodes",
			ownedDeployments: &k8sappsv1.DeploymentList{},
			clabernetesConfigs: map[string]*clabernetesutilcontainerlab.Config{
				"node1": nil,
				"node2": nil,
			},
			expectedCurrent: nil,
			expectedMissing: []string{"node1", "node2"},
			expectedExtra:   []*k8sappsv1.Deployment{},
		},
		{
			name: "extra-nodes",
			ownedDeployments: &k8sappsv1.DeploymentList{
				Items: []k8sappsv1.Deployment{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "resolve-deployment-test",
							Namespace: "clabernetes",
							Labels: map[string]string{
								clabernetesconstants.LabelTopologyNode: "node2",
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
			expectedExtra: []*k8sappsv1.Deployment{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "resolve-deployment-test",
						Namespace: "clabernetes",
						Labels: map[string]string{
							clabernetesconstants.LabelTopologyNode: "node2",
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

				reconciler := clabernetescontrollerstopology.NewDeploymentReconciler(
					&claberneteslogging.FakeInstance{},
					"clabernetes",
					"clabernetes",
					testCase.criKind,
					clabernetesconfig.GetFakeManager,
				)

				got, err := reconciler.Resolve(
					testCase.ownedDeployments,
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
			},
		)
	}
}

func TestRenderDeployment(t *testing.T) {
	cases := []struct {
		name                 string
		owningTopology       *clabernetesapisv1alpha1.Topology
		criKind              string
		imagePullThroughMode string
		clabernetesConfigs   map[string]*clabernetesutilcontainerlab.Config
		nodeName             string
		configManagerGetter  clabernetesconfig.ManagerGetterFunc
	}{
		{
			name: "simple",
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "render-deployment-test",
					Namespace: "clabernetes",
				},
				Spec: clabernetesapisv1alpha1.TopologySpec{
					Connectivity: clabernetesconstants.ConnectivityVXLAN,
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
			clabernetesConfigs: map[string]*clabernetesutilcontainerlab.Config{
				"srl1": {
					Name:   "srl1",
					Prefix: clabernetesutil.ToPointer(""),
					Topology: &clabernetesutilcontainerlab.Topology{
						Defaults: &clabernetesutilcontainerlab.NodeDefinition{
							Ports: []string{
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
							},
						},
						Kinds: nil,
						Nodes: map[string]*clabernetesutilcontainerlab.NodeDefinition{
							"srl1": {
								Kind:  "srl",
								Image: "ghcr.io/nokia/srlinux",
							},
						},
						Links: nil,
					},
					Debug: false,
				},
			},
			nodeName:            "srl1",
			configManagerGetter: clabernetesconfig.GetFakeManager,
		},
		{
			name: "not-privileged-launcher",
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "render-deployment-test",
					Namespace: "clabernetes",
				},
				Spec: clabernetesapisv1alpha1.TopologySpec{
					Connectivity: clabernetesconstants.ConnectivityVXLAN,
					Deployment: clabernetesapisv1alpha1.Deployment{
						PrivilegedLauncher: clabernetesutil.ToPointer(false),
					},
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
			clabernetesConfigs: map[string]*clabernetesutilcontainerlab.Config{
				"srl1": {
					Name:   "srl1",
					Prefix: clabernetesutil.ToPointer(""),
					Topology: &clabernetesutilcontainerlab.Topology{
						Defaults: &clabernetesutilcontainerlab.NodeDefinition{
							Ports: []string{
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
							},
						},
						Kinds: nil,
						Nodes: map[string]*clabernetesutilcontainerlab.NodeDefinition{
							"srl1": {
								Kind:  "srl",
								Image: "ghcr.io/nokia/srlinux",
							},
						},
						Links: nil,
					},
					Debug: false,
				},
			},
			nodeName:            "srl1",
			configManagerGetter: clabernetesconfig.GetFakeManager,
		},
		{
			name: "containerlab-debug",
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "render-deployment-test",
					Namespace: "clabernetes",
				},
				Spec: clabernetesapisv1alpha1.TopologySpec{
					Connectivity: clabernetesconstants.ConnectivityVXLAN,
					Deployment: clabernetesapisv1alpha1.Deployment{
						ContainerlabDebug: clabernetesutil.ToPointer(true),
					},
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
			clabernetesConfigs: map[string]*clabernetesutilcontainerlab.Config{
				"srl1": {
					Name:   "srl1",
					Prefix: clabernetesutil.ToPointer(""),
					Topology: &clabernetesutilcontainerlab.Topology{
						Defaults: &clabernetesutilcontainerlab.NodeDefinition{
							Ports: []string{
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
							},
						},
						Kinds: nil,
						Nodes: map[string]*clabernetesutilcontainerlab.NodeDefinition{
							"srl1": {
								Kind:  "srl",
								Image: "ghcr.io/nokia/srlinux",
							},
						},
						Links: nil,
					},
					Debug: false,
				},
			},
			nodeName:            "srl1",
			configManagerGetter: clabernetesconfig.GetFakeManager,
		},
		{
			name: "launcher-log-level",
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "render-deployment-test",
					Namespace: "clabernetes",
				},
				Spec: clabernetesapisv1alpha1.TopologySpec{
					Connectivity: clabernetesconstants.ConnectivityVXLAN,
					Deployment: clabernetesapisv1alpha1.Deployment{
						LauncherLogLevel: "debug",
					},
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
			clabernetesConfigs: map[string]*clabernetesutilcontainerlab.Config{
				"srl1": {
					Name:   "srl1",
					Prefix: clabernetesutil.ToPointer(""),
					Topology: &clabernetesutilcontainerlab.Topology{
						Defaults: &clabernetesutilcontainerlab.NodeDefinition{
							Ports: []string{
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
							},
						},
						Kinds: nil,
						Nodes: map[string]*clabernetesutilcontainerlab.NodeDefinition{
							"srl1": {
								Kind:  "srl",
								Image: "ghcr.io/nokia/srlinux",
							},
						},
						Links: nil,
					},
					Debug: false,
				},
			},
			nodeName:            "srl1",
			configManagerGetter: clabernetesconfig.GetFakeManager,
		},
		{
			name: "insecure-registries",
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "render-deployment-test",
					Namespace: "clabernetes",
				},
				Spec: clabernetesapisv1alpha1.TopologySpec{
					Connectivity: clabernetesconstants.ConnectivityVXLAN,
					ImagePull: clabernetesapisv1alpha1.ImagePull{
						InsecureRegistries: []string{"1.2.3.4", "potato.com"},
					},
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
			clabernetesConfigs: map[string]*clabernetesutilcontainerlab.Config{
				"srl1": {
					Name:   "srl1",
					Prefix: clabernetesutil.ToPointer(""),
					Topology: &clabernetesutilcontainerlab.Topology{
						Defaults: &clabernetesutilcontainerlab.NodeDefinition{
							Ports: []string{
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
							},
						},
						Kinds: nil,
						Nodes: map[string]*clabernetesutilcontainerlab.NodeDefinition{
							"srl1": {
								Kind:  "srl",
								Image: "ghcr.io/nokia/srlinux",
							},
						},
						Links: nil,
					},
					Debug: false,
				},
			},
			nodeName:            "srl1",
			configManagerGetter: clabernetesconfig.GetFakeManager,
		},
		{
			name: "docker-daemon",
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "render-deployment-test",
					Namespace: "clabernetes",
				},
				Spec: clabernetesapisv1alpha1.TopologySpec{
					Connectivity: clabernetesconstants.ConnectivityVXLAN,
					ImagePull: clabernetesapisv1alpha1.ImagePull{
						DockerDaemonConfig: "sneakydockerdaemonconfig",
					},
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
			clabernetesConfigs: map[string]*clabernetesutilcontainerlab.Config{
				"srl1": {
					Name:   "srl1",
					Prefix: clabernetesutil.ToPointer(""),
					Topology: &clabernetesutilcontainerlab.Topology{
						Defaults: &clabernetesutilcontainerlab.NodeDefinition{
							Ports: []string{
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
							},
						},
						Kinds: nil,
						Nodes: map[string]*clabernetesutilcontainerlab.NodeDefinition{
							"srl1": {
								Kind:  "srl",
								Image: "ghcr.io/nokia/srlinux",
							},
						},
						Links: nil,
					},
					Debug: false,
				},
			},
			nodeName:            "srl1",
			configManagerGetter: clabernetesconfig.GetFakeManager,
		},
		{
			name: "docker-config",
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "render-deployment-test",
					Namespace: "clabernetes",
				},
				Spec: clabernetesapisv1alpha1.TopologySpec{
					Connectivity: clabernetesconstants.ConnectivityVXLAN,
					ImagePull: clabernetesapisv1alpha1.ImagePull{
						DockerConfig: "sneakydockerconfig",
					},
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
			clabernetesConfigs: map[string]*clabernetesutilcontainerlab.Config{
				"srl1": {
					Name:   "srl1",
					Prefix: clabernetesutil.ToPointer(""),
					Topology: &clabernetesutilcontainerlab.Topology{
						Defaults: &clabernetesutilcontainerlab.NodeDefinition{
							Ports: []string{},
						},
						Kinds: nil,
						Nodes: map[string]*clabernetesutilcontainerlab.NodeDefinition{
							"srl1": {
								Kind:  "srl",
								Image: "ghcr.io/nokia/srlinux",
							},
						},
						Links: nil,
					},
					Debug: false,
				},
			},
			nodeName:            "srl1",
			configManagerGetter: clabernetesconfig.GetFakeManager,
		},
		{
			name: "scheduling",
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "render-deployment-test",
					Namespace: "clabernetes",
				},
				Spec: clabernetesapisv1alpha1.TopologySpec{
					Connectivity: clabernetesconstants.ConnectivityVXLAN,
					Deployment: clabernetesapisv1alpha1.Deployment{
						Scheduling: clabernetesapisv1alpha1.Scheduling{
							NodeSelector: map[string]string{
								"somelabel": "somevalue",
							},
							Tolerations: []k8scorev1.Toleration{
								{
									Key:      "sometaintkey",
									Operator: "Equal",
									Value:    "sometaintvalue",
									Effect:   "NoSchedule",
								},
							},
						},
					},
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
			clabernetesConfigs: map[string]*clabernetesutilcontainerlab.Config{
				"srl1": {
					Name:   "srl1",
					Prefix: clabernetesutil.ToPointer(""),
					Topology: &clabernetesutilcontainerlab.Topology{
						Defaults: &clabernetesutilcontainerlab.NodeDefinition{},
						Kinds:    nil,
						Nodes: map[string]*clabernetesutilcontainerlab.NodeDefinition{
							"srl1": {
								Kind:  "srl",
								Image: "ghcr.io/nokia/srlinux",
							},
						},
						Links: nil,
					},
					Debug: false,
				},
			},
			nodeName:            "srl1",
			configManagerGetter: clabernetesconfig.GetFakeManager,
		},
		{
			name: "scheduling-with-affinity",
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "render-deployment-test",
					Namespace: "clabernetes",
				},
				Spec: clabernetesapisv1alpha1.TopologySpec{
					Connectivity: clabernetesconstants.ConnectivityVXLAN,
					Deployment: clabernetesapisv1alpha1.Deployment{
						Scheduling: clabernetesapisv1alpha1.Scheduling{
							NodeSelector: map[string]string{
								"somelabel": "somevalue",
							},
							Tolerations: []k8scorev1.Toleration{
								{
									Key:      "sometaintkey",
									Operator: "Equal",
									Value:    "sometaintvalue",
									Effect:   "NoSchedule",
								},
							},
							Affinity: &k8scorev1.Affinity{
								NodeAffinity: &k8scorev1.NodeAffinity{
									RequiredDuringSchedulingIgnoredDuringExecution: &k8scorev1.NodeSelector{
										NodeSelectorTerms: []k8scorev1.NodeSelectorTerm{
											{
												MatchExpressions: []k8scorev1.NodeSelectorRequirement{
													{
														Key:      "topology.kubernetes.io/zone",
														Operator: k8scorev1.NodeSelectorOpIn,
														Values:   []string{"zone-a", "zone-b"},
													},
												},
											},
										},
									},
								},
								PodAntiAffinity: &k8scorev1.PodAntiAffinity{
									PreferredDuringSchedulingIgnoredDuringExecution: []k8scorev1.WeightedPodAffinityTerm{
										{
											Weight: 100,
											PodAffinityTerm: k8scorev1.PodAffinityTerm{
												LabelSelector: &metav1.LabelSelector{
													MatchLabels: map[string]string{
														"clabernetes/topology": "render-deployment-test",
													},
												},
												TopologyKey: "kubernetes.io/hostname",
											},
										},
									},
								},
							},
						},
					},
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
			clabernetesConfigs: map[string]*clabernetesutilcontainerlab.Config{
				"srl1": {
					Name:   "srl1",
					Prefix: clabernetesutil.ToPointer(""),
					Topology: &clabernetesutilcontainerlab.Topology{
						Defaults: &clabernetesutilcontainerlab.NodeDefinition{},
						Kinds:    nil,
						Nodes: map[string]*clabernetesutilcontainerlab.NodeDefinition{
							"srl1": {
								Kind:  "srl",
								Image: "ghcr.io/nokia/srlinux",
							},
						},
						Links: nil,
					},
					Debug: false,
				},
			},
			nodeName:            "srl1",
			configManagerGetter: clabernetesconfig.GetFakeManager,
		},
		{
			name: "remove-prefix",
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "render-deployment-test",
					Namespace: "clabernetes",
				},
				Spec: clabernetesapisv1alpha1.TopologySpec{
					Connectivity: clabernetesconstants.ConnectivitySlurpeeth,
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
			clabernetesConfigs: map[string]*clabernetesutilcontainerlab.Config{
				"srl1": {
					Name:   "srl1",
					Prefix: clabernetesutil.ToPointer(""),
					Topology: &clabernetesutilcontainerlab.Topology{
						Defaults: &clabernetesutilcontainerlab.NodeDefinition{},
						Kinds:    nil,
						Nodes: map[string]*clabernetesutilcontainerlab.NodeDefinition{
							"srl1": {
								Kind:  "srl",
								Image: "ghcr.io/nokia/srlinux",
							},
						},
						Links: nil,
					},
					Debug: false,
				},
			},
			nodeName:            "srl1",
			configManagerGetter: clabernetesconfig.GetFakeManager,
		},
		{
			name: "custom-clab-version",
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "render-deployment-test",
					Namespace: "clabernetes",
				},
				Spec: clabernetesapisv1alpha1.TopologySpec{
					Deployment: clabernetesapisv1alpha1.Deployment{
						ContainerlabVersion: "0.51.1",
					},
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
			clabernetesConfigs: map[string]*clabernetesutilcontainerlab.Config{
				"srl1": {
					Name:   "srl1",
					Prefix: clabernetesutil.ToPointer(""),
					Topology: &clabernetesutilcontainerlab.Topology{
						Defaults: &clabernetesutilcontainerlab.NodeDefinition{},
						Kinds:    nil,
						Nodes: map[string]*clabernetesutilcontainerlab.NodeDefinition{
							"srl1": {
								Kind:  "srl",
								Image: "ghcr.io/nokia/srlinux",
							},
						},
						Links: nil,
					},
					Debug: false,
				},
			},
			nodeName:            "srl1",
			configManagerGetter: clabernetesconfig.GetFakeManager,
		},
		{
			name: "simple-node-selectors",
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "render-deployment-test-node-selectors",
					Namespace: "clabernetes",
				},
				Spec: clabernetesapisv1alpha1.TopologySpec{
					Connectivity: clabernetesconstants.ConnectivityVXLAN,
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
			clabernetesConfigs: map[string]*clabernetesutilcontainerlab.Config{
				"srl1": {
					Name:   "srl1",
					Prefix: clabernetesutil.ToPointer(""),
					Topology: &clabernetesutilcontainerlab.Topology{
						Defaults: &clabernetesutilcontainerlab.NodeDefinition{
							Ports: []string{
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
							},
						},
						Kinds: nil,
						Nodes: map[string]*clabernetesutilcontainerlab.NodeDefinition{
							"srl1": {
								Kind:  "srl",
								Image: "ghcr.io/nokia/srlinux",
							},
						},
						Links: nil,
					},
					Debug: false,
				},
			},
			nodeName: "srl1",
			configManagerGetter: func() clabernetesconfig.Manager {
				selectors := map[string]map[string]string{
					"internal.io/nokia_sros*": {"node-flavour": "baremetal"},
					"ghcr.io/nokia/srlinux*":  {"node-flavour": "amd64"},
					"default":                 {"node-flavour": "arm64"},
				}

				return clabernetesconfig.NewFakeManager(
					clabernetesconfig.WithNodeSelectors(selectors),
				)
			},
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				reconciler := clabernetescontrollerstopology.NewDeploymentReconciler(
					&claberneteslogging.FakeInstance{},
					"clabernetes",
					"clabernetes",
					testCase.criKind,
					testCase.configManagerGetter,
				)

				got := reconciler.Render(
					testCase.owningTopology,
					testCase.clabernetesConfigs,
					testCase.nodeName,
				)

				if *clabernetestesthelper.Update {
					clabernetestesthelper.WriteTestFixtureJSON(
						t,
						fmt.Sprintf("golden/%s/%s.json", renderDeploymentTestName, testCase.name),
						got,
					)
				}

				var want k8sappsv1.Deployment

				err := json.Unmarshal(
					clabernetestesthelper.ReadTestFixtureFile(
						t,
						fmt.Sprintf("golden/%s/%s.json", renderDeploymentTestName, testCase.name),
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

func TestDeploymentConforms(t *testing.T) {
	cases := []struct {
		name     string
		existing *k8sappsv1.Deployment
		rendered *k8sappsv1.Deployment
		ownerUID apimachinerytypes.UID
		conforms bool
	}{
		{
			name: "simple",
			existing: &k8sappsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{
							UID: apimachinerytypes.UID("clabernetes-testing"),
						},
					},
				},
			},
			rendered: &k8sappsv1.Deployment{},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: true,
		},
		{
			name: "bad-replicas",
			existing: &k8sappsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{
							UID: apimachinerytypes.UID("clabernetes-testing"),
						},
					},
				},
				Spec: k8sappsv1.DeploymentSpec{
					Replicas: clabernetesutil.ToPointer(int32(100)),
				},
			},
			rendered: &k8sappsv1.Deployment{
				Spec: k8sappsv1.DeploymentSpec{
					Replicas: clabernetesutil.ToPointer(int32(1)),
				},
			},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: false,
		},
		{
			name: "bad-selector",
			existing: &k8sappsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{
							UID: apimachinerytypes.UID("clabernetes-testing"),
						},
					},
				},
				Spec: k8sappsv1.DeploymentSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"something": "something",
						},
					},
				},
			},
			rendered: &k8sappsv1.Deployment{
				Spec: k8sappsv1.DeploymentSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"something": "different",
						},
					},
				},
			},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: false,
		},
		{
			name: "bad-containers",
			existing: &k8sappsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{
							UID: apimachinerytypes.UID("clabernetes-testing"),
						},
					},
				},
				Spec: k8sappsv1.DeploymentSpec{},
			},
			rendered: &k8sappsv1.Deployment{
				Spec: k8sappsv1.DeploymentSpec{
					Template: k8scorev1.PodTemplateSpec{
						Spec: k8scorev1.PodSpec{
							Containers: []k8scorev1.Container{
								{
									Name: "some-container",
								},
							},
						},
					},
				},
			},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: false,
		},
		{
			name: "bad-service-account",
			existing: &k8sappsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{
							UID: apimachinerytypes.UID("clabernetes-testing"),
						},
					},
				},
				Spec: k8sappsv1.DeploymentSpec{
					Template: k8scorev1.PodTemplateSpec{
						Spec: k8scorev1.PodSpec{
							ServiceAccountName: "something-else",
						},
					},
				},
			},
			rendered: &k8sappsv1.Deployment{
				Spec: k8sappsv1.DeploymentSpec{
					Template: k8scorev1.PodTemplateSpec{
						Spec: k8scorev1.PodSpec{
							ServiceAccountName: "default",
						},
					},
				},
			},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: false,
		},
		{
			name: "bad-restart-policy",
			existing: &k8sappsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{
							UID: apimachinerytypes.UID("clabernetes-testing"),
						},
					},
				},
				Spec: k8sappsv1.DeploymentSpec{
					Template: k8scorev1.PodTemplateSpec{
						Spec: k8scorev1.PodSpec{
							RestartPolicy: "Never",
						},
					},
				},
			},
			rendered: &k8sappsv1.Deployment{
				Spec: k8sappsv1.DeploymentSpec{
					Template: k8scorev1.PodTemplateSpec{
						Spec: k8scorev1.PodSpec{
							RestartPolicy: "Always",
						},
					},
				},
			},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: false,
		},

		// object meta annotations

		{
			name: "missing-clabernetes-global-annotations",
			existing: &k8sappsv1.Deployment{
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
			rendered: &k8sappsv1.Deployment{
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
			existing: &k8sappsv1.Deployment{
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
			rendered: &k8sappsv1.Deployment{
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
			existing: &k8sappsv1.Deployment{
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
			rendered: &k8sappsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{},
			},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: true,
		},

		// object meta labels

		{
			name: "missing-clabernetes-global-annotations",
			existing: &k8sappsv1.Deployment{
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
			rendered: &k8sappsv1.Deployment{
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
			existing: &k8sappsv1.Deployment{
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
			rendered: &k8sappsv1.Deployment{
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
			existing: &k8sappsv1.Deployment{
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
			rendered: &k8sappsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{},
			},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: true,
		},

		// template object meta annotations

		{
			name: "missing-clabernetes-global-annotations",
			existing: &k8sappsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{
							UID: apimachinerytypes.UID("clabernetes-testing"),
						},
					},
				},
				Spec: k8sappsv1.DeploymentSpec{
					Template: k8scorev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Annotations: map[string]string{
								"somethingelse": "xyz",
							},
						},
					},
				},
			},
			rendered: &k8sappsv1.Deployment{
				Spec: k8sappsv1.DeploymentSpec{
					Template: k8scorev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Annotations: map[string]string{
								"user-provided-global-annotation": "expected-value",
							},
						},
					},
				},
			},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: false,
		},
		{
			name: "clabernetes-global-annotations-wrong-value",
			existing: &k8sappsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{
							UID: apimachinerytypes.UID("clabernetes-testing"),
						},
					},
				},
				Spec: k8sappsv1.DeploymentSpec{
					Template: k8scorev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Annotations: map[string]string{
								"user-provided-global-annotation": "xyz",
							},
						},
					},
				},
			},
			rendered: &k8sappsv1.Deployment{
				Spec: k8sappsv1.DeploymentSpec{
					Template: k8scorev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Annotations: map[string]string{
								"user-provided-global-annotation": "expected-value",
							},
						},
					},
				},
			},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: false,
		},
		{
			name: "extra-annotations-ok",
			existing: &k8sappsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{
							UID: apimachinerytypes.UID("clabernetes-testing"),
						},
					},
				},
				Spec: k8sappsv1.DeploymentSpec{
					Template: k8scorev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Annotations: map[string]string{
								"somethingelseentirely": "thisisok",
							},
						},
					},
				},
			},
			rendered: &k8sappsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{},
			},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: true,
		},

		// template object meta labels

		{
			name: "missing-clabernetes-global-labels",
			existing: &k8sappsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{
							UID: apimachinerytypes.UID("clabernetes-testing"),
						},
					},
				},
				Spec: k8sappsv1.DeploymentSpec{
					Template: k8scorev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{
								"somethingelse": "xyz",
							},
						},
					},
				},
			},
			rendered: &k8sappsv1.Deployment{
				Spec: k8sappsv1.DeploymentSpec{
					Template: k8scorev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{
								"user-provided-global-label": "expected-value",
							},
						},
					},
				},
			},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: false,
		},
		{
			name: "clabernetes-global-labels-wrong-value",
			existing: &k8sappsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{
							UID: apimachinerytypes.UID("clabernetes-testing"),
						},
					},
				},
				Spec: k8sappsv1.DeploymentSpec{
					Template: k8scorev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{
								"user-provided-global-label": "xyz",
							},
						},
					},
				},
			},
			rendered: &k8sappsv1.Deployment{
				Spec: k8sappsv1.DeploymentSpec{
					Template: k8scorev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{
								"user-provided-global-label": "expected-value",
							},
						},
					},
				},
			},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: false,
		},
		{
			name: "extra-labels-ok",
			existing: &k8sappsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{
							UID: apimachinerytypes.UID("clabernetes-testing"),
						},
					},
				},
				Spec: k8sappsv1.DeploymentSpec{
					Template: k8scorev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{
								"somethingelseentirely": "thisisok",
							},
						},
					},
				},
			},
			rendered: &k8sappsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{},
			},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: true,
		},

		{
			name: "bad-owner",
			existing: &k8sappsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{
							UID: apimachinerytypes.UID("evil-imposter"),
						},
					},
				},
			},
			rendered: &k8sappsv1.Deployment{},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: false,
		},
		{
			name: "multiple-owner",
			existing: &k8sappsv1.Deployment{
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
			rendered: &k8sappsv1.Deployment{},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: false,
		},

		// scheduling things
		{
			name: "mismatched-node-selector-simple",
			existing: &k8sappsv1.Deployment{
				Spec: k8sappsv1.DeploymentSpec{
					Template: k8scorev1.PodTemplateSpec{
						Spec: k8scorev1.PodSpec{
							NodeSelector: map[string]string{
								"somenodekey": "somenodevalue",
							},
						},
					},
				},
			},
			rendered: &k8sappsv1.Deployment{},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: false,
		},
		{
			name: "mismatched-node-selector",
			existing: &k8sappsv1.Deployment{
				Spec: k8sappsv1.DeploymentSpec{
					Template: k8scorev1.PodTemplateSpec{
						Spec: k8scorev1.PodSpec{
							NodeSelector: map[string]string{
								"somenodekey": "somenodevalue",
							},
						},
					},
				},
			},
			rendered: &k8sappsv1.Deployment{
				Spec: k8sappsv1.DeploymentSpec{
					Template: k8scorev1.PodTemplateSpec{
						Spec: k8scorev1.PodSpec{
							NodeSelector: map[string]string{
								"somenodekey": "somenodevalue",
								"anotherkey":  "anothervalue",
							},
						},
					},
				},
			},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: false,
		},
		{
			name: "node-selector-ignore-extras",
			existing: &k8sappsv1.Deployment{
				Spec: k8sappsv1.DeploymentSpec{
					Template: k8scorev1.PodTemplateSpec{
						Spec: k8scorev1.PodSpec{
							NodeSelector: map[string]string{
								"somenodekey": "somenodevalue",
								"anotherkey":  "anothervalue",
							},
						},
					},
				},
			},
			rendered: &k8sappsv1.Deployment{
				Spec: k8sappsv1.DeploymentSpec{
					Template: k8scorev1.PodTemplateSpec{
						Spec: k8scorev1.PodSpec{
							NodeSelector: map[string]string{
								"somenodekey": "somenodevalue",
							},
						},
					},
				},
			},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: false,
		},
		{
			name: "mismatched-tolerations-simple",
			existing: &k8sappsv1.Deployment{
				Spec: k8sappsv1.DeploymentSpec{
					Template: k8scorev1.PodTemplateSpec{
						Spec: k8scorev1.PodSpec{
							Tolerations: []k8scorev1.Toleration{
								{
									Key:      "sometoleration",
									Operator: "Equal",
									Value:    "something",
									Effect:   "NoSchedule",
								},
							},
						},
					},
				},
			},
			rendered: &k8sappsv1.Deployment{},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: false,
		},
		{
			name: "mismatched-tolerations",
			existing: &k8sappsv1.Deployment{
				Spec: k8sappsv1.DeploymentSpec{
					Template: k8scorev1.PodTemplateSpec{
						Spec: k8scorev1.PodSpec{
							Tolerations: []k8scorev1.Toleration{
								{
									Key:      "sometoleration",
									Operator: "Equal",
									Value:    "something",
									Effect:   "NoSchedule",
								},
							},
						},
					},
				},
			},
			rendered: &k8sappsv1.Deployment{
				Spec: k8sappsv1.DeploymentSpec{
					Template: k8scorev1.PodTemplateSpec{
						Spec: k8scorev1.PodSpec{
							Tolerations: []k8scorev1.Toleration{
								{
									Key:      "sometoleration",
									Operator: "Equal",
									Value:    "something",
									Effect:   "NoSchedule",
								},
								{
									Key:      "extratoleration",
									Operator: "Equal",
									Value:    "foobar",
									Effect:   "NoSchedule",
								},
							},
						},
					},
				},
			},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: false,
		},
		{
			name:     "mismatched-affinity-existing-has-none",
			existing: &k8sappsv1.Deployment{},
			rendered: &k8sappsv1.Deployment{
				Spec: k8sappsv1.DeploymentSpec{
					Template: k8scorev1.PodTemplateSpec{
						Spec: k8scorev1.PodSpec{
							Affinity: &k8scorev1.Affinity{
								NodeAffinity: &k8scorev1.NodeAffinity{
									RequiredDuringSchedulingIgnoredDuringExecution: &k8scorev1.NodeSelector{
										NodeSelectorTerms: []k8scorev1.NodeSelectorTerm{
											{
												MatchExpressions: []k8scorev1.NodeSelectorRequirement{
													{
														Key:      "topology.kubernetes.io/zone",
														Operator: k8scorev1.NodeSelectorOpIn,
														Values:   []string{"zone-a"},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: false,
		},
		{
			name: "mismatched-affinity-rendered-has-none",
			existing: &k8sappsv1.Deployment{
				Spec: k8sappsv1.DeploymentSpec{
					Template: k8scorev1.PodTemplateSpec{
						Spec: k8scorev1.PodSpec{
							Affinity: &k8scorev1.Affinity{
								NodeAffinity: &k8scorev1.NodeAffinity{
									RequiredDuringSchedulingIgnoredDuringExecution: &k8scorev1.NodeSelector{
										NodeSelectorTerms: []k8scorev1.NodeSelectorTerm{
											{
												MatchExpressions: []k8scorev1.NodeSelectorRequirement{
													{
														Key:      "topology.kubernetes.io/zone",
														Operator: k8scorev1.NodeSelectorOpIn,
														Values:   []string{"zone-a"},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			rendered: &k8sappsv1.Deployment{},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: false,
		},
		{
			name: "mismatched-affinity-different-zones",
			existing: &k8sappsv1.Deployment{
				Spec: k8sappsv1.DeploymentSpec{
					Template: k8scorev1.PodTemplateSpec{
						Spec: k8scorev1.PodSpec{
							Affinity: &k8scorev1.Affinity{
								NodeAffinity: &k8scorev1.NodeAffinity{
									RequiredDuringSchedulingIgnoredDuringExecution: &k8scorev1.NodeSelector{
										NodeSelectorTerms: []k8scorev1.NodeSelectorTerm{
											{
												MatchExpressions: []k8scorev1.NodeSelectorRequirement{
													{
														Key:      "topology.kubernetes.io/zone",
														Operator: k8scorev1.NodeSelectorOpIn,
														Values:   []string{"zone-a"},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			rendered: &k8sappsv1.Deployment{
				Spec: k8sappsv1.DeploymentSpec{
					Template: k8scorev1.PodTemplateSpec{
						Spec: k8scorev1.PodSpec{
							Affinity: &k8scorev1.Affinity{
								NodeAffinity: &k8scorev1.NodeAffinity{
									RequiredDuringSchedulingIgnoredDuringExecution: &k8scorev1.NodeSelector{
										NodeSelectorTerms: []k8scorev1.NodeSelectorTerm{
											{
												MatchExpressions: []k8scorev1.NodeSelectorRequirement{
													{
														Key:      "topology.kubernetes.io/zone",
														Operator: k8scorev1.NodeSelectorOpIn,
														Values:   []string{"zone-b"},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: false,
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				reconciler := clabernetescontrollerstopology.NewDeploymentReconciler(
					&claberneteslogging.FakeInstance{},
					"clabernetes",
					"clabernetes",
					"",
					clabernetesconfig.GetFakeManager,
				)

				actual := reconciler.Conforms(
					testCase.existing,
					testCase.rendered,
					testCase.ownerUID,
				)
				if actual != testCase.conforms {
					clabernetestesthelper.FailOutput(
						t,
						testCase.existing,
						testCase.rendered,
					)
				}
			},
		)
	}
}
