package topology_test

import (
	"encoding/json"
	"fmt"
	"testing"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/srl-labs/clabernetes/config"
	clabernetescontrollerstopology "github.com/srl-labs/clabernetes/controllers/topology"
	claberneteslogging "github.com/srl-labs/clabernetes/logging"
	clabernetestesthelper "github.com/srl-labs/clabernetes/testhelper"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const renderLinksTestName = "linkcr/render-links"

func TestRenderAllLinks(t *testing.T) {
	cases := []struct {
		name           string
		owningTopology *clabernetesapisv1alpha1.Topology
		tunnels        map[string][]*clabernetesapisv1alpha1.PointToPointTunnel
	}{
		{
			name: "simple",
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "render-links-test",
					Namespace: "clabernetes",
				},
			},
			tunnels: map[string][]*clabernetesapisv1alpha1.PointToPointTunnel{
				"srl1": {
					{
						LocalNode:       "srl1",
						LocalInterface:  "e1-1",
						RemoteNode:      "srl2",
						RemoteInterface: "e1-1",
						Destination:     "render-links-test-srl2-vx.clabernetes.svc.cluster.local",
					},
				},
				"srl2": {
					{
						LocalNode:       "srl2",
						LocalInterface:  "e1-1",
						RemoteNode:      "srl1",
						RemoteInterface: "e1-1",
						Destination:     "render-links-test-srl1-vx.clabernetes.svc.cluster.local",
					},
				},
			},
		},
		{
			name: "grouped-nodes",
			owningTopology: &clabernetesapisv1alpha1.Topology{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "render-links-test",
					Namespace: "clabernetes",
				},
			},
			tunnels: map[string][]*clabernetesapisv1alpha1.PointToPointTunnel{
				// srl1a is a secondary node in srl1's group -- the launcher node (map key) is the
				// primary node while the tunnel local node is the secondary itself
				"srl1": {
					{
						LocalNode:       "srl1a",
						LocalInterface:  "e1-1",
						RemoteNode:      "srl2",
						RemoteInterface: "e1-1",
						Destination:     "render-links-test-srl2-vx.clabernetes.svc.cluster.local",
					},
				},
				"srl2": {
					{
						LocalNode:       "srl2",
						LocalInterface:  "e1-1",
						RemoteNode:      "srl1a",
						RemoteInterface: "e1-1",
						Destination:     "render-links-test-srl1-vx.clabernetes.svc.cluster.local",
					},
				},
			},
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				reconciler := clabernetescontrollerstopology.NewLinkReconciler(
					&claberneteslogging.FakeInstance{},
					clabernetesconfig.GetFakeManager,
				)

				got := reconciler.RenderAll(
					testCase.owningTopology,
					testCase.tunnels,
				)

				if *clabernetestesthelper.Update {
					clabernetestesthelper.WriteTestFixtureJSON(
						t,
						fmt.Sprintf("golden/%s/%s.json", renderLinksTestName, testCase.name),
						got,
					)
				}

				var want []*clabernetesapisv1alpha1.Link

				err := json.Unmarshal(
					clabernetestesthelper.ReadTestFixtureFile(
						t,
						fmt.Sprintf("golden/%s/%s.json", renderLinksTestName, testCase.name),
					),
					&want,
				)
				if err != nil {
					t.Fatal(err)
				}

				clabernetestesthelper.MarshaledEqual(t, got, want)
			},
		)
	}
}

