package topology_test

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetescontrollerstopology "github.com/clabernetes/clabernetes/controllers/topology"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
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
	topology.Spec.Deployment.FilesFromConfigMap = map[string][]clabernetesapisv1alpha1.FileFromConfigMap{
		"srl1": {{
			FilePath:      "startup.cfg",
			ConfigMapName: "srl1-config",
			ConfigMapPath: "startup.cfg",
		}},
	}
	topology.Spec.Deployment.FilesFromSecret = map[string][]clabernetesapisv1alpha1.FileFromSecret{
		"srl1": {{
			FilePath: "/etc/device/license.key", SecretName: "srl1-license",
			SecretPath: "license.key",
		}},
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
		// Equal to the shared policy: this must not create a redundant dedicated profile.
		"srl1": {
			Requests: k8scorev1.ResourceList{
				"memory": resource.MustParse("2Gi"),
			},
		},
		// Not a compiled Node: stale/invalid map keys must not create orphan profiles.
		"not-a-node": {
			Requests: k8scorev1.ResourceList{
				"memory": resource.MustParse("1Gi"),
			},
		},
	}
	topology.Spec.ImagePull.Policy = string(k8scorev1.PullAlways)

	compiled, err := clabernetescontrollerstopology.CompileTopology(
		&claberneteslogging.FakeInstance{},
		topology,
	)
	if err != nil {
		t.Fatalf("unexpected error compiling topology: %s", err)
	}

	return topology, compiled
}

//nolint:gocyclo,wsl_v5 // This contract test checks every field on the rendered Node pair.
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

	if len(srl1.Spec.FilesFromConfigMap) != 1 ||
		srl1.Spec.FilesFromConfigMap[0].ConfigMapName != "srl1-config" {
		t.Fatalf(
			"expected ConfigMap payload on rendered node, got %+v",
			srl1.Spec.FilesFromConfigMap,
		)
	}
	if len(srl1.Spec.FilesFromSecret) != 1 ||
		srl1.Spec.FilesFromSecret[0].SecretName != "srl1-license" {
		t.Fatalf("expected Secret payload on rendered node, got %+v", srl1.Spec.FilesFromSecret)
	}

	if srl1.Spec.LauncherProfileRef == nil ||
		srl1.Spec.LauncherProfileRef.Name != "render-test" {
		t.Fatalf("expected shared LauncherProfile reference, got %+v", srl1.Spec.LauncherProfileRef)
	}

	if nodes[0].Spec.LauncherProfileRef == nil ||
		nodes[0].Spec.LauncherProfileRef.Name != "render-test-multitool" {
		t.Fatalf(
			"expected dedicated LauncherProfile reference, got %+v",
			nodes[0].Spec.LauncherProfileRef,
		)
	}
}

