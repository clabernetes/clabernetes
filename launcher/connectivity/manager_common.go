package connectivity

import (
	"context"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesgeneratedclientset "github.com/clabernetes/clabernetes/generated/clientset"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
)

// Manager is an interface defining a connectivity manager -- basically a small abstraction around
// the flavor of how we connect to other launcher pods and their containerlab nodes -- the standard
// way is via vxlan, and there is also an experimental tool "slurpeeth" for connectivity over tcp
// tunnels.
type Manager interface {
	// Run "runs" the connectivity flavor -- in the case of vxlan this simply means spinning up
	// the required tunnels, but for other flavors (slurpeeth) this means running the process that
	// watches the connectivity cr, and handles updates to the slurpeeth process/config. It is
	// expected for the Run method to just call logger.Fatal if there is any issue as this would
	// prevent c9s from doing anything useful anyway!
	Run()
}

type common struct {
	ctx               context.Context
	cancelChan        chan bool
	logger            claberneteslogging.Instance
	clabernetesClient *clabernetesgeneratedclientset.Clientset
	initialTunnels    []*clabernetesapisv1alpha1.PointToPointTunnel
}
