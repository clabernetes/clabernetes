package util_test

import (
	"testing"

	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
	clabernetesutil "github.com/clabernetes/clabernetes/util"
)

func TestStringSetAdd(t *testing.T) {
	cases := []struct {
		name     string
		vals     []string
		expected []string
	}{
		{
			name:     "simple",
			vals:     []string{"one", "two", "three"},
			expected: []string{"one", "two", "three"},
		},
		{
			name:     "empty",
			vals:     []string{},
			expected: []string{},
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				ss := clabernetesutil.NewStringSet()

				for _, s := range testCase.vals {
					ss.Add(s)
				}

				actual := ss.Items()

				// going through set changes order, so just doing this to confirm expectations
				if !clabernetesutil.StringSliceContainsAll(actual, testCase.expected) &&
					!clabernetesutil.StringSliceContainsAll(testCase.expected, actual) {
					clabernetestesthelper.FailOutput(t, actual, testCase.expected)
				}
			})
	}
}

func TestStringSetRemove(t *testing.T) {
	cases := []struct {
		name     string
		vals     []string
		remove   string
		expected []string
	}{
		{
			name:     "simple",
			vals:     []string{"one", "two", "three"},
			remove:   "two",
			expected: []string{"one", "three"},
		},
		{
			name:     "not present",
			vals:     []string{"one", "two", "three"},
			remove:   "four",
			expected: []string{"one", "two", "three"},
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				ss := clabernetesutil.NewStringSetWithValues(testCase.vals...)

				ss.Remove(testCase.remove)

				actual := ss.Items()

				// going through set changes order, so just doing this to confirm expectations
				if !clabernetesutil.StringSliceContainsAll(actual, testCase.expected) &&
					!clabernetesutil.StringSliceContainsAll(testCase.expected, actual) {
					clabernetestesthelper.FailOutput(t, actual, testCase.expected)
				}
			})
	}
}

func TestStringSetContains(t *testing.T) {
	cases := []struct {
		name     string
		vals     []string
		contains string
		expected bool
	}{
		{
			name:     "simple",
			vals:     []string{"one", "two", "three"},
			contains: "two",
			expected: true,
		},
		{
			name:     "simple",
			vals:     []string{"one", "two", "three"},
			contains: "four",
			expected: false,
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				ss := clabernetesutil.NewStringSetWithValues(testCase.vals...)

				actual := ss.Contains(testCase.contains)

				if actual != testCase.expected {
					clabernetestesthelper.FailOutput(t, actual, testCase.expected)
				}
			})
	}
}

func TestStringSetLen(t *testing.T) {
	cases := []struct {
		name     string
		vals     []string
		expected int
	}{
		{
			name:     "simple",
			vals:     []string{"one", "two", "three"},
			expected: 3,
		},
		{
			name:     "simple",
			vals:     []string{},
			expected: 0,
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				ss := clabernetesutil.NewStringSetWithValues(testCase.vals...)

				actual := ss.Len()

				if actual != testCase.expected {
					clabernetestesthelper.FailOutput(t, actual, testCase.expected)
				}
			})
	}
}
