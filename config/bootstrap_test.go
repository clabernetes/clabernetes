package config_test

import (
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	k8scorev1 "k8s.io/api/core/v1"
)

func TestMergeFromBootstrapConfigCRIHostsDir(t *testing.T) {
	t.Parallel()

	const bootstrapHostsDir = "/etc/cri/conf.d/hosts"

	for _, tt := range []struct {
		name           string
		configCRExists bool
		mergeMode      string
		existing       string
		want           string
	}{
		{
			name: "new config uses bootstrap value",
			want: bootstrapHostsDir,
		},
		{
			name:           "merge fills empty value",
			configCRExists: true,
			want:           bootstrapHostsDir,
		},
		{
			name:           "merge preserves existing value",
			configCRExists: true,
			existing:       "/etc/containerd/certs.d",
			want:           "/etc/containerd/certs.d",
		},
		{
			name:           "overwrite replaces existing value",
			configCRExists: true,
			mergeMode:      "overwrite",
			existing:       "/etc/containerd/certs.d",
			want:           bootstrapHostsDir,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			bootstrapConfigMap := &k8scorev1.ConfigMap{
				Data: map[string]string{
					"criHostsDir": bootstrapHostsDir,
					"mergeMode":   tt.mergeMode,
				},
			}
			config := &clabernetesapisv1alpha1.Config{
				Spec: clabernetesapisv1alpha1.ConfigSpec{
					ImagePull: clabernetesapisv1alpha1.ConfigImagePull{
						CRIHostsDir: tt.existing,
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

			if config.Spec.ImagePull.CRIHostsDir != tt.want {
				t.Fatalf(
					"expected CRI hosts directory %q, got %q",
					tt.want,
					config.Spec.ImagePull.CRIHostsDir,
				)
			}
		})
	}
}
