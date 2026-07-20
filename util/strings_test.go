package util_test

import (
	"testing"

	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
	clabernetesutil "github.com/clabernetes/clabernetes/util"
)

func TestIndent(t *testing.T) {
	cases := []struct {
		name        string
		in          string
		indentCount int
		expected    string
	}{
		{
			name:        "simple",
			in:          "a single line",
			indentCount: 1,
			expected:    " a single line",
		},
		{
			name:        "simple-more-indent",
			in:          "a single line",
			indentCount: 4,
			expected:    "    a single line",
		},
		{
			name:        "multi-line",
			in:          "a single line\nanother line",
			indentCount: 2,
			expected:    "  a single line\n  another line",
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				actual := clabernetesutil.Indent(testCase.in, testCase.indentCount)

				if actual != testCase.expected {
					clabernetestesthelper.FailOutput(t, actual, testCase.expected)
				}
			})
	}
}
