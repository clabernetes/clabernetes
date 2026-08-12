package node

import (
	"context"
	"fmt"
	"sort"

	clabernetesapis "github.com/clabernetes/clabernetes/apis"
	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetescontrollers "github.com/clabernetes/clabernetes/controllers"
	clabernetesmanagertypes "github.com/clabernetes/clabernetes/manager/types"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
	k8sappsv1 "k8s.io/api/apps/v1"
	k8scorev1 "k8s.io/api/core/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	clientgoworkqueue "k8s.io/client-go/util/workqueue"
	ctrlruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimecontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	ctrlruntimeevent "sigs.k8s.io/controller-runtime/pkg/event"
	ctrlruntimehandler "sigs.k8s.io/controller-runtime/pkg/handler"
	ctrlruntimereconcile "sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const launcherProfileReferenceField = "spec.launcherProfileRef.name"

// Controller is the clabernetes Node controller -- it turns each (launcher) Node into a
// deployment (plus services/pvc) and stamps observations/allocations into the Node status.
type Controller struct {
	*clabernetescontrollers.BaseController

	reconciler *Reconciler
}

// NewController returns a new Controller.
func NewController(
	clabernetes clabernetesmanagertypes.Clabernetes,
) clabernetescontrollers.Controller {
	baseController := clabernetescontrollers.NewBaseController(
		clabernetes.GetContext(),
		clabernetesapis.Node,
		clabernetes.GetAppName(),
		clabernetes.GetKubeConfig(),
		clabernetes.GetCtrlRuntimeClient(),
	)

	return &Controller{
		BaseController: baseController,
		reconciler: NewReconciler(
			baseController.Log,
			baseController.Client,
			clabernetes.GetCtrlRuntimeMgr().GetAPIReader(),
			clabernetes.GetAppName(),
			clabernetes.GetNamespace(),
			clabernetes.GetClusterCRIKind(),
			clabernetesconfig.GetManager,
		),
	}
}

// SetupWithManager sets up the controller with the Manager.
func (c *Controller) SetupWithManager(mgr ctrlruntime.Manager) error {
	c.BaseController.Log.Infof(
		"setting up %s controller with manager",
		clabernetesapis.Node,
	)

	err := mgr.GetFieldIndexer().IndexField(
		c.Ctx,
		&clabernetesapisv1alpha1.Node{},
		launcherProfileReferenceField,
		launcherProfileReferenceIndex,
	)
	if err != nil {
		return fmt.Errorf("indexing Nodes by LauncherProfile reference: %w", err)
	}

	return ctrlruntime.NewControllerManagedBy(mgr).
		WithOptions(
			ctrlruntimecontroller.Options{
				MaxConcurrentReconciles: 1,
			},
		).
		For(&clabernetesapisv1alpha1.Node{}).
		// group co-members: a (grouped) node's launcher renders that node's services, status
		// and the shared pod, so events on any node also enqueue its (old and new) launcher
		Watches(
			&clabernetesapisv1alpha1.Node{},
			c.launcherEnqueueHandler(),
		).
		// LauncherProfile changes enqueue only groups with explicit references to that profile.
		Watches(
			&clabernetesapisv1alpha1.LauncherProfile{},
			ctrlruntimehandler.EnqueueRequestsFromMapFunc(
				c.enqueueLaunchersForLauncherProfile,
			),
		).
		// links feed the attachment digest of the launchers terminating them
		Watches(
			&clabernetesapisv1alpha1.Link{},
			c.linkEnqueueHandler(),
		).
		// global config is the base of every profile resolution
		Watches(
			&clabernetesapisv1alpha1.Config{},
			ctrlruntimehandler.EnqueueRequestsFromMapFunc(c.enqueueAllNodes),
		).
		// owned objects
		Watches(
			&k8sappsv1.Deployment{},
			ctrlruntimehandler.EnqueueRequestForOwner(
				mgr.GetScheme(),
				mgr.GetRESTMapper(),
				&clabernetesapisv1alpha1.Node{},
			),
		).
		Watches(
			&k8scorev1.Service{},
			ctrlruntimehandler.EnqueueRequestForOwner(
				mgr.GetScheme(),
				mgr.GetRESTMapper(),
				&clabernetesapisv1alpha1.Node{},
			),
		).
		Watches(
			&k8scorev1.PersistentVolumeClaim{},
			ctrlruntimehandler.EnqueueRequestForOwner(
				mgr.GetScheme(),
				mgr.GetRESTMapper(),
				&clabernetesapisv1alpha1.Node{},
			),
		).
		// pods feed the probe statuses
		Watches(
			&k8scorev1.Pod{},
			ctrlruntimehandler.EnqueueRequestsFromMapFunc(c.enqueueNodeForPod),
		).
		Complete(c)
}

// enqueueLauncherFor resolves the launcher node hosting the given node object and returns a
// request for it -- the object itself is included in the resolution view so deletes/updates
// resolve sensibly even when the cache no longer holds the object.
func (c *Controller) enqueueLauncherFor(
	ctx context.Context,
	obj ctrlruntimeclient.Object,
) []ctrlruntimereconcile.Request {
	node, ok := obj.(*clabernetesapisv1alpha1.Node)
	if !ok {
		return nil
	}

	nodes := &clabernetesapisv1alpha1.NodeList{}

	err := c.Client.List(ctx, nodes, ctrlruntimeclient.InNamespace(obj.GetNamespace()))
	if err != nil {
		c.Log.Criticalf("failed listing nodes for launcher enqueue, err: %s", err)

		return nil
	}

	byName := clabernetesutilcontainerlab.NodesByName(nodes.Items)
	byName[node.GetName()] = node

	launcher := clabernetesutilcontainerlab.ResolveLauncherNode(byName, node.GetName())
	if launcher == node.GetName() {
		// the node is its own launcher; the For() watch already enqueued it
		return nil
	}

	return []ctrlruntimereconcile.Request{{
		NamespacedName: apimachinerytypes.NamespacedName{
			Namespace: obj.GetNamespace(),
			Name:      launcher,
		},
	}}
}

// launcherEnqueueHandler enqueues the launcher of a node on create/delete, and on update the
// launchers per both the old and the new object -- so a node moving between groups re-renders
// both affected launcher pods.
func (c *Controller) launcherEnqueueHandler() ctrlruntimehandler.EventHandler {
	enqueue := func(
		ctx context.Context,
		obj ctrlruntimeclient.Object,
		queue clientgoworkqueue.TypedRateLimitingInterface[ctrlruntimereconcile.Request],
	) {
		for _, request := range c.enqueueLauncherFor(ctx, obj) {
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
			c.Log.Infof(
				"observed Node deletion event for %q; reconciling its launcher group",
				apimachinerytypes.NamespacedName{
					Namespace: event.Object.GetNamespace(),
					Name:      event.Object.GetName(),
				}.String(),
			)
			enqueue(ctx, event.Object, queue)
		},
	}
}

// linkEnqueueHandler enqueues launchers for both snapshots of a Link update. This is required for
// endpoint rewires (the former launcher must remove the old termination), while connectivity-only
// changes still enqueue the unchanged terminating launchers for live flavor reconciliation.
func (c *Controller) linkEnqueueHandler() ctrlruntimehandler.EventHandler {
	enqueue := func(
		ctx context.Context,
		queue clientgoworkqueue.TypedRateLimitingInterface[ctrlruntimereconcile.Request],
		objects ...ctrlruntimeclient.Object,
	) {
		for _, request := range c.enqueueLaunchersForLinkObjects(ctx, objects...) {
			queue.Add(request)
		}
	}

	return ctrlruntimehandler.Funcs{
		CreateFunc: func(
			ctx context.Context,
			event ctrlruntimeevent.CreateEvent,
			queue clientgoworkqueue.TypedRateLimitingInterface[ctrlruntimereconcile.Request],
		) {
			enqueue(ctx, queue, event.Object)
		},
		UpdateFunc: func(
			ctx context.Context,
			event ctrlruntimeevent.UpdateEvent,
			queue clientgoworkqueue.TypedRateLimitingInterface[ctrlruntimereconcile.Request],
		) {
			enqueue(ctx, queue, event.ObjectOld, event.ObjectNew)
		},
		DeleteFunc: func(
			ctx context.Context,
			event ctrlruntimeevent.DeleteEvent,
			queue clientgoworkqueue.TypedRateLimitingInterface[ctrlruntimereconcile.Request],
		) {
			enqueue(ctx, queue, event.Object)
		},
	}
}

func launcherProfileReferenceIndex(obj ctrlruntimeclient.Object) []string {
	node, ok := obj.(*clabernetesapisv1alpha1.Node)
	if !ok || node.Spec.LauncherProfileRef == nil ||
		node.Spec.LauncherProfileRef.Name == "" {
		return nil
	}

	return []string{node.Spec.LauncherProfileRef.Name}
}

// enqueueLaunchersForLauncherProfile maps a profile event to only the launcher group primaries
// containing Nodes that explicitly reference it.
func (c *Controller) enqueueLaunchersForLauncherProfile(
	ctx context.Context,
	obj ctrlruntimeclient.Object,
) []ctrlruntimereconcile.Request {
	profile, ok := obj.(*clabernetesapisv1alpha1.LauncherProfile)
	if !ok {
		return nil
	}

	referencingNodes := &clabernetesapisv1alpha1.NodeList{}

	err := c.Client.List(
		ctx,
		referencingNodes,
		ctrlruntimeclient.InNamespace(profile.GetNamespace()),
		ctrlruntimeclient.MatchingFields{
			launcherProfileReferenceField: profile.GetName(),
		},
	)
	if err != nil {
		c.Log.Criticalf("failed listing Nodes referencing LauncherProfile, err: %s", err)

		return nil
	}

	if len(referencingNodes.Items) == 0 {
		return nil
	}

	namespaceNodes := &clabernetesapisv1alpha1.NodeList{}

	err = c.Client.List(
		ctx,
		namespaceNodes,
		ctrlruntimeclient.InNamespace(profile.GetNamespace()),
	)
	if err != nil {
		c.Log.Criticalf("failed listing Nodes for LauncherProfile group mapping, err: %s", err)

		return nil
	}

	nodesByName := clabernetesutilcontainerlab.NodesByName(namespaceNodes.Items)
	launcherNames := make(map[string]bool, len(referencingNodes.Items))

	for idx := range referencingNodes.Items {
		referencingNode := &referencingNodes.Items[idx]
		nodesByName[referencingNode.GetName()] = referencingNode
		launcherNames[clabernetesutilcontainerlab.ResolveLauncherNode(
			nodesByName,
			referencingNode.GetName(),
		)] = true
	}

	requests := make([]ctrlruntimereconcile.Request, 0, len(launcherNames))

	for launcherName := range launcherNames {
		requests = append(requests, ctrlruntimereconcile.Request{
			NamespacedName: apimachinerytypes.NamespacedName{
				Namespace: profile.GetNamespace(),
				Name:      launcherName,
			},
		})
	}

	return requests
}

// enqueueAllNodes enqueues all Nodes in all namespaces (config changes affect everything).
func (c *Controller) enqueueAllNodes(
	ctx context.Context,
	_ ctrlruntimeclient.Object,
) []ctrlruntimereconcile.Request {
	nodes := &clabernetesapisv1alpha1.NodeList{}

	err := c.Client.List(ctx, nodes)
	if err != nil {
		c.Log.Criticalf("failed listing nodes for enqueue, err: %s", err)

		return nil
	}

	requests := make([]ctrlruntimereconcile.Request, len(nodes.Items))

	for idx := range nodes.Items {
		requests[idx] = ctrlruntimereconcile.Request{
			NamespacedName: apimachinerytypes.NamespacedName{
				Namespace: nodes.Items[idx].GetNamespace(),
				Name:      nodes.Items[idx].GetName(),
			},
		}
	}

	return requests
}

// enqueueLaunchersForLink enqueues the launcher nodes terminating each side of a link -- link
// changes can change the launchers' attachment digests.
func (c *Controller) enqueueLaunchersForLink(
	ctx context.Context,
	obj ctrlruntimeclient.Object,
) []ctrlruntimereconcile.Request {
	link, ok := obj.(*clabernetesapisv1alpha1.Link)
	if !ok {
		return nil
	}

	nodes := &clabernetesapisv1alpha1.NodeList{}

	err := c.Client.List(ctx, nodes, ctrlruntimeclient.InNamespace(obj.GetNamespace()))
	if err != nil {
		c.Log.Criticalf("failed listing nodes for link enqueue, err: %s", err)

		return nil
	}

	byName := clabernetesutilcontainerlab.NodesByName(nodes.Items)

	launchers := map[string]bool{}

	for _, endpointNode := range []string{
		link.Spec.EndpointA.NodeName,
		link.Spec.EndpointB.NodeName,
	} {
		if endpointNode == clabernetesapisv1alpha1.LinkHostNodeName {
			continue
		}

		launchers[clabernetesutilcontainerlab.ResolveLauncherNode(byName, endpointNode)] = true
	}

	requests := make([]ctrlruntimereconcile.Request, 0, len(launchers))

	for launcher := range launchers {
		requests = append(requests, ctrlruntimereconcile.Request{
			NamespacedName: apimachinerytypes.NamespacedName{
				Namespace: obj.GetNamespace(),
				Name:      launcher,
			},
		})
	}

	return requests
}

func (c *Controller) enqueueLaunchersForLinkObjects(
	ctx context.Context,
	objects ...ctrlruntimeclient.Object,
) []ctrlruntimereconcile.Request {
	requestsByName := make(map[apimachinerytypes.NamespacedName]ctrlruntimereconcile.Request)

	for _, obj := range objects {
		for _, request := range c.enqueueLaunchersForLink(ctx, obj) {
			requestsByName[request.NamespacedName] = request
		}
	}

	names := make([]apimachinerytypes.NamespacedName, 0, len(requestsByName))
	for name := range requestsByName {
		names = append(names, name)
	}

	sort.Slice(names, func(i, j int) bool {
		if names[i].Namespace != names[j].Namespace {
			return names[i].Namespace < names[j].Namespace
		}

		return names[i].Name < names[j].Name
	})

	requests := make([]ctrlruntimereconcile.Request, 0, len(names))
	for _, name := range names {
		requests = append(requests, requestsByName[name])
	}

	return requests
}

// enqueueNodeForPod enqueues the (launcher) Node a launcher pod belongs to (via the topology
// node label) so pod/probe state lands in the node statuses.
func (c *Controller) enqueueNodeForPod(
	_ context.Context,
	obj ctrlruntimeclient.Object,
) []ctrlruntimereconcile.Request {
	nodeName, ok := obj.GetLabels()[clabernetesconstants.LabelTopologyNode]
	if !ok || nodeName == "" {
		return nil
	}

	return []ctrlruntimereconcile.Request{{
		NamespacedName: apimachinerytypes.NamespacedName{
			Namespace: obj.GetNamespace(),
			Name:      nodeName,
		},
	}}
}
