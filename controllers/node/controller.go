//nolint:err113 // diagnostics are structured one-off errors carrying typed classification.
package node

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
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
	k8snetworkingv1 "k8s.io/api/networking/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	clientgorest "k8s.io/client-go/rest"
	clientgoremotecommand "k8s.io/client-go/tools/remotecommand"
	clientgoworkqueue "k8s.io/client-go/util/workqueue"
	ctrlruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimecontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	ctrlruntimeevent "sigs.k8s.io/controller-runtime/pkg/event"
	ctrlruntimehandler "sigs.k8s.io/controller-runtime/pkg/handler"
	ctrlruntimereconcile "sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const profileReferenceField = "spec.profileRef.name"

// Controller is the clabernetes Node controller -- it turns each (primary) Node into a
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

	reconciler := NewReconciler(
		baseController.Log,
		baseController.Client,
		clabernetes.GetCtrlRuntimeMgr().GetAPIReader(),
		clabernetes.GetAppName(),
		clabernetesconfig.GetManager,
	)
	reconciler.DirectRuntimeImage = clabernetes.GetDeviceRuntimeImage()
	reconciler.DirectContainerExecutor = newDirectContainerExecutor(
		clabernetes.GetKubeConfig(),
		clabernetes.GetKubeClient(),
	)
	readLogs := PlannerLogReader(func(
		ctx context.Context,
		namespace,
		podName,
		containerName string,
	) ([]byte, error) {
		return clabernetes.GetKubeClient().CoreV1().Pods(namespace).GetLogs(
			podName,
			&k8scorev1.PodLogOptions{Container: containerName},
		).DoRaw(ctx)
	})
	reconciler.ImageDiscoveryReconciler.ReadLogs = readLogs
	reconciler.PlannerReconciler.ReadLogs = readLogs

	return &Controller{
		BaseController: baseController,
		reconciler:     reconciler,
	}
}

func newDirectContainerExecutor(
	config *clientgorest.Config,
	client *kubernetes.Clientset,
) DirectContainerExecutor {
	return func(
		ctx context.Context,
		namespace,
		podName,
		containerName string,
		command []string,
	) error {
		if config == nil || client == nil || namespace == "" || podName == "" ||
			containerName == "" || len(command) == 0 {
			return errors.New("direct container exec identity is incomplete")
		}

		request := client.CoreV1().RESTClient().Post().
			Namespace(namespace).
			Resource("pods").
			Name(podName).
			SubResource("exec").
			VersionedParams(&k8scorev1.PodExecOptions{
				Container: containerName,
				Command:   command,
				Stdout:    true,
				Stderr:    true,
			}, clientgoscheme.ParameterCodec)

		executor, err := clientgoremotecommand.NewSPDYExecutor(
			config,
			http.MethodPost,
			request.URL(),
		)
		if err != nil {
			return fmt.Errorf("creating direct container executor: %w", err)
		}

		var stderr bytes.Buffer
		if err = executor.StreamWithContext(ctx, clientgoremotecommand.StreamOptions{
			Stdout: &bytes.Buffer{}, Stderr: &stderr,
		}); err != nil {
			return fmt.Errorf(
				"executing direct container command: %w: %s",
				err,
				stderr.String(),
			)
		}

		return nil
	}
}

