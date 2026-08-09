package util_test

import (
	"testing"

	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
	clabernetesutil "github.com/clabernetes/clabernetes/util"
)

func TestGetEnvStrOrDefault(t *testing.T) {
	cases := []struct {
		name     string
		k        string
		setV     string
		defaultV string
		expected string
	}{
		{
			name:     "simple-default",
			k:        "SOME_ENV_VAR",
			setV:     "",
			defaultV: "foo",
			expected: "foo",
		},
		{
			name:     "simple-already-set",
			k:        "SOME_ENV_VAR",
			setV:     "foo",
			defaultV: "taco",
			expected: "foo",
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				t.Setenv(testCase.k, testCase.setV)

				actual := clabernetesutil.GetEnvStrOrDefault(testCase.k, testCase.defaultV)

				if actual != testCase.expected {
					clabernetestesthelper.FailOutput(t, actual, testCase.expected)
				}
			})
	}
}

func TestGetEnvIntOrDefault(t *testing.T) {
	cases := []struct {
		name     string
		k        string
		setV     string
		defaultV int
		expected int
	}{
		{
			name:     "simple-default",
			k:        "SOME_ENV_VAR",
			setV:     "",
			defaultV: 1,
			expected: 1,
		},
		{
			name:     "simple-already-set",
			k:        "SOME_ENV_VAR",
			setV:     "1",
			defaultV: 2,
			expected: 1,
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				t.Setenv(testCase.k, testCase.setV)

				actual := clabernetesutil.GetEnvIntOrDefault(testCase.k, testCase.defaultV)

				if actual != testCase.expected {
					clabernetestesthelper.FailOutput(t, actual, testCase.expected)
				}
			})
	}
}

func TestGetEnvFloat64OrDefault(t *testing.T) {
	cases := []struct {
		name     string
		k        string
		setV     string
		defaultV float64
		expected float64
	}{
		{
			name:     "simple-default",
			k:        "SOME_ENV_VAR",
			setV:     "",
			defaultV: 1.07,
			expected: 1.07,
		},
		{
			name:     "simple-already-set",
			k:        "SOME_ENV_VAR",
			setV:     "1", // being lazy since precision is lost anyway
			defaultV: 2.91,
			expected: 1,
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				t.Setenv(testCase.k, testCase.setV)

				actual := clabernetesutil.GetEnvFloat64OrDefault(testCase.k, testCase.defaultV)

				if actual != testCase.expected {
					clabernetestesthelper.FailOutput(t, actual, testCase.expected)
				}
			})
	}
}

func TestGetEnvBoolOrDefault(t *testing.T) {
	cases := []struct {
		name     string
		k        string
		setV     string
		defaultV bool
		expected bool
	}{
		{
			name:     "simple-default",
			k:        "SOME_ENV_VAR",
			setV:     "",
			defaultV: true,
			expected: true,
		},
		{
			name:     "simple-already-set",
			k:        "SOME_ENV_VAR",
			setV:     "1",
			defaultV: false,
			expected: true,
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				t.Setenv(testCase.k, testCase.setV)

				actual := clabernetesutil.GetEnvBoolOrDefault(testCase.k, testCase.defaultV)

				if actual != testCase.expected {
					clabernetestesthelper.FailOutput(t, actual, testCase.expected)
				}
			})
	}
}
