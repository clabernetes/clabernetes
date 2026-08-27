package clabverter_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesclabverter "github.com/clabernetes/clabernetes/clabverter"
	clabernetescompiler "github.com/clabernetes/clabernetes/compiler"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetescontrollersnode "github.com/clabernetes/clabernetes/controllers/node"
	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
	"github.com/google/go-cmp/cmp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/yaml"
)

const directManifestEquivalenceDefinition = `
name: entry-equivalence
topology:
  defaults:
    kind: package-owned-kind
    image: example/device:1
    env: {FROM_DEFAULTS: "1"}
  kinds:
    package-owned-kind:
      labels: {tier: imported}
  nodes:
    primary:
      ports: [830/tcp]
    secondary:
      network-mode: container:primary
    remote: {}
  links:
    - endpoints: [primary:eth1, secondary:eth1]
      mtu: 9000
    - endpoints: [primary:eth2, remote:eth1]
`

func TestDirectManifestCanonicalPlanningInputMatchesTopologyCompilation(t *testing.T) {
	directManifest, err := renderDirectManifest(t, directManifestEquivalenceDefinition)
	if err != nil {
		t.Fatal(err)
	}

	directProfiles, directLinks, directNodes := decodeDirectManifestStream(
		t,
		directManifest,
	)

	topology := controllerTopology(t, directManifestEquivalenceDefinition)

	compiled, err := clabernetescompiler.CompileTopology(
		&claberneteslogging.FakeInstance{},
		topology,
	)
	if err != nil {
		t.Fatal(err)
	}

	controllerProfiles := clabernetescompiler.RenderNodeProfiles(
		topology,
		compiled,
		clabernetesconfig.GetFakeManager,
	)
	controllerLinks := clabernetescompiler.RenderLinks(
		topology,
		compiled,
		clabernetesconfig.GetFakeManager,
	)
	controllerNodes := clabernetescompiler.RenderNodes(
		topology,
		compiled,
		clabernetesconfig.GetFakeManager,
	)

	assertPrimitiveSpecsEqual(
		t,
		directProfiles,
		directLinks,
		directNodes,
		controllerProfiles,
		controllerLinks,
		controllerNodes,
	)
	directInputs := canonicalPlanningInputs(t, directNodes, directLinks)

	controllerInputs := canonicalPlanningInputs(t, controllerNodes, controllerLinks)
	if !reflect.DeepEqual(directInputs, controllerInputs) {
		t.Fatalf(
			"canonical planning inputs differ: direct=%s controller=%s",
			directInputs,
			controllerInputs,
		)
	}
}

func TestDirectManifestUsesIdenticalStructuredValidation(t *testing.T) {
	definition := `
name: invalid-entry-equivalence
topology:
  nodes:
    n1:
      kind: package-owned-kind
      image: example/device:1
      unrepresentable-setting: true
`

	_, directErr := renderDirectManifest(t, definition)
	if directErr == nil {
		t.Fatal("direct manifest generation accepted unrepresentable input")
	}

	topology := controllerTopology(t, definition)
	_, controllerErr := clabernetescompiler.CompileTopology(
		&claberneteslogging.FakeInstance{},
		topology,
	)
	directUnsupported := &clabernetescompiler.UnsupportedFeaturesError{}

	controllerUnsupported := &clabernetescompiler.UnsupportedFeaturesError{}
	if !errors.As(directErr, &directUnsupported) ||
		!errors.As(controllerErr, &controllerUnsupported) ||
		!reflect.DeepEqual(directUnsupported.Diagnostics, controllerUnsupported.Diagnostics) {
		t.Fatalf(
			"validation differs: direct=%#v controller=%#v",
			directErr,
			controllerErr,
		)
	}
}

