package manager

import (
	"strings"

	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesmanagertypes "github.com/clabernetes/clabernetes/manager/types"
	clabernetesutil "github.com/clabernetes/clabernetes/util"
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

	c.logger.Info("determining cri sameness (or not)...")

	nodeCriKind, err := cri(c)
	if err != nil {
		c.logger.Fatalf("failed dermining cri sameness, err: %s", err)
	}

	c.criKind = nodeCriKind

	c.logger.Debug("cri sameness check complete...")

	c.logger.Info("verifying link field selector support...")

	err = preStartLinkFieldSelectors(c)
	if err != nil {
		c.logger.Fatalf(
			"the api server rejected the link endpoint field selectors -- clabernetes requires"+
				" kubernetes 1.31+ (crd selectable fields) since launchers select their links"+
				" server side, err: %s",
			err,
		)
	}

	c.logger.Debug("link field selector support verified...")

	c.logger.Debug("pre-start complete...")
}

// preStartLinkFieldSelectors probes the api server with the same field selectors launchers (and
// controllers) use to select links -- on clusters older than 1.31 (no crd selectable fields)
// the probe fails, and clabernetes cannot function without it.
func preStartLinkFieldSelectors(c clabernetesmanagertypes.Clabernetes) error {
	ctx, ctxCancel := c.NewContextWithTimeout()
	defer ctxCancel()

	_, err := c.GetKubeClabernetesClient().ClabernetesV1alpha1().
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

// cri fetches all the nodes in the cluster to determine if the cri(s) in use are the same across
// all nodes. we do this to know if we can/should (if configured to do so) enable "cri pull through"
// mode for images. in this mode launcher pods are configured to pull images directly via the
// underlying cri, but in order to do this we need to know *which* cri (and hence why we need to
// understand if this is the same cri across the cluster) so we know what socket to mount into the
// launcher pods so they can tickle the cri directly.
func cri(c clabernetesmanagertypes.Clabernetes) (string, error) {
	ctx, ctxCancel := c.NewContextWithTimeout()
	defer ctxCancel()

	// use the uncached client since we limit what controller runtime caches (and it doesnt include
	// nodes!)
	client := c.GetKubeClient()

	nodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", err
	}

	nodeCRIs := clabernetesutil.NewStringSet()

	for idx := range nodes.Items {
		criVersion := nodes.Items[idx].Status.NodeInfo.ContainerRuntimeVersion

		switch {
		case strings.HasPrefix(criVersion, clabernetesconstants.KubernetesCRIContainerd):
			nodeCRIs.Add(clabernetesconstants.KubernetesCRIContainerd)
		case strings.HasPrefix(criVersion, clabernetesconstants.KubernetesCRICrio):
			nodeCRIs.Add(clabernetesconstants.KubernetesCRICrio)
		default:
			nodeCRIs.Add(clabernetesconstants.KubernetesCRIUnknown)
		}
	}

	if nodeCRIs.Len() == 1 {
		return nodeCRIs.Items()[0], nil
	}

	return clabernetesconstants.KubernetesCRIUnknown, nil
}
