package launcher //nolint:testpackage // tests exercise the unexported materializer

import (
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
	"gopkg.in/yaml.v3"
)

func materializeTestNode(
	name,
	image,
	networkMode string,
	exposedPorts []clabernetesapisv1alpha1.NodeExposedPort,
) *clabernetesapisv1alpha1.Node {
	node := &clabernetesapisv1alpha1.Node{}
	node.Name = name
	node.Namespace = "clabernetes"
	node.Spec.Image = image
	node.Spec.NetworkMode = networkMode

	if exposedPorts != nil {
		node.Status.ExposedPorts = &clabernetesapisv1alpha1.NodeExposedPorts{
			Ports: exposedPorts,
		}
	}

	return node
}

func materializeTestLink(
	name,
	nodeA,
	interfaceA,
	nodeB,
	interfaceB string,
	mtu,
	tunnelID int,
) clabernetesapisv1alpha1.Link {
	link := clabernetesapisv1alpha1.Link{}
	link.Name = name
	link.Namespace = "clabernetes"
	link.Spec.EndpointA = clabernetesapisv1alpha1.LinkEndpointSpec{
		NodeName:      nodeA,
		InterfaceName: interfaceA,
	}
	link.Spec.EndpointB = clabernetesapisv1alpha1.LinkEndpointSpec{
		NodeName:      nodeB,
		InterfaceName: interfaceB,
	}
	link.Spec.MTU = mtu
	link.Status.TunnelID = tunnelID

	return link
}

func endpointsOf(
	t *testing.T,
	config *clabernetesutilcontainerlab.Config,
) [][]string {
	t.Helper()

	endpoints := make([][]string, len(config.Topology.Links))

	for idx, link := range config.Topology.Links {
		endpoints[idx] = link.Endpoints
	}

	return endpoints
}

func TestMaterializeTopology(t *testing.T) {
	members := map[string]*clabernetesapisv1alpha1.Node{
		"srl1": materializeTestNode(
			"srl1",
			"ghcr.io/nokia/srlinux:latest",
			"",
			[]clabernetesapisv1alpha1.NodeExposedPort{
				{ExposePort: 60_000, DestinationPort: 22, Protocol: "TCP"},
			},
		),
		"sim-a": materializeTestNode(
			"sim-a",
			"ghcr.io/nokia/srlinux:latest",
			"container:srl1",
			[]clabernetesapisv1alpha1.NodeExposedPort{
				{ExposePort: 60_001, DestinationPort: 57400, Protocol: "TCP"},
			},
		),
	}

	links := []clabernetesapisv1alpha1.Link{
		// direct: both ends in this pod
		materializeTestLink("a-direct", "srl1", "e1-1", "sim-a", "eth1", 0, 0),
		// tunnel: remote end elsewhere
		materializeTestLink("b-tunnel", "srl1", "e1-2", "srl9", "e1-2", 9212, 7),
		// host link
		materializeTestLink("c-host", "sim-a", "eth2", "host", "ens5", 0, 0),
		// not ours at all
		materializeTestLink("d-unrelated", "srl8", "e1-1", "srl9", "e1-1", 0, 3),
	}

	config := materializeTopology("srl1", members, links, nil)

	if config.Name != "clabernetes-srl1" {
		t.Fatalf("expected topology name clabernetes-srl1, got %q", config.Name)
	}

	// all allocated expose ports publish on the launcher (primary) node
	expectedPorts := []string{"60000:22/TCP", "60001:57400/TCP"}

	actualPorts := config.Topology.Nodes["srl1"].Ports
	if len(actualPorts) != len(expectedPorts) {
		t.Fatalf("expected ports %v on the launcher node, got %v", expectedPorts, actualPorts)
	}

	if len(config.Topology.Nodes["sim-a"].Ports) != 0 {
		t.Fatalf(
			"expected no ports on the grouped node, got %v",
			config.Topology.Nodes["sim-a"].Ports,
		)
	}

	if config.Topology.Nodes["sim-a"].NetworkMode != "container:srl1" {
		t.Fatal("expected the grouped node definition to be materialized verbatim")
	}

	expectedEndpoints := [][]string{
		{"srl1:e1-1", "sim-a:eth1"},
		{"srl1:e1-2", "host:srl1-e1-2"},
		{"sim-a:eth2", "host:ens5"},
	}

	actualEndpoints := endpointsOf(t, config)

	if len(actualEndpoints) != len(expectedEndpoints) {
		t.Fatalf("expected link stanzas %v, got %v", expectedEndpoints, actualEndpoints)
	}

	for idx := range expectedEndpoints {
		for jdx := range expectedEndpoints[idx] {
			if actualEndpoints[idx][jdx] != expectedEndpoints[idx][jdx] {
				t.Fatalf(
					"expected link stanzas %v, got %v",
					expectedEndpoints,
					actualEndpoints,
				)
			}
		}
	}

	if config.Topology.Links[1].MTU != 9212 {
		t.Fatalf("expected tunnel stanza to carry mtu 9212, got %d", config.Topology.Links[1].MTU)
	}

	// the materialized config must round trip through the containerlab loader
	configBytes, err := yaml.Marshal(config)
	if err != nil {
		t.Fatalf("failed marshaling materialized config: %s", err)
	}

	_, err = clabernetesutilcontainerlab.LoadContainerlabConfig(string(configBytes))
	if err != nil {
		t.Fatalf("materialized config does not load as a containerlab config: %s", err)
	}
}

func TestTunnelsForLinks(t *testing.T) {
	members := map[string]*clabernetesapisv1alpha1.Node{
		"srl1":  materializeTestNode("srl1", "img", "", nil),
		"sim-a": materializeTestNode("sim-a", "img", "container:srl1", nil),
	}

	links := []clabernetesapisv1alpha1.Link{
		materializeTestLink("a-direct", "srl1", "e1-1", "sim-a", "eth1", 0, 0),
		materializeTestLink("b-tunnel", "srl1", "e1-2", "srl9", "e1-2", 9212, 7),
		materializeTestLink("c-unallocated", "sim-a", "eth3", "srl9", "e1-3", 0, 0),
	}

	tunnels := tunnelsForLinks(members, links)

	if len(tunnels) != 1 {
		t.Fatalf("expected exactly one tunnel, got %+v", tunnels)
	}

	if tunnels[0].TunnelID != 7 ||
		tunnels[0].Connectivity != clabernetesapisv1alpha1.LinkConnectivityVXLAN ||
		tunnels[0].Destination != "srl9-vx.clabernetes.svc.cluster.local" {
		t.Fatalf("unexpected tunnel: %+v", tunnels[0])
	}
}