// SetupWithManager sets up the controller with the Manager.
func (c *Controller) SetupWithManager(mgr ctrlruntime.Manager) error {
	c.BaseController.Log.Infof(
		"setting up %s controller with manager",
		clabernetesapis.Node,
	)
	c.reconciler.EventRecorder = mgr.GetEventRecorder("clabernetes-node-controller")

	err := mgr.GetFieldIndexer().IndexField(
		c.Ctx,
		&clabernetesapisv1alpha1.Node{},
		profileReferenceField,
		profileReferenceIndex,
	)
	if err != nil {
		return fmt.Errorf("indexing Nodes by NodeProfile reference: %w", err)
	}

	return ctrlruntime.NewControllerManagedBy(mgr).
		WithOptions(
			ctrlruntimecontroller.Options{
				MaxConcurrentReconciles: 1,
			},
		).
		For(&clabernetesapisv1alpha1.Node{}).
		// group co-members: a (grouped) node's primary renders that node's services, status
		// and the shared pod, so events on any node also enqueue its (old and new) primary
		Watches(
			&clabernetesapisv1alpha1.Node{},
			c.primaryEnqueueHandler(),
		).
		// NodeProfile changes enqueue only groups with explicit references to that profile.
		Watches(
			&clabernetesapisv1alpha1.NodeProfile{},
			ctrlruntimehandler.EnqueueRequestsFromMapFunc(
				c.enqueuePrimariesForNodeProfile,
			),
		).
		// links feed the connectivity plans of the primaries terminating them
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
			&k8scorev1.ConfigMap{},
			ctrlruntimehandler.EnqueueRequestForOwner(
				mgr.GetScheme(),
				mgr.GetRESTMapper(),
				&clabernetesapisv1alpha1.Node{},
			),
		).
		// referenced payload objects are not Node-owned; changes must still re-plan the
		// pod group that consumes them.
		Watches(
			&k8scorev1.ConfigMap{},
			ctrlruntimehandler.EnqueueRequestsFromMapFunc(c.enqueuePrimariesForPayloadObject),
		).
		Watches(
			&k8scorev1.Secret{},
			ctrlruntimehandler.EnqueueRequestsFromMapFunc(c.enqueuePrimariesForPayloadObject),
		).
		Watches(
			&k8scorev1.Secret{},
			ctrlruntimehandler.EnqueueRequestForOwner(
				mgr.GetScheme(),
				mgr.GetRESTMapper(),
				&clabernetesapisv1alpha1.Node{},
			),
		).
		Watches(
			&k8snetworkingv1.NetworkPolicy{},
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

// enqueuePrimaryFor resolves the primary node hosting the given node object and returns a
// request for it -- the object itself is included in the resolution view so deletes/updates
// resolve sensibly even when the cache no longer holds the object.
func (c *Controller) enqueuePrimaryFor(
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
		c.Log.Criticalf("failed listing nodes for primary enqueue, err: %s", err)

		return nil
	}

	byName := clabernetesutilcontainerlab.NodesByName(nodes.Items)
	byName[node.GetName()] = node

	primary := clabernetesutilcontainerlab.ResolvePrimaryNode(byName, node.GetName())
	if primary == node.GetName() {
		// the node is its own primary; the For() watch already enqueued it
		return nil
	}

	return []ctrlruntimereconcile.Request{{
		NamespacedName: apimachinerytypes.NamespacedName{
			Namespace: obj.GetNamespace(),
			Name:      primary,
		},
	}}
}

// primaryEnqueueHandler enqueues the primary of a node on create/delete, and on update the
// primaries per both the old and the new object -- so a node moving between groups re-renders
// both affected device pods.
func (c *Controller) primaryEnqueueHandler() ctrlruntimehandler.EventHandler {
	enqueue := func(
		ctx context.Context,
		obj ctrlruntimeclient.Object,
		queue clientgoworkqueue.TypedRateLimitingInterface[ctrlruntimereconcile.Request],
	) {
		for _, request := range c.enqueuePrimaryFor(ctx, obj) {
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
				"observed Node deletion event for %q; reconciling its pod group",
				apimachinerytypes.NamespacedName{
					Namespace: event.Object.GetNamespace(),
					Name:      event.Object.GetName(),
				}.String(),
			)
			enqueue(ctx, event.Object, queue)
		},
	}
}

