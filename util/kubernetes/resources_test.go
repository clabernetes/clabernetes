package kubernetes_test

import (
	"reflect"
	"testing"

	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
	clabernetesutilkubernetes "github.com/clabernetes/clabernetes/util/kubernetes"
	k8scorev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestYAMLToK8sResourceRequirements(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		expected *k8scorev1.ResourceRequirements
	}{
		{
			name: "simple",
			in: `---
requests:
  memory: 128Mi
  cpu: 50m
`,
			expected: &k8scorev1.ResourceRequirements{
				Limits: k8scorev1.ResourceList{},
				Requests: k8scorev1.ResourceList{
					"memory": resource.MustParse("128Mi"),
					"cpu":    resource.MustParse("50m"),
				},
			},
		},
		{
			name: "simple",
			in: `---
requests:
  memory: 128Mi
  cpu: 50m
limits:
  memory: 256Mi
  cpu: 100m
`,
			expected: &k8scorev1.ResourceRequirements{
				Limits: k8scorev1.ResourceList{
					"memory": resource.MustParse("256Mi"),
					"cpu":    resource.MustParse("100m"),
				},
				Requests: k8scorev1.ResourceList{
					"memory": resource.MustParse("128Mi"),
					"cpu":    resource.MustParse("50m"),
				},
			},
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				actual, err := clabernetesutilkubernetes.YAMLToK8sResourceRequirements(testCase.in)
				if err != nil {
					t.Fatalf(
						"failed calling YAMLToK8sResourceRequirements, error: %s", err,
					)
				}

				if !reflect.DeepEqual(actual, testCase.expected) {
					clabernetestesthelper.FailOutput(t, actual, testCase.expected)
				}
			})
	}
}
