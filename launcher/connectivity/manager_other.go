//go:build !linux

package connectivity

import (
	"context"
	"fmt"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	claberneteserrors "github.com/clabernetes/clabernetes/errors"
	clabernetesgeneratedclientset "github.com/clabernetes/clabernetes/generated/clientset"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
)

// NewManager returns a connectivity Manager for the given connectivity flavor.
func NewManager(
	ctx context.Context,
	cancelChan chan bool,
	logger claberneteslogging.Instance,
	clabernetesClient *clabernetesgeneratedclientset.Clientset,
	initialTunnels []*clabernetesapisv1alpha1.PointToPointTunnel,
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
	default:
		// just excluding slurpeeth for easy testing/linting reasons basically since we assume this
		// will only ever run on linux anyway
		return nil, fmt.Errorf(
			"%w: unknown connectivity kind, cannot create connectivity manager",
			claberneteserrors.ErrLaunch,
		)
	}
}
