//go:build linux

package connectivity

import (
	"context"

	clabernetesgeneratedclientset "github.com/clabernetes/clabernetes/generated/clientset"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
)

// NewManager returns a dispatcher that realizes each terminating Link with its own flavor.
func NewManager(
	ctx context.Context,
	cancelChan chan bool,
	logger claberneteslogging.Instance,
	clabernetesClient *clabernetesgeneratedclientset.Clientset,
	initialTunnels []*Tunnel,
) (Manager, error) {
	c := &common{
		ctx:               ctx,
		cancelChan:        cancelChan,
		logger:            logger,
		clabernetesClient: clabernetesClient,
		initialTunnels:    initialTunnels,
	}

	return &dispatcherManager{
		common:    c,
		vxlan:     &vxlanManager{common: c},
		slurpeeth: &slurpeethManager{common: c},
	}, nil
}
