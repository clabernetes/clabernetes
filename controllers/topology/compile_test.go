package topology_test

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetescontrollerstopology "github.com/clabernetes/clabernetes/controllers/topology"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
)

type warningRecordingLogger struct {
	claberneteslogging.FakeInstance

	warnings []string
}

func (l *warningRecordingLogger) Warn(message string) {
	l.warnings = append(l.warnings, message)
}

func compileDefinitionWithOptions(
	t *testing.T,
	definition string,
	options clabernetescontrollerstopology.CompileOptions,
) (*clabernetescontrollerstopology.CompiledTopology, error) {
	t.Helper()

	topology := &clabernetesapisv1alpha1.Topology{}
	topology.Spec.Definition.Containerlab = definition

	return clabernetescontrollerstopology.CompileTopologyWithOptions(
		&claberneteslogging.FakeInstance{},
		topology,
		options,
	)
}

const flattenTestDefinition = `
name: flatten-test
mgmt:
  ipv4-subnet: 172.20.20.0/24
topology:
  defaults:
    kind: nokia_srlinux
    env:
      FROM_DEFAULTS: "1"
      OVERRIDDEN: defaults
    binds:
      - /defaults:/defaults
    labels:
      tier: lab
      owner: defaults
  kinds:
    nokia_srlinux:
      image: ghcr.io/nokia/srlinux:latest
      type: ixrd2
      env:
        FROM_KIND: "1"
        OVERRIDDEN: kind
      labels:
        vendor: nokia
        owner: kind
    linux:
      image: ghcr.io/srl-labs/network-multitool
  nodes:
    srl1:
      startup-config: some-config
      env:
        OVERRIDDEN: node
      binds:
        - /node:/node
      ports:
        - 21022:22/tcp
        - 5201/udp
      labels:
        owner: roman
        # The exposePorts directive is consumed into ports and never becomes Kubernetes metadata.
        c9s.run/exposePorts: "5201/UDP, 9273/tcp, 9273/tcp"
        # Docker labels are far more permissive than Kubernetes labels, and clabernetes owns its
        # own label namespace and controller keys -- all four of these have to be dropped.
        not a valid key: x
        bad-value: has spaces and a !
        c9s.run/ignoreReconcile: "true"
        app.kubernetes.io/name: user-value
    multitool:
      kind: linux
  links:
    - endpoints: ["srl1:e1-1", "multitool:eth1"]
      mtu: 9212
    - endpoints: ["srl1:e1-2", "host:ens5"]
`

func compileFlattenTest(t *testing.T) *clabernetescontrollerstopology.CompiledTopology {
	t.Helper()

	topology := &clabernetesapisv1alpha1.Topology{}
	topology.Name = "flatten-test"
	topology.Namespace = "clabernetes"
	topology.Spec.Definition.Containerlab = flattenTestDefinition

	compiled, err := clabernetescontrollerstopology.CompileTopology(
		&claberneteslogging.FakeInstance{},
		topology,
	)
	if err != nil {
		t.Fatalf("unexpected error compiling topology: %s", err)
	}

	return compiled
}

