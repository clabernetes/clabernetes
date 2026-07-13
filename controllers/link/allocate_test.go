package link_test

import (
	"testing"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetescontrollerslink "github.com/srl-labs/clabernetes/controllers/link"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func testLink(
	name,
	nodeA,
	interfaceA,
	nodeB,
	interfaceB string,
	tunnelID int,
) clabernetesapisv1alpha1.Link {
	return clabernetesapisv1alpha1.Link{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "clabernetes",
		},
		Spec: clabernetesapisv1alpha1.LinkSpec{
			EndpointA: clabernetesapisv1alpha1.LinkEndpointSpec{
				NodeName:      nodeA,
				InterfaceName: interfaceA,
			},
			EndpointB: clabernetesapisv1alpha1.LinkEndpointSpec{
				NodeName:      nodeB,
				InterfaceName: interfaceB,
			},
		},
		Status: clabernetesapisv1alpha1.LinkStatus{
			TunnelID: tunnelID,
		},
	}
}

func testNode(name, networkMode string) clabernetesapisv1alpha1.Node {
	return clabernetesapisv1alpha1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "clabernetes",
		},
		Spec: clabernetesapisv1alpha1.NodeSpec{
			NodeDefinition: clabernetesapisv1alpha1.NodeDefinition{
				NetworkMode: networkMode,
			},
		},
	}
}

func TestValidateLink(t *testing.T) {
	cases := []struct {
		name        string
		link        clabernetesapisv1alpha1.Link
		expectValid bool
	}{
		{
			name:        "simple",
			link:        testLink("simple", "srl1", "e1-1", "srl2", "e1-1", 0),
			expectValid: true,
		},
		{
			name:        "host-link",
			link:        testLink("host-link", "srl1", "e1-1", "host", "eth1", 0),
			expectValid: true,
		},
		{
			name:        "same-node-different-interfaces",
			link:        testLink("loop", "srl1", "e1-1", "srl1", "e1-2", 0),
			expectValid: true,
		},
		{
			name:        "self-connected-interface",
			link:        testLink("bad", "srl1", "e1-1", "srl1", "e1-1", 0),
			expectValid: false,
		},
		{
			name:        "host-to-host",
			link:        testLink("bad", "host", "eth0", "host", "eth1", 0),
			expectValid: false,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := clabernetescontrollerslink.ValidateLink(&testCase.link)

			if testCase.expectValid && err != nil {
				t.Fatalf("expected link to be valid, got error: %s", err)
			}

			if !testCase.expectValid && err == nil {
				t.Fatal("expected link to be invalid, got no error")
			}
		})
	}
}

func TestResolveLauncherNode(t *testing.T) {
	nodes := map[string]*clabernetesapisv1alpha1.Node{}

	for _, node := range []clabernetesapisv1alpha1.Node{
		testNode("srl1", ""),
		testNode("sim-a", "container:srl1"),
		testNode("chain-b", "container:sim-a"),
		testNode("cycle-x", "container:cycle-y"),
		testNode("cycle-y", "container:cycle-x"),
	} {
		nodes[node.GetName()] = &node
	}

	cases := []struct {
		name     string
		nodeName string
		expected string
	}{
		{name: "standalone", nodeName: "srl1", expected: "srl1"},
		{name: "no-node-object", nodeName: "ghost", expected: "ghost"},
		{name: "secondary", nodeName: "sim-a", expected: "srl1"},
		{name: "chained-secondary", nodeName: "chain-b", expected: "srl1"},
		{name: "cycle-does-not-hang", nodeName: "cycle-x", expected: "cycle-y"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			actual := clabernetescontrollerslink.ResolveLauncherNode(nodes, testCase.nodeName)

			if actual != testCase.expected {
				t.Fatalf("expected launcher node %q, got %q", testCase.expected, actual)
			}
		})
	}
}

