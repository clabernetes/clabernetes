package connectivity

import (
	"context"
	"fmt"
	"os"
	"sort"
	"time"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	clabernetesgeneratedclientset "github.com/srl-labs/clabernetes/generated/clientset"
	claberneteslogging "github.com/srl-labs/clabernetes/logging"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerywatch "k8s.io/apimachinery/pkg/watch"
)

const watchLinksReconnectDelay = 5 * time.Second

// nodeLinkSelectors returns the label selectors matching all links that terminate on the given
// (launcher) node -- one selector for links where the node is the "a" side, and one for links
// where it is the "b" side.
func nodeLinkSelectors(topologyName, nodeName string) []string {
	return []string{
		fmt.Sprintf(
			"%s=%s,%s=%s",
			clabernetesconstants.LabelTopologyOwner,
			topologyName,
			clabernetesconstants.LabelLinkEndpointA,
			nodeName,
		),
		fmt.Sprintf(
			"%s=%s,%s=%s",
			clabernetesconstants.LabelTopologyOwner,
			topologyName,
			clabernetesconstants.LabelLinkEndpointB,
			nodeName,
		),
	}
}

// linkToLocalTunnel converts a link cr to the "local view" tunnel for the given (launcher) node.
func linkToLocalTunnel(
	nodeName string,
	link *clabernetesapisv1alpha1.Link,
) *clabernetesapisv1alpha1.PointToPointTunnel {
	local, remote := link.Spec.EndpointA, link.Spec.EndpointB

	if link.Spec.EndpointB.LauncherNode == nodeName {
		local, remote = remote, local
	}

	return &clabernetesapisv1alpha1.PointToPointTunnel{
		TunnelID:        link.Spec.TunnelID,
		Destination:     remote.Destination,
		LocalNode:       local.NodeName,
		LocalInterface:  local.InterfaceName,
		RemoteNode:      remote.NodeName,
		RemoteInterface: remote.InterfaceName,
	}
}

// ListNodeTunnels lists all link crs that terminate on the given (launcher) node and converts
// them to the node's local tunnel view.
func ListNodeTunnels(
	ctx context.Context,
	clabernetesClient *clabernetesgeneratedclientset.Clientset,
	namespace,
	topologyName,
	nodeName string,
) ([]*clabernetesapisv1alpha1.PointToPointTunnel, error) {
	tunnels := make([]*clabernetesapisv1alpha1.PointToPointTunnel, 0)

	seenLinks := make(map[string]bool)

	for _, selector := range nodeLinkSelectors(topologyName, nodeName) {
		links, err := clabernetesClient.ClabernetesV1alpha1().
			Links(namespace).
			List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			return nil, err
		}

		for i := range links.Items {
			if seenLinks[links.Items[i].Name] {
				continue
			}

			seenLinks[links.Items[i].Name] = true

			tunnels = append(tunnels, linkToLocalTunnel(nodeName, &links.Items[i]))
		}
	}

	sort.Slice(
		tunnels,
		func(i, j int) bool { return tunnels[i].LocalInterface < tunnels[j].LocalInterface },
	)

	return tunnels, nil
}

// watchLinks watches the link crs terminating on this launcher's node -- any time a link event
// occurs the links are re-listed and the (complete, freshly converted) local tunnel view is
// passed to handleUpdate.
func watchLinks(
	ctx context.Context,
	logger claberneteslogging.Instance,
	clabernetesClient *clabernetesgeneratedclientset.Clientset,
	handleUpdate func(nodeTunnels []*clabernetesapisv1alpha1.PointToPointTunnel),
) {
	namespace := os.Getenv(clabernetesconstants.PodNamespaceEnv)
	topologyName := os.Getenv(clabernetesconstants.LauncherTopologyNameEnv)
	nodeName := os.Getenv(clabernetesconstants.LauncherNodeNameEnv)

	// buffered chan of one so link events collapse into a single pending refresh
	linkEvents := make(chan struct{}, 1)

	for _, selector := range nodeLinkSelectors(topologyName, nodeName) {
		go watchLinksWithSelector(ctx, logger, clabernetesClient, namespace, selector, linkEvents)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-linkEvents:
			logger.Info("processing link event(s)")

			nodeTunnels, err := ListNodeTunnels(
				ctx,
				clabernetesClient,
				namespace,
				topologyName,
				nodeName,
			)
			if err != nil {
				logger.Warnf("failed re-listing links after link event, err: %s", err)

				continue
			}

			handleUpdate(nodeTunnels)
		}
	}
}

// watchLinksWithSelector watches links matching the given selector (re-establishing the watch if
// it dies) and pokes the given events channel whenever a link is added/modified/deleted.
func watchLinksWithSelector(
	ctx context.Context,
	logger claberneteslogging.Instance,
	clabernetesClient *clabernetesgeneratedclientset.Clientset,
	namespace,
	selector string,
	linkEvents chan struct{},
) {
	for ctx.Err() == nil {
		watch, err := clabernetesClient.ClabernetesV1alpha1().
			Links(namespace).
			Watch(ctx, metav1.ListOptions{LabelSelector: selector, Watch: true})
		if err != nil {
			if ctx.Err() != nil {
				return
			}

			logger.Warnf(
				"failed watching clabernetes links, will retry in %s, err: %s",
				watchLinksReconnectDelay,
				err,
			)

			time.Sleep(watchLinksReconnectDelay)

			continue
		}

		for event := range watch.ResultChan() {
			switch event.Type {
			case apimachinerywatch.Added,
				apimachinerywatch.Modified,
				apimachinerywatch.Deleted:
				select {
				case linkEvents <- struct{}{}:
				default:
					// a refresh is already pending, nothing to do
				}
			case apimachinerywatch.Bookmark,
				apimachinerywatch.Error:
				logger.Debugf("link watch had %s event occur, ignoring...", event.Type)
			}
		}

		// watch channel closed (api server timeout or similar), loop around and re-watch
	}
}
