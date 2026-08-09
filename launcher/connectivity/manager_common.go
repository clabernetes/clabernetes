package connectivity

import (
	"context"

	clabernetesgeneratedclientset "github.com/clabernetes/clabernetes/generated/clientset"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
)

// Manager reconciles all per-Link connectivity flavors terminating on one launcher.
type Manager interface {
	// Run starts the flavor implementations and the terminating-Link watch.
	Run()
}

// flavorManager owns realizations for one connectivity flavor. The dispatcher ensures a local
// termination is removed from its former manager before it is handed to a different manager.
type flavorManager interface {
	start(initialTunnels []*Tunnel) error
	reconcile(tunnels []*Tunnel) error
}

type common struct {
	ctx               context.Context
	cancelChan        chan bool
	logger            claberneteslogging.Instance
	clabernetesClient *clabernetesgeneratedclientset.Clientset
	initialTunnels    []*Tunnel
}
