//go:build !linux

package connectivity

import (
	"context"

	clabernetesgeneratedclientset "github.com/srl-labs/clabernetes/generated/clientset"
	claberneteslogging "github.com/srl-labs/clabernetes/logging"
)

// NewManager returns a dispatcher. Non-Linux builds retain VXLAN support and report a clear
// runtime error if a slurpeeth Link is encountered.
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
		common: c,
		vxlan:  &vxlanManager{common: c},
	}, nil
}
