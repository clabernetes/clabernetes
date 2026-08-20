package clabverter_test

import (
	"os"
	"testing"

	clabernetesclabverter "github.com/clabernetes/clabernetes/clabverter"
	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
	clabernetestesthelpersuite "github.com/clabernetes/clabernetes/testhelper/suite"
)

func TestMain(m *testing.M) {
	clabernetestesthelper.Flags()

	os.Exit(m.Run())
}

func TestClabverterBasic(t *testing.T) {
	t.Parallel()

	testName := "clabverter-basic"

	namespace := clabernetestesthelper.NewTestNamespace(testName)

	c := clabernetesclabverter.MustNewClabverter(
		"test-fixtures/basic_clab.yaml",
		"",
		"test-fixtures",
		namespace,
		"prefixed",
		"",
		false,
		false,
		false,
		false,
		false,
	)

	err := c.Clabvert()
	if err != nil {
		t.Fatalf("failed running clabversion, err: %s", err)
	}

	// rename the generated topo so we can use the e2e runner thingy
	err = os.Rename("test-fixtures/clabverter-basic.yaml", "test-fixtures/10-apply.yaml")
	if err != nil {
		t.Fatalf("failed renaming clabverted topology, err: %s", err)
	}

	defer func() {
		if *clabernetestesthelper.SkipCleanup {
			return
		}

		err = os.Remove("test-fixtures/10-apply.yaml")
		if err != nil {
			t.Errorf("failed cleaning up clabverted topology file, err: %s", err)
		}

		// remove the clabverted ns output too if exists
		err = os.Remove("test-fixtures/_clabverter-basic-ns.yaml")
		if err != nil {
			t.Errorf("failed cleaning up clabverted namespace file, err: %s", err)
		}
	}()

	steps := clabernetestesthelpersuite.Steps{
		{
			Index:       10,
			Description: "Create a simple containerlab topology from clabverted output",
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
						Name: "srl1",
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeExposeService,
						},
					},
				},
			},
		},
	}

	clabernetestesthelpersuite.Run(t, testName, steps, namespace)
}
