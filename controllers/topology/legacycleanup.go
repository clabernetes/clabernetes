package topology

import (
	"context"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	k8sappsv1 "k8s.io/api/apps/v1"
	k8scorev1 "k8s.io/api/core/v1"
	k8srbacv1 "k8s.io/api/rbac/v1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// legacyCleanup removes objects a pre node/link (0.6.x) controller rendered for this Topology.
// Those objects -- per node deployments/services and the big per topology configmap --
// carried an owner reference to the *Topology*; everything the current controllers render is
// owned by the emitted Node objects instead, so "owned directly by the topology and of a legacy
// kind" identifies exactly the leftovers. The launcher service account and role binding used to
// be owned by every topology in the namespace -- their owner references are stripped so they
// no longer vanish with the last Topology (standalone Nodes need them too). Legacy PVCs are
// deliberately retained here: the emitted Node adopts and reuses them so an upgrade preserves
// the node's working directory.
func (r *Reconciler) legacyCleanup(
	ctx context.Context,
	topology *clabernetesapisv1alpha1.Topology,
) error {
	err := r.legacyCleanupOwnedObjects(ctx, topology)
	if err != nil {
		return err
	}

	return r.legacyCleanupNamespaceResources(ctx, topology)
}

func (r *Reconciler) legacyCleanupOwnedObjects(
	ctx context.Context,
	topology *clabernetesapisv1alpha1.Topology,
) error {
	legacyKinds := []ctrlruntimeclient.ObjectList{
		&k8sappsv1.DeploymentList{},
		&k8scorev1.ServiceList{},
		&k8scorev1.ConfigMapList{},
	}

	for _, list := range legacyKinds {
		err := r.Client.List(ctx, list, ctrlruntimeclient.InNamespace(topology.GetNamespace()))
		if err != nil {
			return err
		}

		objects := apimachineryObjectsFromList(list)

		for _, obj := range objects {
			if !ownedBy(obj, topology) {
				continue
			}

			r.Log.Infof(
				"deleting legacy (pre node/link) %T '%s/%s'",
				obj,
				obj.GetNamespace(),
				obj.GetName(),
			)

			err = r.Client.Delete(ctx, obj)
			if err != nil && !apimachineryerrors.IsNotFound(err) {
				return err
			}
		}
	}

	return nil
}

// apimachineryObjectsFromList flattens the typed lists used by the legacy cleanup into client
// objects.
func apimachineryObjectsFromList(
	list ctrlruntimeclient.ObjectList,
) []ctrlruntimeclient.Object {
	objects := make([]ctrlruntimeclient.Object, 0)

	switch typed := list.(type) {
	case *k8sappsv1.DeploymentList:
		for idx := range typed.Items {
			objects = append(objects, &typed.Items[idx])
		}
	case *k8scorev1.ServiceList:
		for idx := range typed.Items {
			objects = append(objects, &typed.Items[idx])
		}
	case *k8scorev1.PersistentVolumeClaimList:
		for idx := range typed.Items {
			objects = append(objects, &typed.Items[idx])
		}
	case *k8scorev1.ConfigMapList:
		for idx := range typed.Items {
			objects = append(objects, &typed.Items[idx])
		}
	}

	return objects
}

// legacyCleanupNamespaceResources strips topology owner references from the shared launcher
// service account and role binding (pre node/link controllers owner-ref'd them to every
// topology in the namespace).
func (r *Reconciler) legacyCleanupNamespaceResources(
	ctx context.Context,
	topology *clabernetesapisv1alpha1.Topology,
) error {
	for _, obj := range []ctrlruntimeclient.Object{
		&k8scorev1.ServiceAccount{},
		&k8srbacv1.RoleBinding{},
	} {
		var name string

		switch obj.(type) {
		case *k8scorev1.ServiceAccount:
			name = "clabernetes-launcher-service-account"
		case *k8srbacv1.RoleBinding:
			name = "clabernetes-launcher-role-binding"
		}

		err := r.Client.Get(
			ctx,
			apimachinerytypes.NamespacedName{
				Namespace: topology.GetNamespace(),
				Name:      name,
			},
			obj,
		)
		if err != nil {
			if apimachineryerrors.IsNotFound(err) {
				continue
			}

			return err
		}

		if len(obj.GetOwnerReferences()) == 0 {
			continue
		}

		r.Log.Infof(
			"stripping legacy topology owner references from '%s/%s'",
			topology.GetNamespace(),
			name,
		)

		obj.SetOwnerReferences(nil)

		err = r.Client.Update(ctx, obj)
		if err != nil {
			return err
		}
	}

	return nil
}
