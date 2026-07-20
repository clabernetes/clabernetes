package connectivity

import (
	"context"
	"fmt"
	"os"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesgeneratedclientset "github.com/clabernetes/clabernetes/generated/clientset"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerywatch "k8s.io/apimachinery/pkg/watch"
)

func watchConnectivity(
	ctx context.Context,
	logger claberneteslogging.Instance,
	clabernetesClient *clabernetesgeneratedclientset.Clientset,
	handleUpdate func(nodeTunnels []*clabernetesapisv1alpha1.PointToPointTunnel),
) {
	nodeName := os.Getenv(clabernetesconstants.LauncherNodeNameEnv)

	listOptions := metav1.ListOptions{
		FieldSelector: fmt.Sprintf("metadata.name=%s", os.Getenv(
			clabernetesconstants.LauncherTopologyNameEnv,
		)),
		Watch: true,
	}

	watch, err := clabernetesClient.ClabernetesV1alpha1().
		Connectivities(os.Getenv(clabernetesconstants.PodNamespaceEnv)).
		Watch(ctx, listOptions)
	if err != nil {
		logger.Fatalf("failed watching clabernetes connectivity, err: %s", err)
	}

	for event := range watch.ResultChan() {
		switch event.Type {
		case apimachinerywatch.Modified:
			logger.Info("processing connectivity modification event")

			tunnelsCR, ok := event.Object.(*clabernetesapisv1alpha1.Connectivity)
			if !ok {
				logger.Warn(
					"failed casting event object to connectivity custom resource," +
						" this is probably a bug",
				)

				continue
			}

			nodeTunnels, ok := tunnelsCR.Spec.PointToPointTunnels[nodeName]
			if !ok {
				logger.Warnf(
					"no tunnels found for node %q, continuing but things may be broken",
					nodeName,
				)
			}

			handleUpdate(nodeTunnels)
		case apimachinerywatch.Added,
			apimachinerywatch.Deleted,
			apimachinerywatch.Bookmark,
			apimachinerywatch.Error:
			logger.Warnf(
				"connectivity resource had %s event occur, ignoring...", event.Type,
			)
		}
	}
}
