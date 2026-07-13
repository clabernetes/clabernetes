package connectivity

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	clabernetesgeneratedclientset "github.com/srl-labs/clabernetes/generated/clientset"
	claberneteslogging "github.com/srl-labs/clabernetes/logging"
	clabernetesutil "github.com/srl-labs/clabernetes/util"
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

// linkDestination returns the qualified service name of the remote launcher's fabric service --
// this is derived rather than persisted on the link cr: the service name follows directly from
// the topology name and the remote launcher node, the namespace is the link's own namespace, and
// the topology prefix / dns suffix knobs come down from the controller via the launcher env.
func linkDestination(link *clabernetesapisv1alpha1.Link, remoteLauncherNode string) string {
	serviceName := fmt.Sprintf("%s-%s-vx", link.Spec.TopologyName, remoteLauncherNode)

	if strings.EqualFold(
		os.Getenv(clabernetesconstants.LauncherTopologyRemovePrefixEnv),
		clabernetesconstants.True,
	) {
		serviceName = fmt.Sprintf("%s-vx", remoteLauncherNode)
	}

	return fmt.Sprintf(
		"%s.%s.%s",
		serviceName,
		link.GetNamespace(),
		clabernetesutil.GetEnvStrOrDefault(
			clabernetesconstants.LauncherInClusterDNSSuffixEnv,
			clabernetesconstants.KubernetesDefaultInClusterDNSSuffix,
		),
	)
}

// linkToLocalTunnel converts a link cr to the "local view" tunnel for the given (launcher) node.
// Which side is local (and which launcher node terminates the remote side) comes from the link's
// endpoint labels.
func linkToLocalTunnel(
	nodeName string,
	link *clabernetesapisv1alpha1.Link,
) *clabernetesapisv1alpha1.PointToPointTunnel {
	local, remote := link.Spec.EndpointA, link.Spec.EndpointB
	remoteLauncherNode := link.GetLabels()[clabernetesconstants.LabelLinkEndpointB]

	if link.GetLabels()[clabernetesconstants.LabelLinkEndpointB] == nodeName {
		local, remote = remote, local
		remoteLauncherNode = link.GetLabels()[clabernetesconstants.LabelLinkEndpointA]
	}

	return &clabernetesapisv1alpha1.PointToPointTunnel{
		TunnelID:        link.Spec.TunnelID,
		Destination:     linkDestination(link, remoteLauncherNode),
		LocalNode:       local.NodeName,
		LocalInterface:  local.InterfaceName,
		RemoteNode:      remote.NodeName,
		RemoteInterface: remote.InterfaceName,
		MTU:             link.Spec.MTU,
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
	handleUpdate func(nodeTunnels []*clabernetesapisv1alpha1.PointToPointTunnel) error,
) {
	namespace := os.Getenv(clabernetesconstants.PodNamespaceEnv)
	topologyName := os.Getenv(clabernetesconstants.LauncherTopologyNameEnv)
	nodeName := os.Getenv(clabernetesconstants.LauncherNodeNameEnv)

	// buffered chan of one so link events collapse into a single pending refresh
	linkEvents := make(chan struct{}, 1)

	for _, selector := range nodeLinkSelectors(topologyName, nodeName) {
		go watchLinksWithSelector(
			ctx,
			logger,
			clabernetesClient,
			namespace,
			selector,
			linkEvents,
			watchLinksReconnectDelay,
		)
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
				retryLinkRefresh(ctx, linkEvents, watchLinksReconnectDelay)

				continue
			}

			err = handleUpdate(nodeTunnels)
			if err != nil {
				logger.Warnf("failed reconciling links after link event, err: %s", err)
				retryLinkRefresh(ctx, linkEvents, watchLinksReconnectDelay)
			}
		}
	}
}

func signalLinkRefresh(linkEvents chan<- struct{}) {
	select {
	case linkEvents <- struct{}{}:
	default:
		// a refresh is already pending
	}
}

func retryLinkRefresh(ctx context.Context, linkEvents chan<- struct{}, delay time.Duration) {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
	case <-timer.C:
		signalLinkRefresh(linkEvents)
	}
}

func waitForLinkWatchReconnect(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
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
	linkEvents chan<- struct{},
	reconnectDelay time.Duration,
) {
	for ctx.Err() == nil {
		if !watchLinksSession(
			ctx,
			logger,
			clabernetesClient,
			namespace,
			selector,
			linkEvents,
			reconnectDelay,
		) || !waitForLinkWatchReconnect(ctx, reconnectDelay) {
			return
		}
	}
}

// watchLinksSession lists once and consumes the watch rooted at that list's resource version. A
// true result asks the caller to reconnect; false means the context was canceled.
func watchLinksSession(
	ctx context.Context,
	logger claberneteslogging.Instance,
	clabernetesClient *clabernetesgeneratedclientset.Clientset,
	namespace,
	selector string,
	linkEvents chan<- struct{},
	reconnectDelay time.Duration,
) bool {
	links, err := clabernetesClient.ClabernetesV1alpha1().
		Links(namespace).
		List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		if ctx.Err() != nil {
			return false
		}

		logger.Warnf(
			"failed listing clabernetes links before watch, will retry in %s, err: %s",
			reconnectDelay,
			err,
		)

		return true
	}

	// The worker's list closes the gap with the launcher's earlier startup list. Watching from this
	// resource version then closes the gap between this list and watch creation.
	signalLinkRefresh(linkEvents)

	linkWatch, err := clabernetesClient.ClabernetesV1alpha1().
		Links(namespace).
		Watch(ctx, metav1.ListOptions{
			LabelSelector:       selector,
			ResourceVersion:     links.ResourceVersion,
			AllowWatchBookmarks: true,
		})
	if err != nil {
		if ctx.Err() != nil {
			return false
		}

		logger.Warnf(
			"failed watching clabernetes links, will retry in %s, err: %s",
			reconnectDelay,
			err,
		)

		return true
	}

	return consumeLinkWatch(ctx, logger, linkWatch, linkEvents)
}

func consumeLinkWatch(
	ctx context.Context,
	logger claberneteslogging.Instance,
	linkWatch apimachinerywatch.Interface,
	linkEvents chan<- struct{},
) bool {
	defer linkWatch.Stop()

	for {
		select {
		case <-ctx.Done():
			return false
		case event, ok := <-linkWatch.ResultChan():
			if !ok {
				return true
			}

			switch event.Type {
			case apimachinerywatch.Added,
				apimachinerywatch.Modified,
				apimachinerywatch.Deleted:
				signalLinkRefresh(linkEvents)
			case apimachinerywatch.Bookmark:
				logger.Debug("link watch bookmark received")
			case apimachinerywatch.Error:
				logger.Warn("link watch returned an error event; re-listing before reconnect")

				return true
			}
		}
	}
}
