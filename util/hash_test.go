package util_test

import (
	"testing"

	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
	clabernetesutil "github.com/clabernetes/clabernetes/util"
)

func TestHashBytes(t *testing.T) {
	cases := []struct {
		name     string
		in       []byte
		expected string
	}{
		{
			name:     "simple",
			in:       []byte("hashmeplz"),
			expected: "b370f837831aa6b19d65cac4a0f8ef13e8d145027d3b95992232ccb8f0e564b5",
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				actual := clabernetesutil.HashBytes(testCase.in)
				if actual != testCase.expected {
					clabernetestesthelper.FailOutput(t, actual, testCase.expected)
				}
			})
	}
}

func TestHashObject(t *testing.T) {
	cases := []struct {
		name     string
		in       any
		expected string
	}{
		{
			name:     "simple",
			in:       map[string]string{"one": "two"},
			expected: "ae2a0318f5c3a1cf9577fef4ab888b51951dc9ee84cb7012cfb064d4bbfee2a7",
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				_, actual, err := clabernetesutil.HashObject(testCase.in)
				if err != nil {
					t.Fatalf("failed hashing object, err: %s", err)
				}

				if actual != testCase.expected {
					clabernetestesthelper.FailOutput(t, actual, testCase.expected)
				}
			})
	}
}

func TestHashObjectYAML(t *testing.T) {
	cases := []struct {
		name     string
		in       any
		expected string
	}{
		{
			name:     "simple",
			in:       map[string]string{"one": "two"},
			expected: "8fb10bdc1e14b3b2e8e53aa5315b1c11cb87b873a0a1b6f758411c200728c86b",
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				_, actual, err := clabernetesutil.HashObjectYAML(testCase.in)
				if err != nil {
					t.Fatalf("failed hashing object, err: %s", err)
				}

				if actual != testCase.expected {
					clabernetestesthelper.FailOutput(t, actual, testCase.expected)
				}
			})
	}
}
