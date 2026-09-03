package v1alpha1_test

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"
)

func TestExposeSchemasUseOnlyExposeType(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		path string
	}{
		{name: "topology", path: "../../charts/clabernetes/crds/c9s.run_topologies.yaml"},
		{name: "node profile", path: "../../charts/clabernetes/crds/c9s.run_nodeprofiles.yaml"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			raw, err := os.ReadFile(test.path)
			if err != nil {
				t.Fatal(err)
			}

			crd := &apiextensionsv1.CustomResourceDefinition{}
			if err = yaml.Unmarshal(raw, crd); err != nil {
				t.Fatal(err)
			}

			expose := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.
				Properties["spec"].Properties["expose"]
			if _, exists := expose.Properties["disableExpose"]; exists {
				t.Fatal("expose schema still accepts disableExpose")
			}

			exposeType, exists := expose.Properties["exposeType"]
			if !exists {
				t.Fatal("expose schema has no exposeType property")
			}

			values := make([]string, 0, len(exposeType.Enum))
			for _, value := range exposeType.Enum {
				var decoded string
				if err = json.Unmarshal(value.Raw, &decoded); err != nil {
					t.Fatal(err)
				}
				values = append(values, decoded)
			}
			slices.Sort(values)

			expected := []string{"ClusterIP", "Headless", "LoadBalancer", "None"}
			if !slices.Equal(values, expected) {
				t.Fatalf("exposeType enum = %v, want %v", values, expected)
			}
		})
	}
}