func TestClabverterRejectsUnrepresentableOutputAtomically(t *testing.T) {
	testCases := []struct {
		name         string
		definition   string
		topologySpec string
	}{
		{
			name: "unsupported containerlab field",
			definition: `
name: invalid-output
topology:
  nodes:
    n1:
      kind: package-owned-kind
      image: example/device:1
      unrepresentable-setting: true
`,
		},
		{
			name:         "unknown topology policy",
			definition:   directManifestEquivalenceDefinition,
			topologySpec: "unknownPolicy: true\n",
		},
	}

	for _, testCase := range testCases {
		for _, emitCRs := range []bool{false, true} {
			mode := "topology"
			if emitCRs {
				mode = "direct-crs"
			}

			t.Run(testCase.name+"/"+mode, func(t *testing.T) {
				outputDirectory, err := runClabverter(
					t,
					testCase.definition,
					testCase.topologySpec,
					emitCRs,
				)
				if err == nil {
					t.Fatal("clabverter accepted input it cannot represent directly")
				}

				entries, readErr := os.ReadDir(outputDirectory)
				if readErr != nil {
					t.Fatal(readErr)
				}

				if len(entries) != 0 {
					t.Fatalf("clabverter wrote output before validation failed: %v", entries)
				}
			})
		}
	}
}

func renderDirectManifest(t *testing.T, definition string) ([]byte, error) {
	t.Helper()

	config, _, err := clabernetesutilcontainerlab.LoadContainerlabConfig(definition)
	if err != nil {
		t.Fatal(err)
	}

	outputDirectory, err := runClabverter(t, definition, "", true)
	if err != nil {
		return nil, err
	}

	// config.Name was parsed from the test-owned topology and the output root is a fresh TempDir.
	return os.ReadFile(filepath.Join(outputDirectory, config.Name+"-crs.yaml")) //nolint:gosec
}

