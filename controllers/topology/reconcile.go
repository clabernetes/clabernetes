package topology

import (
	"context"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	ctrlruntime "sigs.k8s.io/controller-runtime"
)

// Reconcile handles reconciliation for this controller.
func (c *Controller) Reconcile(
	ctx context.Context,
	req ctrlruntime.Request,
) (ctrlruntime.Result, error) {
	c.BaseController.LogReconcileStart(req)

	topology, err := c.getTopologyFromReq(ctx, req)
	if err != nil {
		if apimachineryerrors.IsNotFound(err) {
			// was deleted, nothing to do
			c.BaseController.LogReconcileCompleteObjectNotExist(req)

			return ctrlruntime.Result{}, nil
		}

		c.BaseController.LogReconcileFailedGettingObject(req, err)

		return ctrlruntime.Result{}, err
	}

	if topology.DeletionTimestamp != nil {
		return ctrlruntime.Result{}, nil
	}

	if c.BaseController.ShouldIgnoreReconcile(topology) {
		return ctrlruntime.Result{}, nil
	}

	// we always reconcile the "namespace" resources first -- meaning the resources that exist in
	// the namespace that are not 1:1 to a Topology -- for example: service account and role
	// binding. These resources are created for the namespace on creation of the first Topology in
	// the namespace, and are removed when the last Topology is removed from the namespace.
	err = c.TopologyReconciler.ReconcileNamespaceResources(ctx, topology)
	if err != nil {
		return ctrlruntime.Result{}, err
	}

	reconcileData, err := NewReconcileData(topology)
	if err != nil {
		c.BaseController.Log.Criticalf(
			"failed processing previously stored containerlab resource, error: %s", err,
		)

		return ctrlruntime.Result{}, err
	}

	// snapshot the status so we know if we need to push an update at the end of the reconcile --
	// conditions (and the status itself) get mutated along the way
	previousStatus := topology.Status.DeepCopy()

	// reconcile the naming -- we *must* do this to ensure that our status field is set!
	c.TopologyReconciler.ReconcileNaming(topology, reconcileData)

	err = c.processDefinition(topology, reconcileData)
	if err != nil {
		c.BaseController.Log.Criticalf("failed processing topology definition, error: %s", err)

		return ctrlruntime.Result{}, err
	}

	err = c.reconcileResources(ctx, topology, reconcileData)
	if err != nil {
		return ctrlruntime.Result{}, err
	}

	err = reconcileData.SetStatus(&topology.Status)
	if err != nil {
		c.BaseController.Log.Criticalf(
			"failed setting object '%s/%s' status, error: %s",
			topology.Namespace,
			topology.Name,
			err,
		)

		return ctrlruntime.Result{}, err
	}

	if reconcileData.ShouldUpdateResource ||
		!apiequality.Semantic.DeepEqual(previousStatus, &topology.Status) {
		err = c.BaseController.Client.Update(ctx, topology)
		if err != nil {
			c.BaseController.Log.Criticalf(
				"failed updating object '%s/%s' error: %s",
				topology.Namespace,
				topology.Name,
				err,
			)

			return ctrlruntime.Result{}, err
		}
	}

	c.BaseController.LogReconcileCompleteSuccess(req)

	return ctrlruntime.Result{}, nil
}

func (c *Controller) reconcileResources(
	ctx context.Context,
	topology *clabernetesapisv1alpha1.Topology,
	reconcileData *ReconcileData,
) error {
	err := c.TopologyReconciler.ReconcileNodes(
		ctx,
		topology,
		reconcileData,
	)
	if err != nil {
		c.BaseController.Log.Criticalf(
			"failed reconciling clabernetes node resources, error: %s",
			err,
		)

		return err
	}

	err = c.TopologyReconciler.ReconcileLinks(
		ctx,
		topology,
		reconcileData,
	)
	if err != nil {
		c.BaseController.Log.Criticalf(
			"failed reconciling clabernetes link resources, error: %s",
			err,
		)

		return err
	}

	err = c.TopologyReconciler.ReconcileServices(
		ctx,
		topology,
		reconcileData,
	)
	if err != nil {
		// error already logged
		return err
	}

	err = c.TopologyReconciler.ReconcilePersistentVolumeClaim(
		ctx,
		topology,
		reconcileData,
	)
	if err != nil {
		c.BaseController.Log.Criticalf("failed reconciling clabernetes pvcs, error: %s", err)

		return err
	}

	err = c.TopologyReconciler.ReconcileDeployments(
		ctx,
		topology,
		reconcileData,
	)
	if err != nil {
		c.BaseController.Log.Criticalf("failed reconciling clabernetes deployments, error: %s", err)

		return err
	}

	err = c.TopologyReconciler.ReconcileNodeStatuses(
		ctx,
		topology,
		reconcileData,
	)
	if err != nil {
		c.BaseController.Log.Criticalf(
			"failed reconciling clabernetes node resource statuses, error: %s",
			err,
		)

		return err
	}

	// finally, clean up any legacy (pre node/link cr) resources hanging around
	c.TopologyReconciler.ReconcileLegacyResources(ctx, topology)

	return nil
}
