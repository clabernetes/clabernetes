package connectivity //nolint:testpackage // tests exercise unexported watch lifecycle helpers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetesgeneratedclientset "github.com/srl-labs/clabernetes/generated/clientset"
	claberneteslogging "github.com/srl-labs/clabernetes/logging"
	"k8s.io/client-go/rest"
)

type initialListWatchGapHandler struct {
	changed                       atomic.Bool
	watches                       atomic.Int64
	watchesWithoutResourceVersion atomic.Int64
}

func (h *initialListWatchGapHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.URL.Query().Get("watch") == "true" {
		h.watches.Add(1)

		if r.URL.Query().Get("resourceVersion") == "" {
			h.watchesWithoutResourceVersion.Add(1)
		}

		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}

		<-r.Context().Done()

		return
	}

	items := ""
	resourceVersion := "1"
	selector := r.URL.Query().Get("labelSelector")

	if h.changed.Load() {
		resourceVersion = "2"

		if strings.Contains(selector, "linkEndpointA") {
			items = `{"apiVersion":"clabernetes.containerlab.dev/v1alpha1","kind":"Link","metadata":{"name":"link-1"},"spec":{"topologyName":"topology","endpointA":{"nodeName":"r1","interfaceName":"e1","launcherNode":"r1","destination":"r1"},"endpointB":{"nodeName":"r2","interfaceName":"e1","launcherNode":"r2","destination":"r2"},"tunnelID":1}}`
		}
	}

	_, _ = fmt.Fprintf(
		w,
		`{"apiVersion":"clabernetes.containerlab.dev/v1alpha1","kind":"LinkList","metadata":{"resourceVersion":%q},"items":[%s]}`,
		resourceVersion,
		items,
	)
}

func TestWatchLinksClosesInitialListWatchGap(t *testing.T) {
	handler := &initialListWatchGapHandler{}
	server := httptest.NewServer(handler)

	defer server.Close()

	client, err := clabernetesgeneratedclientset.NewForConfig(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}

	initial, err := ListNodeTunnels(t.Context(), client, "default", "topology", "r1")
	if err != nil {
		t.Fatal(err)
	}

	if len(initial) != 0 {
		t.Fatalf("expected empty initial list, got %d tunnels", len(initial))
	}

	// The Link is created after launcher startup's list and before either watch starts.
	handler.changed.Store(true)
	t.Setenv("POD_NAMESPACE", "default")
	t.Setenv("LAUNCHER_TOPOLOGY_NAME", "topology")
	t.Setenv("LAUNCHER_NODE_NAME", "r1")

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	updates := make(chan []*clabernetesapisv1alpha1.PointToPointTunnel, 1)

	go watchLinks(
		ctx,
		&claberneteslogging.FakeInstance{},
		client,
		func(tunnels []*clabernetesapisv1alpha1.PointToPointTunnel) error {
			select {
			case updates <- tunnels:
			default:
			}

			return nil
		},
	)

	select {
	case tunnels := <-updates:
		if len(tunnels) != 1 {
			t.Fatalf("expected one tunnel after watch startup, got %d", len(tunnels))
		}
	case <-time.After(time.Second):
		t.Fatal("Link created between initial list and watch startup was not observed")
	}

	deadline := time.Now().Add(time.Second)

	for handler.watches.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	if got := handler.watches.Load(); got != 2 {
		t.Fatalf("expected two selector watches, got %d", got)
	}

	if got := handler.watchesWithoutResourceVersion.Load(); got != 0 {
		t.Fatalf("%d watches started without a list resourceVersion", got)
	}
}

func TestClosedLinkWatchRelistsWithBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var (
		listRequests  atomic.Int64
		watchRequests atomic.Int64
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Query().Get("watch") == "true" {
			watchRequests.Add(1)

			_, _ = fmt.Fprintln(
				w,
				`{"type":"BOOKMARK","object":{"apiVersion":"clabernetes.containerlab.dev/v1alpha1","kind":"Link","metadata":{"name":"link","resourceVersion":"1"}}}`,
			)

			return
		}

		requestNumber := listRequests.Add(1)

		_, _ = fmt.Fprintf(
			w,
			`{"apiVersion":"clabernetes.containerlab.dev/v1alpha1","kind":"LinkList","metadata":{"resourceVersion":%q},"items":[]}`,
			strconv.FormatInt(requestNumber, 10),
		)
	}))
	defer server.Close()

	client, err := clabernetesgeneratedclientset.NewForConfig(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}

	go watchLinksWithSelector(
		ctx,
		&claberneteslogging.FakeInstance{},
		client,
		"default",
		"clabernetes/topologyOwner=topology",
		make(chan struct{}, 1),
		25*time.Millisecond,
	)

	time.Sleep(140 * time.Millisecond)
	cancel()

	if got := watchRequests.Load(); got < 2 {
		t.Fatalf("expected a closed watch to reconnect, got %d watch requests", got)
	} else if got > 7 {
		t.Fatalf("closed watch reconnected without effective backoff: %d requests", got)
	}

	if got := listRequests.Load(); got < 2 {
		t.Fatalf("expected watch reconnect to relist, got %d list requests", got)
	}
}
