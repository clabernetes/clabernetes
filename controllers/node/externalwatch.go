package node

import (
	"context"

	clientgoworkqueue "k8s.io/client-go/util/workqueue"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimeevent "sigs.k8s.io/controller-runtime/pkg/event"
	ctrlruntimehandler "sigs.k8s.io/controller-runtime/pkg/handler"
	ctrlruntimereconcile "sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// externalEnqueueHandler distinguishes informer bootstrap/resync from a change to a
// workload input. Replaying existing inputs must reconcile Nodes without first making
// healthy workloads unready and emitting a burst of condition-transition Events.
func (c *Controller) externalEnqueueHandler(
	mapper ctrlruntimehandler.MapFunc,
) ctrlruntimehandler.EventHandler {
	enqueue := func(
		ctx context.Context,
		queue clientgoworkqueue.TypedRateLimitingInterface[ctrlruntimereconcile.Request],
		invalidate bool,
		objects ...ctrlruntimeclient.Object,
	) {
		seen := make(map[ctrlruntimereconcile.Request]struct{})
		var requests []ctrlruntimereconcile.Request
		for _, object := range objects {
			if object == nil {
				continue
			}
			for _, request := range mapper(ctx, object) {
				if _, exists := seen[request]; exists {
					continue
				}
				seen[request] = struct{}{}
				requests = append(requests, request)
			}
		}
		if invalidate {
			c.invalidateDirectStatusesForRequests(ctx, requests)
		}
		for _, request := range requests {
			queue.Add(request)
		}
	}

	return ctrlruntimehandler.Funcs{
		CreateFunc: func(ctx context.Context, event ctrlruntimeevent.CreateEvent,
			queue clientgoworkqueue.TypedRateLimitingInterface[ctrlruntimereconcile.Request],
		) {
			enqueue(ctx, queue, !event.IsInInitialList, event.Object)
		},
		UpdateFunc: func(ctx context.Context, event ctrlruntimeevent.UpdateEvent,
			queue clientgoworkqueue.TypedRateLimitingInterface[ctrlruntimereconcile.Request],
		) {
			changed := event.ObjectOld == nil || event.ObjectNew == nil ||
				event.ObjectOld.GetResourceVersion() != event.ObjectNew.GetResourceVersion()
			enqueue(ctx, queue, changed, event.ObjectOld, event.ObjectNew)
		},
		DeleteFunc: func(ctx context.Context, event ctrlruntimeevent.DeleteEvent,
			queue clientgoworkqueue.TypedRateLimitingInterface[ctrlruntimereconcile.Request],
		) {
			enqueue(ctx, queue, true, event.Object)
		},
		GenericFunc: func(ctx context.Context, event ctrlruntimeevent.GenericEvent,
			queue clientgoworkqueue.TypedRateLimitingInterface[ctrlruntimereconcile.Request],
		) {
			enqueue(ctx, queue, true, event.Object)
		},
	}
}
