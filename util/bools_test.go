package util_test

import (
	"testing"

	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
	clabernetesutil "github.com/clabernetes/clabernetes/util"
)

func TestAnyBoolTrue(t *testing.T) {
	cases := []struct {
		name     string
		in       []bool
		expected bool
	}{
		{
			name:     "simple",
			in:       []bool{true},
			expected: true,
		},
		{
			name:     "all-true",
			in:       []bool{true, true, true},
			expected: true,
		},
		{
			name:     "one-true",
			in:       []bool{false, true, false},
			expected: true,
		},
		{
			name:     "all-false",
			in:       []bool{false, false, false},
			expected: false,
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				actual := clabernetesutil.AnyBoolTrue(testCase.in...)
				if actual != testCase.expected {
					clabernetestesthelper.FailOutput(t, actual, testCase.expected)
				}
			})
	}
}