func TestCompileContainerlabFlattening(t *testing.T) {
	compiled := compileFlattenTest(t)

	srl1 := compiled.Nodes["srl1"]
	if srl1 == nil {
		t.Fatal("expected compiled node srl1")
	}

	if srl1.Kind != "nokia_srlinux" {
		t.Fatalf("expected kind from defaults to be expanded, got %q", srl1.Kind)
	}

	if srl1.Image != "ghcr.io/nokia/srlinux:latest" {
		t.Fatalf("expected image from kind to be expanded, got %q", srl1.Image)
	}

	if srl1.Type != "ixrd2" {
		t.Fatalf("expected type from kind to be expanded, got %q", srl1.Type)
	}

	if srl1.StartupConfig != "some-config" {
		t.Fatalf("expected node level startup-config, got %q", srl1.StartupConfig)
	}

	expectedEnv := map[string]string{
		"FROM_DEFAULTS": "1",
		"FROM_KIND":     "1",
		"OVERRIDDEN":    "node",
	}
	if !reflect.DeepEqual(srl1.Env, expectedEnv) {
		t.Fatalf("expected merged env %v, got %v", expectedEnv, srl1.Env)
	}

	expectedBinds := []string{"/defaults:/defaults", "/node:/node"}
	if !reflect.DeepEqual(srl1.Binds, expectedBinds) {
		t.Fatalf("expected merged binds %v, got %v", expectedBinds, srl1.Binds)
	}

	// pasted containerlab topologies carry docker style bindings; the host side is dropped since
	// clabernetes allocates it, while destination-only entries pass through untouched
	expectedPorts := []string{"22/tcp", "5201/udp", "9273/tcp"}
	if !reflect.DeepEqual(srl1.Ports, expectedPorts) {
		t.Fatalf("expected normalized node ports %v, got %v", expectedPorts, srl1.Ports)
	}

	multitool := compiled.Nodes["multitool"]
	if multitool == nil {
		t.Fatal("expected compiled node multitool")
	}

	if multitool.Kind != "linux" ||
		multitool.Image != "ghcr.io/srl-labs/network-multitool" {
		t.Fatalf(
			"expected node kind to pick its own kind definition, got kind %q image %q",
			multitool.Kind,
			multitool.Image,
		)
	}

	// node level kind must win over defaults kind -- and only inherit *its* kind's fields
	if multitool.Type != "" {
		t.Fatalf("expected no type bleed from other kinds, got %q", multitool.Type)
	}

	if compiled.Mgmt == nil || compiled.Mgmt.IPv4Subnet != "172.20.20.0/24" {
		t.Fatalf("expected mgmt settings to be compiled, got %+v", compiled.Mgmt)
	}
}

func TestCompileContainerlabExposePortsLabelRejectsInvalidEntries(t *testing.T) {
	topology := &clabernetesapisv1alpha1.Topology{}
	topology.Spec.Definition.Containerlab = `
name: invalid-expose-ports
topology:
  nodes:
    gnmic:
      kind: linux
      image: ghcr.io/openconfig/gnmic:latest
      labels:
        c9s.run/exposePorts: "9273/tcp, not-a-port"
`

	_, err := clabernetescontrollerstopology.CompileTopology(
		&claberneteslogging.FakeInstance{},
		topology,
	)
	if err == nil {
		t.Fatal("expected invalid exposePorts entry to fail compilation")
	}

	unsupported := &clabernetescontrollerstopology.UnsupportedFeaturesError{}
	if !errors.As(err, &unsupported) {
		t.Fatalf("expected UnsupportedFeaturesError, got %T: %s", err, err)
	}

	if len(unsupported.Diagnostics) != 1 ||
		unsupported.Diagnostics[0].Code != "invalid-expose-ports-label" {
		t.Fatalf("unexpected diagnostics: %+v", unsupported.Diagnostics)
	}
}

func TestCompileContainerlabExposePortsLabelInheritance(t *testing.T) {
	topology := &clabernetesapisv1alpha1.Topology{}
	topology.Spec.Definition.Containerlab = `
name: inherited-expose-ports
topology:
  defaults:
    labels:
      c9s.run/exposePorts: "830/tcp"
  kinds:
    metrics:
      labels:
        c9s.run/exposePorts: "9273/TCP, 9273/tcp, 8125/udp"
  nodes:
    default-node:
      kind: linux
      image: alpine
    kind-node:
      kind: metrics
      image: alpine
    node-override:
      kind: metrics
      image: alpine
      labels:
        c9s.run/exposePorts: "57400"
`

	compiled, err := clabernetescontrollerstopology.CompileTopology(
		&claberneteslogging.FakeInstance{},
		topology,
	)
	if err != nil {
		t.Fatalf("unexpected error compiling inherited expose ports: %s", err)
	}

	expectedPorts := map[string][]string{
		"default-node":  {"830/tcp"},
		"kind-node":     {"9273/tcp", "8125/udp"},
		"node-override": {"57400/tcp"},
	}

	for nodeName, want := range expectedPorts {
		node := compiled.Nodes[nodeName]
		if node == nil {
			t.Fatalf("expected compiled node %q", nodeName)
		}

		if !reflect.DeepEqual(node.Ports, want) {
			t.Errorf("node %q ports = %v, want %v", nodeName, node.Ports, want)
		}

		if _, exists := node.Labels[clabernetesconstants.LabelExposePorts]; exists {
			t.Errorf("node %q retained exposePorts directive in labels: %v", nodeName, node.Labels)
		}
	}
}

