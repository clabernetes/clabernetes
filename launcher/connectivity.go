package launcher

import (
	"context"
	"os"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	claberneteslauncherconnectivity "github.com/srl-labs/clabernetes/launcher/connectivity"
)

func (c *clabernetes) connectivity() {
	tunnels, err := c.getTunnels()
	if err != nil {
		c.logger.Fatalf("failed loading tunnels content, err: %s", err)
	}

	connectivityManager, err := claberneteslauncherconnectivity.NewManager(
		c.ctx,
		nil,
		c.logger,
		c.kubeClabernetesClient,
		tunnels,
		os.Getenv(
			clabernetesconstants.LauncherConnectivityKind,
		),
	)
	if err != nil {
		c.logger.Fatalf("failed creating connectivity manager, err: %s", err)
	}

	connectivityManager.Run()
}

func (c *clabernetes) getTunnels() ([]*clabernetesapisv1alpha1.PointToPointTunnel, error) {
	ctx, cancel := context.WithTimeout(c.ctx, clientDefaultTimeout)
	defer cancel()

	return claberneteslauncherconnectivity.ListNodeTunnels(
		ctx,
		c.kubeClabernetesClient,
		os.Getenv(clabernetesconstants.PodNamespaceEnv),
		os.Getenv(clabernetesconstants.LauncherTopologyNameEnv),
		c.nodeName,
	)
}
