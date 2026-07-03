package topology

import (
	"context"

	clabernetesapis "github.com/srl-labs/clabernetes/apis"
	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/srl-labs/clabernetes/config"
	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	clabernetescontrollers "github.com/srl-labs/clabernetes/controllers"
	clabernetesmanagertypes "github.com/srl-labs/clabernetes/manager/types"
	k8sappsv1 "k8s.io/api/apps/v1"
	k8scorev1 "k8s.io/api/core/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	ctrlruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimecontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	ctrlruntimehandler "sigs.k8s.io/controller-runtime/pkg/handler"
	ctrlruntimereconcile "sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Controller is the Containerlab topology controller object.
type Controller struct {
	*clabernetescontrollers.BaseController

	TopologyReconciler *Reconciler
}

// NewController returns a new Controller.
func NewController(
	clabernetes clabernetesmanagertypes.Clabernetes,
) clabernetescontrollers.Controller {
	ctx := clabernetes.GetContext()

	baseController := clabernetescontrollers.NewBaseController(
		ctx,
		clabernetesapis.Topology,
		clabernetes.GetAppName(),
		clabernetes.GetKubeConfig(),
		clabernetes.GetCtrlRuntimeClient(),
	)

	c := &Controller{
		BaseController: baseController,
		TopologyReconciler: NewReconciler(
			baseController.Log,
			baseController.Client,
			clabernetes.GetAppName(),
			clabernetes.GetNamespace(),
			clabernetes.GetClusterCRIKind(),
			clabernetesconfig.GetManager,
		),
	}

	return c
}

// SetupWithManager sets up the controller with the Manager.
func (c *Controller) SetupWithManager(mgr ctrlruntime.Manager) error {
	c.BaseController.Log.Infof(
		"setting up %s controller with manager",
		clabernetesapis.Topology,
	)

	return ctrlruntime.NewControllerManagedBy(mgr).
		WithOptions(
			ctrlruntimecontroller.Options{
				MaxConcurrentReconciles: 1,
			},
		).
		For(&clabernetesapisv1alpha1.Topology{}).
		// watch services so we can update the status of containerlab object with load balancer
		// address
		Watches(
			&k8scorev1.Service{},
			ctrlruntimehandler.EnqueueRequestForOwner(
				mgr.GetScheme(),
				mgr.GetRESTMapper(),
				&clabernetesapisv1alpha1.Topology{},
			),
		).
		// watch owned deployments; we (for now?!) only do this so we can track the status of the
		// startup/readiness probes. maybe in the future we'll do more with this?
		Watches(
			&k8sappsv1.Deployment{},
			ctrlruntimehandler.EnqueueRequestForOwner(
				mgr.GetScheme(),
				mgr.GetRESTMapper(),
				&clabernetesapisv1alpha1.Topology{},
			),
		).
		// watch pods so pod status changes (probe results) trigger topology reconciliation
		Watches(
			&k8scorev1.Pod{},
			ctrlruntimehandler.EnqueueRequestsFromMapFunc(c.enqueueForPod),
		).
		// watch owned node crs so any drift (manual edits, deletions) gets healed
		Watches(
			&clabernetesapisv1alpha1.Node{},
			ctrlruntimehandler.EnqueueRequestForOwner(
				mgr.GetScheme(),
				mgr.GetRESTMapper(),
				&clabernetesapisv1alpha1.Topology{},
			),
		).
		// watch owned link crs so any drift (manual edits, deletions) gets healed
		Watches(
			&clabernetesapisv1alpha1.Link{},
			ctrlruntimehandler.EnqueueRequestForOwner(
				mgr.GetScheme(),
				mgr.GetRESTMapper(),
				&clabernetesapisv1alpha1.Topology{},
			),
		).
		// watch our config cr too so we get any config updates handled
		Watches(
			&clabernetesapisv1alpha1.Config{},
			ctrlruntimehandler.EnqueueRequestsFromMapFunc(
				c.enqueueForAll,
			),
		).
		Complete(c)
}

// enqueueForPod enqueues the owning Topology when a pod status changes.
func (c *Controller) enqueueForPod(
	_ context.Context,
	obj ctrlruntimeclient.Object,
) []ctrlruntimereconcile.Request {
	labels := obj.GetLabels()

	ownerName, ok := labels[clabernetesconstants.LabelTopologyOwner]
	if !ok {
		return nil
	}

	return []ctrlruntimereconcile.Request{{
		NamespacedName: apimachinerytypes.NamespacedName{
			Namespace: obj.GetNamespace(),
			Name:      ownerName,
		},
	}}
}

// enqueueForAll enqueues all Topology CRs for reconciliation.
func (c *Controller) enqueueForAll(
	ctx context.Context,
	_ ctrlruntimeclient.Object,
) []ctrlruntimereconcile.Request {
	topologies := &clabernetesapisv1alpha1.TopologyList{}

	err := c.Client.List(ctx, topologies)
	if err != nil {
		c.Log.Criticalf("failed listing resource objects in EnqueueForAll, err: %s", err)

		return nil
	}

	requests := make([]ctrlruntimereconcile.Request, len(topologies.Items))

	for idx := range topologies.Items {
		requests[idx] = ctrlruntimereconcile.Request{
			NamespacedName: apimachinerytypes.NamespacedName{
				Namespace: topologies.Items[idx].GetNamespace(),
				Name:      topologies.Items[idx].GetName(),
			},
		}
	}

	return requests
}