func runClabverter(
	t *testing.T,
	definition,
	topologySpec string,
	emitCRs bool,
) (string, error) {
	t.Helper()

	testDirectory := t.TempDir()
	topologyPath := filepath.Join(testDirectory, "topology.clab.yml")

	err := os.WriteFile(topologyPath, []byte(definition), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	topologySpecPath := ""
	if topologySpec != "" {
		topologySpecPath = filepath.Join(testDirectory, "topology-spec.yaml")

		err = os.WriteFile(topologySpecPath, []byte(topologySpec), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	outputDirectory := filepath.Join(testDirectory, "output")
	converter := clabernetesclabverter.MustNewClabverter(
		topologyPath,
		topologySpecPath,
		outputDirectory,
		"lab",
		"prefixed",
		"",
		false,
		emitCRs,
		false,
		true,
		false,
	)

	t.Cleanup(func() {
		claberneteslogging.GetManager().DeleteLogger(clabernetesconstants.Clabverter)
	})

	return outputDirectory, converter.Clabvert()
}

func controllerTopology(
	t *testing.T,
	definition string,
) *clabernetesapisv1alpha1.Topology {
	t.Helper()

	config, _, err := clabernetesutilcontainerlab.LoadContainerlabConfig(definition)
	if err != nil {
		t.Fatal(err)
	}

	topology := &clabernetesapisv1alpha1.Topology{
		ObjectMeta: metav1.ObjectMeta{Name: config.Name, Namespace: "lab"},
	}
	topology.Spec.Definition.Containerlab = definition

	return topology
}

func decodeDirectManifestStream(
	t *testing.T,
	content []byte,
) (
	[]*clabernetesapisv1alpha1.NodeProfile,
	[]*clabernetesapisv1alpha1.Link,
	[]*clabernetesapisv1alpha1.Node,
) {
	t.Helper()

	profiles := []*clabernetesapisv1alpha1.NodeProfile{}
	links := []*clabernetesapisv1alpha1.Link{}
	nodes := []*clabernetesapisv1alpha1.Node{}

	for document := range strings.SplitSeq(string(content), "---\n") {
		if strings.TrimSpace(document) == "" {
			continue
		}

		header := &metav1.TypeMeta{}

		err := yaml.Unmarshal([]byte(document), header)
		if err != nil {
			t.Fatal(err)
		}

		switch header.Kind {
		case "NodeProfile":
			profile := &clabernetesapisv1alpha1.NodeProfile{}

			err = yaml.Unmarshal([]byte(document), profile)
			if err != nil {
				t.Fatal(err)
			}

			profiles = append(profiles, profile)
		case "Link":
			link := &clabernetesapisv1alpha1.Link{}

			err = yaml.Unmarshal([]byte(document), link)
			if err != nil {
				t.Fatal(err)
			}

			links = append(links, link)
		case "Node":
			node := &clabernetesapisv1alpha1.Node{}

			err = yaml.Unmarshal([]byte(document), node)
			if err != nil {
				t.Fatal(err)
			}

			nodes = append(nodes, node)
		default:
			t.Fatalf("unexpected direct manifest kind %q", header.Kind)
		}
	}

	return profiles, links, nodes
}

func assertPrimitiveSpecsEqual(
	t *testing.T,
	directProfiles []*clabernetesapisv1alpha1.NodeProfile,
	directLinks []*clabernetesapisv1alpha1.Link,
	directNodes []*clabernetesapisv1alpha1.Node,
	controllerProfiles []*clabernetesapisv1alpha1.NodeProfile,
	controllerLinks []*clabernetesapisv1alpha1.Link,
	controllerNodes []*clabernetesapisv1alpha1.Node,
) {
	t.Helper()

	assertCanonicalJSONEqual(
		t,
		"NodeProfile specs",
		profileSpecsByName(controllerProfiles),
		profileSpecsByName(directProfiles),
	)
	assertCanonicalJSONEqual(
		t,
		"Link specs",
		linkSpecsByName(controllerLinks),
		linkSpecsByName(directLinks),
	)
	assertCanonicalJSONEqual(
		t,
		"Node specs",
		nodeSpecsByName(controllerNodes),
		nodeSpecsByName(directNodes),
	)
	assertCanonicalJSONEqual(
		t,
		"Node planning metadata",
		nodePlanningMetadataByName(controllerNodes),
		nodePlanningMetadataByName(directNodes),
	)
}

func assertCanonicalJSONEqual(t *testing.T, subject string, expected, actual any) {
	t.Helper()

	expectedJSON, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}

	actualJSON, err := json.Marshal(actual)
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(string(expectedJSON), string(actualJSON)); diff != "" {
		t.Fatalf("direct %s differ (-controller +direct):\n%s", subject, diff)
	}
}

func profileSpecsByName(
	profiles []*clabernetesapisv1alpha1.NodeProfile,
) map[string]clabernetesapisv1alpha1.NodeProfileSpec {
	result := make(map[string]clabernetesapisv1alpha1.NodeProfileSpec, len(profiles))
	for _, profile := range profiles {
		result[profile.GetName()] = profile.Spec
	}

	return result
}

func linkSpecsByName(
	links []*clabernetesapisv1alpha1.Link,
) map[string]clabernetesapisv1alpha1.LinkSpec {
	result := make(map[string]clabernetesapisv1alpha1.LinkSpec, len(links))
	for _, link := range links {
		result[link.GetName()] = link.Spec
	}

	return result
}

func nodeSpecsByName(
	nodes []*clabernetesapisv1alpha1.Node,
) map[string]clabernetesapisv1alpha1.NodeSpec {
	result := make(map[string]clabernetesapisv1alpha1.NodeSpec, len(nodes))
	for _, node := range nodes {
		result[node.GetName()] = node.Spec
	}

	return result
}

type nodePlanningMetadata struct {
	Namespace   string            `json:"namespace"`
	Name        string            `json:"name"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

func nodePlanningMetadataByName(
	nodes []*clabernetesapisv1alpha1.Node,
) map[string]nodePlanningMetadata {
	result := make(map[string]nodePlanningMetadata, len(nodes))
	for _, node := range nodes {
		result[node.GetName()] = nodePlanningMetadata{
			Namespace: node.GetNamespace(), Name: node.GetName(),
			Labels: node.GetLabels(), Annotations: node.GetAnnotations(),
		}
	}

	return result
}

func canonicalPlanningInputs(
	t *testing.T,
	nodes []*clabernetesapisv1alpha1.Node,
	links []*clabernetesapisv1alpha1.Link,
) map[string]string {
	t.Helper()

	byName := make(map[string]*clabernetesapisv1alpha1.Node, len(nodes))
	for _, node := range nodes {
		node.UID = apimachinerytypes.UID("uid-node-" + node.GetName())
		byName[node.GetName()] = node
	}

	for index, link := range links {
		link.UID = apimachinerytypes.UID("uid-link-" + link.GetName())
		link.Status.WireID = index + 100
		link.Status.ResolvedEndpoints = &clabernetesapisv1alpha1.LinkResolvedEndpointsStatus{
			EndpointA: resolvedEndpoint(link.Spec.EndpointA, byName),
			EndpointB: resolvedEndpoint(link.Spec.EndpointB, byName),
		}
		link.Status.Conditions = []metav1.Condition{{
			Type:               clabernetesapisv1alpha1.LinkConditionAccepted,
			Status:             metav1.ConditionTrue,
			Reason:             "Accepted",
			LastTransitionTime: metav1.Now(),
		}}
	}

	names := make([]string, 0, len(nodes))
	for name := range byName {
		names = append(names, name)
	}

	slices.Sort(names)

	result := map[string]string{}

	for _, name := range names {
		if clabernetesutilcontainerlab.ResolvePrimaryNode(byName, name) != name {
			continue
		}

		members := clabernetesutilcontainerlab.ResolveGroupMembers(byName, name)

		images := make([]clabernetesinternaldeviceplan.ImageInput, 0, len(members))

		for _, member := range members {
			node := byName[member]
			images = append(images, clabernetesinternaldeviceplan.ImageInput{
				NodeID:          string(node.GetUID()),
				SourceReference: node.Spec.Image,
				DigestReference: "example/device@sha256:" + strings.Repeat("a", 64),
				Platform: clabernetesinternaldeviceplan.Platform{
					OS: "linux", Architecture: "amd64",
				},
			})
		}

		input, err := clabernetescontrollersnode.CompilePlanInput(
			clabernetescontrollersnode.PlanInputCompileRequest{
				Primary: byName[name], GroupMembers: members, NodesByName: byName,
				Links: pointerLinksToValues(links), Images: images,
				Compatibility: clabernetesinternaldeviceplan.Compatibility{
					ContainerlabModule:  clabernetesinternaldeviceplan.ContainerlabModulePath,
					ContainerlabVersion: "v-test",
					RegistryDigest:      "sha256:" + strings.Repeat("b", 64),
					PlanSchemaVersion:   clabernetesinternaldeviceplan.SchemaVersion,
				},
			},
		)
		if err != nil {
			t.Fatal(err)
		}

		canonical, err := input.CanonicalJSON()
		if err != nil {
			t.Fatal(err)
		}

		result[name] = string(canonical)
	}

	return result
}

func resolvedEndpoint(
	endpoint clabernetesapisv1alpha1.LinkEndpointSpec,
	nodes map[string]*clabernetesapisv1alpha1.Node,
) clabernetesapisv1alpha1.LinkResolvedEndpointStatus {
	resolved := clabernetesapisv1alpha1.LinkResolvedEndpointStatus{NodeName: endpoint.NodeName}
	if node := nodes[endpoint.NodeName]; node != nil {
		resolved.UID = node.GetUID()
	}

	return resolved
}

func pointerLinksToValues(
	links []*clabernetesapisv1alpha1.Link,
) []clabernetesapisv1alpha1.Link {
	values := make([]clabernetesapisv1alpha1.Link, 0, len(links))
	for _, link := range links {
		values = append(values, *link.DeepCopy())
	}

	return values
}
