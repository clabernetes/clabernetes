package kubernetes_test

import (
	"testing"

	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
	clabernetesutilkubernetes "github.com/clabernetes/clabernetes/util/kubernetes"
	k8scorev1 "k8s.io/api/core/v1"
)

func TestContainersEqual(t *testing.T) {
	cases := []struct {
		name     string
		a        []k8scorev1.Container
		b        []k8scorev1.Container
		expected bool
	}{
		{
			name:     "simple-empty",
			a:        []k8scorev1.Container{},
			b:        []k8scorev1.Container{},
			expected: true,
		},
		{
			name: "simple",
			a: []k8scorev1.Container{
				{
					Name: "something",
				},
			},
			b: []k8scorev1.Container{
				{
					Name: "something",
				},
			},
			expected: true,
		},
		{
			name: "different-counts",
			a: []k8scorev1.Container{
				{
					Name: "something",
				},
			},
			b:        []k8scorev1.Container{},
			expected: false,
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				actual := clabernetesutilkubernetes.ContainersEqual(testCase.a, testCase.b)
				if actual != testCase.expected {
					clabernetestesthelper.FailOutput(t, actual, testCase.expected)
				}
			})
	}
}
