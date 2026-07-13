package topology_test

import (
	"reflect"
	"testing"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/srl-labs/clabernetes/config"
	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	clabernetescontrollerstopology "github.com/srl-labs/clabernetes/controllers/topology"
	claberneteslogging "github.com/srl-labs/clabernetes/logging"
	clabernetesutil "github.com/srl-labs/clabernetes/util"
	k8scorev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func renderTestTopology(t *testing.T) (
	*clabernetesapisv1alpha1.Topology,
	*clabernetescontrollerstopology.CompiledTopology,
) {
	t.Helper()

	topology := &clabernetesapisv1alpha1.Topology{}
	topology.Name = "render-test"
	topology.Namespace = "clabernetes"
	topology.Spec.Definition.Containerlab = flattenTestDefinition
	topology.Spec.Connectivity = "vxlan"
	topology.Spec.Expose.DisableAutoExpose = true
	topology.Spec.Deployment.FilesFromURL = map[string][]clabernetesapisv1alpha1.FileFromURL{
		"srl1": {{FilePath: "some-config", URL: "http://example.com/config"}},
	}
	topology.Spec.Deployment.Resources = map[string]k8scorev1.ResourceRequirements{
		clabernetesconstants.Default: {
			Requests: k8scorev1.ResourceList{
				"memory": resource.MustParse("2Gi"),
			},
		},
		"multitool": {
			Requests: k8scorev1.ResourceList{
				"memory": resource.MustParse("128Mi"),
			},
		},
	}
	topology.Spec.Deployment.PrivilegedLauncher = clabernetesutil.ToPointer(true)

	compiled, err := clabernetescontrollerstopology.CompileTopology(
		&claberneteslogging.FakeInstance{},
		topology,
	)
	if err != nil {
		t.Fatalf("unexpected error compiling topology: %s", err)
	}

	return topology, compiled
}

func TestRenderNodes(t *testing.T) {
	topology, compiled := renderTestTopology(t)

	nodes := clabernetescontrollerstopology.RenderNodes(
		topology,
		compiled,
		clabernetesconfig.GetFakeManager,
	)

	if len(nodes) != 2 {
		t.Fatalf("expected 2 rendered nodes, got %d", len(nodes))
	}

	// sorted by name -- multitool then srl1
	if nodes[0].GetName() != "multitool" || nodes[1].GetName() != "srl1" {
		t.Fatalf("expected nodes sorted by name, got %q/%q", nodes[0].GetName(), nodes[1].GetName())
	}

	srl1 := nodes[1]

	if srl1.Labels[clabernetesconstants.LabelTopologyOwner] != "render-test" ||
		srl1.Labels[clabernetesconstants.LabelTopologyNode] != "srl1" {
		t.Fatalf("expected owner/node labels on rendered node, got %v", srl1.Labels)
	}

	if srl1.Spec.Image != "ghcr.io/nokia/srlinux:latest" {
		t.Fatalf("expected flattened image on rendered node, got %q", srl1.Spec.Image)
	}

	if len(srl1.Spec.FilesFromURL) != 1 ||
		srl1.Spec.FilesFromURL[0].URL != "http://example.com/config" {
		t.Fatalf("expected files from url on rendered node, got %+v", srl1.Spec.FilesFromURL)
	}
}

func TestRenderLinks(t *testing.T) {
	topology, compiled := renderTestTopology(t)

	links := clabernetescontrollerstopology.RenderLinks(
		topology,
		compiled,
		clabernetesconfig.GetFakeManager,
	)

	if len(links) != 2 {
		t.Fatalf("expected 2 rendered links, got %d", len(links))
	}

	expectedNames := []string{"srl1-e1-1-multitool-eth1", "srl1-e1-2-host-ens5"}

	actualNames := []string{links[0].GetName(), links[1].GetName()}
	if !reflect.DeepEqual(actualNames, expectedNames) {
		t.Fatalf("expected link names %v, got %v", expectedNames, actualNames)
	}

	if links[0].Spec.MTU != 9212 {
		t.Fatalf("expected link mtu to be rendered, got %d", links[0].Spec.MTU)
	}

	if links[0].Status.TunnelID != 0 {
		t.Fatal("the compiler must never allocate tunnel ids -- that is the link controller's job")
	}
}

func TestRenderNodeProfiles(t *testing.T) {
	topology, compiled := renderTestTopology(t)

	profiles := clabernetescontrollerstopology.RenderNodeProfiles(
		topology,
		compiled,
		clabernetesconfig.GetFakeManager,
	)

	if len(profiles) != 2 {
		t.Fatalf("expected topology wide + one per node profile, got %d", len(profiles))
	}

	main := profiles[0]

	if main.GetName() != "render-test" {
		t.Fatalf("expected topology wide profile named after topology, got %q", main.GetName())
	}

	expectedSelector := map[string]string{
		clabernetesconstants.LabelTopologyOwner: "render-test",
	}
	if !reflect.DeepEqual(main.Spec.NodeSelector.MatchLabels, expectedSelector) {
		t.Fatalf(
			"expected topology profile to select the owner label, got %v",
			main.Spec.NodeSelector.MatchLabels,
		)
	}

	if main.Spec.Expose == nil || main.Spec.Expose.DisableAutoExpose == nil ||
		!*main.Spec.Expose.DisableAutoExpose {
		t.Fatalf("expected disableAutoExpose compiled into profile, got %+v", main.Spec.Expose)
	}

	if main.Spec.Resources == nil ||
		!main.Spec.Resources.Requests.Memory().Equal(resource.MustParse("2Gi")) {
		t.Fatalf("expected default resources compiled into profile, got %+v", main.Spec.Resources)
	}

	if main.Spec.Deployment == nil || main.Spec.Deployment.PrivilegedLauncher == nil ||
		!*main.Spec.Deployment.PrivilegedLauncher {
		t.Fatalf(
			"expected privileged launcher compiled into profile, got %+v",
			main.Spec.Deployment,
		)
	}

	if main.Spec.Mgmt == nil || main.Spec.Mgmt.IPv4Subnet != "172.20.20.0/24" {
		t.Fatalf("expected mgmt settings compiled into profile, got %+v", main.Spec.Mgmt)
	}

	assertPerNodeProfile(t, profiles[1])
}

func assertPerNodeProfile(t *testing.T, perNode *clabernetesapisv1alpha1.NodeProfile) {
	t.Helper()

	if perNode.GetName() != "render-test-multitool" {
		t.Fatalf("expected per node profile name, got %q", perNode.GetName())
	}

	if perNode.Spec.Priority != 1 {
		t.Fatalf("expected per node profile priority 1, got %d", perNode.Spec.Priority)
	}

	expectedPerNodeSelector := map[string]string{
		clabernetesconstants.LabelTopologyOwner: "render-test",
		clabernetesconstants.LabelTopologyNode:  "multitool",
	}
	if !reflect.DeepEqual(perNode.Spec.NodeSelector.MatchLabels, expectedPerNodeSelector) {
		t.Fatalf(
			"expected per node profile selector, got %v",
			perNode.Spec.NodeSelector.MatchLabels,
		)
	}

	if perNode.Spec.Resources == nil ||
		!perNode.Spec.Resources.Requests.Memory().Equal(resource.MustParse("128Mi")) {
		t.Fatalf(
			"expected per node resources compiled into profile, got %+v",
			perNode.Spec.Resources,
		)
	}
}
