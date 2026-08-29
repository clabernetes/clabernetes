package deviceplan

import (
	"reflect"
	"testing"

	clabtypes "github.com/srl-labs/containerlab/types"
)

func TestCompleteRuntimeManagementRehydratesInterfaceAndDNSOnly(t *testing.T) {
	t.Parallel()

	plans := []ManagementPlan{
		{NodeID: "node-a", InterfaceName: "pkg-mgmt0"},
		{NodeID: "node-b", InterfaceName: "pkg-mgmt0"},
	}
	explicit := []ManagementInput{{NodeID: "node-a", IPv4: "192.0.2.10/24"}}

	completed := completeRuntimeManagement(explicit, plans, []string{"10.96.0.10"})

	want := []ManagementInput{
		{
			NodeID:        "node-a",
			InterfaceName: "pkg-mgmt0",
			IPv4:          "192.0.2.10/24",
			DNS:           DNSConfig{Servers: []string{"10.96.0.10"}},
		},
	}
	if !reflect.DeepEqual(completed, want) {
		t.Fatalf("completed management = %#v, want %#v", completed, want)
	}

	// Nodes the controller left without an input never receive a synthesized identity: the
	// allocation boundary is the controller, and planning fails closed instead.
	if len(completed) != len(explicit) {
		t.Fatalf("runtime completion synthesized identities: %#v", completed)
	}
}

func TestCompleteRuntimeManagementPreservesControllerDNS(t *testing.T) {
	t.Parallel()

	explicit := []ManagementInput{
		{NodeID: "node-a", IPv4: "192.0.2.10/24", DNS: DNSConfig{Servers: []string{"192.0.2.53"}}},
	}

	completed := completeRuntimeManagement(explicit, nil, []string{"10.96.0.10"})
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

func TestDefinitionManagementAddressMatches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		definition string
		allocated  string
		want       bool
	}{
		{name: "empty definition accepts anything", definition: "", allocated: "", want: true},
		{
			name:       "bare definition matches prefixed allocation",
			definition: "172.80.80.45",
			allocated:  "172.80.80.45/24",
			want:       true,
		},
		{
			name:       "bare definition rejects a different address",
			definition: "172.80.80.45",
			allocated:  "172.80.80.46/24",
			want:       false,
		},
		{
			name:       "prefixed definition must match exactly",
			definition: "172.80.80.45/24",
			allocated:  "172.80.80.45/24",
			want:       true,
		},
		{
			name:       "prefixed definition rejects a different prefix",
			definition: "172.80.80.45/25",
			allocated:  "172.80.80.45/24",
			want:       false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := definitionManagementAddressMatches(test.definition, test.allocated)
			if got != test.want {
				t.Fatalf(
					"definitionManagementAddressMatches(%q, %q) = %v, want %v",
					test.definition,
					test.allocated,
					got,
					test.want,
				)
			}
		})
	}
}

func TestCapAutomaticManagementIPMTU(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		mtu  int
		want int
	}{
		{name: "path below conventional maximum", mtu: 1450, want: 1450},
		{name: "path at conventional maximum", mtu: 1500, want: 1500},
		{name: "jumbo path", mtu: 8084, want: 1500},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := capAutomaticManagementIPMTU(test.mtu); got != test.want {
				t.Fatalf("capAutomaticManagementIPMTU(%d) = %d, want %d", test.mtu, got, test.want)
			}
		})
	}
}
