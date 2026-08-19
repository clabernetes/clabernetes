//nolint:gocritic,noinlineerr,testpackage,wsl_v5 // RPC tests use compact fail-fast assertions.
package hostendpoint

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestDaemonRejectsRequestThatDiffersFromKubernetesState(t *testing.T) {
	t.Parallel()
	pod := testIdentity("lab", "router-pod", "pod-uid")
	endpoint := testEndpoint("lab", "link", "link-uid", "router", "node-uid", "host-a", "eth1")
	state := &fakeState{expected: []Endpoint{endpoint}}
	operations := &fakeOperations{}
	daemon := &Daemon{NodeName: "worker-a", State: state, Operations: operations}
	request := ReconcileRequest{
		SchemaVersion: SchemaVersion,
		Pod:           pod,
		Endpoints:     []Endpoint{endpoint},
	}
	request.Endpoints[0].HostInterface = "host-b"
	if _, err := daemon.Reconcile(context.Background(), request, 1); err == nil {
		t.Fatal("unauthorized request was accepted")
	}
	if len(state.marked) != 0 || len(operations.events) != 0 {
		t.Fatalf("unauthorized request mutated state: %#v %#v", state.marked, operations.events)
	}
}

func TestDaemonRecordsOwnershipBeforeMutationAndSweepsStaleState(t *testing.T) {
	t.Parallel()
	pod := testIdentity("lab", "router-pod", "pod-uid")
	endpoint := testEndpoint("lab", "link", "link-uid", "router", "node-uid", "host-a", "eth1")
	desired := endpoint
	desired.pod = pod
	stale := OwnedEndpoint{
		HostInterface: "stale-a",
		Ownership: Ownership{
			LinkUID: "stale-link-uid", NodeUID: "stale-node-uid", PodUID: "stale-pod-uid",
		},
	}
	events := []string{}
	state := &fakeState{expected: []Endpoint{endpoint}, desired: []Endpoint{desired}}
	state.onMark = func() { events = append(events, "mark") }
	operations := &fakeOperations{owned: []OwnedEndpoint{stale}}
	operations.onDelete = func(OwnedEndpoint) { events = append(events, "delete") }
	operations.onEnsure = func(Endpoint, ObjectIdentity, int) error {
		events = append(events, "ensure")

		return nil
	}
	daemon := &Daemon{NodeName: "worker-a", State: state, Operations: operations}
	_, err := daemon.Reconcile(context.Background(), ReconcileRequest{
		SchemaVersion: SchemaVersion,
		Pod:           pod,
		Endpoints:     []Endpoint{endpoint},
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(events, []string{"mark", "delete", "ensure"}) {
		t.Fatalf("unexpected reconciliation order: %#v", events)
	}
}

func TestDaemonSweepReleasesFinalizerOnlyAfterOwnedStateIsAbsent(t *testing.T) {
	t.Parallel()
	stale := OwnedEndpoint{
		HostInterface: "stale-a",
		Ownership: Ownership{
			LinkUID: "link-uid", NodeUID: "node-uid", PodUID: "pod-uid",
		},
	}
	state := &fakeState{finalizing: []FinalizingLink{{
		Identity: testIdentity("lab", "link", "link-uid"), AppliedNode: "worker-a",
	}}}
	operations := &fakeOperations{owned: []OwnedEndpoint{stale}}
	daemon := &Daemon{NodeName: "worker-a", State: state, Operations: operations}
	if err := daemon.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(operations.deleted) != 1 || len(state.removed) != 1 {
		t.Fatalf(
			"sweep did not delete before finalizing: deleted=%#v removed=%#v",
			operations.deleted,
			state.removed,
		)
	}
}

func TestUnixRPCPassesExactlyOneNetworkNamespaceDescriptor(t *testing.T) {
	pod := testIdentity("lab", "router-pod", "pod-uid")
	endpoint := testEndpoint("lab", "link", "link-uid", "router", "node-uid", "host-a", "eth1")
	desired := endpoint
	desired.pod = pod
	state := &fakeState{expected: []Endpoint{endpoint}, desired: []Endpoint{desired}}
	operations := &fakeOperations{}
	operations.onEnsure = func(_ Endpoint, _ ObjectIdentity, namespaceFD int) error {
		stat := &unix.Stat_t{}
		if err := unix.Fstat(namespaceFD, stat); err != nil {
			return fmt.Errorf("received namespace descriptor is invalid: %w", err)
		}

		return nil
	}
	daemon := &Daemon{NodeName: "worker-a", State: state, Operations: operations}
	ctx, cancel := context.WithCancel(context.Background())
	socketPath := filepath.Join(t.TempDir(), "host-endpoint.sock")
	serveResult := make(chan error, 1)
	go func() { serveResult <- daemon.Serve(ctx, socketPath) }()
	waitForSocket(t, socketPath)
	client := Client{SocketPath: socketPath}
	if _, err := client.Reconcile(ctx, ReconcileRequest{
		SchemaVersion: SchemaVersion,
		Pod:           pod,
		Endpoints:     []Endpoint{endpoint},
	}, "/proc/self/ns/net"); err != nil {
		cancel()
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-serveResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("host-endpoint daemon did not stop")
	}
	if len(operations.ensured) != 1 {
		t.Fatalf("RPC did not realize the endpoint: %#v", operations.ensured)
	}
}

func waitForSocket(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if info, err := os.Stat(path); err == nil && info.Mode()&os.ModeSocket != 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("socket %q did not appear", path)
}

type fakeState struct {
	mutex         sync.Mutex
	expected      []Endpoint
	desired       []Endpoint
	desiredFabric []FabricEndpoint
	finalizing    []FinalizingLink
	marked        []Endpoint
	removed       []ObjectIdentity
	onMark        func()
}

func (s *fakeState) DesiredFabricForNode(context.Context, string) ([]FabricEndpoint, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	return slices.Clone(s.desiredFabric), nil
}

func (s *fakeState) ExpectedForPod(
	context.Context,
	string,
	ObjectIdentity,
) ([]Endpoint, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	return slices.Clone(s.expected), nil
}

func (s *fakeState) DesiredForNode(context.Context, string) ([]Endpoint, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	return slices.Clone(s.desired), nil
}

func (s *fakeState) MarkPending(
	_ context.Context,
	_ string,
	_ ObjectIdentity,
	endpoint Endpoint,
) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.marked = append(s.marked, endpoint)
	if s.onMark != nil {
		s.onMark()
	}

	return nil
}

func (s *fakeState) FinalizingLinks(context.Context, string) ([]FinalizingLink, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	return slices.Clone(s.finalizing), nil
}

func (s *fakeState) RemoveFinalizer(
	_ context.Context,
	_ string,
	identity ObjectIdentity,
) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.removed = append(s.removed, identity)

	return nil
}

