package topology

import (
	"fmt"
	"maps"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	claberneteserrors "github.com/clabernetes/clabernetes/errors"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	clabernetesutil "github.com/clabernetes/clabernetes/util"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
	clabernetesutilkubernetes "github.com/clabernetes/clabernetes/util/kubernetes"
	k8scorev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
)

// PersistentVolumeClaimReconciler is a subcomponent of the "TopologyReconciler" but is exposed for
// testing purposes. This is the component responsible for rendering/validating the optional PVC
// that is used to persist the containerlab directory of a topology's nodes.
type PersistentVolumeClaimReconciler struct {
	log                 claberneteslogging.Instance
	configManagerGetter clabernetesconfig.ManagerGetterFunc
}

// NewPersistentVolumeClaimReconciler returns an instance of PersistentVolumeClaimReconciler.
func NewPersistentVolumeClaimReconciler(
	log claberneteslogging.Instance,
	configManagerGetter clabernetesconfig.ManagerGetterFunc,
) *PersistentVolumeClaimReconciler {
	return &PersistentVolumeClaimReconciler{
		log:                 log,
		configManagerGetter: configManagerGetter,
	}
}

// Resolve accepts a mapping of clabernetes configs and a list of services that are -- by owner
// reference and/or labels -- associated with the topology. It returns a ObjectDiffer object
// that contains the missing, extra, and current services for the topology.
func (r *PersistentVolumeClaimReconciler) Resolve(
	ownedPVCs *k8scorev1.PersistentVolumeClaimList,
	clabernetesConfigs map[string]*clabernetesutilcontainerlab.Config,
	owningTopology *clabernetesapisv1alpha1.Topology,
) (*clabernetesutil.ObjectDiffer[*k8scorev1.PersistentVolumeClaim], error) {
	pvcs := &clabernetesutil.ObjectDiffer[*k8scorev1.PersistentVolumeClaim]{
		Current: map[string]*k8scorev1.PersistentVolumeClaim{},
	}

	for i := range ownedPVCs.Items {
		labels := ownedPVCs.Items[i].Labels

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

		pvcs.Current[nodeName] = &ownedPVCs.Items[i]
	}

	persistenceEnabled := owningTopology.Spec.Deployment.Persistence.Enabled

	if persistenceEnabled {
		allNodes := make([]string, len(clabernetesConfigs))

		var idx int

		for nodeName := range clabernetesConfigs {
			allNodes[idx] = nodeName

			idx++
		}

		pvcs.SetMissing(allNodes)
		pvcs.SetExtra(allNodes)
	} else {
		pvcs.SetExtra(nil)
	}

	return pvcs, nil
}

// Render accepts the owning topology a mapping of clabernetes sub-topology configs and a node name
// and renders the pvc for this node. Note that *Render* (but not RenderAll) accepts an existing
// pvc as well - we do this because the VolumeName field is immutable, so we *must* use the name of
// the volume that got provisioned (if it exists). RenderAll in this case should not ever be used to
// render/re-render existing pvcs, so it can safely pass nil when it calls Render.
func (r *PersistentVolumeClaimReconciler) Render(
	owningTopology *clabernetesapisv1alpha1.Topology,
	nodeName string,
	existingPVC *k8scorev1.PersistentVolumeClaim,
) *k8scorev1.PersistentVolumeClaim {
	owningTopologyName := owningTopology.GetName()

	pvc := r.renderPVCBase(
		owningTopology,
		fmt.Sprintf("%s-%s", owningTopologyName, nodeName),
		nodeName,
	)

	r.renderPVCSpec(owningTopology, pvc, existingPVC)

	return pvc
}

// RenderAll accepts the owning topology a mapping of clabernetes sub-topology configs and a
// list of node names and renders the pvcs for the given nodes.
func (r *PersistentVolumeClaimReconciler) RenderAll(
	owningTopology *clabernetesapisv1alpha1.Topology,
	nodeNames []string,
) []*k8scorev1.PersistentVolumeClaim {
	pvcs := make([]*k8scorev1.PersistentVolumeClaim, len(nodeNames))

	for idx, nodeName := range nodeNames {
		pvcs[idx] = r.Render(
			owningTopology,
			nodeName,
			nil,
		)
	}

	return pvcs
}