//nolint:gocyclo,wsl_v5 // One end-to-end render scenario pins the complete primitive contract.
func TestRenderPlanRelevantTopologyPrimitives(t *testing.T) {
	t.Parallel()

	topology := &clabernetesapisv1alpha1.Topology{}
	topology.Name = "primitive-contract"
	topology.Namespace = "clabernetes"
	topology.Spec.Connectivity = string(clabernetesapisv1alpha1.LinkConnectivitySlurpeeth)
	topology.Spec.ImagePull.PullSecrets = []string{"device-pull"}
	topology.Spec.Definition.Containerlab = `
name: primitive-contract
mgmt:
  ipv4-subnet: 192.0.2.0/24
  ipv4-gw: 192.0.2.1
  ipv4-range: 192.0.2.10-192.0.2.200
  ipv6-subnet: 2001:db8::/64
  ipv6-gw: 2001:db8::1
  ipv6-range: 2001:db8::10-2001:db8::ff
topology:
  defaults:
    kind: package-owned-kind
    labels: { tier: inherited }
    ports: [830/tcp]
  kinds:
    package-owned-kind:
      image: example/device:1
      components:
        - { slot: inherited, type: line-card }
  nodes:
    primary:
      mgmt-ipv4: 192.0.2.10/24
    secondary:
      network-mode: container:primary
      components: []
  links:
    - endpoints: [primary:eth1, secondary:eth1]
      mtu: 9000
`
	topology.Spec.Deployment.FilesFromConfigMap = map[string][]clabernetesapisv1alpha1.FileFromConfigMap{
		"primary": {{FilePath: "/etc/device/startup.cfg", ConfigMapName: "startup"}},
	}
	topology.Spec.Deployment.FilesFromSecret = map[string][]clabernetesapisv1alpha1.FileFromSecret{
		"primary": {{FilePath: "/etc/device/license", SecretName: "license"}},
	}
	topology.Spec.Deployment.FilesFromURL = map[string][]clabernetesapisv1alpha1.FileFromURL{
		"primary": {{FilePath: "/etc/device/blob", URL: "https://example.test/blob"}},
	}

	compiled, err := clabernetescontrollerstopology.CompileTopology(
		&claberneteslogging.FakeInstance{},
		topology,
	)
	if err != nil {
		t.Fatal(err)
	}
	nodes := clabernetescontrollerstopology.RenderNodes(
		topology,
		compiled,
		clabernetesconfig.GetFakeManager,
	)
	links := clabernetescontrollerstopology.RenderLinks(
		topology,
		compiled,
		clabernetesconfig.GetFakeManager,
	)
	profiles := clabernetescontrollerstopology.RenderLauncherProfiles(
		topology,
		compiled,
		clabernetesconfig.GetFakeManager,
	)

	if len(nodes) != 2 || len(links) != 1 || len(profiles) != 1 {
		t.Fatalf(
			"rendered primitives: nodes=%d links=%d profiles=%d",
			len(nodes),
			len(links),
			len(profiles),
		)
	}
	primary, secondary := nodes[0], nodes[1]
	if primary.GetName() != "primary" || primary.Spec.Kind != "package-owned-kind" ||
		primary.Spec.Image != "example/device:1" || primary.Labels["tier"] != "inherited" ||
		!reflect.DeepEqual(primary.Spec.Ports, []string{"830/tcp"}) ||
		len(primary.Spec.Components) != 1 || primary.Spec.MgmtIPv4 != "192.0.2.10/24" {
		t.Fatalf("rendered primary Node = %#v", primary)
	}
	if len(primary.Spec.FilesFromConfigMap) != 1 || len(primary.Spec.FilesFromSecret) != 1 ||
		len(primary.Spec.FilesFromURL) != 1 {
		t.Fatalf("rendered primary payloads = %#v", primary.Spec)
	}
	if secondary.GetName() != "secondary" ||
		secondary.Spec.NetworkMode != "container:primary" || secondary.Spec.Components == nil ||
		len(secondary.Spec.Components) != 0 {
		t.Fatalf("rendered secondary Node = %#v", secondary)
	}
	if links[0].Spec.Connectivity != clabernetesapisv1alpha1.LinkConnectivitySlurpeeth ||
		links[0].Spec.MTU != 9000 || links[0].Spec.EndpointA.NodeName != "primary" ||
		links[0].Spec.EndpointB.NodeName != "secondary" {
		t.Fatalf("rendered Link = %#v", links[0])
	}
	management := profiles[0].Spec.Mgmt
	if management == nil || management.IPv4Subnet != "192.0.2.0/24" ||
		management.IPv4Gw != "192.0.2.1" ||
		management.IPv4Range != "192.0.2.10-192.0.2.200" ||
		management.IPv6Subnet != "2001:db8::/64" || management.IPv6Gw != "2001:db8::1" ||
		management.IPv6Range != "2001:db8::10-2001:db8::ff" {
		t.Fatalf("rendered management policy = %#v", management)
	}
	if profiles[0].Spec.ImagePull == nil ||
		!reflect.DeepEqual(profiles[0].Spec.ImagePull.PullSecrets, []string{"device-pull"}) {
		t.Fatalf("rendered image-pull policy = %#v", profiles[0].Spec.ImagePull)
	}

	// Emitted primitives are snapshots, not aliases into the mutable source Topology object.
	topology.Spec.Deployment.FilesFromConfigMap["primary"][0].FilePath = "changed"
	topology.Spec.ImagePull.PullSecrets[0] = "changed"
	if primary.Spec.FilesFromConfigMap[0].FilePath != "/etc/device/startup.cfg" ||
		profiles[0].Spec.ImagePull.PullSecrets[0] != "device-pull" {
		t.Fatalf("rendered primitive changed with source Topology mutation")
	}
}

func TestTopologyExposePortsReachExposedPorts(t *testing.T) {
	topology := &clabernetesapisv1alpha1.Topology{}
	topology.Name = "expose-ports-test"
	topology.Namespace = "clabernetes"
	topology.Spec.Definition.Containerlab = `
name: expose-ports-test
topology:
  nodes:
    gnmic:
      kind: linux
      image: ghcr.io/openconfig/gnmic:latest
      labels:
        c9s.run/exposePorts: "9273/tcp"
`

	compiled, err := clabernetescontrollerstopology.CompileTopology(
		&claberneteslogging.FakeInstance{},
		topology,
	)
	if err != nil {
		t.Fatalf("unexpected error compiling topology: %s", err)
	}

	nodes := clabernetescontrollerstopology.RenderNodes(
		topology,
		compiled,
		clabernetesconfig.GetFakeManager,
	)
	if len(nodes) != 1 {
		t.Fatalf("expected one rendered node, got %d", len(nodes))
	}

	// The direct runtime resolves exposure from the rendered Node spec, so the directive must
	// land there.
	for _, port := range nodes[0].Spec.Ports {
		if strings.HasPrefix(port, "9273/") {
			return
		}
	}

	t.Fatalf("expected a 9273 entry in rendered Node ports, got %+v", nodes[0].Spec.Ports)
}

