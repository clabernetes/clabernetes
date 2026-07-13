package link

import (
	"context"

	clabernetesapis "github.com/srl-labs/clabernetes/apis"
	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetescontrollers "github.com/srl-labs/clabernetes/controllers"
	clabernetesmanagertypes "github.com/srl-labs/clabernetes/manager/types"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	ctrlruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimebuilder "sigs.k8s.io/controller-runtime/pkg/builder"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimecontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	ctrlruntimehandler "sigs.k8s.io/controller-runtime/pkg/handler"
	ctrlruntimepredicate "sigs.k8s.io/controller-runtime/pkg/predicate"
	ctrlruntimereconcile "sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Controller is the clabernetes Link controller -- it validates Link resources and allocates
// (into the status) the tunnel ids that cross-launcher links use.
type Controller struct {
	*clabernetescontrollers.BaseController

	// apiReader is a live (uncached) reader -- tunnel id allocation reads the namespace's links
	// through it so that, combined with the single reconcile worker, allocation decisions are
	// serialized over up-to-date state rather than possibly stale cache content.
	apiReader ctrlruntimeclient.Reader
}

// NewController returns a new Controller.
func NewController(
	clabernetes clabernetesmanagertypes.Clabernetes,
) clabernetescontrollers.Controller {
	baseController := clabernetescontrollers.NewBaseController(
		clabernetes.GetContext(),
		clabernetesapis.Link,
		clabernetes.GetAppName(),
		clabernetes.GetKubeConfig(),
		clabernetes.GetCtrlRuntimeClient(),
	)

	return &Controller{
		BaseController: baseController,
	}
}

// SetupWithManager sets up the controller with the Manager.
func (c *Controller) SetupWithManager(mgr ctrlruntime.Manager) error {
	c.BaseController.Log.Infof(
		"setting up %s controller with manager",
		clabernetesapis.Link,
	)

	c.apiReader = mgr.GetAPIReader()

	return ctrlruntime.NewControllerManagedBy(mgr).
		WithOptions(
			ctrlruntimecontroller.Options{
				// a single worker serializes allocation decisions (see also apiReader)
				MaxConcurrentReconciles: 1,
			},
		).
		For(&clabernetesapisv1alpha1.Link{}).
		// watch nodes (spec changes only) since node grouping (network-mode) decides which links
		// are same-launcher links (and those need no tunnel id)
		Watches(
			&clabernetesapisv1alpha1.Node{},
			ctrlruntimehandler.EnqueueRequestsFromMapFunc(c.enqueueLinksForNode),
			ctrlruntimebuilder.WithPredicates(ctrlruntimepredicate.GenerationChangedPredicate{}),
		).
		Complete(c)
}

// enqueueLinksForNode enqueues all Links in the changed Node's namespace -- node (spec) changes
// are rare and namespaces are the topology boundary, so this is cheap and always correct (a
// node's network-mode decides same-launcher-ness for links well beyond the node's own name).
func (c *Controller) enqueueLinksForNode(
	ctx context.Context,
	obj ctrlruntimeclient.Object,
) []ctrlruntimereconcile.Request {
	links := &clabernetesapisv1alpha1.LinkList{}

	err := c.Client.List(ctx, links, ctrlruntimeclient.InNamespace(obj.GetNamespace()))
	if err != nil {
		c.Log.Criticalf("failed listing link objects for node enqueue, err: %s", err)

		return nil
	}

	requests := make([]ctrlruntimereconcile.Request, len(links.Items))

	for idx := range links.Items {
		requests[idx] = ctrlruntimereconcile.Request{
			NamespacedName: apimachinerytypes.NamespacedName{
				Namespace: links.Items[idx].GetNamespace(),
				Name:      links.Items[idx].GetName(),
			},
		}
	}

	return requests
}
