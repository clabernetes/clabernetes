package topology

import (
	"context"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	ctrlruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimeutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// getTopologyFromReq fetches the reconcile target Topology from the Request.
func (c *Controller) getTopologyFromReq(
	ctx context.Context,
	req ctrlruntime.Request,
) (*clabernetesapisv1alpha1.Topology, error) {
	containerlab := &clabernetesapisv1alpha1.Topology{}

	err := c.BaseController.Client.Get(
		ctx,
		apimachinerytypes.NamespacedName{
			Namespace: req.Namespace,
			Name:      req.Name,
		},
		containerlab,
	)

	return containerlab, err
}

func (r *Reconciler) createObj(
	ctx context.Context,
	ownerObj,
	createObj ctrlruntimeclient.Object,
	createObjKind string,
) error {
	err := ctrlruntimeutil.SetOwnerReference(ownerObj, createObj, r.Client.Scheme())
	if err != nil {
		return err
	}

	r.Log.Debugf(
		"creating %s '%s/%s'",
		createObjKind,
		createObj.GetNamespace(),
		createObj.GetName(),
	)

	err = r.Client.Create(ctx, createObj)
	if err != nil {
		r.Log.Criticalf(
			"failed creating %s '%s/%s' error: %s",
			createObjKind,
			createObj.GetNamespace(),
			createObj.GetName(),
			err,
		)

		return err
	}

	return nil
}

func (r *Reconciler) getObj(
	ctx context.Context,
	getObj ctrlruntimeclient.Object,
	namespacedName apimachinerytypes.NamespacedName,
	getObjKind string,
) error {
	r.Log.Debugf(
		"getting %s '%s/%s'",
		getObjKind,
		namespacedName.Namespace,
		namespacedName.Name,
	)

	return r.Client.Get(ctx, namespacedName, getObj)
}

func (r *Reconciler) updateObj(
	ctx context.Context,
	updateObj ctrlruntimeclient.Object,
	updateObjKind string,
) error {
	r.Log.Debugf(
		"updating %s '%s/%s'",
		updateObjKind,
		updateObj.GetNamespace(),
		updateObj.GetName(),
	)

	err := r.Client.Update(ctx, updateObj)
	if err != nil {
		r.Log.Criticalf(
			"failed updating %s '%s/%s' error: %s",
			updateObjKind,
			updateObj.GetNamespace(),
			updateObj.GetName(),
			err,
		)

		return err
	}

	return nil
}

func (r *Reconciler) deleteObj(
	ctx context.Context,
	deleteObj ctrlruntimeclient.Object,
	deleteObjKind string,
) error {
	r.Log.Debugf(
		"deleting %s '%s/%s'",
		deleteObjKind,
		deleteObj.GetNamespace(),
		deleteObj.GetName(),
	)

	err := r.Client.Delete(ctx, deleteObj)
	if err != nil {
		r.Log.Criticalf(
			"failed deleting %s '%s/%s' error: %s",
			deleteObjKind,
			deleteObj.GetNamespace(),
			deleteObj.GetName(),
			err,
		)

		return err
	}

	return nil
}
