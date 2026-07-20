package topology

import (
	"maps"
	"reflect"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	clabernetesutilkubernetes "github.com/clabernetes/clabernetes/util/kubernetes"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
)

// ConnectivityReconciler is a subcomponent of the "TopologyReconciler" but is exposed for testing
// purposes. This is the component responsible for rendering/validating the Connectivity cr for the
// Topology.
type ConnectivityReconciler struct {
	log                 claberneteslogging.Instance
	configManagerGetter clabernetesconfig.ManagerGetterFunc
}

// NewConnectivityReconciler returns an instance of ConnectivityReconciler.
func NewConnectivityReconciler(
	log claberneteslogging.Instance,
	configManagerGetter clabernetesconfig.ManagerGetterFunc,
) *ConnectivityReconciler {
	return &ConnectivityReconciler{
		log:                 log,
		configManagerGetter: configManagerGetter,
	}
}

// Render returns a rendered Connectivity cr for the given topology/tunnels.
func (r *ConnectivityReconciler) Render(
	owningTopology *clabernetesapisv1alpha1.Topology,
	tunnels map[string][]*clabernetesapisv1alpha1.PointToPointTunnel,
) *clabernetesapisv1alpha1.Connectivity {
	owningTopologyName := owningTopology.GetName()

	annotations, globalLabels := r.configManagerGetter().GetAllMetadata()

	labels := map[string]string{
		clabernetesconstants.LabelApp:           clabernetesconstants.Clabernetes,
		clabernetesconstants.LabelName:          owningTopologyName,
		clabernetesconstants.LabelTopologyOwner: owningTopologyName,
		clabernetesconstants.LabelTopologyKind:  GetTopologyKind(owningTopology),
	}

	maps.Copy(labels, globalLabels)

	return &clabernetesapisv1alpha1.Connectivity{
		ObjectMeta: metav1.ObjectMeta{
			Name:        owningTopologyName,
			Namespace:   owningTopology.GetNamespace(),
			Annotations: annotations,
			Labels:      labels,
		},
		Spec: clabernetesapisv1alpha1.ConnectivitySpec{
			PointToPointTunnels: tunnels,
		},
	}
}

// Conforms checks if the existing connectivity cr conforms to the rendered expectation.
func (r *ConnectivityReconciler) Conforms(
	existingConnectivity,
	renderedConnectivity *clabernetesapisv1alpha1.Connectivity,
	expectedOwnerUID apimachinerytypes.UID,
) bool {
	if !reflect.DeepEqual(existingConnectivity.Spec, renderedConnectivity.Spec) {
		return false
	}

	if !clabernetesutilkubernetes.ExistingMapStringStringContainsAllExpectedKeyValues(
		existingConnectivity.ObjectMeta.Annotations,
		renderedConnectivity.ObjectMeta.Annotations,
	) {
		return false
	}

	if !clabernetesutilkubernetes.ExistingMapStringStringContainsAllExpectedKeyValues(
		existingConnectivity.ObjectMeta.Labels,
		renderedConnectivity.ObjectMeta.Labels,
	) {
		return false
	}

	if len(existingConnectivity.ObjectMeta.OwnerReferences) != 1 {
		// we should have only one owner reference, the extractor
		return false
	}

	if existingConnectivity.ObjectMeta.OwnerReferences[0].UID != expectedOwnerUID {
		// owner ref uid is not us
		return false
	}

	return true
}
