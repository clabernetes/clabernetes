package connectivity //nolint:testpackage // tests inject unexported VXLAN operations and state

import (
	"context"
	"reflect"
	"testing"

	claberneteserrors "github.com/srl-labs/clabernetes/errors"
	claberneteslogging "github.com/srl-labs/clabernetes/logging"
	"github.com/vishvananda/netlink"
)

func TestUpdateVxlanTunnelsCommitsCurrentState(t *testing.T) {
	initial := testTunnel("remote-a", 1)
	manager := &vxlanManager{
		common: &common{ctx: t.Context(), logger: &claberneteslogging.FakeInstance{}},
		currentTunnels: map[string]*Tunnel{
			tunnelTerminationKey(initial): initial,
		},
	}

	var (
		creates int
		deletes int
	)

	manager.createTunnel = func(string, string, string, int) error {
		creates++

		return nil
	}
	manager.deleteTunnel = func(context.Context, string, string) error {
		deletes++

		return nil
	}

	err := manager.updateVxlanTunnels(nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(manager.currentTunnels) != 0 || deletes != 1 {
		t.Fatalf(
			"delete did not commit empty state: tunnels=%d deletes=%d",
			len(manager.currentTunnels),
			deletes,
		)
	}

	err = manager.updateVxlanTunnels([]*Tunnel{initial})
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(manager.currentTunnels[tunnelTerminationKey(initial)], initial) ||
		creates != 1 {
		t.Fatalf(
			"create did not commit desired state: tunnels=%v creates=%d",
			manager.currentTunnels,
			creates,
		)
	}

	err = manager.updateVxlanTunnels([]*Tunnel{initial})
	if err != nil {
		t.Fatal(err)
	}

	if creates != 1 || deletes != 1 {
		t.Fatalf(
			"unchanged desired state caused operations: creates=%d deletes=%d",
			creates,
			deletes,
		)
	}

	changed := testTunnel("remote-b", 2)

	err = manager.updateVxlanTunnels([]*Tunnel{changed})
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(manager.currentTunnels[tunnelTerminationKey(changed)], changed) {
		t.Fatalf("replacement did not commit changed tunnel: %v", manager.currentTunnels)
	}

	if creates != 2 || deletes != 2 {
		t.Fatalf("replacement operations mismatch: creates=%d deletes=%d", creates, deletes)
	}
}

func TestUpdateVxlanTunnelsKeepsRetryableStateOnFailure(t *testing.T) {
	initial := testTunnel("remote-a", 1)
	manager := &vxlanManager{
		common: &common{ctx: t.Context(), logger: &claberneteslogging.FakeInstance{}},
		currentTunnels: map[string]*Tunnel{
			tunnelTerminationKey(initial): initial,
		},
		deleteTunnel: func(context.Context, string, string) error {
			return claberneteserrors.ErrConnectivity
		},
		createTunnel: func(string, string, string, int) error {
			return claberneteserrors.ErrConnectivity
		},
	}

	err := manager.updateVxlanTunnels(nil)
	if err == nil {
		t.Fatal("expected delete failure")
	}

	if !reflect.DeepEqual(manager.currentTunnels[tunnelTerminationKey(initial)], initial) {
		t.Fatal("failed delete was removed from current state")
	}

	manager.currentTunnels = map[string]*Tunnel{}

	err = manager.updateVxlanTunnels([]*Tunnel{initial})
	if err == nil {
		t.Fatal("expected create failure")
	}

	if len(manager.currentTunnels) != 0 {
		t.Fatal("failed create was committed to current state")
	}
}

func TestVxlanLinkMatchesAltName(t *testing.T) {
	link := &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{
		Name:     "clab-f13c4a22",
		AltNames: []string{"vx-multitool-eth1"},
	}}

	if !vxlanLinkMatchesName(link, "vx-multitool-eth1") {
		t.Fatal("expected long containerlab VXLAN altname to match")
	}

	if vxlanLinkMatchesName(link, "vx-other-eth1") {
		t.Fatal("unexpected VXLAN altname match")
	}
}

func testTunnel(destination string, tunnelID int) *Tunnel {
	return &Tunnel{
		TunnelID:        tunnelID,
		Destination:     destination,
		LocalNode:       "local",
		LocalInterface:  "eth1",
		RemoteNode:      "remote",
		RemoteInterface: "eth1",
	}
}