// TestRenderNodesCarriesContainerlabLabels pins where containerlab node labels end up: the Node's
// metadata, which is where kubernetes labels belong and what carries them on to the launcher
// deployment and its pods. There is deliberately no spec.labels for them to live in.
func TestRenderNodesCarriesContainerlabLabels(t *testing.T) {
	topology, compiled := renderTestTopology(t)

	nodes := clabernetescontrollerstopology.RenderNodes(
		topology,
		compiled,
		clabernetesconfig.GetFakeManager,
	)

	for _, node := range nodes {
		if node.Labels["tier"] != "lab" {
			t.Errorf(
				"expected the topology defaults label on node %q, got %v",
				node.GetName(),
				node.Labels,
			)
		}

		// c9s' own labels must still be intact alongside them
		if node.Labels[clabernetesconstants.LabelTopologyOwner] != "render-test" {
			t.Errorf("expected owner label on node %q, got %v", node.GetName(), node.Labels)
		}
	}

	srl1 := nodes[1]

	if srl1.Labels["owner"] != "roman" || srl1.Labels["vendor"] != "nokia" {
		t.Errorf("expected node and kind level labels on srl1, got %v", srl1.Labels)
	}

	if !slices.Contains(srl1.Spec.Ports, "9273/tcp") {
		t.Errorf("expected exposePorts directive in rendered Node ports, got %v", srl1.Spec.Ports)
	}

	if _, exists := srl1.Labels[clabernetesconstants.LabelExposePorts]; exists {
		t.Errorf("exposePorts directive leaked into rendered Node labels: %v", srl1.Labels)
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

	for _, link := range links {
		if link.Spec.Connectivity != clabernetesapisv1alpha1.LinkConnectivityVXLAN {
			t.Fatalf(
				"expected topology connectivity on Link %q, got %q",
				link.GetName(),
				link.Spec.Connectivity,
			)
		}
	}

	if links[0].Status.TunnelID != 0 {
		t.Fatal("the compiler must never allocate tunnel ids -- that is the link controller's job")
	}

	topology.Spec.Connectivity = ""
	defaultedLinks := clabernetescontrollerstopology.RenderLinks(
		topology,
		compiled,
		clabernetesconfig.GetFakeManager,
	)

	for _, link := range defaultedLinks {
		if link.Spec.Connectivity != clabernetesapisv1alpha1.LinkConnectivityVXLAN {
			t.Fatalf(
				"expected omitted topology connectivity to compile to vxlan, got %+v",
				link.Spec,
			)
		}
	}
}

func TestRenderLauncherProfiles(t *testing.T) {
	topology, compiled := renderTestTopology(t)

	profiles := clabernetescontrollerstopology.RenderLauncherProfiles(
		topology,
		compiled,
		clabernetesconfig.GetFakeManager,
	)

	if len(profiles) != 2 {
		t.Fatalf("expected shared + one dedicated LauncherProfile, got %d", len(profiles))
	}

	main := profiles[0]

	if main.GetName() != "render-test" {
		t.Fatalf("expected topology wide profile named after topology, got %q", main.GetName())
	}

	if main.Spec.Expose == nil || main.Spec.Expose.DisableAutoExpose == nil ||
		!*main.Spec.Expose.DisableAutoExpose {
		t.Fatalf("expected disableAutoExpose compiled into profile, got %+v", main.Spec.Expose)
	}

	if main.Spec.Resources == nil ||
		!main.Spec.Resources.Requests.Memory().Equal(resource.MustParse("2Gi")) {
		t.Fatalf("expected default resources compiled into profile, got %+v", main.Spec.Resources)
	}

	if main.Spec.Deployment != nil {
		t.Fatalf("unexpected empty persistence block: %+v", main.Spec.Deployment)
	}

	if main.Spec.ImagePull == nil || main.Spec.ImagePull.Policy != string(k8scorev1.PullAlways) {
		t.Fatalf("expected direct pull policy compiled into profile, got %+v", main.Spec.ImagePull)
	}

	if main.Spec.Mgmt == nil || main.Spec.Mgmt.IPv4Subnet != "172.20.20.0/24" {
		t.Fatalf("expected mgmt settings compiled into profile, got %+v", main.Spec.Mgmt)
	}

	profileSpecJSON, err := json.Marshal(main.Spec)
	if err != nil {
		t.Fatalf("failed marshaling LauncherProfile spec: %s", err)
	}

	if strings.Contains(string(profileSpecJSON), "connectivity") {
		t.Fatalf("LauncherProfile must not contain connectivity: %s", profileSpecJSON)
	}

	assertPerNodeLauncherProfile(t, profiles[1])
}

func TestRenderLauncherProfilesPreservesAffinity(t *testing.T) {
	topology, compiled := renderTestTopology(t)
	affinity := &k8scorev1.Affinity{
		NodeAffinity: &k8scorev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &k8scorev1.NodeSelector{
				NodeSelectorTerms: []k8scorev1.NodeSelectorTerm{{
					MatchExpressions: []k8scorev1.NodeSelectorRequirement{{
						Key:      "topology.kubernetes.io/zone",
						Operator: k8scorev1.NodeSelectorOpIn,
						Values:   []string{"zone-a", "zone-b"},
					}},
				}},
			},
		},
	}
	topology.Spec.Deployment.Scheduling.Affinity = affinity

	profiles := clabernetescontrollerstopology.RenderLauncherProfiles(
		topology,
		compiled,
		clabernetesconfig.GetFakeManager,
	)

	if len(profiles) != 2 {
		t.Fatalf("expected shared + dedicated profiles, got %d", len(profiles))
	}

	for _, profile := range profiles {
		if profile.Spec.Scheduling == nil {
			t.Fatalf("expected scheduling block on profile %q", profile.GetName())
		}

		if !reflect.DeepEqual(profile.Spec.Scheduling.Affinity, affinity) {
			t.Fatalf(
				"profile %q affinity = %#v, want %#v",
				profile.GetName(), profile.Spec.Scheduling.Affinity, affinity,
			)
		}
	}
}

