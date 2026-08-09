package topology_test

import (
	"reflect"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetescontrollerstopology "github.com/clabernetes/clabernetes/controllers/topology"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
)

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
  kinds:
    nokia_srlinux:
      image: ghcr.io/nokia/srlinux:latest
      type: ixrd2
      env:
        FROM_KIND: "1"
        OVERRIDDEN: kind
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

	if !reflect.DeepEqual(srl1.Ports, []string{"21022:22/tcp"}) {
		t.Fatalf("expected node ports, got %v", srl1.Ports)
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
