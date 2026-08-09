package testhelper

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
)

const (
	helm = "helm"
)

// HelmTest executes a test against a helm chart -- this is a very simple/dumb test meant only to
// ensure that we don't accidentally screw up charts. We do this by storing the "golden" output of
// a rendered chart (and subcharts if applicable) with a given values file.
func HelmTest(t *testing.T, chartName, testName, namespace, valuesFileName, chartsDir string) {
	t.Helper()

	actualDir, err := filepath.Abs("actual")
	if err != nil {
		t.Fatalf("failed getting absolute path for actual dir, error: %s", err)
	}

	var valuesFile string

	if valuesFileName != "" {
		valuesFile = fmt.Sprintf(
			"%s/tests/%s/test-fixtures/%s-values.yaml",
			chartName,
			testName,
			testName,
		)
	}

	err = os.MkdirAll(actualDir, clabernetesconstants.PermissionsEveryoneReadWriteOwnerExecute)
	if err != nil {
		t.Fatalf(
			"failed creating actual output directory, error: %s", err,
		)
	}

	defer func() {
		if !*SkipCleanup {
			err = os.Chdir(chartsDir) //nolint: usetesting
			if err != nil {
				t.Logf("failed changing to a directory, error: %s", err)
			}

			err = os.RemoveAll(actualDir)
			if err != nil {
				t.Logf("failed cleaning up actual output directory, error: %s", err)
			}
		}
	}()

	args := []string{
		"template",
		"./" + chartName,
		"--output-dir",
		actualDir,
	}

	if namespace != "" {
		args = append(args, "--namespace", namespace)
	}

	if valuesFile != "" {
		args = append(args, "--values", valuesFile)
	}

	HelmCommand(
		t,
		chartsDir,
		args...,
	)

	renderedTemplates := ReadAllRenderedTemplates(t, actualDir)

	if *Update {
		for expectedFileName, expectedFileContent := range renderedTemplates {
			WriteTestFixtureFile(
				t,
				fmt.Sprintf("golden/%s", filepath.Base(expectedFileName)),
				expectedFileContent,
			)
		}

		// we just wrote the golden file of course it will match, no need to check
		return
	}

	for expectedFileName, actualContents := range renderedTemplates {
		expected := ReadTestFixtureFile(t, fmt.Sprintf("golden/%s", expectedFileName))

		if !bytes.Equal(
			actualContents,
			expected,
		) {
			FailOutput(t, actualContents, expected)
		}
	}
}

// HelmCommand executes helm with the given arguments.
func HelmCommand(t *testing.T, chartsDir string, args ...string) []byte {
	t.Helper()

	cmd := exec.CommandContext( //nolint: gosec
		t.Context(),
		helm,
		args...,
	)

	cmd.Dir = chartsDir

	return Execute(t, cmd)
}

// ReadAllRenderedTemplates loads all rendered template content into a map -- sub-charts are loaded
// with a "_subchart-<CHARTNAME>-" prefix.
func ReadAllRenderedTemplates(t *testing.T, rootRenderDir string) map[string][]byte {
	t.Helper()

	renderedTemplates := map[string][]byte{}

	parentChartFileNames, err := filepath.Glob(fmt.Sprintf("/%s/*/templates/*.yaml", rootRenderDir))
	if err != nil {
		t.Fatalf("failed globbing parent chart files, error: '%s'", err)
	}

	subChartFileNames, err := filepath.Glob(
		fmt.Sprintf("%s/*/charts/*/templates/*.yaml", rootRenderDir),
	)
	if err != nil {
		t.Fatalf("failed globbing dependency chart files, error: '%s'", err)
	}

	for _, parentChartFileName := range parentChartFileNames {
		var contents []byte

		contents, err = os.ReadFile(parentChartFileName) //nolint:gosec
		if err != nil {
			t.Fatalf(
				"failed reading contents of actual output file %q, error: %s",
				parentChartFileName,
				err,
			)
		}

		renderedTemplates[filepath.Base(parentChartFileName)] = contents
	}

	for _, subChartFileName := range subChartFileNames {
		subChartPathComponents := strings.Split(subChartFileName, string(filepath.Separator))
		subChartName := subChartPathComponents[len(subChartPathComponents)-3]

		var contents []byte

		contents, err = os.ReadFile(subChartFileName) //nolint:gosec
		if err != nil {
			t.Fatalf(
				"failed reading contents of actual output file %q, error: %s",
				subChartFileName,
				err,
			)
		}

		renderedTemplates[fmt.Sprintf("_subchart-%s-%s", subChartName, filepath.Base(subChartFileName))] = contents //nolint:lll
	}

	return renderedTemplates
}
