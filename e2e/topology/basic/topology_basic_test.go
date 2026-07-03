package basic_test

import (
	"fmt"
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
			// this step, while obviously very "basic" does quite a bit of work for us... it ensures
			// that the default ports are allocated, the config is hashed and subdivided up, and our
			// defaults are set properly. more than that, this also asserts that the service(s) are
			// setup as we'd expect.
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
				"service": {
					{
						Name: fmt.Sprintf("%s-srl1", testName),
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeExposeService,
						},
					},
					{
						Name: fmt.Sprintf("%s-srl1-vx", testName),
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeFabricService,
						},
					},
				},
				"deployment": {
					{
						Name: fmt.Sprintf("%s-srl1", testName),
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeDeployment,
						},
					},
				},
				"node.clabernetes.containerlab.dev": {
					{
						Name: fmt.Sprintf("%s-srl1", testName),
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeNode,
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
				"service": {
					{
						Name: fmt.Sprintf("%s-srl1", testName),
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeExposeService,
						},
					},
					{
						Name: fmt.Sprintf("%s-srl1-vx", testName),
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeFabricService,
						},
					},
				},
				"deployment": {
					{
						Name: fmt.Sprintf("%s-srl1", testName),
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeDeployment,
						},
					},
					{
						Name: fmt.Sprintf("%s-srl2", testName),
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeDeployment,
						},
					},
				},
				"node.clabernetes.containerlab.dev": {
					{
						Name: fmt.Sprintf("%s-srl1", testName),
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeNode,
						},
					},
					{
						Name: fmt.Sprintf("%s-srl2", testName),
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeNode,
						},
					},
				},
				"link.clabernetes.containerlab.dev": {
					{
						Name: fmt.Sprintf("%s-srl1-e1-1-srl2-e1-1", testName),
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeLink,
						},
					},
				},
			},
		},
	}

	clabernetestesthelpersuite.Run(t, testName, steps, namespace)
}
