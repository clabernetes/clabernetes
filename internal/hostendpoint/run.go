//nolint:err113,noinlineerr,perfsprint,wsl_v5 // Startup fails at each missing dependency.
package hostendpoint

import (
	"context"
	"fmt"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	k8scorev1 "k8s.io/api/core/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Run starts the node-local host-endpoint daemon against in-cluster Kubernetes state.
func Run(ctx context.Context, nodeName, socketPath string) error {
	if ctx == nil || nodeName == "" {
		return fmt.Errorf("host-endpoint daemon identity is incomplete")
	}
	if socketPath == "" {
		socketPath = DefaultSocketPath
	}
	config, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("loading in-cluster configuration: %w", err)
	}
	scheme := apimachineryruntime.NewScheme()
	if err = clabernetesapisv1alpha1.AddToScheme(scheme); err != nil {
		return fmt.Errorf("registering c9s API types: %w", err)
	}
	if err = k8scorev1.AddToScheme(scheme); err != nil {
		return fmt.Errorf("registering Kubernetes API types: %w", err)
	}
	client, err := ctrlruntimeclient.New(config, ctrlruntimeclient.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("creating host-endpoint Kubernetes client: %w", err)
	}

	return (&Daemon{
		NodeName:   nodeName,
		State:      KubernetesState{Client: client},
		Operations: newOperations(),
	}).Serve(ctx, socketPath)
}
