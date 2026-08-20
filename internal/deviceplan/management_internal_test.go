package deviceplan

import (
	"reflect"
	"testing"

	clabtypes "github.com/srl-labs/containerlab/types"
)

func TestCompleteRuntimeManagementFillsUnaddressedNodesWithPodAddress(t *testing.T) {
	t.Parallel()

	nodes := []NodeInput{{ID: "node-a"}, {ID: "node-b"}}
	plans := []ManagementPlan{
		{NodeID: "node-a", InterfaceName: "pkg-mgmt0"},
		{NodeID: "node-b", InterfaceName: "pkg-mgmt0"},
	}
	explicit := []ManagementInput{{NodeID: "node-a", IPv4: "192.0.2.10/24"}}

	completed := completeRuntimeManagement(
		explicit, nodes, plans, "10.244.1.181", "10.244.1.1", []string{"10.96.0.10"},
	)

	want := []ManagementInput{
		{
			NodeID:        "node-a",
			InterfaceName: "pkg-mgmt0",
			IPv4:          "192.0.2.10/24",
			DNS:           DNSConfig{Servers: []string{"10.96.0.10"}},
		},
		{
			NodeID:        "node-b",
			InterfaceName: "pkg-mgmt0",
			IPv4:          "10.244.1.181",
			IPv4Gateway:   "10.244.1.1",
			DNS:           DNSConfig{Servers: []string{"10.96.0.10"}},
		},
	}
	if !reflect.DeepEqual(completed, want) {
		t.Fatalf("completed management = %#v, want %#v", completed, want)
	}

	unaddressed := completeRuntimeManagement(explicit, nodes, plans, "", "", nil)
	if len(unaddressed) != 1 || unaddressed[0].IPv4 != "192.0.2.10/24" ||
		unaddressed[0].InterfaceName != "pkg-mgmt0" {
		t.Fatalf("interface-only completion = %#v", unaddressed)
	}

	six := completeRuntimeManagement(nil, nodes[:1], plans, "2001:db8::10", "", nil)
	if len(six) != 1 || six[0].IPv6 != "2001:db8::10" || six[0].IPv4 != "" {
		t.Fatalf("IPv6 completion = %#v", six)
	}
}

func TestCompleteRuntimeManagementPreservesControllerDNS(t *testing.T) {
	t.Parallel()

	nodes := []NodeInput{{ID: "node-a"}}
	explicit := []ManagementInput{
		{NodeID: "node-a", IPv4: "192.0.2.10/24", DNS: DNSConfig{Servers: []string{"192.0.2.53"}}},
	}

	completed := completeRuntimeManagement(
		explicit, nodes, nil, "10.244.1.181", "10.244.1.1", []string{"10.96.0.10"},
	)
	if !reflect.DeepEqual(completed[0].DNS.Servers, []string{"192.0.2.53"}) {
		t.Fatalf("controller DNS was overwritten: %#v", completed[0].DNS)
	}
}

func TestApplyManagementDNSKeepsTopologyPrecedence(t *testing.T) {
	t.Parallel()

	topology := &clabtypes.DNSConfig{Servers: []string{"192.0.2.53"}}
	config := &clabtypes.NodeConfig{DNS: topology}

	applyManagementDNS(config, DNSConfig{Servers: []string{"10.96.0.10"}, Search: []string{"svc"}})

	if !reflect.DeepEqual(config.DNS.Servers, []string{"192.0.2.53"}) {
		t.Fatalf("topology DNS servers were overwritten: %#v", config.DNS)
	}

	if !reflect.DeepEqual(config.DNS.Search, []string{"svc"}) {
		t.Fatalf("unset search domains were not completed: %#v", config.DNS)
	}

	if topology.Search != nil {
		t.Fatalf("shared definition DNS struct was mutated: %#v", topology)
	}

	completed := &clabtypes.NodeConfig{}
	applyManagementDNS(completed, DNSConfig{Servers: []string{"10.96.0.10"}})

	if completed.DNS == nil || !reflect.DeepEqual(completed.DNS.Servers, []string{"10.96.0.10"}) {
		t.Fatalf("empty node DNS was not completed: %#v", completed.DNS)
	}

	member := &clabtypes.NodeConfig{NetworkMode: "container:owner"}
	applyManagementDNS(member, DNSConfig{Servers: []string{"10.96.0.10"}})

	if member.DNS != nil {
		t.Fatalf("container-network-mode member received DNS config: %#v", member.DNS)
	}
}
