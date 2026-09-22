//nolint:testpackage // Exercise bootstrap precedence and the locked getter.
package config

import (
	"sync"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	k8scorev1 "k8s.io/api/core/v1"
)

func TestRolloutBootstrapAndGetter(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, mode, value string
		existing          *clabernetesapisv1alpha1.ConfigRollout
		want              int32
		invalid           bool
	}{
		{name: "unset"},
		{name: "enabled", value: "100", want: 100},
		{name: "explicit disabled retained", value: "100", existing: &clabernetesapisv1alpha1.ConfigRollout{}, want: 0},
		{name: "overwrite enabled", value: "100", mode: "overwrite", existing: &clabernetesapisv1alpha1.ConfigRollout{}, want: 100},
		{name: "overwrite disabled", value: "0", mode: "overwrite", existing: &clabernetesapisv1alpha1.ConfigRollout{BatchSize: 100}},
		{name: "negative", value: "-1", invalid: true},
		{name: "overflow", value: "2147483648", invalid: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			config := &clabernetesapisv1alpha1.Config{
				Spec: clabernetesapisv1alpha1.ConfigSpec{Rollout: tt.existing},
			}
			data := map[string]string{"mergeMode": tt.mode}
			if tt.value != "" {
				data["rolloutBatchSize"] = tt.value
			}
			err := MergeFromBootstrapConfig(&k8scorev1.ConfigMap{Data: data}, config, true)
			if (err != nil) != tt.invalid {
				t.Fatalf("unexpected bootstrap error: %v", err)
			}
			if tt.invalid {
				return
			}
			m := &manager{lock: &sync.RWMutex{}, config: &config.Spec}
			if got := m.GetRolloutBatchSize(); got != tt.want {
				t.Fatalf("got %d want %d", got, tt.want)
			}
		})
	}
}

func TestRolloutHostBootstrapAndGetter(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, mode, value string
		existing          *clabernetesapisv1alpha1.ConfigRollout
		want              int32
		invalid           bool
	}{
		{name: "unset"},
		{name: "enabled", value: "100", want: 100},
		{name: "explicit disabled retained", value: "100", existing: &clabernetesapisv1alpha1.ConfigRollout{}, want: 0},
		{name: "overwrite enabled", value: "100", mode: "overwrite", existing: &clabernetesapisv1alpha1.ConfigRollout{}, want: 100},
		{name: "overwrite disabled", value: "0", mode: "overwrite", existing: &clabernetesapisv1alpha1.ConfigRollout{MaxConcurrentPerHost: 100}},
		{name: "negative", value: "-1", invalid: true},
		{name: "overflow", value: "2147483648", invalid: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			config := &clabernetesapisv1alpha1.Config{
				Spec: clabernetesapisv1alpha1.ConfigSpec{Rollout: tt.existing},
			}
			data := map[string]string{"mergeMode": tt.mode}
			if tt.value != "" {
				data["rolloutMaxConcurrentPerHost"] = tt.value
			}
			err := MergeFromBootstrapConfig(&k8scorev1.ConfigMap{Data: data}, config, true)
			if (err != nil) != tt.invalid {
				t.Fatalf("unexpected bootstrap error: %v", err)
			}
			if tt.invalid {
				return
			}
			m := &manager{lock: &sync.RWMutex{}, config: &config.Spec}
			if got := m.GetRolloutMaxConcurrentPerHost(); got != tt.want {
				t.Fatalf("got %d want %d", got, tt.want)
			}
		})
	}
}
