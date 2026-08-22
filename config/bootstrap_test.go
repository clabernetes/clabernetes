package config_test

import (
	"reflect"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	k8scorev1 "k8s.io/api/core/v1"
)

func TestMergeFromBootstrapConfigApplicationImagePullPolicy(t *testing.T) {
	t.Parallel()

	const bootstrapPolicy = "Always"

	for _, tt := range []struct {
		name           string
		configCRExists bool
		mergeMode      string
		existing       string
		want           string
	}{
		{
			name: "new config uses bootstrap value",
			want: bootstrapPolicy,
		},
		{
			name:           "merge fills empty value",
			configCRExists: true,
			want:           bootstrapPolicy,
		},
		{
			name:           "merge preserves existing value",
			configCRExists: true,
			existing:       "Never",
			want:           "Never",
		},
		{
			name:           "overwrite replaces existing value",
			configCRExists: true,
			mergeMode:      "overwrite",
			existing:       "Never",
			want:           bootstrapPolicy,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			bootstrapConfigMap := &k8scorev1.ConfigMap{
				Data: map[string]string{
					"imagePullPolicy": bootstrapPolicy,
					"mergeMode":       tt.mergeMode,
				},
			}
			config := &clabernetesapisv1alpha1.Config{
				Spec: clabernetesapisv1alpha1.ConfigSpec{
					ImagePull: clabernetesapisv1alpha1.ConfigImagePull{
						Policy: tt.existing,
					},
				},
			}

			err := clabernetesconfig.MergeFromBootstrapConfig(
				bootstrapConfigMap,
				config,
				tt.configCRExists,
			)
			if err != nil {
				t.Fatalf("merge bootstrap config: %v", err)
			}

			if config.Spec.ImagePull.Policy != tt.want {
				t.Fatalf(
					"expected image pull policy %q, got %q",
					tt.want,
					config.Spec.ImagePull.Policy,
				)
			}
		})
	}
}

func TestMergeFromBootstrapConfigImagePullSecrets(t *testing.T) {
	t.Parallel()

	bootstrapSecrets := []string{"registry-a", "registry-b"}
	for _, test := range []struct {
		name           string
		configCRExists bool
		mergeMode      string
		existing       []string
		want           []string
	}{
		{name: "new config uses bootstrap value", want: bootstrapSecrets},
		{
			name: "merge fills omitted value", configCRExists: true,
			want: bootstrapSecrets,
		},
		{
			name: "merge preserves explicit empty value", configCRExists: true,
			existing: []string{}, want: []string{},
		},
		{
			name: "merge preserves existing value", configCRExists: true,
			existing: []string{"existing"}, want: []string{"existing"},
		},
		{
			name: "overwrite replaces existing value", configCRExists: true,
			mergeMode: "overwrite", existing: []string{"existing"}, want: bootstrapSecrets,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			bootstrapConfigMap := &k8scorev1.ConfigMap{
				Data: map[string]string{ //nolint:gosec // test fixture identifier, not a credential.
					"imagePullSecrets": "- registry-a\n- registry-b\n",
					"mergeMode":        test.mergeMode,
				},
			}

			config := &clabernetesapisv1alpha1.Config{Spec: clabernetesapisv1alpha1.ConfigSpec{
				ImagePull: clabernetesapisv1alpha1.ConfigImagePull{
					PullSecrets: test.existing,
				},
			}}
			if err := clabernetesconfig.MergeFromBootstrapConfig(
				bootstrapConfigMap,
				config,
				test.configCRExists,
			); err != nil {
				t.Fatal(err)
			}

			if !reflect.DeepEqual(config.Spec.ImagePull.PullSecrets, test.want) {
				t.Fatalf(
					"image pull Secrets = %#v, want %#v",
					config.Spec.ImagePull.PullSecrets,
					test.want,
				)
			}
		})
	}
}

func TestMergeFromBootstrapConfigRegistryMetadataTrust(t *testing.T) {
	t.Parallel()

	bootstrapConfigMap := &k8scorev1.ConfigMap{Data: map[string]string{
		"registryMetadataTrust": `
- registry: existing.example.test
  plainHTTP: true
- registry: added.example.test:5000
  caBundle: test-ca
`,
	}}
	config := &clabernetesapisv1alpha1.Config{Spec: clabernetesapisv1alpha1.ConfigSpec{
		ImagePull: clabernetesapisv1alpha1.ConfigImagePull{
			RegistryMetadataTrust: []clabernetesapisv1alpha1.RegistryMetadataTrustEntry{{
				Registry: "existing.example.test", CABundle: "existing-ca",
			}},
		},
	}}

	if err := clabernetesconfig.MergeFromBootstrapConfig(
		bootstrapConfigMap,
		config,
		true,
	); err != nil {
		t.Fatal(err)
	}

	if got := config.Spec.ImagePull.RegistryMetadataTrust; len(got) != 2 ||
		got[0].CABundle != "existing-ca" || got[1].Registry != "added.example.test:5000" {
		t.Fatalf("merged registry metadata trust = %#v", got)
	}
}
