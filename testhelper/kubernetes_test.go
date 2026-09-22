package testhelper_test

import (
	"reflect"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

func TestNormalizeExposeServiceFinalizers(t *testing.T) {
	t.Parallel()

	const cloudFinalizer = "service.kubernetes.io/load-balancer-cleanup"
	const customFinalizer = "example.com/cleanup"

	for _, testCase := range []struct {
		name       string
		finalizers []string
		want       []string
	}{
		{name: "no cloud controller"},
		{name: "cloud controller", finalizers: []string{cloudFinalizer}},
		{
			name:       "custom finalizer",
			finalizers: []string{customFinalizer},
			want:       []string{customFinalizer},
		},
		{
			name:       "cloud and custom finalizers",
			finalizers: []string{cloudFinalizer, customFinalizer},
			want:       []string{customFinalizer},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			service := k8scorev1.Service{
				ObjectMeta: metav1.ObjectMeta{Finalizers: testCase.finalizers},
				Spec: k8scorev1.ServiceSpec{Ports: []k8scorev1.ServicePort{
					{Name: "ssh", Port: 22, AppProtocol: new("ssh")},
				}},
			}

			data, err := yaml.Marshal(service)
			if err != nil {
				t.Fatal(err)
			}

			var normalized k8scorev1.Service

			err = yaml.Unmarshal(clabernetestesthelper.NormalizeExposeService(t, data), &normalized)
			if err != nil {
				t.Fatal(err)
			}

			if !reflect.DeepEqual(normalized.Finalizers, testCase.want) {
				t.Fatalf("finalizers = %v, want %v", normalized.Finalizers, testCase.want)
			}

			if len(normalized.Spec.Ports) != 1 || normalized.Spec.Ports[0].AppProtocol == nil ||
				*normalized.Spec.Ports[0].AppProtocol != "ssh" {
				t.Fatalf("normalization lost the appProtocol hint: %+v", normalized.Spec.Ports)
			}
		})
	}
}

func TestNormalizeNodePreservesUserAnnotations(t *testing.T) {
	t.Parallel()
	input := []byte(`metadata:
  annotations:
    c9s.run/startup-admitted: runtime-node-uid
    c9s.run/startup-hold: runtime-topology-uid
    example.com/user: retained
`)
	var node clabernetesapisv1alpha1.Node
	if err := yaml.Unmarshal(clabernetestesthelper.NormalizeNode(t, input), &node); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(node.Annotations, map[string]string{"example.com/user": "retained"}) {
		t.Fatalf("unexpected normalized annotations: %v", node.Annotations)
	}
}
