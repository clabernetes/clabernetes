package imagerequest

import (
	clabernetesapis "github.com/clabernetes/clabernetes/apis"
	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetescontrollers "github.com/clabernetes/clabernetes/controllers"
	clabernetesmanagertypes "github.com/clabernetes/clabernetes/manager/types"
	"k8s.io/client-go/kubernetes"
	ctrlruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimecontroller "sigs.k8s.io/controller-runtime/pkg/controller"
)

const (
	concurrentReconciles = 10
)

// NewController returns a new Controller.
func NewController(
	clabernetes clabernetesmanagertypes.Clabernetes,
) clabernetescontrollers.Controller {
	ctx := clabernetes.GetContext()

	baseController := clabernetescontrollers.NewBaseController(
		ctx,
		clabernetesapis.ImageRequest,
		clabernetes.GetAppName(),
		clabernetes.GetKubeConfig(),
		clabernetes.GetCtrlRuntimeClient(),
	)

	c := &Controller{
		BaseController: baseController,
		KubeClient:     clabernetes.GetKubeClient(),
	}

	return c
}

// Controller is the Containerlab topology controller object.
type Controller struct {
	*clabernetescontrollers.BaseController

	// the *uncached* (non ctrl-runtime client) so we can do watches
	KubeClient *kubernetes.Clientset
}

// SetupWithManager sets up the controller with the Manager.
func (c *Controller) SetupWithManager(mgr ctrlruntime.Manager) error {
	c.BaseController.Log.Infof(
		"setting up %s controller with manager",
		clabernetesapis.ImageRequest,
	)

	return ctrlruntime.NewControllerManagedBy(mgr).
		WithOptions(
			ctrlruntimecontroller.Options{
				MaxConcurrentReconciles: concurrentReconciles,
			},
		).
		For(&clabernetesapisv1alpha1.ImageRequest{}).
		Complete(c)
}
