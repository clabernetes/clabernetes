package link

import (
	"context"

	clabernetesapis "github.com/clabernetes/clabernetes/apis"
	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetescontrollers "github.com/clabernetes/clabernetes/controllers"
	clabernetesmanagertypes "github.com/clabernetes/clabernetes/manager/types"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	clientgoworkqueue "k8s.io/client-go/util/workqueue"
	ctrlruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimebuilder "sigs.k8s.io/controller-runtime/pkg/builder"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimecontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	ctrlruntimeevent "sigs.k8s.io/controller-runtime/pkg/event"
	ctrlruntimehandler "sigs.k8s.io/controller-runtime/pkg/handler"
	ctrlruntimepredicate "sigs.k8s.io/controller-runtime/pkg/predicate"
	ctrlruntimereconcile "sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Controller is the clabernetes Link controller -- it validates Link resources and allocates
// (into the status) the wire ids that cross-pod links use.
type Controller struct {
	*clabernetescontrollers.BaseController

	// apiReader is a live (uncached) reader -- wire id allocation reads the namespace's links
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
		// a Link spec change can make another Link gain or lose a deterministic endpoint conflict;
		// enqueue the namespace so stale rejection state and wire allocations always converge
		Watches(
			&clabernetesapisv1alpha1.Link{},
			ctrlruntimehandler.EnqueueRequestsFromMapFunc(c.enqueueLinksInNamespace),
			ctrlruntimebuilder.WithPredicates(ctrlruntimepredicate.GenerationChangedPredicate{}),
		).
		// watch nodes (spec changes only) since node grouping (network-mode) decides which links
		// are same-pod links (and those need no wire id)
		Watches(
			&clabernetesapisv1alpha1.Node{},
			c.nodeEnqueueHandler(),
			ctrlruntimebuilder.WithPredicates(ctrlruntimepredicate.GenerationChangedPredicate{}),
		).
		Complete(c)
}

// nodeEnqueueHandler records actual Node deletion events and enqueues Links whose lifecycle or
// pod grouping may be affected. Reconcile performs the authoritative UID comparison and logs
// each Link it deletes.
func (c *Controller) nodeEnqueueHandler() ctrlruntimehandler.EventHandler {
	enqueue := func(
		ctx context.Context,
		obj ctrlruntimeclient.Object,
		queue clientgoworkqueue.TypedRateLimitingInterface[ctrlruntimereconcile.Request],
	) {
		for _, request := range c.enqueueLinksInNamespace(ctx, obj) {
			queue.Add(request)
		}
	}

	return ctrlruntimehandler.Funcs{
		CreateFunc: func(
			ctx context.Context,
			event ctrlruntimeevent.CreateEvent,
			queue clientgoworkqueue.TypedRateLimitingInterface[ctrlruntimereconcile.Request],
		) {
			enqueue(ctx, event.Object, queue)
		},
		UpdateFunc: func(
			ctx context.Context,
			event ctrlruntimeevent.UpdateEvent,
			queue clientgoworkqueue.TypedRateLimitingInterface[ctrlruntimereconcile.Request],
		) {
			enqueue(ctx, event.ObjectOld, queue)
			enqueue(ctx, event.ObjectNew, queue)
		},
		DeleteFunc: func(
			ctx context.Context,
			event ctrlruntimeevent.DeleteEvent,
			queue clientgoworkqueue.TypedRateLimitingInterface[ctrlruntimereconcile.Request],
		) {
			requests := c.enqueueLinksInNamespace(ctx, event.Object)
			referencedLinks := 0

			for _, request := range requests {
				link := &clabernetesapisv1alpha1.Link{}

				err := c.Client.Get(ctx, request.NamespacedName, link)
				if err == nil &&
					(link.Spec.EndpointA.NodeName == event.Object.GetName() ||
						link.Spec.EndpointB.NodeName == event.Object.GetName()) {
					referencedLinks++
				}

				queue.Add(request)
			}

			c.Log.Infof(
				"observed Node deletion event for %q; scheduled %d referenced Link(s)"+
					" for lifecycle cleanup",
				apimachinerytypes.NamespacedName{
					Namespace: event.Object.GetNamespace(),
					Name:      event.Object.GetName(),
				}.String(),
				referencedLinks,
			)
		},
	}
}

// enqueueLinksInNamespace enqueues all Links in the changed object's namespace. Node grouping and
// endpoint-conflict changes can affect links other than the object that triggered the event.
func (c *Controller) enqueueLinksInNamespace(
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
