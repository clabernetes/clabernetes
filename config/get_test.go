package config //nolint:testpackage // tests exercise the unexported manager getter directly

import (
	"sync"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
)

func TestManagerGetContainerStopSignals(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		want bool
	}{
		{name: "disabled", want: false},
		{name: "enabled", want: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := &manager{
				lock: &sync.RWMutex{},
				config: &clabernetesapisv1alpha1.ConfigSpec{
					Deployment: clabernetesapisv1alpha1.ConfigDeployment{
						ContainerStopSignals: tt.want,
					},
				},
			}

			if got := m.GetContainerStopSignals(); got != tt.want {
				t.Fatalf("expected container stop signals %t, got %t", tt.want, got)
			}
		})
	}
}
