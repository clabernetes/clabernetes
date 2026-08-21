package manager

import (
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetesmanagertypes "github.com/clabernetes/clabernetes/manager/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (c *clabernetes) preStart() {
	c.logger.Info("begin pre-start...")

	c.logger.Info("starting config manager...")

	err := preStartConfig(c)
	if err != nil {
		// we *shouldn't* actually ever hit this as the config manager can start and *not* find a
		// config that it manages just fine, but i guess its possible that something terrible
		// could happen that would prevent us from continuing.
		c.logger.Fatalf("failed starting config manager, err: %s", err)
	}

	c.logger.Debug("config manager started...")

	c.logger.Info("verifying link field selector support...")

	err = preStartLinkFieldSelectors(c)
	if err != nil {
		c.logger.Fatalf(
			"the api server rejected the link endpoint field selectors -- clabernetes requires"+
				" kubernetes 1.31+ (crd selectable fields) since connectivity reconcilers select"+
				" their links"+
				" server side, err: %s",
			err,
		)
	}

	c.logger.Debug("link field selector support verified...")

	c.logger.Debug("pre-start complete...")
}

// preStartLinkFieldSelectors probes the api server with the same field selectors the
// connectivity reconcilers (and
// controllers) use to select links -- on clusters older than 1.31 (no crd selectable fields)
// the probe fails, and clabernetes cannot function without it.
func preStartLinkFieldSelectors(c clabernetesmanagertypes.Clabernetes) error {
	ctx, ctxCancel := c.NewContextWithTimeout()
	defer ctxCancel()

	_, err := c.GetKubeClabernetesClient().C9sV1alpha1().
		Links(c.GetNamespace()).
		List(ctx, metav1.ListOptions{
			FieldSelector: "spec.endpointA.nodeName=clabernetes-field-selector-probe",
			Limit:         1,
		})

	return err
}

// config initializes the config manager singleton.
func preStartConfig(c clabernetesmanagertypes.Clabernetes) error {
	clabernetesconfig.InitManager(
		c.GetContext(),
		c.GetAppName(),
		c.GetNamespace(),
		c.GetKubeClabernetesClient(),
	)

	configManager := clabernetesconfig.GetManager()

	err := configManager.Start()
	if err != nil {
		return err
	}

	return nil
}
