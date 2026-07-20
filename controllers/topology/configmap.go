package topology

import (
	"fmt"
	"maps"
	"reflect"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
	clabernetesutilkubernetes "github.com/clabernetes/clabernetes/util/kubernetes"
	"gopkg.in/yaml.v3"
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
)

// ConfigMapReconciler is a subcomponent of the "TopologyReconciler" but is exposed for testing
// purposes. This is the component responsible for rendering/validating configmaps for a
// clabernetes topology resource.
type ConfigMapReconciler struct {
	log                 claberneteslogging.Instance
	configManagerGetter clabernetesconfig.ManagerGetterFunc
}

// NewConfigMapReconciler returns an instance of ConfigMapReconciler.
func NewConfigMapReconciler(
	log claberneteslogging.Instance,
	configManagerGetter clabernetesconfig.ManagerGetterFunc,
) *ConfigMapReconciler {
	return &ConfigMapReconciler{
		log:                 log,
		configManagerGetter: configManagerGetter,
	}
}

// Render accepts an object (just for name/namespace reasons) and a mapping of clabernetes
// sub-topology configs and tunnels and renders the final configmap for the deployment -- this is
// the configmap that will ultimately be referenced when mounting sub-topologies and tunnel data in
// the clabernetes launcher pod(s) for a given topology.
func (r *ConfigMapReconciler) Render(
	owningTopology *clabernetesapisv1alpha1.Topology,
	clabernetesConfigs map[string]*clabernetesutilcontainerlab.Config,
	filesFromURL map[string][]clabernetesapisv1alpha1.FileFromURL,
	imagePullSecretsString string,
) (*k8scorev1.ConfigMap, error) {
	owningTopologyName := owningTopology.GetName()

	annotations, globalLabels := r.configManagerGetter().GetAllMetadata()

	labels := map[string]string{
		clabernetesconstants.LabelApp:           clabernetesconstants.Clabernetes,
		clabernetesconstants.LabelName:          owningTopologyName,
		clabernetesconstants.LabelTopologyOwner: owningTopologyName,
		clabernetesconstants.LabelTopologyKind:  GetTopologyKind(owningTopology),
	}

	maps.Copy(labels, globalLabels)

	data := map[string]string{
		// we always make this key like the other keys so we can be lazy and not have to wonder if
		// the key / mounted file exists.
		"configured-pull-secrets": imagePullSecretsString,
	}

	for nodeName, nodeTopo := range clabernetesConfigs {
		// always initialize the files from url keys in the configmap, this way we don't have to
		// have any special handling for no tunnels and things always look consistent; we'll
		// override this down below if the node has files to be mounted course!
		data[fmt.Sprintf("%s-files-from-url", nodeName)] = ""

		yamlNodeTopo, err := yaml.Marshal(nodeTopo)
		if err != nil {
			return nil, err
		}

		data[nodeName] = string(yamlNodeTopo)
	}

	for nodeName, nodeFilesFromURL := range filesFromURL {
		// ignore bad node names
		_, nodeOk := clabernetesConfigs[nodeName]
		if !nodeOk {
			continue
		}

		yamlNodeFilesFromURL, err := yaml.Marshal(nodeFilesFromURL)
		if err != nil {
			return nil, err
		}

		data[fmt.Sprintf("%s-files-from-url", nodeName)] = string(yamlNodeFilesFromURL)
	}

	return &k8scorev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:        owningTopologyName,
			Namespace:   owningTopology.GetNamespace(),
			Annotations: annotations,
			Labels:      labels,
		},
		Data: data,
	}, nil
}

// Conforms checks if the existingConfigMap conforms with the renderedConfigMap.
func (r *ConfigMapReconciler) Conforms(
	existingConfigMap,
	renderedConfigMap *k8scorev1.ConfigMap,
	expectedOwnerUID apimachinerytypes.UID,
) bool {
	if !reflect.DeepEqual(existingConfigMap.Data, renderedConfigMap.Data) {
		return false
	}

	if !clabernetesutilkubernetes.ExistingMapStringStringContainsAllExpectedKeyValues(
		existingConfigMap.ObjectMeta.Annotations,
		renderedConfigMap.ObjectMeta.Annotations,
	) {
		return false
	}

	if !clabernetesutilkubernetes.ExistingMapStringStringContainsAllExpectedKeyValues(
		existingConfigMap.ObjectMeta.Labels,
		renderedConfigMap.ObjectMeta.Labels,
	) {
		return false
	}

	if len(existingConfigMap.ObjectMeta.OwnerReferences) != 1 {
		// we should have only one owner reference, the extractor
		return false
	}

	if existingConfigMap.ObjectMeta.OwnerReferences[0].UID != expectedOwnerUID {
		// owner ref uid is not us
		return false
	}

	return true
}
