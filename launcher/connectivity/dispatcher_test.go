package connectivity //nolint:testpackage // tests exercise dispatcher transition ordering

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
)

type fakeFlavorManager struct {
	flavor     clabernetesapisv1alpha1.LinkConnectivity
	current    map[string]*Tunnel
	operations *[]string
}

func (m *fakeFlavorManager) start(initialTunnels []*Tunnel) error {
	m.current = map[string]*Tunnel{}

	return m.reconcile(initialTunnels)
}

func (m *fakeFlavorManager) reconcile(tunnels []*Tunnel) error {
	desired := tunnelsByTermination(tunnels)
	currentKeys := sortedTunnelKeys(m.current)
	desiredKeys := sortedTunnelKeys(desired)

	for _, key := range currentKeys {
		if _, remains := desired[key]; remains {
			continue
		}

		*m.operations = append(*m.operations, "delete:"+string(m.flavor)+":"+key)
	}

	for _, key := range desiredKeys {
		if _, exists := m.current[key]; exists {
			continue
		}

		*m.operations = append(*m.operations, "create:"+string(m.flavor)+":"+key)
	}

	m.current = desired

	return nil
}

func sortedTunnelKeys(tunnels map[string]*Tunnel) []string {
	keys := make([]string, 0, len(tunnels))
	for key := range tunnels {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	return keys
}

func TestDispatcherSupportsMixedLinkFlavors(t *testing.T) {
	operations := []string{}
	vxlan := &fakeFlavorManager{
		flavor:     clabernetesapisv1alpha1.LinkConnectivityVXLAN,
		operations: &operations,
	}
	slurpeeth := &fakeFlavorManager{
		flavor:     clabernetesapisv1alpha1.LinkConnectivitySlurpeeth,
		operations: &operations,
	}
	manager := &dispatcherManager{vxlan: vxlan, slurpeeth: slurpeeth}

	vxlanTunnel := dispatcherTestTunnel(
		"node-a",
		"eth1",
		clabernetesapisv1alpha1.LinkConnectivityVXLAN,
	)
	slurpeethTunnel := dispatcherTestTunnel(
		"node-b",
		"eth1",
		clabernetesapisv1alpha1.LinkConnectivitySlurpeeth,
	)

	err := manager.start([]*Tunnel{vxlanTunnel, slurpeethTunnel})
	if err != nil {
		t.Fatalf("failed starting mixed connectivity: %s", err)
	}

	if !manager.slurpStarted || len(vxlan.current) != 1 || len(slurpeeth.current) != 1 {
		t.Fatalf(
			"expected one tunnel per flavor, vxlan=%v slurpeeth=%v",
			vxlan.current,
			slurpeeth.current,
		)
	}

	expectedOperations := []string{
		"create:vxlan:node-a:eth1",
		"create:slurpeeth:node-b:eth1",
	}
	if !reflect.DeepEqual(operations, expectedOperations) {
		t.Fatalf("expected mixed flavor operations %v, got %v", expectedOperations, operations)
	}
}

func TestDispatcherDeletesOldFlavorBeforeCreatingNew(t *testing.T) {
	operations := []string{}
	vxlan := &fakeFlavorManager{
		flavor:     clabernetesapisv1alpha1.LinkConnectivityVXLAN,
		operations: &operations,
	}
	slurpeeth := &fakeFlavorManager{
		flavor:     clabernetesapisv1alpha1.LinkConnectivitySlurpeeth,
		operations: &operations,
	}
	manager := &dispatcherManager{vxlan: vxlan, slurpeeth: slurpeeth}

	err := manager.start([]*Tunnel{dispatcherTestTunnel(
		"node-a",
		"eth1",
		clabernetesapisv1alpha1.LinkConnectivityVXLAN,
	)})
	if err != nil {
		t.Fatal(err)
	}

	operations = nil

	err = manager.reconcile([]*Tunnel{dispatcherTestTunnel(
		"node-a",
		"eth1",
		clabernetesapisv1alpha1.LinkConnectivitySlurpeeth,
	)})
	if err != nil {
		t.Fatal(err)
	}

	assertOperationOrder(
		t,
		operations,
		"delete:vxlan:node-a:eth1",
		"create:slurpeeth:node-a:eth1",
	)

	operations = nil

	err = manager.reconcile([]*Tunnel{dispatcherTestTunnel(
		"node-a",
		"eth1",
		clabernetesapisv1alpha1.LinkConnectivityVXLAN,
	)})
	if err != nil {
		t.Fatal(err)
	}

	assertOperationOrder(
		t,
		operations,
		"delete:slurpeeth:node-a:eth1",
		"create:vxlan:node-a:eth1",
	)
}

func TestDispatcherRejectsDuplicateLocalTermination(t *testing.T) {
	_, _, err := partitionTunnels([]*Tunnel{
		dispatcherTestTunnel(
			"node-a",
			"eth1",
			clabernetesapisv1alpha1.LinkConnectivityVXLAN,
		),
		dispatcherTestTunnel(
			"node-a",
			"eth1",
			clabernetesapisv1alpha1.LinkConnectivitySlurpeeth,
		),
	})
	if err == nil || !strings.Contains(err.Error(), "node-a:eth1") {
		t.Fatalf("expected deterministic local termination conflict, got %v", err)
	}
}

func dispatcherTestTunnel(
	localNode,
	localInterface string,
	connectivity clabernetesapisv1alpha1.LinkConnectivity,
) *Tunnel {
	return &Tunnel{
		TunnelID:       1,
		Connectivity:   connectivity,
		LocalNode:      localNode,
		LocalInterface: localInterface,
	}
}

func assertOperationOrder(t *testing.T, operations []string, first, second string) {
	t.Helper()

	firstIndex, secondIndex := -1, -1

	for idx, operation := range operations {
		switch operation {
		case first:
			firstIndex = idx
		case second:
			secondIndex = idx
		}
	}

	if firstIndex == -1 || secondIndex == -1 || firstIndex >= secondIndex {
		t.Fatalf("expected %q before %q, got %v", first, second, operations)
	}
}