// linkEnqueueHandler enqueues primaries for both snapshots of a Link update. This is required for
// endpoint rewires (the former primary must remove the old termination), while spec-only
// changes still enqueue the unchanged terminating primaries for live reconciliation.
func (c *Controller) linkEnqueueHandler() ctrlruntimehandler.EventHandler {
	enqueue := func(
		ctx context.Context,
		queue clientgoworkqueue.TypedRateLimitingInterface[ctrlruntimereconcile.Request],
		objects ...ctrlruntimeclient.Object,
	) {
		for _, request := range c.enqueuePrimariesForLinkObjects(ctx, objects...) {
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

func profileReferenceIndex(obj ctrlruntimeclient.Object) []string {
	node, ok := obj.(*clabernetesapisv1alpha1.Node)
	if !ok || node.Spec.ProfileRef == nil ||
		node.Spec.ProfileRef.Name == "" {
		return nil
	}

	return []string{node.Spec.ProfileRef.Name}
}

// enqueuePrimariesForNodeProfile maps a profile event to only the launcher group primaries
// containing Nodes that explicitly reference it.
func (c *Controller) enqueuePrimariesForNodeProfile(
	ctx context.Context,
	obj ctrlruntimeclient.Object,
) []ctrlruntimereconcile.Request {
	profile, ok := obj.(*clabernetesapisv1alpha1.NodeProfile)
	if !ok {
		return nil
	}

	referencingNodes := &clabernetesapisv1alpha1.NodeList{}

	err := c.Client.List(
		ctx,
		referencingNodes,
		ctrlruntimeclient.InNamespace(profile.GetNamespace()),
		ctrlruntimeclient.MatchingFields{
			profileReferenceField: profile.GetName(),
		},
	)
	if err != nil {
		c.Log.Criticalf("failed listing Nodes referencing NodeProfile, err: %s", err)

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
		c.Log.Criticalf("failed listing Nodes for NodeProfile group mapping, err: %s", err)

		return nil
	}

	nodesByName := clabernetesutilcontainerlab.NodesByName(namespaceNodes.Items)
	primaryNames := make(map[string]bool, len(referencingNodes.Items))

	for idx := range referencingNodes.Items {
		referencingNode := &referencingNodes.Items[idx]
		nodesByName[referencingNode.GetName()] = referencingNode
		primaryNames[clabernetesutilcontainerlab.ResolvePrimaryNode(
			nodesByName,
			referencingNode.GetName(),
		)] = true
	}

	requests := make([]ctrlruntimereconcile.Request, 0, len(primaryNames))

	for primaryName := range primaryNames {
		requests = append(requests, ctrlruntimereconcile.Request{
			NamespacedName: apimachinerytypes.NamespacedName{
				Namespace: profile.GetNamespace(),
				Name:      primaryName,
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

// enqueuePrimariesForPayloadObject maps a same-namespace ConfigMap or Secret change to every
// pod group whose Node declarations reference that object. Payload objects are deliberately
// not owned by Nodes, so owner watches cannot provide this invalidation path.
func (c *Controller) enqueuePrimariesForPayloadObject(
	ctx context.Context,
	obj ctrlruntimeclient.Object,
) []ctrlruntimereconcile.Request {
	var references func(*clabernetesapisv1alpha1.Node) bool

	switch payloadObject := obj.(type) {
	case *k8scorev1.ConfigMap:
		references = func(node *clabernetesapisv1alpha1.Node) bool {
			for _, declaration := range node.Spec.FilesFromConfigMap {
				if declaration.ConfigMapName == payloadObject.GetName() {
					return true
				}
			}

			return false
		}
	case *k8scorev1.Secret:
		references = func(node *clabernetesapisv1alpha1.Node) bool {
			for _, declaration := range node.Spec.FilesFromSecret {
				if declaration.SecretName == payloadObject.GetName() {
					return true
				}
			}

			return false
		}
	default:
		return nil
	}

	nodes := &clabernetesapisv1alpha1.NodeList{}
	if err := c.Client.List(
		ctx,
		nodes,
		ctrlruntimeclient.InNamespace(obj.GetNamespace()),
	); err != nil {
		c.Log.Criticalf("failed listing Nodes for payload object enqueue, err: %s", err)

		return nil
	}

	nodesByName := clabernetesutilcontainerlab.NodesByName(nodes.Items)
	primaryNames := map[string]bool{}

	for idx := range nodes.Items {
		node := &nodes.Items[idx]
		if references(node) {
			primaryNames[clabernetesutilcontainerlab.ResolvePrimaryNode(
				nodesByName,
				node.GetName(),
			)] = true
		}
	}

	names := make([]string, 0, len(primaryNames))
	for name := range primaryNames {
		names = append(names, name)
	}

	sort.Strings(names)

	requests := make([]ctrlruntimereconcile.Request, 0, len(names))
	for _, name := range names {
		requests = append(requests, ctrlruntimereconcile.Request{
			NamespacedName: apimachinerytypes.NamespacedName{
				Namespace: obj.GetNamespace(),
				Name:      name,
			},
		})
	}

	return requests
}

// enqueuePrimariesForLink enqueues the primary nodes terminating each side of a link -- link
// changes can change the primaries' connectivity plans.
func (c *Controller) enqueuePrimariesForLink(
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

	primaries := map[string]bool{}

	for _, endpointNode := range []string{
		link.Spec.EndpointA.NodeName,
		link.Spec.EndpointB.NodeName,
	} {
		if endpointNode == clabernetesapisv1alpha1.LinkHostNodeName {
			continue
		}

		primaries[clabernetesutilcontainerlab.ResolvePrimaryNode(byName, endpointNode)] = true
	}

	requests := make([]ctrlruntimereconcile.Request, 0, len(primaries))

	for primary := range primaries {
		requests = append(requests, ctrlruntimereconcile.Request{
			NamespacedName: apimachinerytypes.NamespacedName{
				Namespace: obj.GetNamespace(),
				Name:      primary,
			},
		})
	}

	return requests
}

func (c *Controller) enqueuePrimariesForLinkObjects(
	ctx context.Context,
	objects ...ctrlruntimeclient.Object,
) []ctrlruntimereconcile.Request {
	requestsByName := make(map[apimachinerytypes.NamespacedName]ctrlruntimereconcile.Request)

	for _, obj := range objects {
		for _, request := range c.enqueuePrimariesForLink(ctx, obj) {
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

// enqueueNodeForPod enqueues the (primary) Node a device pod belongs to (via the topology
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
