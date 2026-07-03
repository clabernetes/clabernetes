package topology

import (
	"fmt"
	"maps"
	"reflect"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/srl-labs/clabernetes/config"
	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	claberneteserrors "github.com/srl-labs/clabernetes/errors"
	claberneteslogging "github.com/srl-labs/clabernetes/logging"
	clabernetesutil "github.com/srl-labs/clabernetes/util"
	clabernetesutilcontainerlab "github.com/srl-labs/clabernetes/util/containerlab"
	clabernetesutilkubernetes "github.com/srl-labs/clabernetes/util/kubernetes"
	"gopkg.in/yaml.v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
)

// NodeResourceName returns the name of per-node resources (deployment, node cr) for the given
// node of the given topology.
func NodeResourceName(owningTopology *clabernetesapisv1alpha1.Topology, nodeName string) string {
	if ResolveTopologyRemovePrefix(owningTopology) {
		return nodeName
	}

	return fmt.Sprintf("%s-%s", owningTopology.GetName(), nodeName)
}

// NodeReconciler is a subcomponent of the "TopologyReconciler" but is exposed for testing
// purposes. This is the component responsible for rendering/validating the Node crs for a
// clabernetes topology resource.
type NodeReconciler struct {
	log                 claberneteslogging.Instance
	configManagerGetter clabernetesconfig.ManagerGetterFunc
}

// NewNodeReconciler returns an instance of NodeReconciler.
func NewNodeReconciler(
	log claberneteslogging.Instance,
	configManagerGetter clabernetesconfig.ManagerGetterFunc,
) *NodeReconciler {
	return &NodeReconciler{
		log:                 log,
		configManagerGetter: configManagerGetter,
	}
}

// Resolve accepts a mapping of clabernetes configs and a list of node crs that are -- by owner
// reference and/or labels -- associated with the topology. It returns a ObjectDiffer object
// that contains the missing, extra, and current node crs for the topology.
func (r *NodeReconciler) Resolve(
	ownedNodes *clabernetesapisv1alpha1.NodeList,
	clabernetesConfigs map[string]*clabernetesutilcontainerlab.Config,
	_ *clabernetesapisv1alpha1.Topology,
) (*clabernetesutil.ObjectDiffer[*clabernetesapisv1alpha1.Node], error) {
	nodes := &clabernetesutil.ObjectDiffer[*clabernetesapisv1alpha1.Node]{
		Current: map[string]*clabernetesapisv1alpha1.Node{},
	}

	for i := range ownedNodes.Items {
		labels := ownedNodes.Items[i].Labels

		if labels == nil {
			return nil, fmt.Errorf(
				"%w: labels are nil, but we expect to see topology owner label here",
				claberneteserrors.ErrInvalidData,
			)
		}

		nodeName, ok := labels[clabernetesconstants.LabelTopologyNode]
		if !ok || nodeName == "" {
			return nil, fmt.Errorf(
				"%w: topology node label is missing or empty",
				claberneteserrors.ErrInvalidData,
			)
		}

		nodes.Current[nodeName] = &ownedNodes.Items[i]
	}

	allNodes := make([]string, len(clabernetesConfigs))

	var nodeIdx int

	for nodeName := range clabernetesConfigs {
		allNodes[nodeIdx] = nodeName

		nodeIdx++
	}

	nodes.SetMissing(allNodes)
	nodes.SetExtra(allNodes)

	return nodes, nil
}

// Render accepts the owning topology, the reconcile data, and a node name and renders the final
// node cr for this node.
func (r *NodeReconciler) Render(
	owningTopology *clabernetesapisv1alpha1.Topology,
	reconcileData *ReconcileData,
	nodeName string,
) (*clabernetesapisv1alpha1.Node, error) {
	owningTopologyName := owningTopology.GetName()

	configBytes, err := yaml.Marshal(reconcileData.ResolvedConfigs[nodeName])
	if err != nil {
		return nil, err
	}

	annotations, globalLabels := r.configManagerGetter().GetAllMetadata()

	labels := map[string]string{
		clabernetesconstants.LabelApp:           clabernetesconstants.Clabernetes,
		clabernetesconstants.LabelName:          owningTopologyName,
		clabernetesconstants.LabelTopologyOwner: owningTopologyName,
		clabernetesconstants.LabelTopologyNode:  nodeName,
		clabernetesconstants.LabelTopologyKind:  GetTopologyKind(owningTopology),
	}

	maps.Copy(labels, globalLabels)

	return &clabernetesapisv1alpha1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:        NodeResourceName(owningTopology, nodeName),
			Namespace:   owningTopology.GetNamespace(),
			Annotations: annotations,
			Labels:      labels,
		},
		Spec: clabernetesapisv1alpha1.NodeSpec{
			TopologyName:     owningTopologyName,
			NodeName:         nodeName,
			Config:           string(configBytes),
			FilesFromURL:     owningTopology.Spec.Deployment.FilesFromURL[nodeName],
			ImagePullSecrets: owningTopology.Spec.ImagePull.PullSecrets,
		},
	}, nil
}

// RenderAll accepts the owning topology, the reconcile data, and a list of node names and renders
// the final node crs for the given nodes.
func (r *NodeReconciler) RenderAll(
	owningTopology *clabernetesapisv1alpha1.Topology,
	reconcileData *ReconcileData,
	nodeNames []string,
) ([]*clabernetesapisv1alpha1.Node, error) {
	nodes := make([]*clabernetesapisv1alpha1.Node, len(nodeNames))

	for idx, nodeName := range nodeNames {
		node, err := r.Render(
			owningTopology,
			reconcileData,
			nodeName,
		)
		if err != nil {
			return nil, err
		}

		nodes[idx] = node
	}

	return nodes, nil
}

// Conforms checks if the existing node cr conforms to the rendered expectation. Note that only
// the *spec* (and metadata) is compared -- the status is written by the controller after
// processing deployments and is not part of the rendered object.
func (r *NodeReconciler) Conforms(
	existingNode,
	renderedNode *clabernetesapisv1alpha1.Node,
	expectedOwnerUID apimachinerytypes.UID,
) bool {
	if !reflect.DeepEqual(existingNode.Spec, renderedNode.Spec) {
		return false
	}

	if !clabernetesutilkubernetes.ExistingMapStringStringContainsAllExpectedKeyValues(
		existingNode.ObjectMeta.Annotations,
		renderedNode.ObjectMeta.Annotations,
	) {
		return false
	}

	if !clabernetesutilkubernetes.ExistingMapStringStringContainsAllExpectedKeyValues(
		existingNode.ObjectMeta.Labels,
		renderedNode.ObjectMeta.Labels,
	) {
		return false
	}

	if len(existingNode.ObjectMeta.OwnerReferences) != 1 {
		// we should have only one owner reference, the owning topology
		return false
	}

	if existingNode.ObjectMeta.OwnerReferences[0].UID != expectedOwnerUID {
		// owner ref uid is not us
		return false
	}

	return true
}
