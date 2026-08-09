package launcher

import (
	claberneteslauncherconnectivity "github.com/clabernetes/clabernetes/launcher/connectivity"
)

func (c *clabernetes) connectivity() {
	connectivityManager, err := claberneteslauncherconnectivity.NewManager(
		c.ctx,
		nil,
		c.logger,
		c.kubeClabernetesClient,
		// the tunnel snapshot listed while materializing the topology -- so the tunnels line up
		// with the (boot time) host side veths; the manager's link watch takes over from there
		c.initialTunnels,
	)
	if err != nil {
		c.logger.Fatalf("failed creating connectivity manager, err: %s", err)
	}

	connectivityManager.Run()
}
