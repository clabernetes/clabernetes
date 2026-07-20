package testhelper

import (
	"fmt"
	"testing"

	clabernetesutil "github.com/clabernetes/clabernetes/util"
)

// FailOutput is a simple func to nicely print actual vs expected output when a test fails.
func FailOutput(t *testing.T, actual, expected any) {
	t.Helper()

	switch actual.(type) {
	case string, []byte:
		diff := unifiedDiff(t, actual, expected)

		actualExpectedOut := fmt.Sprintf("\n\033[0;36m*** actual   >>>\033[0m"+
			"\n%s"+
			"\n\033[0;36m<<< actual   ***\033[0m"+
			"\n\033[0;35m*** expected >>>\033[0m"+
			"\n%s"+
			"\n\033[0;35m<<< expected ***\033[0m",
			actual, expected,
		)
		diffOut := fmt.Sprintf("\n\033[0;97m*** diff     >>>\033[0m"+
			"\n%s"+
			"\n\033[0;97m<<< diff     ***\033[0m", diff)

		if *OnlyDiff {
			t.Fatalf(
				"actual and expected outputs do not match...%s",
				diffOut,
			)
		} else {
			t.Fatalf(
				"actual and expected outputs do not match...%s%s",
				actualExpectedOut,
				diffOut,
			)
		}
	default:
		t.Fatalf(
			"actual and expected outputs do not match..."+
				"\n\033[0;36m*** actual   >>>\033[0m"+
				"\n%+v"+
				"\n\033[0;36m<<< actual   ***\033[0m"+
				"\n\033[0;35m*** expected >>>\033[0m"+
				"\n%+v"+
				"\n\033[0;35m<<< expected ***\033[0m",
			actual,
			expected,
		)
	}
}

func unifiedDiff(t *testing.T, actual, expected any) string {
	t.Helper()

	diff, err := clabernetesutil.UnifiedDiff(actual, expected)
	if err != nil {
		t.Fatalf("failed generating diff, err: %s", err)
	}

	return diff
}
