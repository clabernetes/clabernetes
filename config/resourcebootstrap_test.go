package config_test

import (
	"reflect"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	k8scorev1 "k8s.io/api/core/v1"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
)

func testResources(cpu, memory string) clabernetesapisv1alpha1.ResourceRequirements {
	cpuQuantity := apiresource.MustParse(cpu)
	memoryQuantity := apiresource.MustParse(memory)

	return clabernetesapisv1alpha1.ResourceRequirements{
		Requests: &clabernetesapisv1alpha1.ResourceList{
			CPU: &cpuQuantity, Memory: &memoryQuantity,
		},
	}
}

func testBootstrapResourceMaps() (
	map[string]clabernetesapisv1alpha1.ResourceRequirements,
	map[string]clabernetesapisv1alpha1.ResourceRequirements,
) {
	return map[string]clabernetesapisv1alpha1.ResourceRequirements{
			"nokia_srlinux": testResources("2", "2Gi"),
			"linux":         testResources("200m", "512Mi"),
		}, map[string]clabernetesapisv1alpha1.ResourceRequirements{
			"^registry\\.example/srl:.*$": testResources("3", "3Gi"),
		}
}

func TestMergeFromBootstrapConfigResourceMaps(t *testing.T) {
	t.Parallel()

	bootstrapKinds, bootstrapImages := testBootstrapResourceMaps()

	for _, test := range []struct {
		name           string
		configExists   bool
		mergeMode      string
		existingKinds  map[string]clabernetesapisv1alpha1.ResourceRequirements
		existingImages map[string]clabernetesapisv1alpha1.ResourceRequirements
		wantKinds      map[string]clabernetesapisv1alpha1.ResourceRequirements
		wantImages     map[string]clabernetesapisv1alpha1.ResourceRequirements
	}{
		{
			name: "fresh Config receives both maps", wantKinds: bootstrapKinds,
			wantImages: bootstrapImages,
		},
		{
			name: "merge preserves nil maps", configExists: true,
		},
		{
			name: "merge opts into non-nil kind map independently", configExists: true,
			existingKinds: map[string]clabernetesapisv1alpha1.ResourceRequirements{
				"nokia_srlinux": testResources("4", "4Gi"),
			},
			wantKinds: map[string]clabernetesapisv1alpha1.ResourceRequirements{
				"nokia_srlinux": testResources("4", "4Gi"),
				"linux":         testResources("200m", "512Mi"),
			},
		},
		{
			name: "merge opts into non-nil image map independently", configExists: true,
			existingImages: map[string]clabernetesapisv1alpha1.ResourceRequirements{
				"^registry\\.example/srl:.*$": testResources("6", "6Gi"),
			},
			wantImages: map[string]clabernetesapisv1alpha1.ResourceRequirements{
				"^registry\\.example/srl:.*$": testResources("6", "6Gi"),
			},
		},
		{
			name: "merge fills explicitly empty enabled maps", configExists: true,
			existingKinds:  map[string]clabernetesapisv1alpha1.ResourceRequirements{},
			existingImages: map[string]clabernetesapisv1alpha1.ResourceRequirements{},
			wantKinds:      bootstrapKinds, wantImages: bootstrapImages,
		},
		{
			name: "overwrite replaces user maps", configExists: true, mergeMode: "overwrite",
			existingKinds: map[string]clabernetesapisv1alpha1.ResourceRequirements{
				"custom": testResources("8", "8Gi"),
			},
			existingImages: map[string]clabernetesapisv1alpha1.ResourceRequirements{
				"custom": testResources("8", "8Gi"),
			},
			wantKinds: bootstrapKinds, wantImages: bootstrapImages,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			bootstrap := &k8scorev1.ConfigMap{Data: map[string]string{
				"mergeMode": test.mergeMode,
				"resourcesByKind": `
nokia_srlinux:
  requests: {cpu: "2", memory: 2Gi}
linux:
  requests: {cpu: 200m, memory: 512Mi}
`,
				"resourcesByImage": `
'^registry\.example/srl:.*$':
  requests: {cpu: "3", memory: 3Gi}
`,
			}}
			config := &clabernetesapisv1alpha1.Config{Spec: clabernetesapisv1alpha1.ConfigSpec{
				Deployment: clabernetesapisv1alpha1.ConfigDeployment{
					ResourcesByKind:  test.existingKinds,
					ResourcesByImage: test.existingImages,
				},
			}}

			if err := clabernetesconfig.MergeFromBootstrapConfig(
				bootstrap, config, test.configExists,
			); err != nil {
				t.Fatalf("merge bootstrap config: %v", err)
			}

			if !reflect.DeepEqual(config.Spec.Deployment.ResourcesByKind, test.wantKinds) {
				t.Fatalf("kind resources = %#v, want %#v",
					config.Spec.Deployment.ResourcesByKind, test.wantKinds)
			}
			if !reflect.DeepEqual(config.Spec.Deployment.ResourcesByImage, test.wantImages) {
				t.Fatalf("image resources = %#v, want %#v",
					config.Spec.Deployment.ResourcesByImage, test.wantImages)
			}
		})
	}
}

func TestMergeFromBootstrapConfigResourceMapParseError(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"resourcesByKind", "resourcesByImage"} {
		t.Run(key, func(t *testing.T) {
			t.Parallel()

			bootstrap := &k8scorev1.ConfigMap{Data: map[string]string{key: "["}}
			if err := clabernetesconfig.MergeFromBootstrapConfig(
				bootstrap, &clabernetesapisv1alpha1.Config{}, true,
			); err == nil {
				t.Fatalf("expected %s parse error", key)
			}
		})
	}
}