func TestCompileContainerlabExposePortsLabelRejectsEmptyAndMultipleInvalidEntries(t *testing.T) {
	tests := map[string]struct {
		value          string
		expectedErrors int
	}{
		"empty middle entry": {
			value:          "9273/tcp,,8125/udp",
			expectedErrors: 1,
		},
		"trailing empty entry": {
			value:          "9273/tcp,",
			expectedErrors: 1,
		},
		"multiple invalid entries": {
			value:          "not-a-port, 70000/tcp, 8125/sctp",
			expectedErrors: 3,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			topology := &clabernetesapisv1alpha1.Topology{}
			topology.Spec.Definition.Containerlab = fmt.Sprintf(`
name: invalid-expose-ports
topology:
  nodes:
    gnmic:
      kind: linux
      image: ghcr.io/openconfig/gnmic:latest
      labels:
        c9s.run/exposePorts: %q
`, test.value)

			_, err := clabernetescontrollerstopology.CompileTopology(
				&claberneteslogging.FakeInstance{},
				topology,
			)
			if err == nil {
				t.Fatal("expected invalid exposePorts entries to fail compilation")
			}

			unsupported := &clabernetescontrollerstopology.UnsupportedFeaturesError{}
			if !errors.As(err, &unsupported) {
				t.Fatalf("expected UnsupportedFeaturesError, got %T: %s", err, err)
			}

			if len(unsupported.Diagnostics) != test.expectedErrors {
				t.Fatalf(
					"expected %d invalid-entry diagnostics, got %+v",
					test.expectedErrors,
					unsupported.Diagnostics,
				)
			}
		})
	}
}

// TestCompileContainerlabLabels covers containerlab node labels, which become kubernetes labels on
// the emitted Node rather than docker labels on the node container. They inherit like env does, and
// anything kubernetes would reject -- or that sits in c9s' own label namespace -- is
// dropped here, since the alternative is an emitted Node the apiserver refuses to create.
func TestCompileContainerlabLabels(t *testing.T) {
	compiled := compileFlattenTest(t)

	expected := map[string]map[string]string{
		// defaults, then kind, then node, most specific winning "owner"
		"srl1": {"tier": "lab", "vendor": "nokia", "owner": "roman"},
		// the linux kind declares no labels, so only the topology defaults apply
		"multitool": {"tier": "lab", "owner": "defaults"},
	}

	for nodeName, expectedLabels := range expected {
		node := compiled.Nodes[nodeName]
		if node == nil {
			t.Fatalf("expected compiled node %q", nodeName)
		}

		if !reflect.DeepEqual(node.Labels, expectedLabels) {
			t.Errorf(
				"node %q: expected labels %v, got %v",
				nodeName,
				expectedLabels,
				node.Labels,
			)
		}
	}
}

func TestCompileContainerlabLinks(t *testing.T) {
	compiled := compileFlattenTest(t)

	expected := []clabernetescontrollerstopology.CompiledLink{
		{
			EndpointA: clabernetesapisv1alpha1.LinkEndpointSpec{
				NodeName:      "srl1",
				InterfaceName: "e1-1",
			},
			EndpointB: clabernetesapisv1alpha1.LinkEndpointSpec{
				NodeName:      "multitool",
				InterfaceName: "eth1",
			},
			MTU: 9212,
		},
		{
			EndpointA: clabernetesapisv1alpha1.LinkEndpointSpec{
				NodeName:      "srl1",
				InterfaceName: "e1-2",
			},
			EndpointB: clabernetesapisv1alpha1.LinkEndpointSpec{
				NodeName:      "host",
				InterfaceName: "ens5",
			},
		},
	}

	if !reflect.DeepEqual(compiled.Links, expected) {
		t.Fatalf("expected links %+v, got %+v", expected, compiled.Links)
	}
}