// Conforms checks if the existingService conforms with the renderedService.
func (r *PersistentVolumeClaimReconciler) Conforms(
	existingPVC,
	renderedPVC *k8scorev1.PersistentVolumeClaim,
	expectedOwnerUID apimachinerytypes.UID,
) bool {
	existingClaimSize := existingPVC.Spec.Resources.Requests.Storage().Value()
	renderedClaimSize := renderedPVC.Spec.Resources.Requests.Storage().Value()

	if renderedClaimSize != existingClaimSize {
		if renderedClaimSize > existingClaimSize {
			// we only "dont conform" if the rendered claim size is greater than the existing claim;
			// we do this because we can *expand* but not shrink pvc claims
			return false
		}

		r.log.Warnf(
			"existing claim size of %q is *smaller* than desired claim size of %q,"+
				" however claim size can only be increased, not shrunk, ignoring...",
			existingPVC.Spec.Resources.Requests.Storage().String(),
			renderedPVC.Spec.Resources.Requests.Storage().String(),
		)
	}

	if !clabernetesutilkubernetes.ExistingMapStringStringContainsAllExpectedKeyValues(
		existingPVC.ObjectMeta.Annotations,
		renderedPVC.ObjectMeta.Annotations,
	) {
		return false
	}

	if !clabernetesutilkubernetes.ExistingMapStringStringContainsAllExpectedKeyValues(
		existingPVC.ObjectMeta.Labels,
		renderedPVC.ObjectMeta.Labels,
	) {
		return false
	}

	if len(existingPVC.ObjectMeta.OwnerReferences) != 1 {
		// we should have only one owner reference, the extractor
		return false
	}

	if existingPVC.ObjectMeta.OwnerReferences[0].UID != expectedOwnerUID {
		// owner ref uid is not us
		return false
	}

	// note: spec is immutable after creation so not bothering checking that

	return true
}

func (r *PersistentVolumeClaimReconciler) renderPVCBase(
	owningTopology *clabernetesapisv1alpha1.Topology,
	name,
	nodeName string,
) *k8scorev1.PersistentVolumeClaim {
	owningTopologyName := owningTopology.GetName()

	annotations, globalLabels := r.configManagerGetter().GetAllMetadata()

	deploymentName := fmt.Sprintf("%s-%s", owningTopologyName, nodeName)

	if ResolveTopologyRemovePrefix(owningTopology) {
		deploymentName = nodeName
	}

	selectorLabels := map[string]string{
		clabernetesconstants.LabelApp:           clabernetesconstants.Clabernetes,
		clabernetesconstants.LabelName:          deploymentName,
		clabernetesconstants.LabelTopologyOwner: owningTopologyName,
		clabernetesconstants.LabelTopologyNode:  nodeName,
	}

	labels := map[string]string{
		clabernetesconstants.LabelTopologyKind: GetTopologyKind(owningTopology),
	}

	maps.Copy(labels, selectorLabels)
	maps.Copy(labels, globalLabels)

	return &k8scorev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   owningTopology.GetNamespace(),
			Annotations: annotations,
			Labels:      labels,
		},
	}
}

func (r *PersistentVolumeClaimReconciler) renderPVCSpec(
	owningTopology *clabernetesapisv1alpha1.Topology,
	pvc *k8scorev1.PersistentVolumeClaim,
	existingPVC *k8scorev1.PersistentVolumeClaim,
) {
	persistence := owningTopology.Spec.Deployment.Persistence

	var storageClassName *string

	if persistence.StorageClassName != "" {
		storageClassName = clabernetesutil.ToPointer(persistence.StorageClassName)
	}

	pvcSize := resource.MustParse("5Gi")

	if persistence.ClaimSize != "" {
		userClaimSize, err := resource.ParseQuantity(persistence.ClaimSize)
		if err != nil {
			r.log.Warnf(
				"user provided claim size %q failed parsing, using default value instead",
				persistence.ClaimSize,
				err,
			)
		} else {
			pvcSize = userClaimSize
		}
	}

	pvc.Spec = k8scorev1.PersistentVolumeClaimSpec{
		AccessModes: []k8scorev1.PersistentVolumeAccessMode{
			k8scorev1.ReadWriteOnce,
		},
		Resources: k8scorev1.VolumeResourceRequirements{
			Requests: k8scorev1.ResourceList{
				"storage": pvcSize,
			},
		},
		StorageClassName: storageClassName,
		VolumeMode:       clabernetesutil.ToPointer(k8scorev1.PersistentVolumeFilesystem),
	}

	if existingPVC != nil {
		// VolumeName is immutable, if this pvc already exists, ensure we copy the volume name!
		pvc.Spec.VolumeName = existingPVC.Spec.VolumeName
	}
}
