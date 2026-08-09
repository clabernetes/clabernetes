package util_test

import (
	"testing"

	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
	clabernetesutil "github.com/clabernetes/clabernetes/util"
)

func TestStringSliceContainsAll(t *testing.T) {
	cases := []struct {
		name     string
		ss       []string
		vals     []string
		expected bool
	}{
		{
			name:     "simple",
			ss:       []string{"one", "two", "three"},
			vals:     []string{"two"},
			expected: true,
		},
		{
			name:     "duplicate vals",
			ss:       []string{"one", "two", "three"},
			vals:     []string{"two", "two"},
			expected: true,
		},
		{
			name:     "more vals than ss",
			ss:       []string{"one", "two", "three"},
			vals:     []string{"two", "two", "two", "two"},
			expected: true,
		},
		{
			name:     "doesnt have all vals",
			ss:       []string{"one", "two", "three"},
			vals:     []string{"one", "two", "three", "four"},
			expected: false,
		},
		{
			name:     "no match",
			ss:       []string{"one", "two", "three"},
			vals:     []string{"four"},
			expected: false,
		},
		{
			name:     "empty ss",
			ss:       []string{},
			vals:     []string{"two"},
			expected: false,
		},
		{
			name:     "empty vals",
			ss:       []string{"one", "two", "three"},
			vals:     []string{},
			expected: true,
		},
		{
			name:     "empty ss and vals",
			ss:       []string{},
			vals:     []string{},
			expected: true,
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				actual := clabernetesutil.StringSliceContainsAll(testCase.ss, testCase.vals)

				if actual != testCase.expected {
					clabernetestesthelper.FailOutput(t, actual, testCase.expected)
				}
			})
	}
}

func TestStringSliceDifference(t *testing.T) {
	cases := []struct {
		name     string
		a        []string
		b        []string
		expected []string
	}{
		{
			name:     "simple",
			a:        []string{"one", "two", "two", "two", "three", "three"},
			b:        []string{"one", "two", "two", "two", "three", "four"},
			expected: []string{"four"},
		},
		{
			name:     "a has more elements",
			a:        []string{"one", "two", "two", "two", "three", "four"},
			b:        []string{"one", "two", "two", "two", "three"},
			expected: []string{},
		},
		{
			name:     "empty",
			a:        []string{},
			expected: []string{},
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				actual := clabernetesutil.StringSliceDifference(testCase.a, testCase.b)

				if !clabernetesutil.StringSliceEqual(actual, testCase.expected) {
					clabernetestesthelper.FailOutput(t, actual, testCase.expected)
				}
			})
	}
}
