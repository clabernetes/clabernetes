package kubernetes_test

import (
	"testing"

	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
	clabernetesutilkubernetes "github.com/clabernetes/clabernetes/util/kubernetes"
)

func TestContainersEqualAnnotationsOrLabelsConform(t *testing.T) {
	cases := []struct {
		name     string
		a        map[string]string
		b        map[string]string
		expected bool
	}{
		{
			name:     "simple-empty",
			a:        nil,
			b:        nil,
			expected: true,
		},
		{
			name: "simple",
			a: map[string]string{
				"something": "neat",
			},
			b: map[string]string{
				"something": "neat",
			},
			expected: true,
		},
		{
			name: "different-keys",
			a: map[string]string{
				"something": "neat",
			},
			b: map[string]string{
				"different": "neat",
			},
			expected: false,
		},
		{
			name: "expected-has-more",
			a: map[string]string{
				"something": "neat",
			},
			b: map[string]string{
				"something": "neat",
				"different": "neat",
			},
			expected: false,
		},
		{
			name: "existing-has-more",
			a: map[string]string{
				"something": "neat",
				"different": "neat",
			},
			b: map[string]string{
				"something": "neat",
			},
			expected: true,
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				actual := clabernetesutilkubernetes.ExistingMapStringStringContainsAllExpectedKeyValues(
					testCase.a,
					testCase.b,
				)
				if actual != testCase.expected {
					clabernetestesthelper.FailOutput(t, actual, testCase.expected)
				}
			})
	}
}
