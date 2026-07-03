package launcher

import (
	"context"
	"fmt"
	"os"
	"time"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	claberneteserrors "github.com/srl-labs/clabernetes/errors"
	"gopkg.in/yaml.v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	fetchNodeMaxAttempts   = 5
	fetchNodeRetryInterval = 5 * time.Second
)

// fetchNodeResource fetches "our" node cr -- the cr the clabernetes controller rendered for this
// launcher's node -- and dumps its contents (the sub-topology, files from url, and pull secrets)
// to disk where the rest of the launcher (and containerlab itself) expects them.
func (c *clabernetes) fetchNodeResource() {
	node, err := c.getNodeResource()
	if err != nil {
		c.logger.Fatalf("failed fetching clabernetes node resource, err: %s", err)
	}

	filesFromURLBytes, err := yaml.Marshal(node.Spec.FilesFromURL)
	if err != nil {
		c.logger.Fatalf("failed marshaling files from url data, err: %s", err)
	}

	imagePullSecretsBytes, err := yaml.Marshal(node.Spec.ImagePullSecrets)
	if err != nil {
		c.logger.Fatalf("failed marshaling image pull secrets data, err: %s", err)
	}

	for fileName, contents := range map[string][]byte{
		"topo.clab.yaml":               []byte(node.Spec.Config),
		"files-from-url.yaml":          filesFromURLBytes,
		"configured-pull-secrets.yaml": imagePullSecretsBytes,
	} {
		err = os.WriteFile(
			fileName,
			contents,
			clabernetesconstants.PermissionsEveryoneRead,
		)
		if err != nil {
			c.logger.Fatalf("failed writing %q to disk, err: %s", fileName, err)
		}
	}

	c.logger.Debug("fetched node resource and wrote topology data to disk")
}

func (c *clabernetes) getNodeResource() (*clabernetesapisv1alpha1.Node, error) {
	namespace := os.Getenv(clabernetesconstants.PodNamespaceEnv)

	selector := fmt.Sprintf(
		"%s=%s,%s=%s",
		clabernetesconstants.LabelTopologyOwner,
		os.Getenv(clabernetesconstants.LauncherTopologyNameEnv),
		clabernetesconstants.LabelTopologyNode,
		c.nodeName,
	)

	var lastErr error

	for attempt := range fetchNodeMaxAttempts {
		if attempt > 0 {
			time.Sleep(fetchNodeRetryInterval)
		}

		ctx, cancel := context.WithTimeout(c.ctx, clientDefaultTimeout)

		nodes, err := c.kubeClabernetesClient.ClabernetesV1alpha1().
			Nodes(namespace).
			List(ctx, metav1.ListOptions{LabelSelector: selector})

		cancel()

		if err != nil {
			lastErr = err

			c.logger.Warnf(
				"failed listing clabernetes node resources, will retry, err: %s", err,
			)

			continue
		}

		if len(nodes.Items) != 1 {
			lastErr = fmt.Errorf(
				"%w: expected exactly one node resource matching selector %q, got %d",
				claberneteserrors.ErrLaunch,
				selector,
				len(nodes.Items),
			)

			c.logger.Warnf("%s, will retry...", lastErr)

			continue
		}

		return &nodes.Items[0], nil
	}

	return nil, lastErr
}