type fakeOperations struct {
	mutex         sync.Mutex
	owned         []OwnedEndpoint
	ownedFabric   []OwnedFabricObject
	ensured       []Endpoint
	ensuredFabric []FabricEndpoint
	deleted       []OwnedEndpoint
	deletedFabric []OwnedFabricObject
	events        []string
	fabricReady   bool
	onEnsure      func(Endpoint, ObjectIdentity, int) error
	onDelete      func(OwnedEndpoint)
}

func (o *fakeOperations) ListFabric(context.Context) ([]OwnedFabricObject, error) {
	o.mutex.Lock()
	defer o.mutex.Unlock()

	return slices.Clone(o.ownedFabric), nil
}

func (o *fakeOperations) EnsureFabric(
	_ context.Context,
	endpoint FabricEndpoint,
	_ ObjectIdentity,
	_ string,
	_ int,
) (FabricStatus, error) {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	o.ensuredFabric = append(o.ensuredFabric, endpoint)
	o.events = append(o.events, "ensureFabric")

	return FabricStatus{LinkUID: endpoint.Link.UID, Ready: o.fabricReady}, nil
}

func (o *fakeOperations) ReconcileFabricTransports(
	context.Context,
	[]FabricEndpoint,
	string,
) error {
	return nil
}

func (o *fakeOperations) DeleteFabric(_ context.Context, object OwnedFabricObject) error {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	o.deletedFabric = append(o.deletedFabric, object)
	o.owned = slices.Clone(o.owned)

	return nil
}

func (o *fakeOperations) List(context.Context) ([]OwnedEndpoint, error) {
	o.mutex.Lock()
	defer o.mutex.Unlock()

	return slices.Clone(o.owned), nil
}

func (o *fakeOperations) Ensure(
	_ context.Context,
	endpoint Endpoint,
	pod ObjectIdentity,
	namespaceFD int,
) error {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	o.ensured = append(o.ensured, endpoint)
	o.events = append(o.events, "ensure")
	if o.onEnsure != nil {
		return o.onEnsure(endpoint, pod, namespaceFD)
	}

	return nil
}

func (o *fakeOperations) Delete(_ context.Context, endpoint OwnedEndpoint) error {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	o.deleted = append(o.deleted, endpoint)
	o.events = append(o.events, "delete")
	o.owned = slices.DeleteFunc(o.owned, func(candidate OwnedEndpoint) bool {
		return candidate == endpoint
	})
	if o.onDelete != nil {
		o.onDelete(endpoint)
	}

	return nil
}
