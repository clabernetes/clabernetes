package topology

import (
	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	clabernetesutil "github.com/srl-labs/clabernetes/util"
	clabernetesutilcontainerlab "github.com/srl-labs/clabernetes/util/containerlab"
)

// ReconcileData is a struct that holds data that is common during a reconciliation process
// regardless of the type of clabernetes topology that is being reconciled.
type ReconcileData struct {
	Kind string

	// PreviousConfigs holds the previously rendered sub-topologies -- these are loaded from the
	// Node objects of the topology (during the node reconciliation phase) and are used to know if
	// a given node's config changed between the last and current reconciliation (and therefore
	// needs a restart).
	PreviousConfigs map[string]*clabernetesutilcontainerlab.Config
	ResolvedConfigs map[string]*clabernetesutilcontainerlab.Config

	ResolvedTunnels map[string][]*clabernetesapisv1alpha1.PointToPointTunnel

	ResolvedExposedPorts map[string]*clabernetesapisv1alpha1.ExposedPorts

	PreviousNodeStatuses map[string]string
	NodeStatuses         map[string]string
	TopologyReady        bool

	TopologyState     clabernetesapisv1alpha1.TopologyState
	NodeProbeStatuses map[string]*clabernetesapisv1alpha1.NodeProbeStatuses

	NodesNeedingReboot clabernetesutil.StringSet

	ShouldUpdateResource bool
}

// NewReconcileData accepts a Topology object and returns a ReconcileData object.
func NewReconcileData(
	owningTopology *clabernetesapisv1alpha1.Topology,
) (*ReconcileData, error) {
	rd := &ReconcileData{
		PreviousConfigs: make(map[string]*clabernetesutilcontainerlab.Config),
		ResolvedConfigs: make(map[string]*clabernetesutilcontainerlab.Config),

		ResolvedTunnels: make(map[string][]*clabernetesapisv1alpha1.PointToPointTunnel),

		ResolvedExposedPorts: map[string]*clabernetesapisv1alpha1.ExposedPorts{},

		PreviousNodeStatuses: make(map[string]string),
		NodeStatuses:         make(map[string]string),
		NodeProbeStatuses:    make(map[string]*clabernetesapisv1alpha1.NodeProbeStatuses),
		NodesNeedingReboot:   clabernetesutil.NewStringSet(),

		TopologyState: owningTopology.Status.TopologyState,
	}

	return rd, nil
}

// SetStatus accepts a topology status and updates it with the ReconcileData information. This is
// called prior to updating a clabernetes topology object so that the aggregated node information
// that we set in ReconcileData makes its way to the CR. Note that all *per node* information
// lives on the Node objects for the topology, so the Topology status only ever holds (small)
// aggregate data.
func (r *ReconcileData) SetStatus(
	owningTopologyStatus *clabernetesapisv1alpha1.TopologyStatus,
) error {
	owningTopologyStatus.Kind = r.Kind

	owningTopologyStatus.NodeCount = len(r.ResolvedConfigs)

	readyCount := 0

	for _, nodeStatus := range r.NodeStatuses {
		if nodeStatus == clabernetesconstants.NodeStatusReady {
			readyCount++
		}
	}

	owningTopologyStatus.ReadyNodeCount = readyCount

	owningTopologyStatus.TopologyReady = r.TopologyReady
	owningTopologyStatus.TopologyState = r.TopologyState

	return nil
}
