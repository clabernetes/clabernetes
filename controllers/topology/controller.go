package topology

import (
	clabernetesapis "github.com/clabernetes/clabernetes/apis"
	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetescontrollers "github.com/clabernetes/clabernetes/controllers"
	clabernetesmanagertypes "github.com/clabernetes/clabernetes/manager/types"
	ctrlruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimecontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	ctrlruntimehandler "sigs.k8s.io/controller-runtime/pkg/handler"
)

// Controller is the Containerlab topology controller object -- despite the grand name it is
// "just" a compiler these days: it compiles a Topology definition into Node/Link/LauncherProfile
// objects and aggregates their statuses; all actual reconciliation happens in the node and link
// controllers, identically for compiled and hand written objects.
type Controller struct {
	*clabernetescontrollers.BaseController

	reconciler *Reconciler
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
		reconciler: NewReconciler(
			baseController.Log,
			baseController.Client,
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
		// watch the emitted objects: node status changes feed the aggregated topology status,
		// and spec drift on any emitted object gets re-rendered away
		Watches(
			&clabernetesapisv1alpha1.Node{},
			ctrlruntimehandler.EnqueueRequestForOwner(
				mgr.GetScheme(),
				mgr.GetRESTMapper(),
				&clabernetesapisv1alpha1.Topology{},
			),
		).
		Watches(
			&clabernetesapisv1alpha1.Link{},
			ctrlruntimehandler.EnqueueRequestForOwner(
				mgr.GetScheme(),
				mgr.GetRESTMapper(),
				&clabernetesapisv1alpha1.Topology{},
			),
		).
		Watches(
			&clabernetesapisv1alpha1.LauncherProfile{},
			ctrlruntimehandler.EnqueueRequestForOwner(
				mgr.GetScheme(),
				mgr.GetRESTMapper(),
				&clabernetesapisv1alpha1.Topology{},
			),
		).
		Complete(c)
}
