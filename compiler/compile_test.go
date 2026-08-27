package compiler_test

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetescompiler "github.com/clabernetes/clabernetes/compiler"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
)

func compileDefinition(
	t *testing.T,
	definition string,
) (*clabernetescompiler.CompiledTopology, error) {
	t.Helper()

	topology := &clabernetesapisv1alpha1.Topology{}
	topology.Spec.Definition.Containerlab = definition

	return clabernetescompiler.CompileTopology(
		&claberneteslogging.FakeInstance{},
		topology,
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
        - 22/tcp
        - 5201/udp
      labels:
        owner: roman
        # The exposePorts directive is consumed into ports and never becomes Kubernetes metadata.
        c9s.run/exposePorts: "5201/UDP, 9273/tcp, 9273/tcp"
    multitool:
      kind: linux
  links:
    - endpoints: ["srl1:e1-1", "multitool:eth1"]
      mtu: 9212
    - endpoints: ["srl1:e1-2", "host:ens5"]
`

func compileFlattenTest(t *testing.T) *clabernetescompiler.CompiledTopology {
	t.Helper()

	topology := &clabernetesapisv1alpha1.Topology{}
	topology.Name = "flatten-test"
	topology.Namespace = "clabernetes"
	topology.Spec.Definition.Containerlab = flattenTestDefinition

	compiled, err := clabernetescompiler.CompileTopology(
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

	// destination-only entries pass through untouched
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

//nolint:gocyclo,wsl_v5 // One scenario checks the complete package-owned inheritance contract.
func TestCompileContainerlabUsesImportedInheritanceSemantics(t *testing.T) {
	t.Parallel()

	compiled, err := compileDefinition(t, `
name: imported-inheritance
topology:
  defaults:
    kind: package-owned-kind
    exec: [defaults-exec]
    env-files: [defaults.env]
    binds:
      - /defaults:/shared
      - /defaults-only:/defaults-only
    devices: [/dev/default]
    cap-add: [NET_ADMIN]
    security-opts: [seccomp=defaults.json]
    tmpfs: { /run: defaults, /defaults: rw }
    ports: [1000/tcp]
    env: { FROM_DEFAULTS: "1", OVERRIDE: defaults }
    sysctls: { net.ipv4.ip_forward: "1" }
    labels: { tier: defaults, owner: defaults }
    mgmt-ipv4: 192.0.2.250/24
    config:
      vars: { from_defaults: true, override: defaults }
    certificate:
      issue: true
      validity-duration: 24h
      sans: [defaults.example]
  kinds:
    package-owned-kind:
      image: example/device:1
      exec: [kind-exec]
      env-files: [kind.env]
      binds: [/kind:/shared]
      devices: [/dev/kind]
      cap-add: [SYS_ADMIN]
      security-opts: [apparmor=kind]
      tmpfs: { /run: kind }
      ports: [2000/tcp]
      env: { FROM_KIND: "1", OVERRIDE: kind }
      labels: { tier: kind }
      config:
        vars: { from_kind: true, override: kind }
      certificate:
        key-size: 4096
      components:
        - { slot: inherited, type: line-card }
  nodes:
    primary:
      exec: [node-exec]
      env-files: [node.env]
      binds: [/node:/shared]
      ports: [3000/udp]
      env: { FROM_NODE: "1", OVERRIDE: node }
      labels: { owner: node }
      tmpfs: { /run: node }
      config:
        vars: { from_node: true, override: node }
      certificate:
        sans: [node.example]
      components: []
      mgmt-ipv4: 192.0.2.10/24
    secondary:
      network-mode: container:primary
`)
	if err != nil {
		t.Fatal(err)
	}

	primary := compiled.Nodes["primary"]
	if primary == nil {
		t.Fatal("compiled topology has no primary Node")
	}
	if got, want := primary.Exec, []string{"defaults-exec", "kind-exec", "node-exec"}; !reflect.DeepEqual(
		got,
		want,
	) {
		t.Fatalf("effective exec = %q, want %q", got, want)
	}
	if got, want := primary.EnvFiles, []string{"defaults.env", "kind.env", "node.env"}; !reflect.DeepEqual(
		got,
		want,
	) {
		t.Fatalf("effective env-files = %q, want %q", got, want)
	}
	if got, want := primary.Binds,
		[]string{"/defaults-only:/defaults-only", "/node:/shared"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("effective binds = %q, want %q", got, want)
	}
	if got, want := primary.Ports, []string{"3000/udp"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("effective ports = %q, want most-specific %q", got, want)
	}
	if got, want := primary.Devices, []string{"/dev/default", "/dev/kind"}; !reflect.DeepEqual(
		got,
		want,
	) {
		t.Fatalf("effective devices = %q, want %q", got, want)
	}
	if got, want := primary.CapAdd, []string{"NET_ADMIN", "SYS_ADMIN"}; !reflect.DeepEqual(
		got,
		want,
	) {
		t.Fatalf("effective capabilities = %q, want %q", got, want)
	}
	if got, want := primary.SecurityOpts,
		[]string{"seccomp=defaults.json", "apparmor=kind"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("effective security options = %q, want %q", got, want)
	}
	if got, want := primary.Tmpfs,
		map[string]string{"/defaults": "rw", "/run": "node"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("effective tmpfs = %q, want %q", got, want)
	}
	if got, want := primary.Env, map[string]string{
		"FROM_DEFAULTS": "1", "FROM_KIND": "1", "FROM_NODE": "1", "OVERRIDE": "node",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("effective env = %q, want %q", got, want)
	}
	if got, want := primary.Labels, map[string]string{"tier": "kind", "owner": "node"}; !reflect.DeepEqual(
		got,
		want,
	) {
		t.Fatalf("effective labels = %q, want %q", got, want)
	}
	if primary.Config == nil || len(primary.Config.Vars) != 4 ||
		string(primary.Config.Vars["override"].Raw) != `"node"` {
		t.Fatalf("effective config vars = %#v", primary.Config)
	}
	if primary.Certificate == nil || primary.Certificate.Issue == nil ||
		!*primary.Certificate.Issue || primary.Certificate.KeySize != 4096 ||
		primary.Certificate.ValidityDuration != "24h0m0s" ||
		!reflect.DeepEqual(primary.Certificate.SANs, []string{"node.example"}) {
		t.Fatalf("effective certificate = %#v", primary.Certificate)
	}
	if primary.MgmtIPv4 != "192.0.2.10/24" {
		t.Fatalf("primary management address = %q", primary.MgmtIPv4)
	}
	if primary.Components == nil || len(primary.Components) != 0 {
		t.Fatalf("explicit component clearing was not preserved: %#v", primary.Components)
	}

	secondary := compiled.Nodes["secondary"]
	if secondary == nil || secondary.NetworkMode != "container:primary" ||
		secondary.MgmtIPv4 != "" || len(secondary.Components) != 1 ||
		secondary.Components[0].Slot != "inherited" {
		t.Fatalf("effective secondary Node = %#v", secondary)
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

	_, err := clabernetescompiler.CompileTopology(
		&claberneteslogging.FakeInstance{},
		topology,
	)
	if err == nil {
		t.Fatal("expected invalid exposePorts entry to fail compilation")
	}

	unsupported := &clabernetescompiler.UnsupportedFeaturesError{}
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

	compiled, err := clabernetescompiler.CompileTopology(
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

			_, err := clabernetescompiler.CompileTopology(
				&claberneteslogging.FakeInstance{},
				topology,
			)
			if err == nil {
				t.Fatal("expected invalid exposePorts entries to fail compilation")
			}

			unsupported := &clabernetescompiler.UnsupportedFeaturesError{}
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

	expected := []clabernetescompiler.CompiledLink{
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
	compiled, err := compileDefinition(t, `
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
`)
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
	compiled, err := compileDefinition(t, `
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
`)
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
	compiled, err := compileDefinition(t, `
name: srsim-components-empty
topology:
  nodes:
    device:
      kind: nokia_srsim
      image: internal-registry/norc/sr-sim:25.10.R4
      type: SR-1-48D
      license: license.txt
      components: []
`)
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

func TestCompileTopologyRejectsLossyCompatibility(t *testing.T) {
	definition := `
name: strict-test
mgmt:
  ipv4-subnet: 172.30.30.0/24
topology:
  nodes:
    n1:
      kind: linux
      image: ghcr.io/srl-labs/network-multitool
      cpu-set: 0-1
      ports: [22022:22/tcp]
  links:
    - endpoints: ["n1:eth1", "host:veth-review"]
      labels: {purpose: review}
      vars: {delay: 10}
`

	_, err := compileDefinition(t, definition)
	if err == nil {
		t.Fatal("strict compilation accepted lossy topology")
	}

	unsupported := &clabernetescompiler.UnsupportedFeaturesError{}
	if !errors.As(err, &unsupported) {
		t.Fatalf("expected UnsupportedFeaturesError, got %T: %s", err, err)
	}

	codes := make([]string, 0, len(unsupported.Diagnostics))
	for _, diagnostic := range unsupported.Diagnostics {
		codes = append(codes, diagnostic.Code)
	}

	for _, expected := range []string{
		"unsupported-field",
		"unsupported-link-labels",
		"unsupported-link-vars",
	} {
		if !slices.Contains(codes, expected) {
			t.Errorf("expected diagnostic %q, got %v", expected, codes)
		}
	}

	// Host port pinning is lossless to drop inside the cluster, so it warns instead of failing.
	if slices.Contains(codes, "host-port-pinning") {
		t.Errorf("host port pinning should be a warning, got fatal diagnostics %v", codes)
	}
}

func TestCompileTopologyIgnoresDockerOnlyManagementFields(t *testing.T) {
	t.Parallel()

	compiled, err := compileDefinition(t, `
name: docker-management
mgmt:
  network: st
  bridge: br-st
  mtu: 1500
  external-access: false
  skip-when-unused: false
  driver-opts: {a: b}
  ipv4-subnet: 172.30.30.0/24
topology:
  nodes:
    n1: {kind: linux, image: alpine}
`)
	if err != nil {
		t.Fatalf("Docker-only management fields must be accepted and ignored, got: %s", err)
	}

	if compiled.Mgmt == nil || compiled.Mgmt.IPv4Subnet != "172.30.30.0/24" {
		t.Fatalf("management policy was not preserved: %+v", compiled.Mgmt)
	}
}

func TestCompileTopologyWiresExtendedNodeVocabulary(t *testing.T) {
	t.Parallel()

	compiled, err := compileDefinition(t, `
name: extended-vocabulary
topology:
  defaults:
    image-pull-policy: IfNotPresent
  kinds:
    linux:
      restart-policy: unless-stopped
      startup-delay: 7
      cpu: 1.5
      memory: 1Gb
      link-apply-mode: live
      healthcheck:
        test: [CMD, "true"]
        interval: 10
        timeout: 3
        retries: 2
        start-period: 5
  nodes:
    n1:
      kind: linux
      image: alpine
      aliases: [n1-alt]
`)
	if err != nil {
		t.Fatalf("extended vocabulary must compile, got: %s", err)
	}

	node, ok := compiled.Nodes["n1"]
	if !ok {
		t.Fatalf("compiled nodes = %+v, want n1", compiled.Nodes)
	}

	if node.ImagePullPolicy != "IfNotPresent" || node.RestartPolicy != "unless-stopped" ||
		node.StartupDelay != 7 || node.CPU != 1.5 || node.Memory != "1Gb" ||
		node.LinkApplyMode != "live" {
		t.Fatalf("inherited vocabulary was not flattened onto the node: %+v", node)
	}

	if node.Healthcheck == nil || node.Healthcheck.Interval != 10 ||
		node.Healthcheck.StartPeriod != 5 ||
		!slices.Equal(node.Healthcheck.Test, []string{"CMD", "true"}) {
		t.Fatalf("inherited healthcheck was not flattened onto the node: %+v", node.Healthcheck)
	}

	if !slices.Equal(node.Aliases, []string{"n1-alt"}) {
		t.Fatalf("aliases = %+v, want [n1-alt]", node.Aliases)
	}
}

func TestCompileTopologyRejectsUnportableVocabularyValues(t *testing.T) {
	t.Parallel()

	_, err := compileDefinition(t, `
name: unportable-vocabulary
topology:
  nodes:
    n1:
      kind: linux
      image: alpine
      restart-policy: "on-failure"
      image-pull-policy: sometimes
      link-apply-mode: bounce
      aliases: [Bad_Alias, n2, shared]
    n2:
      kind: linux
      image: alpine
      aliases: [shared]
`)
	if err == nil {
		t.Fatal("unportable vocabulary values must fail compilation")
	}

	unsupported := &clabernetescompiler.UnsupportedFeaturesError{}
	if !errors.As(err, &unsupported) {
		t.Fatalf("expected UnsupportedFeaturesError, got %T: %s", err, err)
	}

	codes := make([]string, 0, len(unsupported.Diagnostics))
	for _, diagnostic := range unsupported.Diagnostics {
		codes = append(codes, diagnostic.Code)
	}

	for _, expected := range []string{
		"unsupported-restart-policy",
		"unsupported-image-pull-policy",
		"unsupported-link-apply-mode",
		"invalid-alias",
		"duplicate-alias",
	} {
		if !slices.Contains(codes, expected) {
			t.Errorf("expected diagnostic %q, got %v", expected, codes)
		}
	}
}

func TestCompileTopologyDocumentsRejectedVocabulary(t *testing.T) {
	t.Parallel()

	_, err := compileDefinition(t, `
name: rejected-vocabulary
topology:
  nodes:
    n1:
      kind: linux
      image: alpine
      runtime: podman
      stages:
        create:
          wait-for:
            - node: n2
              stage: healthy
`)
	if err == nil {
		t.Fatal("rejected vocabulary must fail compilation")
	}

	unsupported := &clabernetescompiler.UnsupportedFeaturesError{}
	if !errors.As(err, &unsupported) {
		t.Fatalf("expected UnsupportedFeaturesError, got %T: %s", err, err)
	}

	rejected := map[string]bool{}

	for _, diagnostic := range unsupported.Diagnostics {
		if diagnostic.Code == "unsupported-field" &&
			strings.Contains(diagnostic.Message, "is rejected:") {
			rejected[diagnostic.Path] = true
		}
	}

	for _, field := range []string{"runtime", "stages"} {
		if !rejected[field] {
			t.Errorf(
				"expected documented rejection for field %q, got diagnostics %+v",
				field,
				unsupported.Diagnostics,
			)
		}
	}
}

func TestCompileTopologyNormalizesHostPinnedPortsAndGroups(t *testing.T) {
	t.Parallel()

	compiled, err := compileDefinition(t, `
name: lossy-but-portable
topology:
  groups:
    telemetry:
      env:
        ROLE: telemetry
  nodes:
    n1:
      kind: linux
      image: alpine
      group: telemetry
      ports: ["9090:9090", "127.0.0.1:3000:3000/tcp"]
`)
	if err != nil {
		t.Fatalf("host-pinned ports and groups must compile, got: %s", err)
	}

	node := compiled.Nodes["n1"]
	if node == nil {
		t.Fatal("compiled topology has no node n1")
	}

	if node.Group != "telemetry" {
		t.Fatalf("group was not preserved: %+v", node)
	}

	if node.Env["ROLE"] != "telemetry" {
		t.Fatalf("group-scoped configuration was not inherited: %+v", node.Env)
	}

	wantPorts := []string{"9090", "3000/tcp"}
	if !reflect.DeepEqual(node.Ports, wantPorts) {
		t.Fatalf("ports = %q, want normalized Pod-side %q", node.Ports, wantPorts)
	}
}

func TestCompileTopologyWithOptionsRejectsRemovedWarningMode(t *testing.T) {
	t.Parallel()

	topology := &clabernetesapisv1alpha1.Topology{}
	topology.Spec.Definition.Containerlab = `
name: removed-warning-mode
topology:
  nodes:
    n1: {kind: linux, image: alpine}
`

	_, err := clabernetescompiler.CompileTopologyWithOptions(
		&claberneteslogging.FakeInstance{},
		topology,
		clabernetescompiler.CompileOptions{
			UnsupportedFieldPolicy: clabernetescompiler.UnsupportedFieldPolicy("warn"),
		},
	)
	if err == nil {
		t.Fatal("compiler accepted removed warning mode")
	}
}

//nolint:wsl_v5 // Keeping the expected diagnostic set beside compilation makes ordering explicit.
func TestCompileTopologyDiagnosticsAreSortedAndLocated(t *testing.T) {
	definition := `
name: diagnostic-locations
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
	_, err := compileDefinition(t, definition)
	if err == nil {
		t.Fatal("compiler accepted lossy Link metadata")
	}
	unsupported := &clabernetescompiler.UnsupportedFeaturesError{}
	if !errors.As(err, &unsupported) {
		t.Fatalf("expected UnsupportedFeaturesError, got %T: %s", err, err)
	}

	want := []clabernetescompiler.Diagnostic{
		{
			Code: "unsupported-link-labels", Path: "topology.links[0].labels",
			Message: "link labels are not preserved by the c9s Link API",
		},
		{
			Code: "unsupported-link-vars", Path: "topology.links[1].vars",
			Message: "link vars are not preserved by the c9s Link API",
		},
	}
	if !reflect.DeepEqual(unsupported.Diagnostics, want) {
		t.Fatalf("diagnostics = %#v, want %#v", unsupported.Diagnostics, want)
	}
}

func TestCompileTopologyAlwaysRejectsImpossibleStructures(t *testing.T) {
	tests := map[string]string{
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

			_, err := clabernetescompiler.CompileTopology(
				&claberneteslogging.FakeInstance{},
				topology,
			)
			if err == nil {
				t.Fatal("compiler accepted structurally unsupported topology")
			}

			unsupported := &clabernetescompiler.UnsupportedFeaturesError{}
			if !errors.As(err, &unsupported) {
				t.Fatalf("expected UnsupportedFeaturesError, got %T: %s", err, err)
			}
		})
	}
}

//nolint:wsl_v5 // The single result assertion follows compilation directly.
func TestCompileTopologyDefersOpaqueKindCapabilityToImportedPlanner(t *testing.T) {
	compiled, err := compileDefinition(t, `
name: opaque-kind-test
topology:
  nodes:
    node-a: {kind: package-owned-kind}
`)
	if err != nil {
		t.Fatalf("compiling opaque imported kind: %v", err)
	}
	if compiled.Nodes["node-a"] == nil ||
		compiled.Nodes["node-a"].Kind != "package-owned-kind" {
		t.Fatalf("compiled opaque Node = %#v", compiled.Nodes["node-a"])
	}
}
