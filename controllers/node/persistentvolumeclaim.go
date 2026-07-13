package node

import (
	"maps"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/srl-labs/clabernetes/config"
	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	claberneteslogging "github.com/srl-labs/clabernetes/logging"
	clabernetesutil "github.com/srl-labs/clabernetes/util"
	clabernetesutilkubernetes "github.com/srl-labs/clabernetes/util/kubernetes"
	k8scorev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
)

// PersistentVolumeClaimReconciler renders/validates the optional PVC that is used to persist
// the containerlab directory of a node -- exposed for testing purposes.
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

// Render renders the pvc for the given (launcher) node. Note that Render accepts an existing
// pvc as well -- the VolumeName field is immutable, so we *must* carry over the name of the
// volume that got provisioned (if it exists).
func (r *PersistentVolumeClaimReconciler) Render(
	node *clabernetesapisv1alpha1.Node,
	resolvedProfile *ResolvedProfile,
	existingPVC *k8scorev1.PersistentVolumeClaim,
) *k8scorev1.PersistentVolumeClaim {
	nodeName := node.GetName()

	annotations, globalLabels := r.configManagerGetter().GetAllMetadata()

	labels := map[string]string{
		clabernetesconstants.LabelApp:          clabernetesconstants.Clabernetes,
		clabernetesconstants.LabelName:         nodeName,
		clabernetesconstants.LabelTopologyNode: nodeName,
	}

	maps.Copy(labels, globalLabels)

	if owner, ok := node.GetLabels()[clabernetesconstants.LabelTopologyOwner]; ok {
		labels[clabernetesconstants.LabelTopologyOwner] = owner
	}

	pvc := &k8scorev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:        nodeName,
			Namespace:   node.GetNamespace(),
			Annotations: annotations,
			Labels:      labels,
		},
	}

	persistence := resolvedProfile.Persistence

	var storageClassName *string

	if persistence.StorageClassName != "" {
		storageClassName = clabernetesutil.ToPointer(persistence.StorageClassName)
	}

	pvcSize := resource.MustParse("5Gi")

	if persistence.ClaimSize != "" {
		userClaimSize, err := resource.ParseQuantity(persistence.ClaimSize)
		if err != nil {
			r.log.Warnf(
				"user provided claim size %q failed parsing, using default value instead: %s",
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

	return pvc
}

// Conforms checks if the existing pvc conforms with the rendered pvc.
func (r *PersistentVolumeClaimReconciler) Conforms(
	existingPVC,
	renderedPVC *k8scorev1.PersistentVolumeClaim,
	expectedOwnerUID apimachinerytypes.UID,
) bool {
	existingClaimSize := existingPVC.Spec.Resources.Requests.Storage().Value()
	renderedClaimSize := renderedPVC.Spec.Resources.Requests.Storage().Value()

	if renderedClaimSize != existingClaimSize {
		if renderedClaimSize > existingClaimSize {
			// we only "dont conform" if the rendered claim size is greater than the existing
			// claim; we do this because we can *expand* but not shrink pvc claims
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
		// we should have only one owner reference, the owning node
		return false
	}

	if existingPVC.ObjectMeta.OwnerReferences[0].UID != expectedOwnerUID {
		// owner ref uid is not us
		return false
	}

	// note: spec is immutable after creation so not bothering checking that

	return true
}
