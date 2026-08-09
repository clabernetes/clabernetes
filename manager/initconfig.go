package manager

import (
	"fmt"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesmanagertypes "github.com/clabernetes/clabernetes/manager/types"
	clabernetesutil "github.com/clabernetes/clabernetes/util"
	k8scorev1 "k8s.io/api/core/v1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func initializeConfig(c clabernetesmanagertypes.Clabernetes) {
	logger := c.GetBaseLogger()

	bootstrapConfigMap, err := initGetBootstrapConfigmap(c)
	if err != nil {
		if apimachineryerrors.IsNotFound(err) {
			logger.Infof(
				"bootstrap config configmap not found, probably already processed. continuing...",
			)

			return
		}

		logger.Warnf(
			"failed fetching bootstrap config configmap, will continue without processing,,"+
				" but there may be other issues. err: %s", err,
		)

		return
	}

	configCR, configCRExists, err := initConfigGetConfigCR(c)
	if err != nil {
		logger.Warnf(
			"failed fetching global config, will continue, but there may be other issues. err: %s",
			err,
		)

		return
	}

	err = initConfigCreateOrUpdateConfig(c, bootstrapConfigMap, configCR, configCRExists)
	if err != nil {
		logger.Warnf(
			"failed updating global config, will continue, but there may be other issues. err: %s",
			err,
		)

		return
	}

	err = initBootstrapConfigDelete(c, bootstrapConfigMap)
	if err != nil {
		logger.Warnf(
			"failed removing bootstrap config configmap, will continue, but there may be "+
				"other issues. err: %s", err,
		)
	}
}

func initGetBootstrapConfigmap(
	c clabernetesmanagertypes.Clabernetes,
) (*k8scorev1.ConfigMap, error) {
	ctx, ctxCancel := c.NewContextWithTimeout()
	defer ctxCancel()

	bootstrapConfigMap, err := c.GetKubeClient().CoreV1().ConfigMaps(c.GetNamespace()).Get(
		ctx,
		fmt.Sprintf("%s-config", c.GetAppName()),
		metav1.GetOptions{},
	)
	if err != nil {
		return nil, err
	}

	return bootstrapConfigMap, nil
}

func initConfigGetConfigCR(
	c clabernetesmanagertypes.Clabernetes,
) (*clabernetesapisv1alpha1.Config, bool, error) {
	ctx, ctxCancel := c.NewContextWithTimeout()
	defer ctxCancel()

	configCR, err := c.GetKubeClabernetesClient().
		C9sV1alpha1().
		Configs(c.GetNamespace()).
		Get(
			ctx,
			clabernetesconstants.Clabernetes,
			metav1.GetOptions{},
		)
	if err != nil {
		if apimachineryerrors.IsNotFound(err) {
			return &clabernetesapisv1alpha1.Config{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clabernetesconstants.Clabernetes,
					Namespace: c.GetNamespace(),
				},
			}, false, nil
		}

		return nil, false, err
	}

	return configCR, true, nil
}

func initConfigCreateOrUpdateConfig(
	c clabernetesmanagertypes.Clabernetes,
	bootstrapConfigMap *k8scorev1.ConfigMap,
	configCR *clabernetesapisv1alpha1.Config,
	configCRExists bool,
) error {
	_, startingCRHash, err := clabernetesutil.HashObject(configCR)
	if err != nil {
		return err
	}

	err = clabernetesconfig.MergeFromBootstrapConfig(bootstrapConfigMap, configCR, configCRExists)
	if err != nil {
		return err
	}

	_, endingCRHash, err := clabernetesutil.HashObject(configCR)
	if err != nil {
		return err
	}

	if startingCRHash == endingCRHash {
		// hash didnt change, nothing to do
		return nil
	}

	ctx, ctxCancel := c.NewContextWithTimeout()
	defer ctxCancel()

	if configCRExists {
		_, err = c.GetKubeClabernetesClient().
			C9sV1alpha1().
			Configs(c.GetNamespace()).
			Update(
				ctx,
				configCR,
				metav1.UpdateOptions{},
			)
		if err != nil {
			return err
		}
	} else {
		_, err = c.GetKubeClabernetesClient().C9sV1alpha1().Configs(c.GetNamespace()).
			Create(
				ctx,
				configCR,
				metav1.CreateOptions{},
			)
		if err != nil {
			return err
		}
	}

	return nil
}

func initBootstrapConfigDelete(
	c clabernetesmanagertypes.Clabernetes,
	bootstrapConfigMap *k8scorev1.ConfigMap,
) error {
	ctx, ctxCancel := c.NewContextWithTimeout()
	defer ctxCancel()

	return c.GetKubeClient().CoreV1().ConfigMaps(bootstrapConfigMap.Namespace).Delete(
		ctx,
		bootstrapConfigMap.Name,
		metav1.DeleteOptions{},
	)
}