func TestAllocateTunnelIDs(t *testing.T) {
	cases := []struct {
		name          string
		existingLinks map[string]*clabernetesapisv1alpha1.Link
		renderedLinks []*clabernetesapisv1alpha1.Link
		maxID         int
		expectedIDs   []int
		expectError   bool
	}{
		{
			name:          "all-new",
			existingLinks: nil,
			renderedLinks: []*clabernetesapisv1alpha1.Link{
				{ObjectMeta: metav1.ObjectMeta{Name: "link-a"}},
				{ObjectMeta: metav1.ObjectMeta{Name: "link-b"}},
			},
			expectedIDs: []int{1, 2},
			maxID:       10,
		},
		{
			name: "keep-existing-ids",
			existingLinks: map[string]*clabernetesapisv1alpha1.Link{
				"link-b": {
					ObjectMeta: metav1.ObjectMeta{Name: "link-b"},
					Status:     clabernetesapisv1alpha1.LinkStatus{TunnelID: 1},
				},
			},
			renderedLinks: []*clabernetesapisv1alpha1.Link{
				{ObjectMeta: metav1.ObjectMeta{Name: "link-a"}},
				{ObjectMeta: metav1.ObjectMeta{Name: "link-b"}},
				{ObjectMeta: metav1.ObjectMeta{Name: "link-c"}},
			},
			expectedIDs: []int{2, 1, 3},
			maxID:       10,
		},
		{
			name: "stale-existing-links-ignored",
			existingLinks: map[string]*clabernetesapisv1alpha1.Link{
				"link-gone": {
					ObjectMeta: metav1.ObjectMeta{Name: "link-gone"},
					Status:     clabernetesapisv1alpha1.LinkStatus{TunnelID: 1},
				},
			},
			renderedLinks: []*clabernetesapisv1alpha1.Link{
				{ObjectMeta: metav1.ObjectMeta{Name: "link-a"}},
			},
			expectedIDs: []int{1},
			maxID:       10,
		},
		{
			name: "duplicate-existing-ids-reallocated",
			existingLinks: map[string]*clabernetesapisv1alpha1.Link{
				"link-a": {
					ObjectMeta: metav1.ObjectMeta{Name: "link-a"},
					Status:     clabernetesapisv1alpha1.LinkStatus{TunnelID: 7},
				},
				"link-b": {
					ObjectMeta: metav1.ObjectMeta{Name: "link-b"},
					Status:     clabernetesapisv1alpha1.LinkStatus{TunnelID: 7},
				},
			},
			renderedLinks: []*clabernetesapisv1alpha1.Link{
				{ObjectMeta: metav1.ObjectMeta{Name: "link-a"}},
				{ObjectMeta: metav1.ObjectMeta{Name: "link-b"}},
			},
			maxID:       10,
			expectedIDs: []int{7, 1},
		},
		{
			name: "invalid-existing-ids-reallocated",
			existingLinks: map[string]*clabernetesapisv1alpha1.Link{
				"link-a": {
					ObjectMeta: metav1.ObjectMeta{Name: "link-a"},
					Status:     clabernetesapisv1alpha1.LinkStatus{TunnelID: -1},
				},
				"link-b": {
					ObjectMeta: metav1.ObjectMeta{Name: "link-b"},
					Status:     clabernetesapisv1alpha1.LinkStatus{TunnelID: 11},
				},
			},
			renderedLinks: []*clabernetesapisv1alpha1.Link{
				{ObjectMeta: metav1.ObjectMeta{Name: "link-a"}},
				{ObjectMeta: metav1.ObjectMeta{Name: "link-b"}},
			},
			maxID:       10,
			expectedIDs: []int{1, 2},
		},
		{
			name: "id-space-exhausted",
			renderedLinks: []*clabernetesapisv1alpha1.Link{
				{ObjectMeta: metav1.ObjectMeta{Name: "link-a"}},
				{ObjectMeta: metav1.ObjectMeta{Name: "link-b"}},
			},
			maxID:       1,
			expectError: true,
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				err := clabernetescontrollerstopology.AllocateTunnelIDs(
					testCase.existingLinks,
					testCase.renderedLinks,
					testCase.maxID,
				)
				if testCase.expectError {
					if err == nil {
						t.Fatal("expected tunnel allocation to fail")
					}

					return
				}

				if err != nil {
					t.Fatal(err)
				}

				for idx, renderedLink := range testCase.renderedLinks {
					if renderedLink.Status.TunnelID != testCase.expectedIDs[idx] {
						clabernetestesthelper.FailOutput(
							t,
							renderedLink.Status.TunnelID,
							testCase.expectedIDs[idx],
						)
					}
				}
			},
		)
	}
}

func TestLinkResourceNamePreservesDistinctInterfaces(t *testing.T) {
	topology := &clabernetesapisv1alpha1.Topology{
		ObjectMeta: metav1.ObjectMeta{Name: "collision"},
	}

	underscoreName := clabernetescontrollerstopology.LinkResourceName(
		topology,
		clabernetesapisv1alpha1.LinkEndpointSpec{NodeName: "r1", InterfaceName: "e1_1"},
		clabernetesapisv1alpha1.LinkEndpointSpec{NodeName: "r2", InterfaceName: "e2_1"},
	)
	hyphenName := clabernetescontrollerstopology.LinkResourceName(
		topology,
		clabernetesapisv1alpha1.LinkEndpointSpec{NodeName: "r1", InterfaceName: "e1-1"},
		clabernetesapisv1alpha1.LinkEndpointSpec{NodeName: "r2", InterfaceName: "e2-1"},
	)

	if underscoreName == hyphenName {
		t.Fatalf("distinct interfaces collapsed to Link name %q", underscoreName)
	}
}
