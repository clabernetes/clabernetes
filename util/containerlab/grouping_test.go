package containerlab_test

import (
	"reflect"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
)

func testGroupingNode(name, networkMode string) clabernetesapisv1alpha1.Node {
	node := clabernetesapisv1alpha1.Node{}
	node.Name = name
	node.Spec.NetworkMode = networkMode

	return node
}

func testGroupingNodes() map[string]*clabernetesapisv1alpha1.Node {
	return clabernetesutilcontainerlab.NodesByName(
		[]clabernetesapisv1alpha1.Node{
			testGroupingNode("srl1", ""),
			testGroupingNode("sim-a", "container:srl1"),
			testGroupingNode("chain-b", "container:sim-a"),
			testGroupingNode("cycle-x", "container:cycle-y"),
			testGroupingNode("cycle-y", "container:cycle-x"),
			testGroupingNode("srl2", ""),
		},
	)
}

func TestResolveLauncherNode(t *testing.T) {
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
			actual := clabernetesutilcontainerlab.ResolveLauncherNode(
				testGroupingNodes(),
				testCase.nodeName,
			)

			if actual != testCase.expected {
				t.Fatalf("expected launcher node %q, got %q", testCase.expected, actual)
			}
		})
	}
}

func TestResolveGroupMembers(t *testing.T) {
	cases := []struct {
		name         string
		launcherNode string
		expected     []string
	}{
		{name: "group", launcherNode: "srl1", expected: []string{"srl1", "chain-b", "sim-a"}},
		{name: "standalone", launcherNode: "srl2", expected: []string{"srl2"}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			actual := clabernetesutilcontainerlab.ResolveGroupMembers(
				testGroupingNodes(),
				testCase.launcherNode,
			)

			if !reflect.DeepEqual(actual, testCase.expected) {
				t.Fatalf("expected members %v, got %v", testCase.expected, actual)
			}
		})
	}
}