func TestCompileContainerlabStructuredVethLink(t *testing.T) {
	compiled, err := compileDefinitionWithOptions(t, `
name: structured-veth
topology:
  nodes:
    srsim:
      kind: nokia_srsim
      image: registry.example/nokia_srsim:latest
      type: sr-7
      components:
        - slot: A
        - slot: 1
    client:
      kind: linux
      image: alpine:latest
  links:
    - type: veth
      endpoints:
        - node: srsim
          interface: 1/1/c1/1
        - node: client
          interface: eth1
`, clabernetescontrollerstopology.CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if len(compiled.Links) != 1 {
		t.Fatalf("compiled links = %+v, want one", compiled.Links)
	}

	link := compiled.Links[0]
	if link.EndpointA.NodeName != "srsim" || link.EndpointA.InterfaceName != "1/1/c1/1" ||
		link.EndpointB.NodeName != "client" || link.EndpointB.InterfaceName != "eth1" {
		t.Fatalf("compiled structured veth link = %+v", link)
	}
}

func TestCompileContainerlabBriefVethLink(t *testing.T) {
	compiled, err := compileDefinitionWithOptions(t, `
name: brief-veth
topology:
  nodes:
    srsim:
      kind: nokia_srsim
      image: registry.example/nokia_srsim:latest
    client:
      kind: linux
      image: alpine:latest
  links:
    - type: veth
      endpoints: ["srsim:1/1/c1/1", "client:eth1"]
`, clabernetescontrollerstopology.CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if len(compiled.Links) != 1 {
		t.Fatalf("compiled links = %+v, want one", compiled.Links)
	}

	link := compiled.Links[0]
	if link.EndpointA.NodeName != "srsim" || link.EndpointA.InterfaceName != "1/1/c1/1" ||
		link.EndpointB.NodeName != "client" || link.EndpointB.InterfaceName != "eth1" {
		t.Fatalf("compiled brief veth link = %+v", link)
	}
}

func TestCompileContainerlabSRSimEmptyComponents(t *testing.T) {
	compiled, err := compileDefinitionWithOptions(t, `
name: srsim-components-empty
topology:
  nodes:
    device:
      kind: nokia_srsim
      image: internal-registry/norc/sr-sim:25.10.R4
      type: SR-1-48D
      license: license.txt
      components: []
`, clabernetescontrollerstopology.CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}

	device, ok := compiled.Nodes["device"]
	if !ok {
		t.Fatalf("compiled nodes = %+v, want device", compiled.Nodes)
	}

	if device.License != "license.txt" {
		t.Fatalf("compiled license = %q, want license.txt", device.License)
	}

	if len(device.Components) != 0 {
		t.Fatalf("compiled components = %+v, want no components", device.Components)
	}
}

func TestCompileTopologyStrictRejectsLossyCompatibility(t *testing.T) {
	definition := `
name: strict-test
mgmt:
  ipv4-subnet: 172.30.30.0/24
topology:
  nodes:
    n1:
      kind: linux
      image: ghcr.io/srl-labs/network-multitool
      cpu: 2
      ports: [22022:22/tcp]
  links:
    - endpoints: ["n1:eth1", "host:veth-review"]
      labels: {purpose: review}
      vars: {delay: 10}
`

	// The compatibility controller remains permissive.
	topology := &clabernetesapisv1alpha1.Topology{}
	topology.Spec.Definition.Containerlab = definition

	_, err := clabernetescontrollerstopology.CompileTopology(
		&claberneteslogging.FakeInstance{},
		topology,
	)
	if err != nil {
		t.Fatalf("warning policy unexpectedly rejected topology: %s", err)
	}

	_, err = compileDefinitionWithOptions(
		t,
		definition,
		clabernetescontrollerstopology.CompileOptions{
			UnsupportedFieldPolicy: clabernetescontrollerstopology.UnsupportedFieldPolicyError,
		},
	)
	if err == nil {
		t.Fatal("strict compilation accepted lossy topology")
	}

	unsupported := &clabernetescontrollerstopology.UnsupportedFeaturesError{}
	if !errors.As(err, &unsupported) {
		t.Fatalf("expected UnsupportedFeaturesError, got %T: %s", err, err)
	}

	codes := make([]string, 0, len(unsupported.Diagnostics))
	for _, diagnostic := range unsupported.Diagnostics {
		codes = append(codes, diagnostic.Code)
	}

	for _, expected := range []string{
		"host-port-pinning",
		"management-network-semantics",
		"unsupported-field",
		"unsupported-link-labels",
		"unsupported-link-vars",
	} {
		if !slices.Contains(codes, expected) {
			t.Errorf("expected diagnostic %q, got %v", expected, codes)
		}
	}
}

func TestCompileTopologyWarningsIncludeLocations(t *testing.T) {
	definition := `
name: warning-locations
topology:
  nodes:
    n1: {kind: linux, image: alpine}
    n2: {kind: linux, image: alpine}
  links:
    - endpoints: ["n1:eth1", "n2:eth1"]
      labels: {purpose: first}
    - endpoints: ["n1:eth2", "n2:eth2"]
      vars: {purpose: second}
`
	topology := &clabernetesapisv1alpha1.Topology{}
	topology.Spec.Definition.Containerlab = definition
	logger := &warningRecordingLogger{}

	_, err := clabernetescontrollerstopology.CompileTopology(logger, topology)
	if err != nil {
		t.Fatalf("warning policy unexpectedly rejected topology: %s", err)
	}

	want := []string{
		"topology.links[0].labels: link labels are not preserved by the c9s Link API",
		"topology.links[1].vars: link vars are not preserved by the c9s Link API",
	}
	if !reflect.DeepEqual(logger.warnings, want) {
		t.Fatalf("warnings = %q, want %q", logger.warnings, want)
	}
}

func TestCompileTopologyAlwaysRejectsImpossibleStructures(t *testing.T) {
	tests := map[string]string{
		"bridge pseudo node": `
name: bridge-test
topology:
  nodes:
    br0: {kind: bridge}
`,
		"mgmt endpoint": `
name: mgmt-test
topology:
  nodes:
    n1: {kind: linux, image: alpine}
  links:
    - endpoints: ["n1:eth1", "mgmt-net:n1-eth1"]
`,
		"missing node": `
name: missing-test
topology:
  nodes:
    n1: {kind: linux, image: alpine}
  links:
    - endpoints: ["n1:eth1", "n2:eth1"]
`,
		"explicit vxlan": `
name: vxlan-test
topology:
  nodes:
    n1: {kind: linux, image: alpine}
  links:
    - type: vxlan
      remote: 192.0.2.1
      endpoint: {node: n1, interface: eth1}
`,
		"explicit host with brief endpoints": `
name: explicit-host-test
topology:
  nodes:
    n1: {kind: linux, image: alpine}
  links:
    - type: host
      endpoints: ["n1:eth1", "host:veth-n1"]
`,
		"host network mode": `
name: host-network-test
topology:
  nodes:
    n1: {kind: linux, image: alpine, network-mode: host}
`,
		"missing network mode primary": `
name: missing-primary-test
topology:
  nodes:
    n1: {kind: linux, image: alpine, network-mode: container:missing}
`,
		"network mode cycle": `
name: cycle-test
topology:
  nodes:
    n1: {kind: linux, image: alpine, network-mode: container:n2}
    n2: {kind: linux, image: alpine, network-mode: container:n1}
`,
	}

	for name, definition := range tests {
		t.Run(name, func(t *testing.T) {
			topology := &clabernetesapisv1alpha1.Topology{}
			topology.Spec.Definition.Containerlab = definition

			_, err := clabernetescontrollerstopology.CompileTopology(
				&claberneteslogging.FakeInstance{},
				topology,
			)
			if err == nil {
				t.Fatal("warning policy accepted structurally unsupported topology")
			}

			unsupported := &clabernetescontrollerstopology.UnsupportedFeaturesError{}
			if !errors.As(err, &unsupported) {
				t.Fatalf("expected UnsupportedFeaturesError, got %T: %s", err, err)
			}
		})
	}
}
