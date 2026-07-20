package launcher

import (
	clabernetesgeneratedclientset "github.com/clabernetes/clabernetes/generated/clientset"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	"k8s.io/client-go/rest"
)

func mustNewKubeClabernetesClient(
	logger claberneteslogging.Instance,
) *clabernetesgeneratedclientset.Clientset {
	kubeConfig, err := rest.InClusterConfig()
	if err != nil {
		logger.Fatalf("failed getting in cluster kubeconfig, err: %s", err)
	}

	kubeClabernetesClient, err := clabernetesgeneratedclientset.NewForConfig(kubeConfig)
	if err != nil {
		logger.Fatalf(
			"failed creating clabernetes kube client from in cluster kubeconfig, err: %s",
			err,
		)
	}

	return kubeClabernetesClient
}
