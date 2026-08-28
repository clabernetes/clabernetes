package compiler_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetescompiler "github.com/clabernetes/clabernetes/compiler"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	k8scorev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

const uppercaseNamesDefinition = `
name: name-test
topology:
  defaults:
    kind: linux
    image: alpine:3
  nodes:
    R1: {}
    Client_2: {}
    R1-console:
      network-mode: container:R1
  links:
    - endpoints: ["R1:eth1", "Client_2:eth1"]
    - endpoints: ["Client_2:eth2", "host:ens5"]
`

func TestCompileSanitizesNodeNamesKubernetesCannotCarry(t *testing.T) {
	compiled, err := compileDefinition(t, uppercaseNamesDefinition)
	if err != nil {
		t.Fatal(err)
	}

	nodeNames := make([]string, 0, len(compiled.Nodes))
	for nodeName := range compiled.Nodes {
		nodeNames = append(nodeNames, nodeName)
	}

	slices.Sort(nodeNames)

	if !reflect.DeepEqual(nodeNames, []string{"client-2", "r1", "r1-console"}) {
		t.Fatalf("compiled node names = %v, want the sanitized names", nodeNames)
	}

	for compiledName, sourceName := range map[string]string{
		"r1":         "R1",
		"client-2":   "Client_2",
		"r1-console": "R1-console",
	} {
		if got := compiled.SourceNodeName(compiledName); got != sourceName {
			t.Fatalf("SourceNodeName(%q) = %q, want %q", compiledName, got, sourceName)
		}
	}

	if got := compiled.Nodes["r1-console"].NetworkMode; got != "container:r1" {
		t.Fatalf("r1-console network-mode = %q, want container:r1", got)
	}

	expectedLinks := []clabernetescompiler.CompiledLink{
		{
			EndpointA: clabernetesapisv1alpha1.LinkEndpointSpec{
				NodeName:      "r1",
				InterfaceName: "eth1",
			},
			EndpointB: clabernetesapisv1alpha1.LinkEndpointSpec{
				NodeName:      "client-2",
				InterfaceName: "eth1",
			},
		},
		{
			EndpointA: clabernetesapisv1alpha1.LinkEndpointSpec{
				NodeName:      "client-2",
				InterfaceName: "eth2",
			},
			EndpointB: clabernetesapisv1alpha1.LinkEndpointSpec{
				NodeName:      "host",
				InterfaceName: "ens5",
			},
		},
	}

	if !reflect.DeepEqual(compiled.Links, expectedLinks) {
		t.Fatalf("compiled links = %+v, want %+v", compiled.Links, expectedLinks)
	}
}

func TestCompileRejectsNodeNamesCollidingOnceSanitized(t *testing.T) {
	_, err := compileDefinition(t, `
name: colliding
topology:
  nodes:
    R1:
      kind: linux
      image: alpine:3
    r1:
      kind: linux
      image: alpine:3
`)
	if err == nil || !strings.Contains(err.Error(), "both map onto") {
		t.Fatalf("compile error = %v, want a colliding node name error", err)
	}
}

// TestRenderFollowsSanitizedNodeNames pins the translation of the node-keyed policy a Topology
// author writes: it names the nodes the definition names, while the objects carry the sanitized
// names.
func TestRenderFollowsSanitizedNodeNames(t *testing.T) {
	topology := &clabernetesapisv1alpha1.Topology{}
	topology.Name = "name-test"
	topology.Namespace = "clabernetes"
	topology.Spec.Definition.Containerlab = uppercaseNamesDefinition
	topology.Spec.Deployment.FilesFromConfigMap = map[string][]clabernetesapisv1alpha1.FileFromConfigMap{
		"R1": {{
			FilePath:      "startup.cfg",
			ConfigMapName: "name-test-r1-startup-config",
			ConfigMapPath: "startup-config",
		}},
	}
	topology.Spec.Deployment.Resources = map[string]k8scorev1.ResourceRequirements{
		"R1": {Requests: k8scorev1.ResourceList{"memory": resource.MustParse("512Mi")}},
	}
	topology.Spec.StatusProbes = clabernetesapisv1alpha1.StatusProbes{
		Enabled:       true,
		ExcludedNodes: []string{"Client_2"},
		NodeProbeConfigurations: map[string]clabernetesapisv1alpha1.ProbeConfiguration{
			"R1": {StartupSeconds: 60},
		},
	}

	compiled, err := clabernetescompiler.CompileTopology(
		&claberneteslogging.FakeInstance{},
		topology,
	)
	if err != nil {
		t.Fatal(err)
	}

	nodes := clabernetescompiler.RenderNodes(topology, compiled, clabernetesconfig.GetFakeManager)

	var rendered *clabernetesapisv1alpha1.Node

	for _, node := range nodes {
		if node.GetName() == "r1" {
			rendered = node
		}
	}

	if rendered == nil {
		t.Fatal("no r1 node was rendered")
	}

	if len(rendered.Spec.FilesFromConfigMap) != 1 ||
		rendered.Spec.FilesFromConfigMap[0].ConfigMapName != "name-test-r1-startup-config" {
		t.Fatalf("r1 files = %+v, want the payload keyed by R1", rendered.Spec.FilesFromConfigMap)
	}

	if rendered.Spec.ProfileRef == nil || rendered.Spec.ProfileRef.Name != "name-test-r1" {
		t.Fatalf("r1 profile ref = %+v, want the dedicated profile", rendered.Spec.ProfileRef)
	}

	assertSanitizedProfiles(t, clabernetescompiler.RenderNodeProfiles(
		topology,
		compiled,
		clabernetesconfig.GetFakeManager,
	))
}

// assertSanitizedProfiles checks the policy the profiles carry for the renamed nodes.
func assertSanitizedProfiles(t *testing.T, profiles []*clabernetesapisv1alpha1.NodeProfile) {
	t.Helper()

	for _, profile := range profiles {
		if profile.GetName() == "name-test-r1" {
			if profile.Spec.Resources == nil ||
				profile.Spec.Resources.Requests.Memory().String() != "512Mi" {
				t.Fatalf("r1 profile resources = %+v, want the policy keyed by R1",
					profile.Spec.Resources)
			}
		}

		if profile.Spec.StatusProbes == nil {
			t.Fatalf("profile %q has no status probes", profile.GetName())
		}

		if !slices.Contains(profile.Spec.StatusProbes.ExcludedNodes, "client-2") {
			t.Fatalf("profile %q excluded nodes = %v, want the sanitized node name",
				profile.GetName(), profile.Spec.StatusProbes.ExcludedNodes)
		}

		if _, ok := profile.Spec.StatusProbes.NodeProbeConfigurations["r1"]; !ok {
			t.Fatalf("profile %q node probe configurations = %v, want them keyed by r1",
				profile.GetName(), profile.Spec.StatusProbes.NodeProbeConfigurations)
		}
	}
}
