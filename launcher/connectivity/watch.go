package connectivity

import (
	"context"
	"fmt"
	"os"
	"sort"
	"time"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesgeneratedclientset "github.com/clabernetes/clabernetes/generated/clientset"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerywatch "k8s.io/apimachinery/pkg/watch"
)

const watchLinksReconnectDelay = 5 * time.Second

// NodeLinkFieldSelectors returns the field selectors matching all links that terminate on the
// given local (containerlab) nodes -- one selector per node per link side. This is what keeps a
// launcher's watch stream scoped to its own degree: the api server (kubernetes 1.31+, crd
// selectable fields) filters server side, no labels involved.
func NodeLinkFieldSelectors(localNodes map[string]bool) []string {
	nodeNames := make([]string, 0, len(localNodes))

	for nodeName := range localNodes {
		nodeNames = append(nodeNames, nodeName)
	}

	sort.Strings(nodeNames)

	selectors := make([]string, 0, 2*len(nodeNames)) //nolint:mnd

	for _, nodeName := range nodeNames {
		selectors = append(
			selectors,
			fmt.Sprintf("spec.endpointA.nodeName=%s", nodeName),
			fmt.Sprintf("spec.endpointB.nodeName=%s", nodeName),
		)
	}

	return selectors
}

// ListNodeLinks lists all link crs that terminate on the given local nodes (deduplicated --
// links between two local nodes match two selectors).
func ListNodeLinks(
	ctx context.Context,
	clabernetesClient *clabernetesgeneratedclientset.Clientset,
	namespace string,
	localNodes map[string]bool,
) ([]clabernetesapisv1alpha1.Link, error) {
	seenLinks := make(map[string]bool)

	links := make([]clabernetesapisv1alpha1.Link, 0)

	for _, selector := range NodeLinkFieldSelectors(localNodes) {
		selectorLinks, err := clabernetesClient.C9sV1alpha1().
			Links(namespace).
			List(ctx, metav1.ListOptions{FieldSelector: selector})
		if err != nil {
			return nil, err
		}

		for idx := range selectorLinks.Items {
			if seenLinks[selectorLinks.Items[idx].Name] {
				continue
			}

			seenLinks[selectorLinks.Items[idx].Name] = true

			links = append(links, selectorLinks.Items[idx])
		}
	}

	sort.Slice(links, func(i, j int) bool { return links[i].Name < links[j].Name })

	return links, nil
}

// ListNodeTunnels lists all link crs that terminate on the given local nodes and converts them
// to the local tunnel view.
func ListNodeTunnels(
	ctx context.Context,
	clabernetesClient *clabernetesgeneratedclientset.Clientset,
	namespace string,
	localNodes map[string]bool,
) ([]*Tunnel, error) {
	links, err := ListNodeLinks(ctx, clabernetesClient, namespace, localNodes)
	if err != nil {
		return nil, err
	}

	return TunnelsFromLinks(localNodes, links), nil
}

// watchLinks watches the link crs terminating on this launcher's nodes -- any time a link event
// occurs the links are re-listed and the (complete, freshly converted) local tunnel view is
// passed to handleUpdate.
func watchLinks(
	ctx context.Context,
	logger claberneteslogging.Instance,
	clabernetesClient *clabernetesgeneratedclientset.Clientset,
	handleUpdate func(nodeTunnels []*Tunnel) error,
) {
	namespace := os.Getenv(clabernetesconstants.PodNamespaceEnv)
	localNodes := LocalNodesFromEnv()

	// buffered chan of one so link events collapse into a single pending refresh
	linkEvents := make(chan struct{}, 1)

	for _, selector := range NodeLinkFieldSelectors(localNodes) {
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
			logger.Info("processing link events")

			nodeTunnels, err := ListNodeTunnels(
				ctx,
				clabernetesClient,
				namespace,
				localNodes,
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

// watchLinksWithSelector watches links matching the given (field) selector (re-establishing the
// watch if it dies) and pokes the given events channel whenever a link is
// added/modified/deleted.
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
	links, err := clabernetesClient.C9sV1alpha1().
		Links(namespace).
		List(ctx, metav1.ListOptions{FieldSelector: selector})
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

	// The worker's list closes the gap with the launcher's earlier startup list. Watching from
	// this resource version then closes the gap between this list and watch creation.
	signalLinkRefresh(linkEvents)

	linkWatch, err := clabernetesClient.C9sV1alpha1().
		Links(namespace).
		Watch(ctx, metav1.ListOptions{
			FieldSelector:       selector,
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
