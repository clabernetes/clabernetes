package default_values_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
)

func TestMain(m *testing.M) {
	clabernetestesthelper.Flags()

	os.Exit(m.Run())
}

// TestCustomValues -- similar to the default one but we can just chuck in some custom values stuff
// in here to test lots of the helm rendering at once.
func TestCustomValues(t *testing.T) {
	t.Parallel()

	testName := "customized_values"
	chartName := "clabernetes"

	chartsDir, err := filepath.Abs("../../..")
	if err != nil {
		t.Error(err)
	}

	clabernetestesthelper.HelmTest(
		t,
		chartName,
		testName,
		clabernetesconstants.Clabernetes,
		fmt.Sprintf("%s-values.yaml", testName),
		chartsDir,
	)
}
