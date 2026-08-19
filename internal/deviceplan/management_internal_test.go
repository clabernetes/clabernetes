package deviceplan

import (
	"reflect"
	"testing"
)

func TestCompleteRuntimeManagementFillsUnaddressedNodesWithPodAddress(t *testing.T) {
	t.Parallel()

	nodes := []NodeInput{{ID: "node-a"}, {ID: "node-b"}}
	plans := []ManagementPlan{
		{NodeID: "node-a", InterfaceName: "pkg-mgmt0"},
		{NodeID: "node-b", InterfaceName: "pkg-mgmt0"},
	}
	explicit := []ManagementInput{{NodeID: "node-a", IPv4: "192.0.2.10/24"}}

	completed := completeRuntimeManagement(explicit, nodes, plans, "10.244.1.181", "10.244.1.1")
	want := []ManagementInput{
		{NodeID: "node-a", InterfaceName: "pkg-mgmt0", IPv4: "192.0.2.10/24"},
		{
			NodeID:        "node-b",
			InterfaceName: "pkg-mgmt0",
			IPv4:          "10.244.1.181",
			IPv4Gateway:   "10.244.1.1",
		},
	}
	if !reflect.DeepEqual(completed, want) {
		t.Fatalf("completed management = %#v, want %#v", completed, want)
	}

	unaddressed := completeRuntimeManagement(explicit, nodes, plans, "", "")
	if len(unaddressed) != 1 || unaddressed[0].IPv4 != "192.0.2.10/24" ||
		unaddressed[0].InterfaceName != "pkg-mgmt0" {
		t.Fatalf("interface-only completion = %#v", unaddressed)
	}

	six := completeRuntimeManagement(nil, nodes[:1], plans, "2001:db8::10", "")
	if len(six) != 1 || six[0].IPv6 != "2001:db8::10" || six[0].IPv4 != "" {
		t.Fatalf("IPv6 completion = %#v", six)
	}
}
