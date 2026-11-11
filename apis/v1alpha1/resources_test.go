package v1alpha1_test

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	k8scorev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
	sigsyaml "sigs.k8s.io/yaml"
)

func TestResourceRequirementsFlattenedRoundTrip(t *testing.T) {
	cpu := apiresource.MustParse("1500m")
	memory := apiresource.MustParse("2Gi")
	ephemeral := apiresource.MustParse("500Mi")
	hugepages := apiresource.MustParse("4Gi")
	gpu := apiresource.MustParse("2")
	original := &clabernetesapisv1alpha1.ResourceRequirements{
		Requests: &clabernetesapisv1alpha1.ResourceList{
			CPU: &cpu, Memory: &memory,
			AdditionalResources: map[k8scorev1.ResourceName]apiresource.Quantity{
				k8scorev1.ResourceEphemeralStorage: ephemeral,
				"hugepages-2Mi":                    hugepages,
				"example.com/gpu":                  gpu,
			},
		},
		Claims: []k8scorev1.ResourceClaim{{Name: "gpu"}},
	}

	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "additionalResources") {
		t.Fatalf("additional resource names were wrapped instead of flattened: %s", encoded)
	}

	decoded := &clabernetesapisv1alpha1.ResourceRequirements{}
	if err = json.Unmarshal(encoded, decoded); err != nil {
		t.Fatal(err)
	}

	wantNative := &k8scorev1.ResourceRequirements{
		Requests: k8scorev1.ResourceList{
			k8scorev1.ResourceCPU:              cpu,
			k8scorev1.ResourceMemory:           memory,
			k8scorev1.ResourceEphemeralStorage: ephemeral,
			"hugepages-2Mi":                    hugepages,
			"example.com/gpu":                  gpu,
		},
		Limits: nil,
		Claims: []k8scorev1.ResourceClaim{{Name: "gpu"}},
	}
	if got := decoded.ToCoreV1(); !reflect.DeepEqual(got, wantNative) {
		t.Fatalf("native resources after round trip = %#v, want %#v", got, wantNative)
	}

	decodedCopy := decoded.DeepCopy()
	decodedCopy.Requests.AdditionalResources["example.com/gpu"] = apiresource.MustParse("8")
	originalGPU := decoded.Requests.AdditionalResources["example.com/gpu"]
	if got := originalGPU.String(); got != "2" {
		t.Fatalf("deep copy shares additional resources: %q", got)
	}
}

func TestResourceRequirementsCRDSchemaIsStructuredAndDocumented(t *testing.T) {
	root := schemaAt(t, "../../assets/crd/c9s.run_configs.yaml")
	configDeployment := root.Properties["spec"].Properties["deployment"].Properties
	generic := configDeployment["resourcesDefault"]
	for _, path := range []struct {
		name string
		root *apiextensionsv1.JSONSchemaProps
	}{
		{name: "resourcesDefault", root: &generic},
		{name: "resourcesByKind", root: configDeployment["resourcesByKind"].AdditionalProperties.Schema},
		{name: "resourcesByImage", root: configDeployment["resourcesByImage"].AdditionalProperties.Schema},
	} {
		t.Run(path.name, func(t *testing.T) {
			assertStructuredResourceSchema(t, *path.root)
		})
	}

	for _, crd := range []struct {
		name  string
		file  string
		field string
	}{
		{name: "NodeProfile", file: "../../assets/crd/c9s.run_nodeprofiles.yaml", field: "resources"},
		{name: "Topology", file: "../../assets/crd/c9s.run_topologies.yaml", field: "resources"},
	} {
		t.Run(crd.name, func(t *testing.T) {
			root = schemaAt(t, crd.file)
			resourceField := root.Properties["spec"]
			if crd.name == "Topology" {
				resourceField = resourceField.Properties["deployment"]
			}
			resourceField = resourceField.Properties[crd.field]
			if crd.name == "Topology" {
				resourceField = *resourceField.AdditionalProperties.Schema
			}
			assertStructuredResourceSchema(t, resourceField)
		})
	}
}

func schemaAt(t *testing.T, path string) apiextensionsv1.JSONSchemaProps {
	t.Helper()

	raw, err := os.ReadFile(path) //nolint:gosec // paths are fixed repository schema fixtures
	if err != nil {
		t.Fatal(err)
	}
	crd := &apiextensionsv1.CustomResourceDefinition{}
	if err = sigsyaml.Unmarshal(raw, crd); err != nil {
		t.Fatalf("decode CRD %s: %v", path, err)
	}

	return *crd.Spec.Versions[0].Schema.OpenAPIV3Schema
}

func assertStructuredResourceSchema(t *testing.T, schema apiextensionsv1.JSONSchemaProps) {
	t.Helper()

	for _, field := range []string{"requests", "limits"} {
		entry := schema.Properties[field]
		if entry.Description == "" {
			t.Errorf("%s has no description", field)
		}
		if entry.XPreserveUnknownFields == nil || !*entry.XPreserveUnknownFields {
			t.Errorf("%s does not preserve additional Kubernetes resource names", field)
		}

		for _, resourceName := range []string{"cpu", "memory"} {
			property := entry.Properties[resourceName]
			if property.Description == "" {
				t.Errorf("%s.%s has no quantity description", field, resourceName)
			}
			if !property.XIntOrString {
				t.Errorf("%s.%s is not a Kubernetes quantity", field, resourceName)
			}
		}
		if _, exists := entry.Properties["additionalResources"]; exists {
			t.Errorf("%s leaks an additionalResources YAML wrapper", field)
		}
	}

	claims := schema.Properties["claims"]
	if claims.Description == "" || claims.Items == nil || claims.Items.Schema == nil ||
		claims.Items.Schema.Properties["name"].Description == "" {
		t.Errorf("claims is missing structured name documentation: %#v", claims)
	}
}
