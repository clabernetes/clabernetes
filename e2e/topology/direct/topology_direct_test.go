package direct_test

import (
	"os"
	"testing"

	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
	clabernetestesthelpersuite "github.com/clabernetes/clabernetes/testhelper/suite"
)

func TestMain(m *testing.M) {
	clabernetestesthelper.Flags()

	os.Exit(m.Run())
}

// TestNodeLinkDirect exercises the primary api without any Topology object: hand written Node,
// Link, and LauncherProfile objects must yield launcher deployments, per-node services, tunnel id
// allocations, and status stamping -- and a rewire (changing a link's remote interface) must
// keep the allocated tunnel id (the launchers move the tunnel live, no pod roll).
func TestNodeLinkDirect(t *testing.T) {
	t.Parallel()

	testName := "topology-direct"

	namespace := clabernetestesthelper.NewTestNamespace(testName)

	steps := clabernetestesthelpersuite.Steps{
		{
			Index:       10,
			Description: "Create Node/Link/LauncherProfile objects directly -- no Topology",
			AssertObjects: map[string][]clabernetestesthelpersuite.AssertObject{
				"launcherprofile": {
					{
						Name:           "direct",
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{},
					},
				},
				"node.c9s.run": {
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
				},
				"service": {
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
		{
			Index: 20,
			Description: "Rewire the link's b side to another interface -- the tunnel id must" +
				" be retained (live tunnel move, no re-allocation)",
			AssertObjects: map[string][]clabernetestesthelpersuite.AssertObject{
				"link": {
					{
						Name: "srl1-e1-1-srl2-e1-1",
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
