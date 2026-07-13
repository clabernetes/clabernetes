//go:build linux

package connectivity

import (
	"context"
	"fmt"

	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	claberneteserrors "github.com/srl-labs/clabernetes/errors"
	clabernetesgeneratedclientset "github.com/srl-labs/clabernetes/generated/clientset"
	claberneteslogging "github.com/srl-labs/clabernetes/logging"
)

// NewManager returns a connectivity Manager for the given connectivity flavor.
func NewManager(
	ctx context.Context,
	cancelChan chan bool,
	logger claberneteslogging.Instance,
	clabernetesClient *clabernetesgeneratedclientset.Clientset,
	initialTunnels []*Tunnel,
	connectivityKind string,
) (Manager, error) {
	c := &common{
		ctx:               ctx,
		cancelChan:        cancelChan,
		logger:            logger,
		clabernetesClient: clabernetesClient,
		initialTunnels:    initialTunnels,
	}

	switch connectivityKind {
	case clabernetesconstants.ConnectivityVXLAN:
		return &vxlanManager{
			common: c,
		}, nil
	case clabernetesconstants.ConnectivitySlurpeeth:
		return &slurpeethManager{
			common: c,
		}, nil
	default:
		return nil, fmt.Errorf(
			"%w: unknown connectivity kind, cannot create connectivity manager",
			claberneteserrors.ErrLaunch,
		)
	}
}
