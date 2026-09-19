package directruntime_test

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesinternaldirectruntime "github.com/clabernetes/clabernetes/internal/directruntime"
)

// delayedPeerOperations pauses local endpoint creation, then allows a missing peer to resolve.
// Only the running helper accesses the embedded operation recorder.
type delayedPeerOperations struct {
	*fakeLinkOperations

	localStarted chan struct{}
	localRelease chan struct{}
	peerReady    atomic.Bool
	announced    atomic.Bool
}

func (o *delayedPeerOperations) EnsureFabricEndpoint(
	spec clabernetesinternaldirectruntime.FabricEndpointSpec,
) (clabernetesinternaldirectruntime.FabricEndpointResult, error) {
	if o.announced.CompareAndSwap(false, true) {
		close(o.localStarted)
	}
	<-o.localRelease
	o.fabricUnready = !o.peerReady.Load()

	return o.fakeLinkOperations.EnsureFabricEndpoint(spec)
}

func TestConnectivityStartupWaitsForLocalSetupButNotRemotePeers(t *testing.T) {
	// A fixed Pod-address listener is part of the runtime contract; keep this test serial.
	input, plan := connectivityTestInputAndPlan(t)
	setWireLink(t, &input, &plan, "peer-node-uid", "peer-wire", 73, 1450)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	operations := &delayedPeerOperations{
		fakeLinkOperations: &fakeLinkOperations{},
		localStarted:       make(chan struct{}),
		localRelease:       make(chan struct{}),
	}
	result := make(chan error, 1)
	go func() {
		result <- clabernetesinternaldirectruntime.RunConnectivityWithLifecycleOperations(ctx, input, plan, clabernetesinternaldirectruntime.ConnectivityOptions{
			StateDirectory: t.TempDir(), PodNamespace: "lab", PodName: "router-pod", PodUID: "pod-uid", PodAddress: "127.0.0.1", FilterOperations: &fakeTransportFilterOperations{}, RevisionPollInterval: 10 * time.Millisecond,
		}, operations, nil)
	}()
	released := false
	defer func() {
		if !released {
			close(operations.localRelease)
		}
		cancel()
		select {
		case err := <-result:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("helper did not stop")
		}
	}()
	select {
	case <-operations.localStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("local initialization did not reach endpoint creation")
	}
	probe := func(path string, want int) {
		t.Helper()
		client := &http.Client{Timeout: time.Second}
		deadline := time.Now().Add(8 * time.Second)
		for {
			request, err := http.NewRequestWithContext(
				t.Context(),
				http.MethodGet,
				fmt.Sprintf(
					"http://127.0.0.1:%d%s",
					clabernetesconstants.ConnectivityReadinessPort,
					path,
				),
				http.NoBody,
			)
			if err != nil {
				t.Fatal(err)
			}
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			if response.StatusCode == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s returned %d, want %d", path, response.StatusCode, want)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	probe(clabernetesinternaldirectruntime.ConnectivityStartupPath, http.StatusServiceUnavailable)
	probe(clabernetesinternaldirectruntime.ConnectivityReadinessPath, http.StatusServiceUnavailable)
	close(operations.localRelease)
	released = true
	probe(clabernetesinternaldirectruntime.ConnectivityStartupPath, http.StatusOK)
	probe(clabernetesinternaldirectruntime.ConnectivityReadinessPath, http.StatusServiceUnavailable)
	operations.peerReady.Store(true)
	probe(clabernetesinternaldirectruntime.ConnectivityReadinessPath, http.StatusOK)
	probe(clabernetesinternaldirectruntime.ConnectivityStartupPath, http.StatusOK)
}
