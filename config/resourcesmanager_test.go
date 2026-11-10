package config //nolint:testpackage // tests exercise the unexported manager getter directly

import (
	"sync"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	k8scorev1 "k8s.io/api/core/v1"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
)

func TestManagerAndFakeResolveIndependentResourceCopies(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name        string
		new         func(map[string]k8scorev1.ResourceRequirements) Manager
		mutateInput bool
	}{
		{
			name: "manager",
			new: func(resources map[string]k8scorev1.ResourceRequirements) Manager {
				apiResources := map[string]clabernetesapisv1alpha1.ResourceRequirements{}
				for kind, value := range resources {
					apiResources[kind] = *clabernetesapisv1alpha1.FromCoreV1ResourceRequirements(&value)
				}

				return &manager{
					lock: &sync.RWMutex{},
					config: &clabernetesapisv1alpha1.ConfigSpec{
						Deployment: clabernetesapisv1alpha1.ConfigDeployment{ResourcesByKind: apiResources},
					},
				}
			},
		},
		{
			name: "fake",
			new: func(resources map[string]k8scorev1.ResourceRequirements) Manager {
				return NewFakeManager(WithResourcesByKind(resources))
			},
			mutateInput: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			resources := map[string]k8scorev1.ResourceRequirements{
				"linux": {Requests: k8scorev1.ResourceList{
					k8scorev1.ResourceCPU: apiresource.MustParse("100m"),
				}},
			}
			manager := test.new(resources)
			if test.mutateInput {
				original := resources["linux"]
				original.Requests[k8scorev1.ResourceCPU] = apiresource.MustParse("8")
				resources["linux"] = original
			}

			got, err := manager.GetResources("linux", "alpine:3.22")
			if err != nil {
				t.Fatal(err)
			}
			if got.Requests.Cpu().String() != "100m" {
				t.Fatalf(
					"selected CPU = %q, want original configured value 100m",
					got.Requests.Cpu(),
				)
			}

			got.Requests[k8scorev1.ResourceCPU] = apiresource.MustParse("4")
			again, err := manager.GetResources("linux", "alpine:3.22")
			if err != nil {
				t.Fatal(err)
			}
			if again.Requests.Cpu().String() != "100m" {
				t.Fatalf("selected resources were shared with caller: %q", again.Requests.Cpu())
			}
		})
	}
}