func TestRenderLauncherProfilesOmitsUnusedSharedProfile(t *testing.T) {
	topology, compiled := renderTestTopology(t)
	topology.Spec.Deployment.Resources = map[string]k8scorev1.ResourceRequirements{
		"multitool": {
			Requests: k8scorev1.ResourceList{
				"memory": resource.MustParse("128Mi"),
			},
		},
		"srl1": {
			Requests: k8scorev1.ResourceList{
				"memory": resource.MustParse("2Gi"),
			},
		},
	}

	profiles := clabernetescontrollerstopology.RenderLauncherProfiles(
		topology,
		compiled,
		clabernetesconfig.GetFakeManager,
	)

	if len(profiles) != 2 {
		t.Fatalf("expected only two referenced dedicated profiles, got %d", len(profiles))
	}

	expectedNames := []string{"render-test-multitool", "render-test-srl1"}

	actualNames := []string{profiles[0].GetName(), profiles[1].GetName()}
	if !reflect.DeepEqual(actualNames, expectedNames) {
		t.Fatalf("expected dedicated profiles %v, got %v", expectedNames, actualNames)
	}

	for _, profile := range profiles {
		if profile.Spec.Mgmt == nil || profile.Spec.Mgmt.IPv4Subnet != "172.20.20.0/24" {
			t.Fatalf(
				"expected every dedicated profile to retain topology mgmt compatibility, got %+v",
				profile.Spec.Mgmt,
			)
		}
	}
}

func assertPerNodeLauncherProfile(
	t *testing.T,
	perNode *clabernetesapisv1alpha1.LauncherProfile,
) {
	t.Helper()

	if perNode.GetName() != "render-test-multitool" {
		t.Fatalf("expected dedicated LauncherProfile name, got %q", perNode.GetName())
	}

	if perNode.Spec.Resources == nil ||
		!perNode.Spec.Resources.Requests.Memory().Equal(resource.MustParse("128Mi")) {
		t.Fatalf(
			"expected per node resources compiled into profile, got %+v",
			perNode.Spec.Resources,
		)
	}

	if perNode.Spec.Expose == nil || perNode.Spec.Expose.DisableAutoExpose == nil ||
		!*perNode.Spec.Expose.DisableAutoExpose {
		t.Fatalf(
			"expected dedicated profile to contain complete shared policy, got %+v",
			perNode.Spec,
		)
	}

	if perNode.Spec.Mgmt == nil || perNode.Spec.Mgmt.IPv4Subnet != "172.20.20.0/24" {
		t.Fatalf(
			"expected dedicated profile to retain topology mgmt compatibility, got %+v",
			perNode.Spec.Mgmt,
		)
	}
}
