package topology

import (
	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesutil "github.com/clabernetes/clabernetes/util"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
	"gopkg.in/yaml.v3"
)

// ReconcileData is a struct that holds data that is common during a reconciliation process
// regardless of the type of clabernetes topology that is being reconciled.
type ReconcileData struct {
	Kind string

	PreviousHashes clabernetesapisv1alpha1.ReconcileHashes
	ResolvedHashes clabernetesapisv1alpha1.ReconcileHashes

	PreviousConfigs      map[string]*clabernetesutilcontainerlab.Config
	ResolvedConfigs      map[string]*clabernetesutilcontainerlab.Config
	ResolvedConfigsBytes []byte

	ResolvedTunnels map[string][]*clabernetesapisv1alpha1.PointToPointTunnel

	ResolvedExposedPorts map[string]*clabernetesapisv1alpha1.ExposedPorts

	PreviousNodeStatuses map[string]string
	NodeStatuses         map[string]string
	TopologyReady        bool

	TopologyState     clabernetesapisv1alpha1.TopologyState
	NodeProbeStatuses map[string]clabernetesapisv1alpha1.NodeProbeStatuses

	NodesNeedingReboot clabernetesutil.StringSet

	ShouldUpdateResource bool
}

// NewReconcileData accepts a Topology object and returns a ReconcileData object.
func NewReconcileData(
	owningTopology *clabernetesapisv1alpha1.Topology,
) (*ReconcileData, error) {
	status := owningTopology.Status

	rd := &ReconcileData{
		PreviousHashes: status.ReconcileHashes,
		ResolvedHashes: clabernetesapisv1alpha1.ReconcileHashes{
			FilesFromURL: make(map[string]string),
		},

		PreviousConfigs: make(map[string]*clabernetesutilcontainerlab.Config),
		ResolvedConfigs: make(map[string]*clabernetesutilcontainerlab.Config),

		ResolvedTunnels: make(map[string][]*clabernetesapisv1alpha1.PointToPointTunnel),

		ResolvedExposedPorts: map[string]*clabernetesapisv1alpha1.ExposedPorts{},

		PreviousNodeStatuses: owningTopology.Status.NodeReadiness,
		NodeStatuses:         make(map[string]string),
		NodeProbeStatuses:    make(map[string]clabernetesapisv1alpha1.NodeProbeStatuses),
		NodesNeedingReboot:   clabernetesutil.NewStringSet(),
	}

	for nodeName, nodeConfig := range status.Configs {
		rd.PreviousConfigs[nodeName] = &clabernetesutilcontainerlab.Config{}

		err := yaml.Unmarshal([]byte(nodeConfig), rd.PreviousConfigs[nodeName])
		if err != nil {
			return nil, err
		}
	}

	return rd, nil
}

// SetStatus accepts a topology status and updates it with the ReconcileData information. This is
// called prior to updating a clabernetes topology object so that the hashes and information that
// we set in ReconcileData makes its way to the CR.
func (r *ReconcileData) SetStatus(
	owningTopologyStatus *clabernetesapisv1alpha1.TopologyStatus,
) error {
	owningTopologyStatus.Kind = r.Kind
	owningTopologyStatus.ExposedPorts = r.ResolvedExposedPorts

	owningTopologyStatus.ReconcileHashes = r.ResolvedHashes

	owningTopologyStatus.Configs = make(map[string]string)

	for nodeName, nodeConfig := range r.ResolvedConfigs {
		configBytes, err := yaml.Marshal(nodeConfig)
		if err != nil {
			return err
		}

		owningTopologyStatus.Configs[nodeName] = string(configBytes)
	}

	owningTopologyStatus.NodeReadiness = r.NodeStatuses
	owningTopologyStatus.TopologyReady = r.TopologyReady
	owningTopologyStatus.TopologyState = r.TopologyState
	owningTopologyStatus.NodeProbeStatuses = r.NodeProbeStatuses

	return nil
}

// ConfigMapHasChanges returns true if the data that gets stored in the topology configmap has
// changed between the last reconcile and the current iteration. This is just a helper to be more
// verbose/clear what we are checking rather than having a giant conditional in the Reconciler.
func (r *ReconcileData) ConfigMapHasChanges() bool {
	if r.PreviousHashes.Config != r.ResolvedHashes.Config {
		return true
	}

	if r.PreviousHashes.ImagePullSecrets != r.ResolvedHashes.ImagePullSecrets {
		return true
	}

	if r.NodesNeedingReboot.Len() != 0 {
		return true
	}

	return false
}
