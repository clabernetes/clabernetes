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

const renderServiceExposeTestName = "serviceexpose/render-service"

func TestResolveServiceExpose(t *testing.T) {
	cases := []struct {
		name               string
		ownedServices      *k8scorev1.ServiceList
		clabernetesConfigs map[string]*clabernetesutilcontainerlab.Config
		owningTopology     *clabernetesapisv1alpha1.Topology
		expectedCurrent    []string
		expectedMissing    []string
		expectedExtra      []*k8scorev1.Service
	}{
		{
			name:               "simple",
			ownedServices:      &k8scorev1.ServiceList{},
			clabernetesConfigs: map[string]*clabernetesutilcontainerlab.Config{},
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "render-deployment-test",
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
			expectedCurrent: nil,
			expectedMissing: nil,
			expectedExtra:   []*k8scorev1.Service{},
		},
		{
			name:          "missing-nodes",
			ownedServices: &k8scorev1.ServiceList{},
			clabernetesConfigs: map[string]*clabernetesutilcontainerlab.Config{
				"node1": nil,
				"node2": nil,
			},
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "resolve-servicefabric-test",
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
								clabernetesconstants.LabelTopologyServiceType: clabernetesconstants.TopologyServiceTypeExpose,
								clabernetesconstants.LabelTopologyNode:        "node2",
							},
						},
					},
				},
			},
			clabernetesConfigs: map[string]*clabernetesutilcontainerlab.Config{
				"node1": nil,
			},
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "resolve-servicefabric-test",
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
			expectedCurrent: nil,
			expectedMissing: nil,
			expectedExtra: []*k8scorev1.Service{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "resolve-servicefabric-test",
						Namespace: "clabernetes",
						Labels: map[string]string{
							clabernetesconstants.LabelTopologyServiceType: clabernetesconstants.TopologyServiceTypeExpose,
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

				reconciler := clabernetescontrollerstopology.NewServiceExposeReconciler(
					&claberneteslogging.FakeInstance{},
					clabernetesconfig.GetFakeManager,
				)

				got, err := reconciler.Resolve(
					testCase.ownedServices,
					testCase.clabernetesConfigs,
					testCase.owningTopology,
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

func TestRenderServiceExpose(t *testing.T) {
	cases := []struct {
		name                 string
		owningTopology       *clabernetesapisv1alpha1.Topology
		owningTopologyStatus *clabernetesapisv1alpha1.TopologyStatus
		clabernetesConfigs   map[string]*clabernetesutilcontainerlab.Config
		nodeName             string
	}{
		{
			name: "simple",
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "render-service-expose-test",
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
			owningTopologyStatus: &clabernetesapisv1alpha1.TopologyStatus{
				ExposedPorts: map[string]*clabernetesapisv1alpha1.ExposedPorts{},
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
		},
		{
			name: "simple-no-prefix",
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "render-service-expose-test",
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
			owningTopologyStatus: &clabernetesapisv1alpha1.TopologyStatus{
				ExposedPorts: map[string]*clabernetesapisv1alpha1.ExposedPorts{},
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
		},
		{
			name: "simple-cluster-ip-expose-type",
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "render-service-expose-test",
					Namespace: "clabernetes",
				},
				Spec: clabernetesapisv1alpha1.TopologySpec{
					Expose: clabernetesapisv1alpha1.Expose{
						ExposeType: string(k8scorev1.ServiceTypeClusterIP),
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
			owningTopologyStatus: &clabernetesapisv1alpha1.TopologyStatus{
				ExposedPorts: map[string]*clabernetesapisv1alpha1.ExposedPorts{},
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
		},
		{
			name: "use-mgmt-ip-as-loadbalancer-ip-both-ipv4-and-ipv6",
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "render-service-expose-test",
					Namespace: "clabernetes",
				},
				Spec: clabernetesapisv1alpha1.TopologySpec{
					Expose: clabernetesapisv1alpha1.Expose{
						UseNodeMgmtIpv4Address: true,
						UseNodeMgmtIpv6Address: true,
						ExposeType:             string(k8scorev1.ServiceTypeLoadBalancer),
					},
					Definition: clabernetesapisv1alpha1.Definition{
						Containerlab: `---
    name: test
    topology:
      nodes:
        node1:
          kind: srl
          image: ghcr.io/nokia/srlinux
		  mgmt-ipv4: 10.1.2.3
		  mgmt-ipv6: 2001:db8:85a3::8a2e:370:7334
`,
					},
				},
			},
			owningTopologyStatus: &clabernetesapisv1alpha1.TopologyStatus{
				ExposedPorts: map[string]*clabernetesapisv1alpha1.ExposedPorts{},
			},
			clabernetesConfigs: map[string]*clabernetesutilcontainerlab.Config{
				"node1": {
					Name:   "node1",
					Prefix: clabernetesutil.ToPointer(""),
					Topology: &clabernetesutilcontainerlab.Topology{
						Defaults: &clabernetesutilcontainerlab.NodeDefinition{Ports: []string{}},
						Nodes: map[string]*clabernetesutilcontainerlab.NodeDefinition{
							"node1": {
								Kind:     "srl",
								Image:    "ghcr.io/nokia/srlinux",
								MgmtIPv4: "10.1.2.3",
								MgmtIPv6: "2001:db8:85a3::8a2e:370:7334",
							},
						},
					},
					Debug: false,
				},
			},
			nodeName: "node1",
		},
		{
			name: "use-mgmt-ip-as-loadbalancer-ipv6",
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "render-service-expose-test",
					Namespace: "clabernetes",
				},
				Spec: clabernetesapisv1alpha1.TopologySpec{
					Expose: clabernetesapisv1alpha1.Expose{
						UseNodeMgmtIpv6Address: true,
						ExposeType:             string(k8scorev1.ServiceTypeLoadBalancer),
					},
					Definition: clabernetesapisv1alpha1.Definition{
						Containerlab: `---
    name: test
    topology:
      nodes:
        node1:
          kind: srl
          image: ghcr.io/nokia/srlinux
		  mgmt-ipv6: 2001:db8:85a3::8a2e:370:7334
`,
					},
				},
			},
			owningTopologyStatus: &clabernetesapisv1alpha1.TopologyStatus{
				ExposedPorts: map[string]*clabernetesapisv1alpha1.ExposedPorts{},
			},
			clabernetesConfigs: map[string]*clabernetesutilcontainerlab.Config{
				"node1": {
					Name:   "node1",
					Prefix: clabernetesutil.ToPointer(""),
					Topology: &clabernetesutilcontainerlab.Topology{
						Defaults: &clabernetesutilcontainerlab.NodeDefinition{Ports: []string{}},
						Nodes: map[string]*clabernetesutilcontainerlab.NodeDefinition{
							"node1": {
								Kind:     "srl",
								Image:    "ghcr.io/nokia/srlinux",
								MgmtIPv6: "2001:db8:85a3::8a2e:370:7334",
							},
						},
					},
					Debug: false,
				},
			},
			nodeName: "node1",
		},
		{
			name: "headless-expose-type",
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "render-service-expose-test",
					Namespace: "clabernetes",
				},
				Spec: clabernetesapisv1alpha1.TopologySpec{
					Expose: clabernetesapisv1alpha1.Expose{
						ExposeType: "Headless",
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
					RemoveTopologyPrefix: clabernetesutil.ToPointer(true),
				},
			},
			owningTopologyStatus: &clabernetesapisv1alpha1.TopologyStatus{
				ExposedPorts: map[string]*clabernetesapisv1alpha1.ExposedPorts{},
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
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				reconciler := clabernetescontrollerstopology.NewServiceExposeReconciler(
					&claberneteslogging.FakeInstance{},
					clabernetesconfig.GetFakeManager,
				)

				reconcileData, err := clabernetescontrollerstopology.NewReconcileData(
					testCase.owningTopology,
				)
				if err != nil {
					t.Fatalf("error creating ReconcileData, err: %s", err)
				}

				reconcileData.ResolvedConfigs = testCase.clabernetesConfigs

				got := reconciler.Render(
					testCase.owningTopology,
					reconcileData,
					testCase.nodeName,
				)

				if *clabernetestesthelper.Update {
					clabernetestesthelper.WriteTestFixtureJSON(
						t,
						fmt.Sprintf(
							"golden/%s/%s.json",
							renderServiceExposeTestName,
							testCase.name,
						),
						got,
					)

					clabernetestesthelper.WriteTestFixtureJSON(
						t,
						fmt.Sprintf(
							"golden/%s/%s-status.json",
							renderServiceExposeTestName,
							testCase.name,
						),
						reconcileData.ResolvedExposedPorts,
					)
				}

				var want k8scorev1.Service

				err = json.Unmarshal(
					clabernetestesthelper.ReadTestFixtureFile(
						t,
						fmt.Sprintf(
							"golden/%s/%s.json",
							renderServiceExposeTestName,
							testCase.name,
						),
					),
					&want,
				)
				if err != nil {
					t.Fatal(err)
				}

				var wantExposePortsStatus map[string]*clabernetesapisv1alpha1.ExposedPorts

				err = json.Unmarshal(
					clabernetestesthelper.ReadTestFixtureFile(
						t,
						fmt.Sprintf(
							"golden/%s/%s-status.json",
							renderServiceExposeTestName,
							testCase.name,
						),
					),
					&wantExposePortsStatus,
				)
				if err != nil {
					t.Fatal(err)
				}

				clabernetestesthelper.MarshaledEqual(t, got, want)
			})
	}
}
