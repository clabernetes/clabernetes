package launcher

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	claberneteserrors "github.com/clabernetes/clabernetes/errors"
	claberneteslauncherconnectivity "github.com/clabernetes/clabernetes/launcher/connectivity"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
	"gopkg.in/yaml.v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	fetchNodeMaxAttempts   = 60
	fetchNodeRetryInterval = 5 * time.Second

	linkAttachmentsDigestFile = "/clabernetes/podinfo/link-attachments-digest"
)

// fetchNodeResources fetches the primary api objects this launcher realizes -- its own Node,
// the Nodes of any group members, and the Links terminating on any of them -- then verifies the
// link view is *complete* against the attachments digest annotation (available through the
// downward api), materializes the containerlab topology, and writes everything the rest of the
// launcher expects to disk. The whole sequence retries until the digest matches: a mismatch
// simply means the objects are still settling (or the pod roll for a wiring change is about to
// replace us anyway).
func (c *clabernetes) fetchNodeResources() {
	namespace := os.Getenv(clabernetesconstants.PodNamespaceEnv)

	localNodes := claberneteslauncherconnectivity.LocalNodesFromEnv()

	var members map[string]*clabernetesapisv1alpha1.Node

	var links []clabernetesapisv1alpha1.Link

	var lastErr error

	for attempt := range fetchNodeMaxAttempts {
		if attempt > 0 {
			time.Sleep(fetchNodeRetryInterval)
		}

		members, lastErr = c.getNodeResources(namespace, localNodes)
		if lastErr != nil {
			c.logger.Warnf("failed fetching node resources, will retry, err: %s", lastErr)

			continue
		}

		links, lastErr = c.getLinkResources(namespace, localNodes)
		if lastErr != nil {
			c.logger.Warnf("failed fetching link resources, will retry, err: %s", lastErr)

			continue
		}

		lastErr = verifyLinkAttachmentsDigest(localNodes, links)
		if lastErr != nil {
			c.logger.Warnf("%s, will retry...", lastErr)

			continue
		}

		break
	}

	if lastErr != nil {
		c.logger.Fatalf("failed fetching clabernetes node resources, err: %s", lastErr)
	}

	c.initialTunnels = tunnelsForLinks(members, links)

	config := materializeTopology(c.nodeName, members, links, mgmtNetworkFromEnv(c))

	c.writeNodeFiles(config, members)

	c.logger.Debug("fetched node resources and wrote topology data to disk")
}

func (c *clabernetes) getNodeResources(
	namespace string,
	localNodes map[string]bool,
) (map[string]*clabernetesapisv1alpha1.Node, error) {
	members := make(map[string]*clabernetesapisv1alpha1.Node, len(localNodes))

	for nodeName := range localNodes {
		ctx, cancel := context.WithTimeout(c.ctx, clientDefaultTimeout)

		node, err := c.kubeClabernetesClient.C9sV1alpha1().
			Nodes(namespace).
			Get(ctx, nodeName, metav1.GetOptions{})

		cancel()

		if err != nil {
			return nil, fmt.Errorf("fetching node %q: %w", nodeName, err)
		}

		members[nodeName] = node
	}

	return members, nil
}

func (c *clabernetes) getLinkResources(
	namespace string,
	localNodes map[string]bool,
) ([]clabernetesapisv1alpha1.Link, error) {
	ctx, cancel := context.WithTimeout(c.ctx, clientDefaultTimeout)
	defer cancel()

	return claberneteslauncherconnectivity.ListNodeLinks(
		ctx,
		c.kubeClabernetesClient,
		namespace,
		localNodes,
	)
}

// verifyLinkAttachmentsDigest compares the digest of the fetched links against the digest the
// controller stamped on the pod (via the downward api) -- a match proves the launcher's link
// view is exactly the view the controller rendered this pod for.
func verifyLinkAttachmentsDigest(
	localNodes map[string]bool,
	links []clabernetesapisv1alpha1.Link,
) error {
	expectedDigest, err := os.ReadFile(linkAttachmentsDigestFile)
	if err != nil {
		return fmt.Errorf("reading link attachments digest file: %w", err)
	}

	members := make([]string, 0, len(localNodes))

	for nodeName := range localNodes {
		members = append(members, nodeName)
	}

	actualDigest := clabernetesutilcontainerlab.LinkAttachmentsDigest(members, links)

	if actualDigest != strings.TrimSpace(string(expectedDigest)) {
		return fmt.Errorf(
			"%w: fetched link view does not (yet) match the link attachments digest",
			claberneteserrors.ErrLaunch,
		)
	}

	return nil
}

// mgmtNetworkFromEnv returns the containerlab management network settings passed down by the
// controller (if any).
func mgmtNetworkFromEnv(c *clabernetes) *clabernetesutilcontainerlab.MgmtNet {
	raw := os.Getenv(clabernetesconstants.LauncherMgmtNetworkEnv)
	if raw == "" {
		return nil
	}

	mgmt := &clabernetesutilcontainerlab.MgmtNet{}

	err := json.Unmarshal([]byte(raw), mgmt)
	if err != nil {
		c.logger.Warnf("failed parsing mgmt network settings, ignoring, err: %s", err)

		return nil
	}

	return mgmt
}

// writeNodeFiles writes the materialized topology, the files-from-url data (the union over all
// group members), and the configured pull secrets to the places the rest of the launcher (and
// containerlab itself) expects them.
func (c *clabernetes) writeNodeFiles(
	config *clabernetesutilcontainerlab.Config,
	members map[string]*clabernetesapisv1alpha1.Node,
) {
	configBytes, err := yaml.Marshal(config)
	if err != nil {
		c.logger.Fatalf("failed marshaling materialized topology, err: %s", err)
	}

	filesFromURL := make([]clabernetesapisv1alpha1.FileFromURL, 0)

	for _, member := range members {
		filesFromURL = append(filesFromURL, member.Spec.FilesFromURL...)
	}

	filesFromURLBytes, err := yaml.Marshal(filesFromURL)
	if err != nil {
		c.logger.Fatalf("failed marshaling files from url data, err: %s", err)
	}

	var pullSecrets []string

	rawPullSecrets := os.Getenv(clabernetesconstants.LauncherPullSecretsEnv)
	if rawPullSecrets != "" {
		pullSecrets = strings.Split(rawPullSecrets, ",")
	}

	pullSecretsBytes, err := yaml.Marshal(pullSecrets)
	if err != nil {
		c.logger.Fatalf("failed marshaling image pull secrets data, err: %s", err)
	}

	for fileName, contents := range map[string][]byte{
		"topo.clab.yaml":               configBytes,
		"files-from-url.yaml":          filesFromURLBytes,
		"configured-pull-secrets.yaml": pullSecretsBytes,
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
}
