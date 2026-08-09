package manager

import (
	"fmt"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesmanagertypes "github.com/clabernetes/clabernetes/manager/types"
	k8sappsv1 "k8s.io/api/apps/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	ctrlruntimeutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// postStart handles tasks that can be done after the manager is started and the controller(s) are
// up and doing their thing. these are tasks that can basically be done whenever, but should be
// sorted at startup.
func (c *clabernetes) postStart() {
	c.logger.Info("begin post-start...")

	c.logger.Info("ensuring owner reference on global config")

	err := enforceConfigOwnerReference(c)
	if err != nil {
		c.logger.Warn("could not set owner reference on global config singleton, continuing...")
	}

	c.logger.Debug("post-start complete...")
}

func enforceConfigOwnerReference(c clabernetesmanagertypes.Clabernetes) error {
	logger := c.GetBaseLogger()

	ctrlRuntimeClient := c.GetCtrlRuntimeClient()

	ctx, ctxCancel := c.NewContextWithTimeout()

	clabernetesConfig := &clabernetesapisv1alpha1.Config{}

	err := ctrlRuntimeClient.Get(ctx, apimachinerytypes.NamespacedName{
		Namespace: c.GetNamespace(),
		Name:      clabernetesconstants.Clabernetes,
	}, clabernetesConfig)

	ctxCancel()

	if err != nil {
		logger.Criticalf("failed fetching global config singleton, err: %s", err)

		return err
	}

	managerDeployment := &k8sappsv1.Deployment{}

	ctx, ctxCancel = c.NewContextWithTimeout()

	err = ctrlRuntimeClient.Get(ctx, apimachinerytypes.NamespacedName{
		Namespace: c.GetNamespace(),
		Name:      fmt.Sprintf("%s-manager", c.GetAppName()),
	}, managerDeployment)

	ctxCancel()

	if err != nil {
		logger.Criticalf("failed fetching manager deployment, err: %s", err)

		return err
	}

	if len(clabernetesConfig.ObjectMeta.OwnerReferences) == 1 &&
		clabernetesConfig.ObjectMeta.OwnerReferences[0].UID == managerDeployment.UID {
		// for now at least we assume if there is any owner reference its ours and we are good
		return nil
	}

	err = ctrlruntimeutil.SetOwnerReference(managerDeployment, clabernetesConfig, c.GetScheme())
	if err != nil {
		return err
	}

	ctx, ctxCancel = c.NewContextWithTimeout()
	defer ctxCancel()

	err = ctrlRuntimeClient.Update(ctx, clabernetesConfig)
	if err != nil {
		logger.Criticalf("failed setting owner reference on global config object, err: %s", err)

		return err
	}

	return nil
}
