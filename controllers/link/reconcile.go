package link

import (
	"context"
	"fmt"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	ctrlruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Reconcile handles reconciliation for this controller.
func (c *Controller) Reconcile(
	ctx context.Context,
	req ctrlruntime.Request,
) (ctrlruntime.Result, error) {
	c.BaseController.LogReconcileStart(req)

	link := &clabernetesapisv1alpha1.Link{}

	err := c.BaseController.Client.Get(ctx, req.NamespacedName, link)
	if err != nil {
		if apimachineryerrors.IsNotFound(err) {
			// was deleted; nothing to do -- absence of the link is what frees its tunnel id
			c.BaseController.LogReconcileCompleteObjectNotExist(req)

			return ctrlruntime.Result{}, nil
		}

		c.BaseController.LogReconcileFailedGettingObject(req, err)

		return ctrlruntime.Result{}, err
	}

	if link.DeletionTimestamp != nil {
		return ctrlruntime.Result{}, nil
	}

	if c.BaseController.ShouldIgnoreReconcile(link) {
		return ctrlruntime.Result{}, nil
	}

	err = ValidateLink(link)
	if err != nil {
		// terminally invalid until the spec changes -- clear any stale allocation and stamp the
		// rejection so no node controller or launcher can continue realizing it
		c.BaseController.Log.Criticalf(
			"link '%s/%s' is invalid and will not be processed: %s",
			link.GetNamespace(),
			link.GetName(),
			err,
		)

		return ctrlruntime.Result{}, c.updateLinkStatus(
			ctx,
			link,
			clabernetesapisv1alpha1.LinkStatus{Error: err.Error()},
		)
	}

	// the namespace's links are read through the live (uncached) reader -- allocation decisions
	// must see the ids written by the immediately preceding reconciles
	namespaceLinks := &clabernetesapisv1alpha1.LinkList{}

	err = c.apiReader.List(ctx, namespaceLinks, ctrlruntimeclient.InNamespace(req.Namespace))
	if err != nil {
		c.BaseController.Log.Criticalf("failed listing links in namespace, err: %s", err)

		return ctrlruntime.Result{}, err
	}

	if conflictingLink := FindEndpointConflict(link, namespaceLinks.Items); conflictingLink != "" {
		conflictError := fmt.Sprintf("endpoint already claimed by link %q", conflictingLink)

		c.BaseController.Log.Criticalf(
			"link '%s/%s' claims an endpoint already wired by link %q, skipping allocation",
			link.GetNamespace(),
			link.GetName(),
			conflictingLink,
		)

		return ctrlruntime.Result{}, c.updateLinkStatus(
			ctx,
			link,
			clabernetesapisv1alpha1.LinkStatus{Error: conflictError},
		)
	}

	// grouping (which decides same-launcher-ness) comes from the cached node view -- node spec
	// changes enqueue the namespace's links, so this converges
	namespaceNodes := &clabernetesapisv1alpha1.NodeList{}

	err = c.BaseController.Client.List(
		ctx,
		namespaceNodes,
		ctrlruntimeclient.InNamespace(req.Namespace),
	)
	if err != nil {
		c.BaseController.Log.Criticalf("failed listing nodes in namespace, err: %s", err)

		return ctrlruntime.Result{}, err
	}

	desiredTunnelID, err := ResolveDesiredTunnelID(
		link,
		namespaceLinks.Items,
		namespaceNodes.Items,
	)
	if err != nil {
		c.BaseController.Log.Criticalf("failed resolving tunnel id for link, err: %s", err)

		return ctrlruntime.Result{}, err
	}

	desiredStatus := clabernetesapisv1alpha1.LinkStatus{TunnelID: desiredTunnelID}

	if desiredStatus == link.Status {
		c.BaseController.LogReconcileCompleteSuccess(req)

		return ctrlruntime.Result{}, nil
	}

	c.BaseController.Log.Infof(
		"allocating tunnel id %d to link '%s/%s' (was %d)",
		desiredTunnelID,
		link.GetNamespace(),
		link.GetName(),
		link.Status.TunnelID,
	)

	err = c.updateLinkStatus(ctx, link, desiredStatus)
	if err != nil {
		c.BaseController.Log.Criticalf(
			"failed updating link '%s/%s' status, err: %s",
			link.GetNamespace(),
			link.GetName(),
			err,
		)

		return ctrlruntime.Result{}, err
	}

	c.BaseController.LogReconcileCompleteSuccess(req)

	return ctrlruntime.Result{}, nil
}

func (c *Controller) updateLinkStatus(
	ctx context.Context,
	link *clabernetesapisv1alpha1.Link,
	desiredStatus clabernetesapisv1alpha1.LinkStatus,
) error {
	if link.Status == desiredStatus {
		return nil
	}

	link.Status = desiredStatus

	return c.BaseController.Client.Update(ctx, link)
}
