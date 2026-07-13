package node_test

import (
	"testing"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/srl-labs/clabernetes/config"
	clabernetescontrollersnode "github.com/srl-labs/clabernetes/controllers/node"
)

func testExposeNode(
	name string,
	ports []string,
	status *clabernetesapisv1alpha1.NodeExposedPorts,
) *clabernetesapisv1alpha1.Node {
	node := &clabernetesapisv1alpha1.Node{}
	node.Name = name
	node.Namespace = "clabernetes"
	node.Spec.Ports = ports
	node.Status.ExposedPorts = status

	return node
}

func testResolvedProfile(
	t *testing.T,
	mutate func(profile *clabernetescontrollersnode.ResolvedProfile),
) *clabernetescontrollersnode.ResolvedProfile {
	t.Helper()

	resolved, err := clabernetescontrollersnode.ResolveProfile(
		testProfileNode("whatever", nil),
		nil,
		clabernetesconfig.GetFakeManager,
	)
	if err != nil {
		t.Fatalf("unexpected error resolving base profile: %s", err)
	}

	if mutate != nil {
		mutate(resolved)
	}

	return resolved
}

func findPort(
	exposedPorts *clabernetesapisv1alpha1.NodeExposedPorts,
	destinationPort int,
	protocol string,
) *clabernetesapisv1alpha1.NodeExposedPort {
	for idx := range exposedPorts.Ports {
		if exposedPorts.Ports[idx].DestinationPort == destinationPort &&
			exposedPorts.Ports[idx].Protocol == protocol {
			return &exposedPorts.Ports[idx]
		}
	}

	return nil
}

func TestResolveExposedPortsAutoExpose(t *testing.T) {
	exposedPorts, err := clabernetescontrollersnode.ResolveExposedPorts(
		testExposeNode("srl1", nil, nil),
		testResolvedProfile(t, nil),
		nil,
	)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if exposedPorts == nil {
		t.Fatal("expected auto expose default allocations, got nil")
	}

	// 13 default tcp ports + 1 udp (snmp)
	if len(exposedPorts.Ports) != 14 {
		t.Fatalf("expected 14 default port allocations, got %d", len(exposedPorts.Ports))
	}

	sshPort := findPort(exposedPorts, 22, "TCP")
	if sshPort == nil || sshPort.ExposePort < 60_000 {
		t.Fatalf("expected ssh allocation in the 60000+ range, got %+v", sshPort)
	}

	snmpPort := findPort(exposedPorts, 161, "UDP")
	if snmpPort == nil || snmpPort.ExposePort != 60_000 {
		t.Fatalf("expected snmp (udp pool) allocation 60000, got %+v", snmpPort)
	}
}

func TestResolveExposedPortsDisabled(t *testing.T) {
	exposedPorts, err := clabernetescontrollersnode.ResolveExposedPorts(
		testExposeNode("srl1", []string{"60123:57400/tcp"}, nil),
		testResolvedProfile(t, func(profile *clabernetescontrollersnode.ResolvedProfile) {
			profile.DisableExpose = true
		}),
		nil,
	)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if exposedPorts != nil {
		t.Fatalf("expected nil allocations with expose disabled, got %+v", exposedPorts)
	}
}

func TestResolveExposedPortsUserPortsHonored(t *testing.T) {
	exposedPorts, err := clabernetescontrollersnode.ResolveExposedPorts(
		testExposeNode("srl1", []string{"60123:57400/tcp", "830/tcp"}, nil),
		testResolvedProfile(t, func(profile *clabernetescontrollersnode.ResolvedProfile) {
			profile.DisableAutoExpose = true
		}),
		nil,
	)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if len(exposedPorts.Ports) != 2 {
		t.Fatalf("expected 2 allocations, got %+v", exposedPorts.Ports)
	}

	gnmiPort := findPort(exposedPorts, 57400, "TCP")
	if gnmiPort == nil || gnmiPort.ExposePort != 60123 {
		t.Fatalf("expected user provided expose port 60123 to be honored, got %+v", gnmiPort)
	}

	netconfPort := findPort(exposedPorts, 830, "TCP")
	if netconfPort == nil || netconfPort.ExposePort != 60_000 {
		t.Fatalf("expected lowest free allocation 60000, got %+v", netconfPort)
	}
}

func TestResolveExposedPortsRetention(t *testing.T) {
	previous := &clabernetesapisv1alpha1.NodeExposedPorts{
		LoadBalancerAddress: "172.18.255.1",
		Ports: []clabernetesapisv1alpha1.NodeExposedPort{
			{ExposePort: 60_005, DestinationPort: 830, Protocol: "TCP"},
		},
	}

	exposedPorts, err := clabernetescontrollersnode.ResolveExposedPorts(
		testExposeNode("srl1", []string{"830/tcp", "57400/tcp"}, previous),
		testResolvedProfile(t, func(profile *clabernetescontrollersnode.ResolvedProfile) {
			profile.DisableAutoExpose = true
		}),
		nil,
	)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	netconfPort := findPort(exposedPorts, 830, "TCP")
	if netconfPort == nil || netconfPort.ExposePort != 60_005 {
		t.Fatalf("expected previous allocation 60005 to be retained, got %+v", netconfPort)
	}

	if exposedPorts.LoadBalancerAddress != "172.18.255.1" {
		t.Fatalf(
			"expected load balancer address to carry forward, got %q",
			exposedPorts.LoadBalancerAddress,
		)
	}
}

func TestResolveExposedPortsGroupTaken(t *testing.T) {
	taken := map[string]map[int]bool{
		"TCP": {60_000: true, 60_001: true},
	}

	exposedPorts, err := clabernetescontrollersnode.ResolveExposedPorts(
		testExposeNode("sim-a", []string{"830/tcp"}, nil),
		testResolvedProfile(t, func(profile *clabernetescontrollersnode.ResolvedProfile) {
			profile.DisableAutoExpose = true
		}),
		taken,
	)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	netconfPort := findPort(exposedPorts, 830, "TCP")
	if netconfPort == nil || netconfPort.ExposePort != 60_002 {
		t.Fatalf(
			"expected allocation to skip ports taken by group members, got %+v",
			netconfPort,
		)
	}
}
