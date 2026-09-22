package link

import (
	"context"

	clabernetesapis "github.com/clabernetes/clabernetes/apis"
	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetescontrollers "github.com/clabernetes/clabernetes/controllers"
	clabernetesmanagertypes "github.com/clabernetes/clabernetes/manager/types"
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
// (into the status) the wire ids that cross-pod links use.
type Controller struct {
	*clabernetescontrollers.BaseController

	// apiReader supplies one fresh namespace snapshot per pass. The single worker updates
	// its reservations after each successful write, without relying on informer freshness.
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
		Named(string(clabernetesapis.Link)).
		// a Link spec change can make another Link gain or lose a deterministic endpoint conflict;
		// enqueue the namespace so stale rejection state and wire allocations always converge
		Watches(
			&clabernetesapisv1alpha1.Link{},
			ctrlruntimehandler.EnqueueRequestsFromMapFunc(enqueueNamespace),
		).
		// watch nodes (spec changes only) since node grouping (network-mode) decides which links
		// are same-pod links (and those need no wire id)
		Watches(
			&clabernetesapisv1alpha1.Node{},
			ctrlruntimehandler.EnqueueRequestsFromMapFunc(enqueueNamespace),
			ctrlruntimebuilder.WithPredicates(ctrlruntimepredicate.GenerationChangedPredicate{}),
		).
		Complete(c)
}

// enqueueNamespace coalesces inventory events into one allocation pass. Link status events
// are included to repair externally changed allocations; the controller's own writes produce
// only one additional, read-only pass. Node status changes do not affect link allocation.
func enqueueNamespace(
	_ context.Context,
	obj ctrlruntimeclient.Object,
) []ctrlruntimereconcile.Request {
	return []ctrlruntimereconcile.Request{{NamespacedName: apimachinerytypes.NamespacedName{
		Namespace: obj.GetNamespace(),
	}}}
}
