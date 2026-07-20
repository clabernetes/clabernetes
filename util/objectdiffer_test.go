package util_test

import (
	"testing"

	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
	clabernetesutil "github.com/clabernetes/clabernetes/util"
)

func TestObjectDifferGetCurrentObjectNames(t *testing.T) {
	cases := []struct {
		name     string
		current  map[string]string
		expected []string
	}{
		{
			name: "simple",
			current: map[string]string{
				"one": "something",
				"two": "neato",
			},
			expected: []string{"one", "two"},
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				od := clabernetesutil.ObjectDiffer[string]{
					Current: testCase.current,
				}

				actual := od.CurrentObjectNames()

				if !clabernetesutil.StringSliceContainsAll(actual, testCase.expected) {
					clabernetestesthelper.FailOutput(t, actual, testCase.expected)
				}
			})
	}
}

func TestObjectDifferSetMissing(t *testing.T) {
	cases := []struct {
		name        string
		current     map[string]string
		allExpected []string
		expected    []string
	}{
		{
			name: "simple",
			current: map[string]string{
				"one": "something",
				"two": "neato",
			},
			allExpected: []string{"one", "two", "seven", "eleven"},
			expected:    []string{"seven", "eleven"},
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				od := clabernetesutil.ObjectDiffer[string]{
					Current: testCase.current,
				}

				od.SetMissing(testCase.allExpected)

				actual := od.Missing

				if !clabernetesutil.StringSliceContainsAll(actual, testCase.expected) {
					clabernetestesthelper.FailOutput(t, actual, testCase.expected)
				}
			})
	}
}

func TestObjectDifferSeExtra(t *testing.T) {
	cases := []struct {
		name        string
		current     map[string]string
		allExpected []string
		expected    []string
	}{
		{
			name: "simple",
			current: map[string]string{
				"one": "something",
				"two": "neato",
			},
			allExpected: []string{"one", "seven", "eleven"},
			expected:    []string{"neato"},
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				od := clabernetesutil.ObjectDiffer[string]{
					Current: testCase.current,
				}

				od.SetExtra(testCase.allExpected)

				actual := od.Extra

				if !clabernetesutil.StringSliceContainsAll(actual, testCase.expected) {
					clabernetestesthelper.FailOutput(t, actual, testCase.expected)
				}
			})
	}
}