func TestFindEndpointConflict(t *testing.T) {
	cases := []struct {
		name           string
		link           clabernetesapisv1alpha1.Link
		namespaceLinks []clabernetesapisv1alpha1.Link
		expected       string
	}{
		{
			name: "no-conflict",
			link: testLink("a-link", "srl1", "e1-1", "srl2", "e1-1", 0),
			namespaceLinks: []clabernetesapisv1alpha1.Link{
				testLink("a-link", "srl1", "e1-1", "srl2", "e1-1", 0),
				testLink("b-link", "srl1", "e1-2", "srl2", "e1-2", 0),
			},
			expected: "",
		},
		{
			name: "loses-to-lexically-smaller",
			link: testLink("z-link", "srl1", "e1-1", "srl3", "e1-1", 0),
			namespaceLinks: []clabernetesapisv1alpha1.Link{
				testLink("a-link", "srl1", "e1-1", "srl2", "e1-1", 0),
				testLink("z-link", "srl1", "e1-1", "srl3", "e1-1", 0),
			},
			expected: "a-link",
		},
		{
			name: "wins-over-lexically-larger",
			link: testLink("a-link", "srl1", "e1-1", "srl2", "e1-1", 0),
			namespaceLinks: []clabernetesapisv1alpha1.Link{
				testLink("a-link", "srl1", "e1-1", "srl2", "e1-1", 0),
				testLink("z-link", "srl1", "e1-1", "srl3", "e1-1", 0),
			},
			expected: "",
		},
		{
			name: "host-endpoints-not-exclusive",
			link: testLink("z-link", "srl2", "e1-9", "host", "eth1", 0),
			namespaceLinks: []clabernetesapisv1alpha1.Link{
				testLink("a-link", "srl1", "e1-1", "host", "eth1", 0),
				testLink("z-link", "srl2", "e1-9", "host", "eth1", 0),
			},
			expected: "",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			actual := clabernetescontrollerslink.FindEndpointConflict(
				&testCase.link,
				testCase.namespaceLinks,
			)

			if actual != testCase.expected {
				t.Fatalf("expected conflict %q, got %q", testCase.expected, actual)
			}
		})
	}
}

func TestResolveDesiredTunnelID(t *testing.T) {
	cases := []struct {
		name           string
		link           clabernetesapisv1alpha1.Link
		namespaceLinks []clabernetesapisv1alpha1.Link
		namespaceNodes []clabernetesapisv1alpha1.Node
		expected       int
	}{
		{
			name:     "first-link-gets-lowest-id",
			link:     testLink("a-link", "srl1", "e1-1", "srl2", "e1-1", 0),
			expected: 1,
		},
		{
			name: "existing-id-is-retained",
			link: testLink("a-link", "srl1", "e1-1", "srl2", "e1-1", 7),
			namespaceLinks: []clabernetesapisv1alpha1.Link{
				testLink("a-link", "srl1", "e1-1", "srl2", "e1-1", 7),
			},
			expected: 7,
		},
		{
			name: "lowest-free-id-skips-used",
			link: testLink("c-link", "srl1", "e1-3", "srl2", "e1-3", 0),
			namespaceLinks: []clabernetesapisv1alpha1.Link{
				testLink("a-link", "srl1", "e1-1", "srl2", "e1-1", 1),
				testLink("b-link", "srl1", "e1-2", "srl2", "e1-2", 3),
				testLink("c-link", "srl1", "e1-3", "srl2", "e1-3", 0),
			},
			expected: 2,
		},
		{
			name: "duplicate-id-loser-reallocates",
			link: testLink("z-link", "srl1", "e1-2", "srl2", "e1-2", 1),
			namespaceLinks: []clabernetesapisv1alpha1.Link{
				testLink("a-link", "srl1", "e1-1", "srl2", "e1-1", 1),
				testLink("z-link", "srl1", "e1-2", "srl2", "e1-2", 1),
			},
			expected: 2,
		},
		{
			name: "duplicate-id-winner-keeps",
			link: testLink("a-link", "srl1", "e1-1", "srl2", "e1-1", 1),
			namespaceLinks: []clabernetesapisv1alpha1.Link{
				testLink("a-link", "srl1", "e1-1", "srl2", "e1-1", 1),
				testLink("z-link", "srl1", "e1-2", "srl2", "e1-2", 1),
			},
			expected: 1,
		},
		{
			name:     "host-link-needs-no-id",
			link:     testLink("a-link", "srl1", "e1-1", "host", "eth1", 4),
			expected: 0,
		},
		{
			name: "same-launcher-link-needs-no-id",
			link: testLink("a-link", "srl1", "e1-1", "sim-a", "eth1", 4),
			namespaceNodes: []clabernetesapisv1alpha1.Node{
				testNode("srl1", ""),
				testNode("sim-a", "container:srl1"),
			},
			expected: 0,
		},
		{
			name: "cross-launcher-grouped-nodes-get-id",
			link: testLink("a-link", "sim-a", "eth1", "sim-b", "eth1", 0),
			namespaceNodes: []clabernetesapisv1alpha1.Node{
				testNode("srl1", ""),
				testNode("srl2", ""),
				testNode("sim-a", "container:srl1"),
				testNode("sim-b", "container:srl2"),
			},
			expected: 1,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			actual, err := clabernetescontrollerslink.ResolveDesiredTunnelID(
				&testCase.link,
				testCase.namespaceLinks,
				testCase.namespaceNodes,
			)
			if err != nil {
				t.Fatalf("unexpected error: %s", err)
			}

			if actual != testCase.expected {
				t.Fatalf("expected tunnel id %d, got %d", testCase.expected, actual)
			}
		})
	}
}
