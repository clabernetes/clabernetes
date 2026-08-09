package basic_test

import (
	"os"
	"testing"

	clabernetestesthelper "github.com/srl-labs/clabernetes/testhelper"
	clabernetestesthelpersuite "github.com/srl-labs/clabernetes/testhelper/suite"
)

func TestMain(m *testing.M) {
	clabernetestesthelper.Flags()

	os.Exit(m.Run())
}

func TestContainerlabBasic(t *testing.T) {
	t.Parallel()

	testName := "topology-basic"

	namespace := clabernetestesthelper.NewTestNamespace(testName)

	steps := clabernetestesthelpersuite.Steps{
		{
			// the topology compiles to Node/Link/LauncherProfile objects and the Node controller
			// realizes those -- so this asserts the whole compile + realize pipeline including
			// the (unprefixed! the namespace is the topology boundary) deployment and services
			Index:       10,
			Description: "Create a simple containerlab topology with just one node",
			AssertObjects: map[string][]clabernetestesthelpersuite.AssertObject{
				"topology": {
					{
						Name: testName,
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeTopology,
						},
					},
				},
				"node.clabernetes.containerlab.dev": {
					{
						Name: "srl1",
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeNode,
						},
					},
				},
				"launcherprofile": {
					{
						Name:           testName,
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{},
					},
				},
				"service": {
					{
						Name: "srl1",
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeExposeService,
						},
					},
					{
						Name: "srl1-vx",
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeFabricService,
						},
					},
				},
				"deployment": {
					{
						Name: "srl1",
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeDeployment,
						},
					},
				},
			},
		},
		{
			// this step we add a second node to topo and actually configure some links this time.
			Index:       20,
			Description: "Add a node and connect them",
			AssertObjects: map[string][]clabernetestesthelpersuite.AssertObject{
				"topology": {
					{
						Name: testName,
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeTopology,
						},
					},
				},
				"node.clabernetes.containerlab.dev": {
					{
						Name: "srl1",
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeNode,
						},
					},
					{
						Name: "srl2",
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeNode,
						},
					},
				},
				"link": {
					{
						Name: "srl1-e1-1-srl2-e1-1",
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeLink,
						},
					},
					{
						Name: "srl1-e1-3-host-eth13",
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeLink,
						},
					},
				},
				"service": {
					{
						Name: "srl1",
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeExposeService,
						},
					},
					{
						Name: "srl1-vx",
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeFabricService,
						},
					},
					{
						Name: "srl2-vx",
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeFabricService,
						},
					},
				},
				"deployment": {
					{
						Name: "srl1",
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeDeployment,
						},
					},
					{
						Name: "srl2",
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeDeployment,
						},
					},
				},
			},
		},
	}

	clabernetestesthelpersuite.Run(t, testName, steps, namespace)
}
