package directruntime_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	clabernetesdeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	clabernetesdirectruntime "github.com/clabernetes/clabernetes/internal/directruntime"
	claberneteshostendpoint "github.com/clabernetes/clabernetes/internal/hostendpoint"
)

type fakeLinkOperations struct {
	sysctls             []string
	pairs               []string
	interfaces          []clabernetesdirectruntime.VethInterface
	deletions           []string
	managementAddresses []string
	managementRoutes    []string
	podTransport        string
	pairSignal          chan struct{}
	ensurePairError     error
	vxlanInterfaces     []clabernetesdirectruntime.VXLANInterface
	vxlanEnsures        []string
	vxlanPeerAddresses  map[string]string
	resolvePeer         func(string) (string, error)
	vxlanPeerEnsures    []string
	vxlanPeerSignal     chan string
	vxlanDeletions      []string
	resolvePeerError    error
}

func (f *fakeLinkOperations) ResolvePodTransportInterface(podAddress string) (string, error) {
	if podAddress == "" {
		return "", fmt.Errorf("Pod transport address is empty")
	}
	if f.podTransport == "" {
		f.podTransport = "pod-transport"
	}

	return f.podTransport, nil
}

func (f *fakeLinkOperations) EnsureSysctl(name, value string) error {
	f.sysctls = append(f.sysctls, name+"="+value)

	return nil
}

type fakeSlurpeethRuntime struct {
	reconciles chan []clabernetesdirectruntime.SlurpeethSegment
	errors     chan error
	ready      func() (bool, error)
	closed     atomic.Int64
}

type fakeHostEndpointReconciler struct {
	requests chan claberneteshostendpoint.ReconcileRequest
	err      error
}

func (f *fakeHostEndpointReconciler) Reconcile(
	_ context.Context,
	request claberneteshostendpoint.ReconcileRequest,
	networkNamespacePath string,
) error {
	if networkNamespacePath != "/proc/self/ns/net" {
		return fmt.Errorf("network namespace path = %q", networkNamespacePath)
	}
	if f.requests != nil {
		f.requests <- request
	}

	return f.err
}

func (f *fakeSlurpeethRuntime) Reconcile(
	_ context.Context,
	segments []clabernetesdirectruntime.SlurpeethSegment,
) error {
	cloned := append([]clabernetesdirectruntime.SlurpeethSegment(nil), segments...)
	if f.reconciles != nil {
		f.reconciles <- cloned
	}

	return nil
}

func (f *fakeSlurpeethRuntime) Ready() (bool, error) {
	if f.ready != nil {
		return f.ready()
	}

	return true, nil
}

func (f *fakeSlurpeethRuntime) Errors() <-chan error {
	return f.errors
}

func (f *fakeSlurpeethRuntime) Close() error {
	f.closed.Add(1)

	return nil
}

func (f *fakeLinkOperations) EnsureVethPair(left, right string, mtu int, owner string) error {
	if f.ensurePairError != nil {
		return f.ensurePairError
	}
	f.pairs = append(f.pairs, fmt.Sprintf("%s/%s/%d/%s", left, right, mtu, owner))
	remaining := f.interfaces[:0]
	for _, intf := range f.interfaces {
		if intf.Owner != owner {
			remaining = append(remaining, intf)
		}
	}
	f.interfaces = remaining
	f.interfaces = append(f.interfaces,
		clabernetesdirectruntime.VethInterface{Name: left, PeerName: right, Owner: owner},
		clabernetesdirectruntime.VethInterface{Name: right, PeerName: left, Owner: owner},
	)
	if f.pairSignal != nil {
		select {
		case f.pairSignal <- struct{}{}:
		default:
		}
	}

	return nil
}

func (f *fakeLinkOperations) ListVethInterfaces(
	ownerPrefix string,
) ([]clabernetesdirectruntime.VethInterface, error) {
	result := []clabernetesdirectruntime.VethInterface{}
	for _, intf := range f.interfaces {
		if strings.HasPrefix(intf.Owner, ownerPrefix) {
			result = append(result, intf)
		}
	}

	return result, nil
}

func (f *fakeLinkOperations) DeleteVethPair(name, owner string) error {
	f.deletions = append(f.deletions, name+"/"+owner)
	remaining := f.interfaces[:0]
	for _, intf := range f.interfaces {
		if intf.Owner != owner {
			remaining = append(remaining, intf)
		}
	}
	f.interfaces = remaining

	return nil
}

func (f *fakeLinkOperations) ListVXLANInterfaces(
	ownerPrefix string,
) ([]clabernetesdirectruntime.VXLANInterface, error) {
	result := []clabernetesdirectruntime.VXLANInterface{}
	for _, intf := range f.vxlanInterfaces {
		if strings.HasPrefix(intf.Owner, ownerPrefix) {
			result = append(result, intf)
		}
	}

	return result, nil
}

func (f *fakeLinkOperations) EnsureVXLANInterface(
	name string,
	tunnelID,
	mtu,
	destinationPort int,
	owner string,
) error {
	f.vxlanEnsures = append(
		f.vxlanEnsures,
		fmt.Sprintf("%s/%d/%d/%d/%s", name, tunnelID, mtu, destinationPort, owner),
	)
	remaining := f.vxlanInterfaces[:0]
	for _, intf := range f.vxlanInterfaces {
		if intf.Owner != owner {
			remaining = append(remaining, intf)
		}
	}
	f.vxlanInterfaces = append(remaining, clabernetesdirectruntime.VXLANInterface{
		Name: name, Owner: owner, TunnelID: tunnelID, MTU: mtu,
		DestinationPort: destinationPort,
	})

	return nil
}

func (f *fakeLinkOperations) ResolvePeerAddress(
	_ context.Context,
	destination string,
) (string, error) {
	if f.resolvePeerError != nil {
		return "", f.resolvePeerError
	}
	if f.resolvePeer != nil {
		return f.resolvePeer(destination)
	}
	address := f.vxlanPeerAddresses[destination]
	if address == "" {
		return "", clabernetesdirectruntime.ErrPeerTransportUnavailable
	}

	return address, nil
}

func (f *fakeLinkOperations) EnsureVXLANPeer(name, address, owner string) error {
	f.vxlanPeerEnsures = append(f.vxlanPeerEnsures, name+"/"+address+"/"+owner)
	if f.vxlanPeerSignal != nil {
		select {
		case f.vxlanPeerSignal <- address:
		default:
		}
	}

	return nil
}

func (f *fakeLinkOperations) DeleteVXLANInterface(name, owner string) error {
	f.vxlanDeletions = append(f.vxlanDeletions, name+"/"+owner)
	remaining := f.vxlanInterfaces[:0]
	for _, intf := range f.vxlanInterfaces {
		if intf.Owner != owner {
			remaining = append(remaining, intf)
		}
	}
	f.vxlanInterfaces = remaining

	return nil
}

func (f *fakeLinkOperations) EnsureManagementRoute(
	interfaceName,
	source,
	destination,
	gateway string,
	metric,
	table int,
	owner string,
) error {
	f.managementRoutes = append(
		f.managementRoutes,
		fmt.Sprintf(
			"%s/%s/%s/%s/%d/%d/%s",
			interfaceName,
			source,
			destination,
			gateway,
			metric,
			table,
			owner,
		),
	)

	return nil
}

func (f *fakeLinkOperations) EnsureManagementAddress(
	interfaceName,
	address,
	owner string,
) error {
	f.managementAddresses = append(
		f.managementAddresses,
		interfaceName+"/"+address+"/"+owner,
	)

	return nil
}

func TestZeroInterfaceConnectivityPublishesPlanBoundReadiness(t *testing.T) {
	t.Parallel()

	input, plan := connectivityTestInputAndPlan(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	state := t.TempDir()
	if err := clabernetesdirectruntime.RunConnectivity(ctx, input, plan, state); err != nil {
		t.Fatal(err)
	}
	if err := clabernetesdirectruntime.ConnectivityReady(plan, state); err != nil {
		t.Fatal(err)
	}
	plan.Planner.Revision = "changed"
	if err := clabernetesdirectruntime.ConnectivityReady(plan, state); err == nil {
		t.Fatal("ConnectivityReady() accepted a marker for another plan")
	}
}

func TestConnectivityAppliesPackageSysctlsDeterministicallyBeforeReadiness(t *testing.T) {
	t.Parallel()

	input, plan := connectivityTestInputAndPlan(t)
	plan.Containers[0].Security.Sysctls = []clabernetesdeviceplan.KeyValue{
		{Name: "net.ipv6.conf.all.disable_ipv6", Value: "0"},
		{Name: "net.ipv4.ip_forward", Value: "1"},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	operations := &fakeLinkOperations{}
	state := t.TempDir()
	if err := clabernetesdirectruntime.RunConnectivityWithOperations(
		ctx,
		input,
		plan,
		state,
		operations,
	); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"net.ipv4.ip_forward=1",
		"net.ipv6.conf.all.disable_ipv6=0",
	}
	if len(operations.sysctls) != len(want) {
		t.Fatalf("sysctl operations = %#v, want %#v", operations.sysctls, want)
	}
	for index := range want {
		if operations.sysctls[index] != want[index] {
			t.Fatalf("sysctl operations = %#v, want %#v", operations.sysctls, want)
		}
	}
	if err := clabernetesdirectruntime.ConnectivityReady(plan, state); err != nil {
		t.Fatal(err)
	}
}

func TestConnectivityRejectsConflictingNetworkNamespaceSysctls(t *testing.T) {
	t.Parallel()

	_, plan := connectivityTestInputAndPlan(t)
	plan.Containers[0].RuntimeID = "runtime-a"
	plan.Containers[0].Security.Sysctls = []clabernetesdeviceplan.KeyValue{
		{Name: "net.ipv4.ip_forward", Value: "1"},
	}
	second := plan.Containers[0]
	second.ID = "container-b"
	second.RuntimeID = "runtime-b"
	second.Security.Sysctls = []clabernetesdeviceplan.KeyValue{
		{Name: "net.ipv4.ip_forward", Value: "0"},
	}
	plan.Containers = append(plan.Containers, second)
	plan.Nodes[0].ContainerIDs = append(plan.Nodes[0].ContainerIDs, second.ID)
	plan.Nodes[0].ReadinessContainerIDs = append(
		plan.Nodes[0].ReadinessContainerIDs,
		second.ID,
	)
	if err := clabernetesdirectruntime.ValidatePlanCapabilities(plan); err == nil ||
		!strings.Contains(err.Error(), "conflicting network-namespace sysctl") {
		t.Fatalf("ValidatePlanCapabilities() error = %v", err)
	}
}

func TestConnectivityRejectsUnsafeNetworkNamespaceSysctlName(t *testing.T) {
	t.Parallel()

	_, plan := connectivityTestInputAndPlan(t)
	plan.Containers[0].Security.Sysctls = []clabernetesdeviceplan.KeyValue{
		{Name: "../kernel.hostname", Value: "unexpected"},
	}
	if err := clabernetesdirectruntime.ValidatePlanCapabilities(plan); err == nil ||
		!strings.Contains(err.Error(), "sysctl name is invalid") {
		t.Fatalf("ValidatePlanCapabilities() error = %v", err)
	}
}

func TestImportedApplicationRuntimeUsesItsCurrentNetworkNamespace(t *testing.T) {
	t.Parallel()

	input, plan := connectivityTestInputAndPlan(t)
	plan.Containers[0].RuntimeID = "runtime-a"
	runtime, err := clabernetesdirectruntime.NewImportedApplicationRuntime(
		input,
		plan,
		plan.Containers[0].ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	path, err := runtime.GetNSPath(context.Background(), "runtime-a")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/proc/self/ns/net" {
		t.Fatalf("GetNSPath() = %q", path)
	}
}

func TestConnectivityFailsClosedForUnimplementedInterfaces(t *testing.T) {
	t.Parallel()

	input, plan := connectivityTestInputAndPlan(t)
	input.Interfaces = []clabernetesdeviceplan.InterfaceInput{{
		ID: "interface-a", NodeID: "node-a", Name: "eth1", LinkID: "link-a",
		Connectivity: "unknown-transport", TunnelID: 1,
	}}
	inputDigest, err := input.Digest()
	if err != nil {
		t.Fatal(err)
	}
	plan.InputDigest = inputDigest
	plan.Interfaces = []clabernetesdeviceplan.InterfacePlan{{
		ID: "interface-a", NodeID: "node-a", NamespaceOwnerID: "container-a",
		Name: "eth1", LinkID: "link-a", Connectivity: "unknown-transport", TunnelID: 1,
		LinkApplyMode: clabernetesdeviceplan.LinkApplyLive,
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = clabernetesdirectruntime.RunConnectivity(ctx, input, plan, t.TempDir()); err == nil {
		t.Fatal("RunConnectivity() accepted unimplemented interface realization")
	}
}

func TestHostConnectivityReconcilesImmutableRequestBeforeReadiness(t *testing.T) {
	t.Parallel()

	input, plan := connectivityTestInputAndPlan(t)
	setHostLink(t, &input, &plan)
	reconciler := &fakeHostEndpointReconciler{
		requests: make(chan claberneteshostendpoint.ReconcileRequest, 1),
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	state := t.TempDir()
	if err := clabernetesdirectruntime.RunConnectivityWithLifecycleOperations(
		ctx,
		input,
		plan,
		clabernetesdirectruntime.ConnectivityOptions{
			StateDirectory:         state,
			PodNamespace:           "lab",
			PodName:                "router-pod",
			PodUID:                 "pod-uid-a",
			HostEndpointReconciler: reconciler,
		},
		&fakeLinkOperations{},
		nil,
	); err != nil {
		t.Fatal(err)
	}
	request := <-reconciler.requests
	if request.SchemaVersion != claberneteshostendpoint.SchemaVersion ||
		request.Pod != (claberneteshostendpoint.ObjectIdentity{
			Namespace: "lab", Name: "router-pod", UID: "pod-uid-a",
		}) || len(request.Endpoints) != 1 {
		t.Fatalf("host-endpoint request = %#v", request)
	}
	endpoint := request.Endpoints[0]
	if endpoint.Link != (claberneteshostendpoint.ObjectIdentity{
		Namespace: "lab", Name: "host-link-a", UID: "link-uid-a",
	}) || endpoint.Node != (claberneteshostendpoint.ObjectIdentity{
		Namespace: "lab", Name: "router", UID: "node-a",
	}) || endpoint.HostInterface != "c9s-host-a" || endpoint.PodInterface != "eth1" ||
		endpoint.MTU != 1450 {
		t.Fatalf("host endpoint = %#v", endpoint)
	}
	if err := clabernetesdirectruntime.ConnectivityReady(plan, state); err != nil {
		t.Fatal(err)
	}
}

func TestHostConnectivityFailurePreventsReadiness(t *testing.T) {
	t.Parallel()

	input, plan := connectivityTestInputAndPlan(t)
	setHostLink(t, &input, &plan)
	state := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := clabernetesdirectruntime.RunConnectivityWithLifecycleOperations(
		ctx,
		input,
		plan,
		clabernetesdirectruntime.ConnectivityOptions{
			StateDirectory: state,
			PodNamespace:   "lab",
			PodName:        "router-pod",
			PodUID:         "pod-uid-a",
			HostEndpointReconciler: &fakeHostEndpointReconciler{
				err: errors.New("collision with foreign host interface"),
			},
		},
		&fakeLinkOperations{},
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "collision with foreign host interface") {
		t.Fatalf("RunConnectivityWithLifecycleOperations() error = %v", err)
	}
	if err = clabernetesdirectruntime.ConnectivityReady(plan, state); err == nil {
		t.Fatal("failed host endpoint published connectivity readiness")
	}
}

func TestHostConnectivityLiveRemovalReconcilesEmptyPodSet(t *testing.T) {
	t.Parallel()

	baseInput, basePlan := connectivityTestInputAndPlan(t)
	setHostLink(t, &baseInput, &basePlan)
	desiredInput, desiredPlan := connectivityTestInputAndPlan(t)
	initialRevision, err := clabernetesdirectruntime.NewConnectivityRevision(
		baseInput,
		basePlan,
		baseInput,
		basePlan,
	)
	if err != nil {
		t.Fatal(err)
	}
	desiredRevision, err := clabernetesdirectruntime.NewConnectivityRevision(
		baseInput,
		basePlan,
		desiredInput,
		desiredPlan,
	)
	if err != nil {
		t.Fatal(err)
	}
	initialRaw, err := initialRevision.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	desiredRaw, err := desiredRevision.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	revisionPath := filepath.Join(t.TempDir(), "revision.json")
	writeConnectivityRevisionFile(t, revisionPath, initialRaw)
	reconciler := &fakeHostEndpointReconciler{
		requests: make(chan claberneteshostendpoint.ReconcileRequest, 32),
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- clabernetesdirectruntime.RunConnectivityWithLifecycleOperations(
			ctx,
			baseInput,
			basePlan,
			clabernetesdirectruntime.ConnectivityOptions{
				StateDirectory:           t.TempDir(),
				PodNamespace:             "lab",
				PodName:                  "router-pod",
				PodUID:                   "pod-uid-a",
				ConnectivityRevisionPath: revisionPath,
				RevisionPollInterval:     5 * time.Millisecond,
				HostEndpointReconciler:   reconciler,
			},
			&fakeLinkOperations{},
			nil,
		)
	}()
	t.Cleanup(cancel)
	select {
	case request := <-reconciler.requests:
		if len(request.Endpoints) != 1 {
			t.Fatalf("initial host endpoint request = %#v", request)
		}
	case runErr := <-errCh:
		t.Fatalf("connectivity helper exited before initial host Link: %v", runErr)
	case <-time.After(time.Second):
		t.Fatal("connectivity helper did not reconcile the initial host Link")
	}
	writeConnectivityRevisionFile(t, revisionPath, desiredRaw)
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		select {
		case request := <-reconciler.requests:
			if len(request.Endpoints) != 0 {
				continue
			}
			cancel()
			if runErr := <-errCh; runErr != nil {
				t.Fatal(runErr)
			}

			return
		case runErr := <-errCh:
			t.Fatalf("connectivity helper exited before removing host Link: %v", runErr)
		case <-deadline.C:
			t.Fatal("connectivity helper did not reconcile an empty host endpoint set")
		}
	}
}

func TestConnectivityHelperRestartRecoversEveryLinkFlavor(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		flavor    string
		configure func(*testing.T, *clabernetesdeviceplan.Input, *clabernetesdeviceplan.Plan)
	}{
		{
			name: "loopback", flavor: "local",
			configure: func(
				t *testing.T,
				input *clabernetesdeviceplan.Input,
				plan *clabernetesdeviceplan.Plan,
			) {
				setLoopbackLink(t, input, plan, 1500)
			},
		},
		{
			name: "same-pod", flavor: "local",
			configure: func(
				t *testing.T,
				input *clabernetesdeviceplan.Input,
				plan *clabernetesdeviceplan.Plan,
			) {
				setSamePodLink(t, input, plan, "node-b-uid")
			},
		},
		{
			name: "vxlan", flavor: "vxlan",
			configure: func(
				t *testing.T,
				input *clabernetesdeviceplan.Input,
				plan *clabernetesdeviceplan.Plan,
			) {
				setVXLANLink(t, input, plan, "peer-node-uid", "peer-vx", 73, 1450)
			},
		},
		{
			name: "slurpeeth", flavor: "slurpeeth",
			configure: func(
				t *testing.T,
				input *clabernetesdeviceplan.Input,
				plan *clabernetesdeviceplan.Plan,
			) {
				setSlurpeethLink(t, input, plan, "peer-node-uid", "peer-vx", 73, 1450)
			},
		},
		{name: "host", flavor: "host", configure: setHostLink},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			input, plan := connectivityTestInputAndPlan(t)
			testCase.configure(t, &input, &plan)
			state := t.TempDir()
			operations := &fakeLinkOperations{
				vxlanPeerAddresses: map[string]string{"peer-vx": "10.244.2.17"},
			}
			hostReconciler := &fakeHostEndpointReconciler{
				requests: make(chan claberneteshostendpoint.ReconcileRequest, 4),
			}
			run := func() error {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				options := clabernetesdirectruntime.ConnectivityOptions{
					StateDirectory: state,
					PodNamespace:   "lab",
					PodName:        "router-pod",
					PodUID:         "pod-uid-a",
				}
				switch testCase.flavor {
				case "host":
					options.HostEndpointReconciler = hostReconciler

					return clabernetesdirectruntime.RunConnectivityWithLifecycleOperations(
						ctx, input, plan, options, operations, nil,
					)
				case "slurpeeth":
					return clabernetesdirectruntime.RunConnectivityWithRuntimeOperations(
						ctx,
						input,
						plan,
						options,
						operations,
						nil,
						&fakeSlurpeethRuntime{},
					)
				default:
					return clabernetesdirectruntime.RunConnectivityWithLifecycleOperations(
						ctx, input, plan, options, operations, nil,
					)
				}
			}

			switch testCase.flavor {
			case "local":
				operations.ensurePairError = errors.New("injected local Link failure")
			case "vxlan", "slurpeeth":
				operations.resolvePeerError = errors.New("injected peer resolution failure")
			case "host":
				hostReconciler.err = errors.New("injected host endpoint failure")
			}
			if err := run(); err == nil {
				t.Fatal("partial failure was accepted")
			}
			if err := clabernetesdirectruntime.ConnectivityReady(plan, state); err == nil {
				t.Fatal("partial failure published connectivity readiness")
			}
			operations.ensurePairError = nil
			operations.resolvePeerError = nil
			hostReconciler.err = nil

			for restart := 0; restart < 2; restart++ {
				if err := run(); err != nil {
					t.Fatalf("helper run %d: %v", restart+1, err)
				}
			}
			if err := clabernetesdirectruntime.ConnectivityReady(plan, state); err != nil {
				t.Fatal(err)
			}

			switch testCase.flavor {
			case "local", "slurpeeth":
				if len(operations.interfaces) != 2 || len(operations.deletions) != 0 ||
					len(operations.pairs) < 2 {
					t.Fatalf(
						"recovered veth state = interfaces %#v, deletions %#v, ensures %#v",
						operations.interfaces,
						operations.deletions,
						operations.pairs,
					)
				}
				latest := operations.pairs[len(operations.pairs)-2:]
				if strings.SplitN(latest[0], "/", 4)[3] !=
					strings.SplitN(latest[1], "/", 4)[3] {
					t.Fatalf("helper restart changed veth ownership: %#v", latest)
				}
			case "vxlan":
				if len(operations.vxlanInterfaces) != 1 || len(operations.vxlanDeletions) != 0 ||
					len(operations.vxlanPeerEnsures) != 2 {
					t.Fatalf(
						"recovered VXLAN state = interfaces %#v, deletions %#v, peers %#v",
						operations.vxlanInterfaces,
						operations.vxlanDeletions,
						operations.vxlanPeerEnsures,
					)
				}
				if strings.SplitN(operations.vxlanPeerEnsures[0], "/", 3)[2] !=
					strings.SplitN(operations.vxlanPeerEnsures[1], "/", 3)[2] {
					t.Fatalf(
						"helper restart changed VXLAN ownership: %#v",
						operations.vxlanPeerEnsures,
					)
				}
			case "host":
				requests := []claberneteshostendpoint.ReconcileRequest{}
				for len(hostReconciler.requests) != 0 {
					requests = append(requests, <-hostReconciler.requests)
				}
				if len(requests) != 3 || !reflect.DeepEqual(requests[1], requests[2]) {
					t.Fatalf("helper restart host requests = %#v", requests)
				}
			}
		})
	}
}

func TestVXLANConnectivityUsesAllocatedTunnelAndCurrentPeerAddress(t *testing.T) {
	t.Parallel()

	input, plan := connectivityTestInputAndPlan(t)
	setVXLANLink(t, &input, &plan, "peer-node-uid", "peer-vx", 73, 1450)
	operations := &fakeLinkOperations{vxlanPeerAddresses: map[string]string{
		"peer-vx": "10.244.2.17",
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	state := t.TempDir()
	if err := clabernetesdirectruntime.RunConnectivityWithLifecycleOperations(
		ctx,
		input,
		plan,
		clabernetesdirectruntime.ConnectivityOptions{
			StateDirectory: state,
			PodUID:         "pod-uid-a",
		},
		operations,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	if len(operations.vxlanEnsures) != 1 ||
		!strings.HasPrefix(operations.vxlanEnsures[0], "package-a/73/1450/14789/") ||
		len(operations.vxlanPeerEnsures) != 1 ||
		!strings.HasPrefix(
			operations.vxlanPeerEnsures[0],
			"package-a/10.244.2.17/c9s:direct:v1:",
		) {
		t.Fatalf(
			"VXLAN operations = ensure %#v, peer %#v",
			operations.vxlanEnsures,
			operations.vxlanPeerEnsures,
		)
	}
	if err := clabernetesdirectruntime.ConnectivityReady(plan, state); err != nil {
		t.Fatal(err)
	}
}

func TestVXLANConnectivityPreparesInterfaceButStaysUnreadyWithoutPeer(t *testing.T) {
	t.Parallel()

	input, plan := connectivityTestInputAndPlan(t)
	setVXLANLink(t, &input, &plan, "peer-node-uid", "peer-vx", 73, 1450)
	operations := &fakeLinkOperations{
		resolvePeerError: clabernetesdirectruntime.ErrPeerTransportUnavailable,
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	state := t.TempDir()
	err := clabernetesdirectruntime.RunConnectivityWithLifecycleOperations(
		ctx,
		input,
		plan,
		clabernetesdirectruntime.ConnectivityOptions{
			StateDirectory: state,
			PodUID:         "pod-uid-a",
		},
		operations,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(operations.vxlanEnsures) != 1 || len(operations.vxlanPeerEnsures) != 0 {
		t.Fatalf(
			"unresolved VXLAN operations = ensure %#v, peer %#v",
			operations.vxlanEnsures,
			operations.vxlanPeerEnsures,
		)
	}
	if err = clabernetesdirectruntime.ConnectivityReady(plan, state); err == nil {
		t.Fatal("unresolved VXLAN peer published connectivity readiness")
	}
}

func TestVXLANConnectivityReconcilesPodRescheduleRewireAndCleanup(t *testing.T) {
	t.Parallel()

	input, plan := connectivityTestInputAndPlan(t)
	setVXLANLink(t, &input, &plan, "peer-node-uid", "peer-vx", 73, 1450)
	operations := &fakeLinkOperations{vxlanPeerAddresses: map[string]string{
		"peer-vx": "10.244.2.17",
	}}
	run := func(currentInput clabernetesdeviceplan.Input, currentPlan clabernetesdeviceplan.Plan) {
		t.Helper()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := clabernetesdirectruntime.RunConnectivityWithLifecycleOperations(
			ctx,
			currentInput,
			currentPlan,
			clabernetesdirectruntime.ConnectivityOptions{
				StateDirectory: t.TempDir(),
				PodUID:         "pod-uid-a",
			},
			operations,
			nil,
		); err != nil {
			t.Fatal(err)
		}
	}
	run(input, plan)
	firstOwner := operations.vxlanInterfaces[0].Owner
	operations.vxlanPeerAddresses["peer-vx"] = "10.244.7.31"
	run(input, plan)
	if len(operations.vxlanDeletions) != 0 ||
		!strings.Contains(operations.vxlanPeerEnsures[1], "/10.244.7.31/") {
		t.Fatalf(
			"Pod reschedule operations = deletes %#v, peers %#v",
			operations.vxlanDeletions,
			operations.vxlanPeerEnsures,
		)
	}

	rewiredInput, rewiredPlan := connectivityTestInputAndPlan(t)
	setVXLANLink(t, &rewiredInput, &rewiredPlan, "replacement-peer-uid", "replacement-vx", 81, 1400)
	operations.vxlanPeerAddresses["replacement-vx"] = "10.244.9.44"
	run(rewiredInput, rewiredPlan)
	if len(operations.vxlanDeletions) != 1 ||
		!strings.HasSuffix(operations.vxlanDeletions[0], "/"+firstOwner) ||
		operations.vxlanInterfaces[0].Owner == firstOwner {
		t.Fatalf(
			"VXLAN rewire did not replace exact UID ownership: deletes %#v, state %#v",
			operations.vxlanDeletions,
			operations.vxlanInterfaces,
		)
	}

	emptyInput, emptyPlan := connectivityTestInputAndPlan(t)
	run(emptyInput, emptyPlan)
	if len(operations.vxlanDeletions) != 2 || len(operations.vxlanInterfaces) != 0 {
		t.Fatalf(
			"VXLAN cleanup = deletes %#v, state %#v",
			operations.vxlanDeletions,
			operations.vxlanInterfaces,
		)
	}
}

func TestVXLANConnectivityTracksHeadlessPeerAddressWithoutPlanChange(t *testing.T) {
	t.Parallel()

	input, plan := connectivityTestInputAndPlan(t)
	setVXLANLink(t, &input, &plan, "peer-node-uid", "peer-vx", 73, 1450)
	revision, err := clabernetesdirectruntime.NewConnectivityRevision(input, plan, input, plan)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := revision.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	revisionPath := filepath.Join(t.TempDir(), "revision.json")
	writeConnectivityRevisionFile(t, revisionPath, raw)
	var currentAddress atomic.Value
	currentAddress.Store("10.244.2.17")
	operations := &fakeLinkOperations{
		resolvePeer: func(string) (string, error) {
			return currentAddress.Load().(string), nil
		},
		vxlanPeerSignal: make(chan string, 8),
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- clabernetesdirectruntime.RunConnectivityWithLifecycleOperations(
			ctx,
			input,
			plan,
			clabernetesdirectruntime.ConnectivityOptions{
				StateDirectory:           t.TempDir(),
				PodUID:                   "pod-uid-a",
				ConnectivityRevisionPath: revisionPath,
				RevisionPollInterval:     5 * time.Millisecond,
			},
			operations,
			nil,
		)
	}()
	t.Cleanup(cancel)
	select {
	case address := <-operations.vxlanPeerSignal:
		if address != "10.244.2.17" {
			t.Fatalf("initial VXLAN peer = %q", address)
		}
	case runErr := <-errCh:
		t.Fatalf("connectivity helper exited before initial peer: %v", runErr)
	case <-time.After(time.Second):
		t.Fatal("connectivity helper did not resolve initial peer")
	}
	currentAddress.Store("10.244.7.31")
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		select {
		case address := <-operations.vxlanPeerSignal:
			if address != "10.244.7.31" {
				continue
			}
			cancel()
			if runErr := <-errCh; runErr != nil {
				t.Fatal(runErr)
			}

			return
		case runErr := <-errCh:
			t.Fatalf("connectivity helper exited before peer reschedule: %v", runErr)
		case <-deadline.C:
			t.Fatal("connectivity helper did not reconcile rescheduled peer")
		}
	}
}

func TestConnectivityHelperAppliesProjectedVXLANRewireWithoutRestart(t *testing.T) {
	t.Parallel()

	baseInput, basePlan := connectivityTestInputAndPlan(t)
	setVXLANLink(t, &baseInput, &basePlan, "peer-node-uid", "peer-vx", 73, 1450)
	desiredInput, desiredPlan := connectivityTestInputAndPlan(t)
	setVXLANLink(
		t,
		&desiredInput,
		&desiredPlan,
		"replacement-peer-uid",
		"replacement-vx",
		81,
		1400,
	)
	initialRevision, err := clabernetesdirectruntime.NewConnectivityRevision(
		baseInput,
		basePlan,
		baseInput,
		basePlan,
	)
	if err != nil {
		t.Fatal(err)
	}
	desiredRevision, err := clabernetesdirectruntime.NewConnectivityRevision(
		baseInput,
		basePlan,
		desiredInput,
		desiredPlan,
	)
	if err != nil {
		t.Fatal(err)
	}
	initialRaw, err := initialRevision.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	desiredRaw, err := desiredRevision.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	revisionPath := filepath.Join(t.TempDir(), "revision.json")
	writeConnectivityRevisionFile(t, revisionPath, initialRaw)
	state := t.TempDir()
	operations := &fakeLinkOperations{
		vxlanPeerAddresses: map[string]string{
			"peer-vx":        "10.244.2.17",
			"replacement-vx": "10.244.9.44",
		},
		vxlanPeerSignal: make(chan string, 8),
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- clabernetesdirectruntime.RunConnectivityWithLifecycleOperations(
			ctx,
			baseInput,
			basePlan,
			clabernetesdirectruntime.ConnectivityOptions{
				StateDirectory:           state,
				PodUID:                   "pod-uid-a",
				ConnectivityRevisionPath: revisionPath,
				RevisionPollInterval:     5 * time.Millisecond,
			},
			operations,
			nil,
		)
	}()
	t.Cleanup(cancel)
	select {
	case address := <-operations.vxlanPeerSignal:
		if address != "10.244.2.17" {
			t.Fatalf("initial VXLAN peer = %q", address)
		}
	case runErr := <-errCh:
		t.Fatalf("connectivity helper exited before initial VXLAN readiness: %v", runErr)
	case <-time.After(time.Second):
		t.Fatal("connectivity helper did not realize initial VXLAN peer")
	}
	writeConnectivityRevisionFile(t, revisionPath, desiredRaw)
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		select {
		case address := <-operations.vxlanPeerSignal:
			if address != "10.244.9.44" {
				continue
			}
			for clabernetesdirectruntime.ConnectivityReadyWithRevision(
				basePlan,
				state,
				revisionPath,
			) != nil {
				select {
				case runErr := <-errCh:
					t.Fatalf("connectivity helper exited before revision readiness: %v", runErr)
				case <-deadline.C:
					t.Fatal("connectivity helper did not publish VXLAN revision readiness")
				case <-time.After(5 * time.Millisecond):
				}
			}
			cancel()
			if runErr := <-errCh; runErr != nil {
				t.Fatal(runErr)
			}
			if len(operations.vxlanDeletions) != 1 ||
				len(operations.vxlanInterfaces) != 1 ||
				operations.vxlanInterfaces[0].TunnelID != 81 {
				t.Fatalf(
					"projected VXLAN rewire = deletes %#v, state %#v",
					operations.vxlanDeletions,
					operations.vxlanInterfaces,
				)
			}

			return
		case runErr := <-errCh:
			t.Fatalf("connectivity helper exited before projected VXLAN rewire: %v", runErr)
		case <-deadline.C:
			t.Fatal("connectivity helper did not apply projected VXLAN rewire")
		}
	}
}

func TestSlurpeethConnectivityUsesOwnedVethAndCurrentPeerAddress(t *testing.T) {
	t.Parallel()

	input, plan := connectivityTestInputAndPlan(t)
	setSlurpeethLink(t, &input, &plan, "peer-node-uid", "peer-vx", 73, 1450)
	operations := &fakeLinkOperations{vxlanPeerAddresses: map[string]string{
		"peer-vx": "10.244.2.17",
	}}
	runtime := &fakeSlurpeethRuntime{
		reconciles: make(chan []clabernetesdirectruntime.SlurpeethSegment, 1),
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	state := t.TempDir()
	if err := clabernetesdirectruntime.RunConnectivityWithRuntimeOperations(
		ctx,
		input,
		plan,
		clabernetesdirectruntime.ConnectivityOptions{
			StateDirectory: state,
			PodUID:         "pod-uid-a",
		},
		operations,
		nil,
		runtime,
	); err != nil {
		t.Fatal(err)
	}
	if len(operations.pairs) != 1 ||
		!strings.HasPrefix(operations.pairs[0], "package-a/c9ss") ||
		!strings.Contains(operations.pairs[0], "/1450/c9s:direct:v1:") ||
		!strings.Contains(operations.pairs[0], ":slurpeeth:") {
		t.Fatalf("slurpeeth veth operations = %#v", operations.pairs)
	}
	segments := <-runtime.reconciles
	if len(segments) != 1 || segments[0].ID != 73 ||
		!strings.HasPrefix(segments[0].Interface, "c9ss") ||
		segments[0].Destination != "10.244.2.17" ||
		!strings.Contains(segments[0].Owner, ":slurpeeth:") {
		t.Fatalf("slurpeeth segments = %#v", segments)
	}
	if runtime.closed.Load() != 1 {
		t.Fatalf("slurpeeth runtime close count = %d", runtime.closed.Load())
	}
	if err := clabernetesdirectruntime.ConnectivityReady(plan, state); err != nil {
		t.Fatal(err)
	}
}

func TestSlurpeethPendingPeerKeepsHelperAliveAndTogglesReadiness(t *testing.T) {
	t.Parallel()

	input, plan := connectivityTestInputAndPlan(t)
	setSlurpeethLink(t, &input, &plan, "peer-node-uid", "peer-vx", 73, 1450)
	operations := &fakeLinkOperations{vxlanPeerAddresses: map[string]string{
		"peer-vx": "10.244.2.17",
	}}
	var peerReady atomic.Bool
	runtime := &fakeSlurpeethRuntime{
		reconciles: make(chan []clabernetesdirectruntime.SlurpeethSegment, 1024),
		errors:     make(chan error, 1),
		ready: func() (bool, error) {
			return peerReady.Load(), nil
		},
	}
	state := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- clabernetesdirectruntime.RunConnectivityWithRuntimeOperations(
			ctx,
			input,
			plan,
			clabernetesdirectruntime.ConnectivityOptions{
				StateDirectory: state, PodUID: "pod-uid-a", RevisionPollInterval: 5 * time.Millisecond,
			},
			operations,
			nil,
			runtime,
		)
	}()
	t.Cleanup(cancel)
	select {
	case <-runtime.reconciles:
	case runErr := <-errCh:
		t.Fatalf("connectivity helper exited while slurpeeth peer was pending: %v", runErr)
	case <-time.After(time.Second):
		t.Fatal("connectivity helper did not configure pending slurpeeth peer")
	}
	if err := clabernetesdirectruntime.ConnectivityReady(plan, state); err == nil {
		t.Fatal("pending slurpeeth peer published connectivity readiness")
	}
	peerReady.Store(true)
	deadline := time.Now().Add(time.Second)
	for clabernetesdirectruntime.ConnectivityReady(plan, state) != nil {
		select {
		case runErr := <-errCh:
			t.Fatalf("connectivity helper exited before slurpeeth peer became ready: %v", runErr)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("connected slurpeeth peer did not publish connectivity readiness")
		}
		time.Sleep(time.Millisecond)
	}
	peerReady.Store(false)
	deadline = time.Now().Add(time.Second)
	for clabernetesdirectruntime.ConnectivityReady(plan, state) == nil {
		select {
		case runErr := <-errCh:
			t.Fatalf("connectivity helper exited after slurpeeth peer loss: %v", runErr)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("lost slurpeeth peer retained connectivity readiness")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if runErr := <-errCh; runErr != nil {
		t.Fatal(runErr)
	}
}

func TestSlurpeethConnectivityPreparesInterfaceButStaysUnreadyWithoutPeer(t *testing.T) {
	t.Parallel()

	input, plan := connectivityTestInputAndPlan(t)
	setSlurpeethLink(t, &input, &plan, "peer-node-uid", "peer-vx", 73, 1450)
	operations := &fakeLinkOperations{
		resolvePeerError: clabernetesdirectruntime.ErrPeerTransportUnavailable,
	}
	runtime := &fakeSlurpeethRuntime{
		reconciles: make(chan []clabernetesdirectruntime.SlurpeethSegment, 1),
	}
	state := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := clabernetesdirectruntime.RunConnectivityWithRuntimeOperations(
		ctx,
		input,
		plan,
		clabernetesdirectruntime.ConnectivityOptions{
			StateDirectory: state,
			PodUID:         "pod-uid-a",
		},
		operations,
		nil,
		runtime,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(operations.pairs) != 1 {
		t.Fatalf("unresolved slurpeeth veth operations = %#v", operations.pairs)
	}
	select {
	case segments := <-runtime.reconciles:
		if len(segments) != 0 {
			t.Fatalf("unresolved slurpeeth peer started process with %#v", segments)
		}
	default:
	}
	if err = clabernetesdirectruntime.ConnectivityReady(plan, state); err == nil {
		t.Fatal("unresolved slurpeeth peer published connectivity readiness")
	}
}

func TestSlurpeethConnectivityTracksPeerAddressWithoutPlanChange(t *testing.T) {
	t.Parallel()

	input, plan := connectivityTestInputAndPlan(t)
	setSlurpeethLink(t, &input, &plan, "peer-node-uid", "peer-vx", 73, 1450)
	revision, err := clabernetesdirectruntime.NewConnectivityRevision(input, plan, input, plan)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := revision.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	desiredInput, desiredPlan := connectivityTestInputAndPlan(t)
	setSlurpeethLink(
		t,
		&desiredInput,
		&desiredPlan,
		"replacement-peer-uid",
		"replacement-vx",
		81,
		1400,
	)
	desiredRevision, err := clabernetesdirectruntime.NewConnectivityRevision(
		input,
		plan,
		desiredInput,
		desiredPlan,
	)
	if err != nil {
		t.Fatal(err)
	}
	desiredRaw, err := desiredRevision.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	revisionPath := filepath.Join(t.TempDir(), "revision.json")
	writeConnectivityRevisionFile(t, revisionPath, raw)
	var currentAddress atomic.Value
	currentAddress.Store("10.244.2.17")
	operations := &fakeLinkOperations{resolvePeer: func(destination string) (string, error) {
		if destination == "replacement-vx" {
			return "10.244.9.44", nil
		}

		return currentAddress.Load().(string), nil
	}}
	runtime := &fakeSlurpeethRuntime{
		reconciles: make(chan []clabernetesdirectruntime.SlurpeethSegment, 8),
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- clabernetesdirectruntime.RunConnectivityWithRuntimeOperations(
			ctx,
			input,
			plan,
			clabernetesdirectruntime.ConnectivityOptions{
				StateDirectory:           t.TempDir(),
				PodUID:                   "pod-uid-a",
				ConnectivityRevisionPath: revisionPath,
				RevisionPollInterval:     5 * time.Millisecond,
			},
			operations,
			nil,
			runtime,
		)
	}()
	t.Cleanup(cancel)
	select {
	case segments := <-runtime.reconciles:
		if len(segments) != 1 || segments[0].Destination != "10.244.2.17" {
			t.Fatalf("initial slurpeeth segments = %#v", segments)
		}
		firstOwner := segments[0].Owner
		currentAddress.Store("10.244.7.31")
		deadline := time.NewTimer(time.Second)
		defer deadline.Stop()
		for {
			select {
			case current := <-runtime.reconciles:
				if len(current) != 1 || current[0].Destination != "10.244.7.31" {
					continue
				}

				writeConnectivityRevisionFile(t, revisionPath, desiredRaw)
				for {
					select {
					case rewired := <-runtime.reconciles:
						if len(rewired) != 1 || rewired[0].ID != 81 ||
							rewired[0].Destination != "10.244.9.44" {
							continue
						}
						if rewired[0].Owner == firstOwner || len(operations.deletions) != 1 {
							t.Fatalf(
								"projected slurpeeth rewire = segments %#v deletions %#v",
								rewired,
								operations.deletions,
							)
						}
						cancel()
						if runErr := <-errCh; runErr != nil {
							t.Fatal(runErr)
						}

						return
					case runErr := <-errCh:
						t.Fatalf("connectivity helper exited before projected rewire: %v", runErr)
					case <-deadline.C:
						t.Fatal("connectivity helper did not apply projected slurpeeth rewire")
					}
				}
			case runErr := <-errCh:
				t.Fatalf("connectivity helper exited before peer reschedule: %v", runErr)
			case <-deadline.C:
				t.Fatal("connectivity helper did not reconcile rescheduled slurpeeth peer")
			}
		}
	case runErr := <-errCh:
		t.Fatalf("connectivity helper exited before initial peer: %v", runErr)
	case <-time.After(time.Second):
		t.Fatal("connectivity helper did not configure initial slurpeeth peer")
	}
}

func TestSlurpeethConnectivityReconcilesUIDRewireAndCleanup(t *testing.T) {
	t.Parallel()

	operations := &fakeLinkOperations{vxlanPeerAddresses: map[string]string{
		"peer-vx":        "10.244.2.17",
		"replacement-vx": "10.244.9.44",
	}}
	runtime := &fakeSlurpeethRuntime{
		reconciles: make(chan []clabernetesdirectruntime.SlurpeethSegment, 3),
	}
	run := func(input clabernetesdeviceplan.Input, plan clabernetesdeviceplan.Plan) {
		t.Helper()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := clabernetesdirectruntime.RunConnectivityWithRuntimeOperations(
			ctx,
			input,
			plan,
			clabernetesdirectruntime.ConnectivityOptions{
				StateDirectory: t.TempDir(), PodUID: "pod-uid-a",
			},
			operations,
			nil,
			runtime,
		); err != nil {
			t.Fatal(err)
		}
	}
	input, plan := connectivityTestInputAndPlan(t)
	setSlurpeethLink(t, &input, &plan, "peer-node-uid", "peer-vx", 73, 1450)
	run(input, plan)
	first := <-runtime.reconciles
	firstOwner := first[0].Owner

	rewiredInput, rewiredPlan := connectivityTestInputAndPlan(t)
	setSlurpeethLink(
		t,
		&rewiredInput,
		&rewiredPlan,
		"replacement-peer-uid",
		"replacement-vx",
		81,
		1400,
	)
	run(rewiredInput, rewiredPlan)
	stopped := <-runtime.reconciles
	rewired := <-runtime.reconciles
	if len(operations.deletions) != 1 ||
		!strings.HasSuffix(operations.deletions[0], "/"+firstOwner) ||
		len(stopped) != 0 ||
		len(rewired) != 1 || rewired[0].Owner == firstOwner || rewired[0].ID != 81 {
		t.Fatalf(
			"slurpeeth rewire = deletes %#v stop %#v segments %#v",
			operations.deletions,
			stopped,
			rewired,
		)
	}

	emptyInput, emptyPlan := connectivityTestInputAndPlan(t)
	run(emptyInput, emptyPlan)
	cleaned := <-runtime.reconciles
	if len(operations.deletions) != 2 || len(operations.interfaces) != 0 || len(cleaned) != 0 {
		t.Fatalf(
			"slurpeeth cleanup = deletes %#v interfaces %#v segments %#v",
			operations.deletions,
			operations.interfaces,
			cleaned,
		)
	}
}

func TestSlurpeethProcessFailureClearsConnectivityReadiness(t *testing.T) {
	t.Parallel()

	input, plan := connectivityTestInputAndPlan(t)
	setSlurpeethLink(t, &input, &plan, "peer-node-uid", "peer-vx", 73, 1450)
	operations := &fakeLinkOperations{vxlanPeerAddresses: map[string]string{
		"peer-vx": "10.244.2.17",
	}}
	runtime := &fakeSlurpeethRuntime{
		reconciles: make(chan []clabernetesdirectruntime.SlurpeethSegment, 1),
		errors:     make(chan error, 1),
	}
	state := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- clabernetesdirectruntime.RunConnectivityWithRuntimeOperations(
			ctx,
			input,
			plan,
			clabernetesdirectruntime.ConnectivityOptions{
				StateDirectory: state, PodUID: "pod-uid-a",
			},
			operations,
			nil,
			runtime,
		)
	}()
	select {
	case <-runtime.reconciles:
	case runErr := <-errCh:
		t.Fatalf("connectivity helper exited before slurpeeth readiness: %v", runErr)
	case <-time.After(time.Second):
		t.Fatal("connectivity helper did not reconcile slurpeeth")
	}
	deadline := time.Now().Add(time.Second)
	for clabernetesdirectruntime.ConnectivityReady(plan, state) != nil {
		if time.Now().After(deadline) {
			t.Fatal("connectivity helper did not publish readiness")
		}
		time.Sleep(time.Millisecond)
	}
	processErr := errors.New("slurpeeth child exited")
	runtime.errors <- processErr
	if runErr := <-errCh; !errors.Is(runErr, processErr) {
		t.Fatalf("connectivity helper error = %v", runErr)
	}
	if err := clabernetesdirectruntime.ConnectivityReady(plan, state); err == nil {
		t.Fatal("failed slurpeeth process retained connectivity readiness")
	}
}

func TestDirectRuntimeRequiresImmutableApplicationImages(t *testing.T) {
	t.Parallel()

	_, plan := connectivityTestInputAndPlan(t)
	plan.Containers[0].ImageDigest = ""
	if err := clabernetesdirectruntime.ValidatePlanCapabilities(plan); err == nil ||
		!strings.Contains(err.Error(), "immutable image digest") {
		t.Fatalf("ValidatePlanCapabilities() error = %v", err)
	}
}

func TestImportedEndpointLifecycleRejectsNonDistinctHostNamespace(t *testing.T) {
	t.Parallel()

	input, plan := connectivityTestInputAndPlan(t)
	plan.Actions = []clabernetesdeviceplan.Action{{
		ID:    "imported-deploy-endpoints/node-a",
		Phase: clabernetesdeviceplan.PhaseInterfaceFixup,
		Target: clabernetesdeviceplan.ActionTarget{
			NodeID: "node-a", ContainerID: "container-a", NamespaceOwnerID: "container-a",
		},
		Kind:                    clabernetesdeviceplan.ActionImportedDeployEndpoints,
		ImportedDeployEndpoints: &clabernetesdeviceplan.ImportedDeployEndpointsAction{},
	}}
	err := clabernetesdirectruntime.RunConnectivityWithOptions(
		context.Background(),
		input,
		plan,
		clabernetesdirectruntime.ConnectivityOptions{
			StateDirectory: t.TempDir(), ArtifactRoot: t.TempDir(), Revision: "test",
			HostNetworkNamespacePath: "/proc/self/ns/net",
		},
	)
	var capabilityErr *clabernetesdeviceplan.Error
	if !errors.As(err, &capabilityErr) ||
		capabilityErr.Code != clabernetesdeviceplan.ErrorUnsupported ||
		capabilityErr.Field != "runtime.networkNamespace" ||
		capabilityErr.Behavior != "host-network-namespace" {
		t.Fatalf("RunConnectivityWithOptions() capability error = %#v, %v", capabilityErr, err)
	}
}

func TestManagementConnectivityAddsPackageSelectedAddressesBeforeReadiness(t *testing.T) {
	t.Parallel()

	input, plan := connectivityTestInputAndPlan(t)
	input.Management = []clabernetesdeviceplan.ManagementInput{{
		NodeID: "node-a", IPv4: "192.0.2.10/24", IPv6: "2001:db8::10/64",
	}}
	digest, err := input.Digest()
	if err != nil {
		t.Fatal(err)
	}
	plan.InputDigest = digest
	plan.Management = []clabernetesdeviceplan.ManagementPlan{{
		ID: "management/node-a", NodeID: "node-a",
		InterfaceSelector: clabernetesdeviceplan.ManagementInterfacePodTransport,
		IPv4:              "192.0.2.10/24", IPv6: "2001:db8::10/64",
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	operations := &fakeLinkOperations{podTransport: "resolved-pod-interface"}
	state := t.TempDir()
	if err = clabernetesdirectruntime.RunConnectivityWithLifecycleOperations(
		ctx,
		input,
		plan,
		clabernetesdirectruntime.ConnectivityOptions{
			StateDirectory: state,
			PodAddress:     "10.244.0.12",
		},
		operations,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	if len(operations.managementAddresses) != 2 {
		t.Fatalf("management address operations = %#v", operations.managementAddresses)
	}
	for _, operation := range operations.managementAddresses {
		if !strings.HasPrefix(operation, "resolved-pod-interface/") ||
			!strings.Contains(operation, "/c9s:sha256:") {
			t.Fatalf("management address operation = %q", operation)
		}
	}
	if err = clabernetesdirectruntime.ConnectivityReady(plan, state); err != nil {
		t.Fatal(err)
	}
}

func TestManagementConnectivityRejectsPodTransportPrefixOverlapBeforeAddressMutation(t *testing.T) {
	t.Parallel()

	input, plan := connectivityTestInputAndPlan(t)
	input.Management = []clabernetesdeviceplan.ManagementInput{{
		NodeID: "node-a", IPv4: "10.244.0.10/16",
	}}
	digest, err := input.Digest()
	if err != nil {
		t.Fatal(err)
	}
	plan.InputDigest = digest
	plan.Management = []clabernetesdeviceplan.ManagementPlan{{
		ID: "management/node-a", NodeID: "node-a",
		InterfaceSelector: clabernetesdeviceplan.ManagementInterfacePodTransport,
		IPv4:              "10.244.0.10/16",
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	operations := &fakeLinkOperations{podTransport: "resolved-pod-interface"}
	err = clabernetesdirectruntime.RunConnectivityWithLifecycleOperations(
		ctx,
		input,
		plan,
		clabernetesdirectruntime.ConnectivityOptions{
			StateDirectory: t.TempDir(), PodAddress: "10.244.2.180",
		},
		operations,
		nil,
	)
	var capabilityErr *clabernetesdeviceplan.Error
	if !errors.As(err, &capabilityErr) ||
		capabilityErr.Code != clabernetesdeviceplan.ErrorUnsupported ||
		capabilityErr.Field != "management[0].ipv4" ||
		capabilityErr.Behavior != "management-preflight" {
		t.Fatalf("management overlap error = %#v", err)
	}
	if len(operations.managementAddresses) != 0 || len(operations.managementRoutes) != 0 {
		t.Fatalf(
			"management overlap mutated networking: addresses=%#v routes=%#v",
			operations.managementAddresses,
			operations.managementRoutes,
		)
	}
}

func TestManagementConnectivityUsesSourceSpecificGatewayAndRoutes(t *testing.T) {
	t.Parallel()

	input, plan := connectivityTestInputAndPlan(t)
	input.Management = []clabernetesdeviceplan.ManagementInput{{
		NodeID: "node-a", IPv4: "192.0.2.10/24", IPv4Gateway: "192.0.2.1",
	}}
	digest, err := input.Digest()
	if err != nil {
		t.Fatal(err)
	}
	plan.InputDigest = digest
	plan.Management = []clabernetesdeviceplan.ManagementPlan{{
		ID: "management/node-a", NodeID: "node-a", InterfaceName: "eth0",
		IPv4: "192.0.2.10/24", IPv4Gateway: "192.0.2.1",
		Routes: []clabernetesdeviceplan.Route{{
			Destination: "198.51.100.0/24", Gateway: "192.0.2.1", Metric: 7,
		}},
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	operations := &fakeLinkOperations{}
	if err = clabernetesdirectruntime.RunConnectivityWithOperations(
		ctx,
		input,
		plan,
		t.TempDir(),
		operations,
	); err != nil {
		t.Fatal(err)
	}
	if len(operations.managementRoutes) != 2 {
		t.Fatalf("management route operations = %#v", operations.managementRoutes)
	}
	if !strings.Contains(operations.managementRoutes[0], "/0.0.0.0/0/192.0.2.1/0/10000/") ||
		!strings.Contains(operations.managementRoutes[1], "/198.51.100.0/24/192.0.2.1/7/10000/") {
		t.Fatalf("source-specific route operations = %#v", operations.managementRoutes)
	}
}

func TestManagementConnectivityRejectsPlanThatDiffersFromAcceptedInput(t *testing.T) {
	t.Parallel()

	input, plan := connectivityTestInputAndPlan(t)
	input.Management = []clabernetesdeviceplan.ManagementInput{{
		NodeID: "node-a", IPv4: "192.0.2.10/24",
	}}
	digest, err := input.Digest()
	if err != nil {
		t.Fatal(err)
	}
	plan.InputDigest = digest
	plan.Management = []clabernetesdeviceplan.ManagementPlan{{
		ID: "management/node-a", NodeID: "node-a", InterfaceName: "eth0",
		IPv4: "192.0.2.11/24",
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = clabernetesdirectruntime.RunConnectivityWithOperations(
		ctx,
		input,
		plan,
		t.TempDir(),
		&fakeLinkOperations{},
	)
	if err == nil || !strings.Contains(err.Error(), "differs from accepted input") {
		t.Fatalf("RunConnectivityWithOperations() error = %v", err)
	}
}

func TestLocalConnectivityCreatesPackageNamedVethPairBeforeReadiness(t *testing.T) {
	t.Parallel()

	input, plan := connectivityTestInputAndPlan(t)
	input.Interfaces = []clabernetesdeviceplan.InterfaceInput{
		{
			ID:            "link-a/a",
			NodeID:        "node-a",
			Name:          "requested-a",
			LinkID:        "link-a",
			PeerNodeID:    "node-a",
			PeerInterface: "requested-b",
			Connectivity:  "loopback",
			MTU:           1500,
		},
		{
			ID:            "link-a/b",
			NodeID:        "node-a",
			Name:          "requested-b",
			LinkID:        "link-a",
			PeerNodeID:    "node-a",
			PeerInterface: "requested-a",
			Connectivity:  "loopback",
			MTU:           1500,
		},
	}
	digest, err := input.Digest()
	if err != nil {
		t.Fatal(err)
	}
	plan.InputDigest = digest
	plan.Interfaces = []clabernetesdeviceplan.InterfacePlan{
		{
			ID:               "link-a/a",
			NodeID:           "node-a",
			NamespaceOwnerID: "container-a",
			Name:             "package-a",
			LinkID:           "link-a",
			PeerNodeID:       "node-a",
			PeerInterface:    "requested-b",
			Connectivity:     "loopback",
			MTU:              1500,
			LinkApplyMode:    clabernetesdeviceplan.LinkApplyLive,
			RequiredAtStart:  true,
		},
		{
			ID:               "link-a/b",
			NodeID:           "node-a",
			NamespaceOwnerID: "container-a",
			Name:             "package-b",
			LinkID:           "link-a",
			PeerNodeID:       "node-a",
			PeerInterface:    "requested-a",
			Connectivity:     "loopback",
			MTU:              1500,
			LinkApplyMode:    clabernetesdeviceplan.LinkApplyLive,
			RequiredAtStart:  true,
		},
	}
	for _, intf := range plan.Interfaces {
		plan.Actions = append(plan.Actions, clabernetesdeviceplan.Action{
			ID: "wait/" + intf.ID, Phase: clabernetesdeviceplan.PhasePreStart,
			Target: clabernetesdeviceplan.ActionTarget{
				NodeID: "node-a", ContainerID: "container-a", NamespaceOwnerID: "container-a",
			},
			Kind: clabernetesdeviceplan.ActionWaitInterface,
			WaitInterface: &clabernetesdeviceplan.WaitInterfaceAction{
				InterfaceID: intf.ID, TimeoutSeconds: 30,
			},
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	operations := &fakeLinkOperations{}
	state := t.TempDir()
	if err = clabernetesdirectruntime.RunConnectivityWithLifecycleOperations(
		ctx,
		input,
		plan,
		clabernetesdirectruntime.ConnectivityOptions{
			StateDirectory: state,
			PodUID:         "pod-uid-a",
		},
		operations,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	if len(operations.pairs) != 1 ||
		!strings.HasPrefix(operations.pairs[0], "package-a/package-b/1500/c9s:direct:v1:") {
		t.Fatalf("veth operations = %#v", operations.pairs)
	}
	if err = clabernetesdirectruntime.ConnectivityReady(plan, state); err != nil {
		t.Fatal(err)
	}
}

func TestLocalConnectivityReconcilesMTUWithoutChangingUIDOwnership(t *testing.T) {
	t.Parallel()

	input, plan := connectivityTestInputAndPlan(t)
	setLoopbackLink(t, &input, &plan, 1500)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	operations := &fakeLinkOperations{}
	options := clabernetesdirectruntime.ConnectivityOptions{
		StateDirectory: t.TempDir(),
		PodUID:         "pod-uid-a",
	}
	if err := clabernetesdirectruntime.RunConnectivityWithLifecycleOperations(
		ctx,
		input,
		plan,
		options,
		operations,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	firstOwner := strings.SplitN(operations.pairs[0], "/", 4)[3]

	setLoopbackLink(t, &input, &plan, 9000)
	plan.Planner.Revision = "changed-plan-digest"
	if err := clabernetesdirectruntime.RunConnectivityWithLifecycleOperations(
		ctx,
		input,
		plan,
		options,
		operations,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	if len(operations.pairs) != 2 {
		t.Fatalf("veth operations = %#v", operations.pairs)
	}
	secondOwner := strings.SplitN(operations.pairs[1], "/", 4)[3]
	if firstOwner != secondOwner || !strings.Contains(operations.pairs[1], "/9000/") {
		t.Fatalf("live MTU operations = %#v", operations.pairs)
	}
	if len(operations.deletions) != 0 {
		t.Fatalf("MTU-only change deleted pair: %#v", operations.deletions)
	}
}

func TestLocalConnectivityDeletesOnlyStalePairsOwnedByRunningPod(t *testing.T) {
	t.Parallel()

	input, plan := connectivityTestInputAndPlan(t)
	setLoopbackLink(t, &input, &plan, 1500)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	operations := &fakeLinkOperations{}
	options := clabernetesdirectruntime.ConnectivityOptions{
		StateDirectory: t.TempDir(),
		PodUID:         "pod-uid-a",
	}
	if err := clabernetesdirectruntime.RunConnectivityWithLifecycleOperations(
		ctx,
		input,
		plan,
		options,
		operations,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	ownedInterfaces := len(operations.interfaces)
	operations.interfaces = append(operations.interfaces,
		clabernetesdirectruntime.VethInterface{
			Name: "foreign-a", PeerName: "foreign-b", Owner: "foreign-owner",
		},
		clabernetesdirectruntime.VethInterface{
			Name: "foreign-b", PeerName: "foreign-a", Owner: "foreign-owner",
		},
	)
	input.Interfaces = nil
	plan.Interfaces = nil
	plan.Actions = nil
	digest, err := input.Digest()
	if err != nil {
		t.Fatal(err)
	}
	plan.InputDigest = digest
	if err = clabernetesdirectruntime.RunConnectivityWithLifecycleOperations(
		ctx,
		input,
		plan,
		options,
		operations,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	if ownedInterfaces != 2 || len(operations.deletions) != 1 ||
		len(operations.interfaces) != 2 || operations.interfaces[0].Owner != "foreign-owner" {
		t.Fatalf(
			"cleanup result: owned=%d deleted=%#v remaining=%#v",
			ownedInterfaces,
			operations.deletions,
			operations.interfaces,
		)
	}
}

func TestLocalConnectivityDoesNotAdoptFormerPodUIDState(t *testing.T) {
	t.Parallel()

	input, plan := connectivityTestInputAndPlan(t)
	setLoopbackLink(t, &input, &plan, 1500)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	operations := &fakeLinkOperations{}
	if err := clabernetesdirectruntime.RunConnectivityWithLifecycleOperations(
		ctx,
		input,
		plan,
		clabernetesdirectruntime.ConnectivityOptions{
			StateDirectory: t.TempDir(), PodUID: "former-pod-uid",
		},
		operations,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	formerOwner := strings.SplitN(operations.pairs[0], "/", 4)[3]
	operations.pairs = nil
	if err := clabernetesdirectruntime.RunConnectivityWithLifecycleOperations(
		ctx,
		input,
		plan,
		clabernetesdirectruntime.ConnectivityOptions{
			StateDirectory: t.TempDir(), PodUID: "replacement-pod-uid",
		},
		operations,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	if len(operations.deletions) != 0 || len(operations.pairs) != 1 ||
		strings.HasSuffix(operations.pairs[0], formerOwner) {
		t.Fatalf(
			"replacement Pod touched former ownership: pairs=%#v deleted=%#v",
			operations.pairs,
			operations.deletions,
		)
	}
}

func TestSamePodConnectivityReplacesPairWhenBoundNodeUIDChanges(t *testing.T) {
	t.Parallel()

	input, plan := connectivityTestInputAndPlan(t)
	setSamePodLink(t, &input, &plan, "node-b-uid")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	operations := &fakeLinkOperations{}
	options := clabernetesdirectruntime.ConnectivityOptions{
		StateDirectory: t.TempDir(), PodUID: "pod-uid-a",
	}
	if err := clabernetesdirectruntime.RunConnectivityWithLifecycleOperations(
		ctx,
		input,
		plan,
		options,
		operations,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	firstOwner := strings.SplitN(operations.pairs[0], "/", 4)[3]

	replacementInput, replacementPlan := connectivityTestInputAndPlan(t)
	setSamePodLink(t, &replacementInput, &replacementPlan, "replacement-node-b-uid")
	if err := clabernetesdirectruntime.RunConnectivityWithLifecycleOperations(
		ctx,
		replacementInput,
		replacementPlan,
		options,
		operations,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	if len(operations.pairs) != 2 || len(operations.deletions) != 1 {
		t.Fatalf(
			"Node replacement reconciliation: pairs=%#v deleted=%#v",
			operations.pairs,
			operations.deletions,
		)
	}
	secondOwner := strings.SplitN(operations.pairs[1], "/", 4)[3]
	if firstOwner == secondOwner {
		t.Fatalf("Node UID did not contribute to Link ownership: %q", firstOwner)
	}
}

func TestLocalConnectivityRejectsFlavorAndNodeIdentityConflicts(t *testing.T) {
	t.Parallel()

	_, plan := connectivityTestInputAndPlan(t)
	plan.Interfaces = []clabernetesdeviceplan.InterfacePlan{
		{
			ID: "link/a", NodeID: "node-a", NamespaceOwnerID: "container-a",
			Name: "eth1", LinkID: "link-uid", PeerNodeID: "node-a",
			Connectivity: "same-pod", LinkApplyMode: clabernetesdeviceplan.LinkApplyLive,
		},
		{
			ID: "link/b", NodeID: "node-a", NamespaceOwnerID: "container-a",
			Name: "eth2", LinkID: "link-uid", PeerNodeID: "node-a",
			Connectivity: "loopback", LinkApplyMode: clabernetesdeviceplan.LinkApplyLive,
		},
	}
	if err := clabernetesdirectruntime.ValidatePlanCapabilities(plan); err == nil ||
		!strings.Contains(err.Error(), "connectivity semantics") {
		t.Fatalf("ValidatePlanCapabilities() error = %v", err)
	}
}

func TestLocalConnectivityDoesNotPublishReadinessOnInterfaceConflict(t *testing.T) {
	t.Parallel()

	input, plan := connectivityTestInputAndPlan(t)
	setLoopbackLink(t, &input, &plan, 1500)
	conflict := errors.New("foreign interface name conflict")
	state := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := clabernetesdirectruntime.RunConnectivityWithLifecycleOperations(
		ctx,
		input,
		plan,
		clabernetesdirectruntime.ConnectivityOptions{
			StateDirectory: state,
			PodUID:         "pod-uid-a",
		},
		&fakeLinkOperations{ensurePairError: conflict},
		nil,
	)
	if !errors.Is(err, conflict) {
		t.Fatalf("RunConnectivityWithLifecycleOperations() error = %v", err)
	}
	if readyErr := clabernetesdirectruntime.ConnectivityReady(plan, state); readyErr == nil {
		t.Fatal("connectivity helper published readiness after an interface conflict")
	}
}

func TestConnectivityRevisionReproducesPlannerVerifiedLiveInterfacePlan(t *testing.T) {
	t.Parallel()

	baseInput, basePlan := connectivityTestInputAndPlan(t)
	desiredInput, desiredPlan := connectivityTestInputAndPlan(t)
	setLoopbackLink(t, &desiredInput, &desiredPlan, 1500)
	revision, err := clabernetesdirectruntime.NewConnectivityRevision(
		baseInput,
		basePlan,
		desiredInput,
		desiredPlan,
	)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := revision.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := clabernetesdirectruntime.DecodeConnectivityRevision(raw)
	if err != nil {
		t.Fatal(err)
	}
	appliedInput, appliedPlan, err := clabernetesdirectruntime.ApplyConnectivityRevision(
		baseInput,
		basePlan,
		decoded,
	)
	if err != nil {
		t.Fatal(err)
	}
	inputDigest, err := appliedInput.Digest()
	if err != nil {
		t.Fatal(err)
	}
	desiredInputDigest, err := desiredInput.Digest()
	if err != nil {
		t.Fatal(err)
	}
	planDigest, err := appliedPlan.Digest()
	if err != nil {
		t.Fatal(err)
	}
	desiredPlanDigest, err := desiredPlan.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if inputDigest != desiredInputDigest || planDigest != desiredPlanDigest ||
		decoded.DesiredPlanDigest != desiredPlanDigest {
		t.Fatalf(
			"revision identities = input %q/%q plan %q/%q revision %q",
			inputDigest,
			desiredInputDigest,
			planDigest,
			desiredPlanDigest,
			decoded.DesiredPlanDigest,
		)
	}
}

func TestConnectivityRevisionDefersInterfaceDerivedColdPlanDrift(t *testing.T) {
	t.Parallel()

	baseInput, basePlan := connectivityTestInputAndPlan(t)
	desiredInput, desiredPlan := connectivityTestInputAndPlan(t)
	setLoopbackLink(t, &desiredInput, &desiredPlan, 1500)
	basePlan.Containers[0].Environment = []clabernetesdeviceplan.KeyValue{{
		Name: "package-created-endpoint-count", Value: "0",
	}}
	desiredPlan.Containers[0].Environment = []clabernetesdeviceplan.KeyValue{{
		Name: "package-created-endpoint-count", Value: "2",
	}}
	revision, err := clabernetesdirectruntime.NewConnectivityRevision(
		baseInput,
		basePlan,
		desiredInput,
		desiredPlan,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, appliedPlan, err := clabernetesdirectruntime.ApplyConnectivityRevision(
		baseInput,
		basePlan,
		revision,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(
		appliedPlan.Containers[0].Environment,
		basePlan.Containers[0].Environment,
	) {
		t.Fatalf(
			"live plan changed cold container metadata: %#v",
			appliedPlan.Containers[0].Environment,
		)
	}
}

func TestConnectivityRevisionRejectsNonInterfaceInputDrift(t *testing.T) {
	t.Parallel()

	baseInput, basePlan := connectivityTestInputAndPlan(t)
	desiredInput, desiredPlan := connectivityTestInputAndPlan(t)
	setLoopbackLink(t, &desiredInput, &desiredPlan, 1500)
	desiredInput.Nodes[0].Definition = []byte(`{"kind":"package-kind","image":"changed"}`)
	desiredInputDigest, err := desiredInput.Digest()
	if err != nil {
		t.Fatal(err)
	}
	desiredPlan.InputDigest = desiredInputDigest
	_, err = clabernetesdirectruntime.NewConnectivityRevision(
		baseInput,
		basePlan,
		desiredInput,
		desiredPlan,
	)
	if err == nil || !strings.Contains(err.Error(), "outside connectivity") {
		t.Fatalf("NewConnectivityRevision() error = %v", err)
	}
}

func TestConnectivityRevisionRejectsPlannerIdentityDrift(t *testing.T) {
	t.Parallel()

	baseInput, basePlan := connectivityTestInputAndPlan(t)
	desiredInput, desiredPlan := connectivityTestInputAndPlan(t)
	setLoopbackLink(t, &desiredInput, &desiredPlan, 1500)
	desiredPlan.Planner.Revision = "changed"
	_, err := clabernetesdirectruntime.NewConnectivityRevision(
		baseInput,
		basePlan,
		desiredInput,
		desiredPlan,
	)
	if err == nil || !strings.Contains(err.Error(), "planner identity") {
		t.Fatalf("NewConnectivityRevision() error = %v", err)
	}
}

func TestConnectivityRevisionRejectsNonLiveEndpointLifecycle(t *testing.T) {
	t.Parallel()

	baseInput, basePlan := connectivityTestInputAndPlan(t)
	desiredInput, desiredPlan := connectivityTestInputAndPlan(t)
	setLoopbackLink(t, &desiredInput, &desiredPlan, 1500)
	desiredPlan.Interfaces[0].LinkApplyMode = clabernetesdeviceplan.LinkApplyRecreate
	_, err := clabernetesdirectruntime.NewConnectivityRevision(
		baseInput,
		basePlan,
		desiredInput,
		desiredPlan,
	)
	if err == nil || !strings.Contains(err.Error(), "Live") {
		t.Fatalf("NewConnectivityRevision() error = %v", err)
	}
}

func TestConnectivityRevisionProjectsRestartWithoutAllowingRecreate(t *testing.T) {
	t.Parallel()

	baseInput, basePlan := connectivityTestInputAndPlan(t)
	desiredInput, desiredPlan := connectivityTestInputAndPlan(t)
	setLoopbackLink(t, &desiredInput, &desiredPlan, 1500)
	for index := range desiredPlan.Interfaces {
		desiredPlan.Interfaces[index].LinkApplyMode = clabernetesdeviceplan.LinkApplyRestart
	}
	revision, err := clabernetesdirectruntime.NewConnectivityRevisionForMode(
		baseInput,
		basePlan,
		desiredInput,
		desiredPlan,
		clabernetesdeviceplan.LinkApplyRestart,
	)
	if err != nil {
		t.Fatal(err)
	}
	if revision.MaximumMode != clabernetesdeviceplan.LinkApplyRestart {
		t.Fatalf("Restart revision = %#v", revision)
	}
	if _, _, err = clabernetesdirectruntime.ApplyConnectivityRevision(
		baseInput,
		basePlan,
		revision,
	); err != nil {
		t.Fatal(err)
	}
	for index := range desiredPlan.Interfaces {
		desiredPlan.Interfaces[index].LinkApplyMode = clabernetesdeviceplan.LinkApplyRecreate
	}
	if _, err = clabernetesdirectruntime.NewConnectivityRevisionForMode(
		baseInput,
		basePlan,
		desiredInput,
		desiredPlan,
		clabernetesdeviceplan.LinkApplyRestart,
	); err == nil || !strings.Contains(err.Error(), "Recreate") {
		t.Fatalf("Restart projection accepted Recreate: %v", err)
	}
}

func TestEvaluateConnectivityTransitionUsesMostDisruptiveAffectedPlanMode(t *testing.T) {
	t.Parallel()

	for _, mode := range []clabernetesdeviceplan.LinkApplyMode{
		clabernetesdeviceplan.LinkApplyLive,
		clabernetesdeviceplan.LinkApplyRestart,
		clabernetesdeviceplan.LinkApplyRecreate,
	} {
		mode := mode
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()

			baseInput, basePlan := connectivityTestInputAndPlan(t)
			desiredInput, desiredPlan := connectivityTestInputAndPlan(t)
			setLoopbackLink(t, &desiredInput, &desiredPlan, 1500)
			for index := range desiredPlan.Interfaces {
				desiredPlan.Interfaces[index].LinkApplyMode = mode
			}
			desiredPlan.Containers[0].Environment = []clabernetesdeviceplan.KeyValue{{
				Name: "package-created-endpoint-count", Value: "2",
			}}

			transition, err := clabernetesdirectruntime.EvaluateConnectivityTransition(
				baseInput,
				basePlan,
				desiredInput,
				desiredPlan,
			)
			if err != nil {
				t.Fatal(err)
			}
			if !transition.Changed || transition.RequiredMode != mode ||
				!reflect.DeepEqual(transition.AffectedNodeIDs, []string{"node-a"}) {
				t.Fatalf("connectivity transition = %#v, want changed %s", transition, mode)
			}
		})
	}

	baseInput, basePlan := connectivityTestInputAndPlan(t)
	unchanged, err := clabernetesdirectruntime.EvaluateConnectivityTransition(
		baseInput,
		basePlan,
		baseInput,
		basePlan,
	)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Changed || unchanged.RequiredMode != clabernetesdeviceplan.LinkApplyLive {
		t.Fatalf("unchanged connectivity transition = %#v", unchanged)
	}
}

func TestEvaluateConnectivityTransitionUsesRemovedEndpointPlanMode(t *testing.T) {
	t.Parallel()

	for _, mode := range []clabernetesdeviceplan.LinkApplyMode{
		clabernetesdeviceplan.LinkApplyLive,
		clabernetesdeviceplan.LinkApplyRestart,
		clabernetesdeviceplan.LinkApplyRecreate,
	} {
		mode := mode
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()

			baseInput, basePlan := connectivityTestInputAndPlan(t)
			desiredInput, desiredPlan := connectivityTestInputAndPlan(t)
			setLoopbackLink(t, &baseInput, &basePlan, 1500)
			for index := range basePlan.Interfaces {
				basePlan.Interfaces[index].LinkApplyMode = mode
			}

			transition, err := clabernetesdirectruntime.EvaluateConnectivityTransition(
				baseInput,
				basePlan,
				desiredInput,
				desiredPlan,
			)
			if err != nil {
				t.Fatal(err)
			}
			if !transition.Changed || transition.RequiredMode != mode {
				t.Fatalf("removed connectivity transition = %#v, want changed %s", transition, mode)
			}
		})
	}
}

func TestEvaluateConnectivityTransitionEscalatesMixedAffectedEndpoints(t *testing.T) {
	t.Parallel()

	baseInput, basePlan := connectivityTestInputAndPlan(t)
	desiredInput, desiredPlan := connectivityTestInputAndPlan(t)
	appendLoopbackLink(
		t,
		&desiredInput,
		&desiredPlan,
		"link-live",
		"link-uid-live",
		"package-a",
		"package-b",
		1500,
		clabernetesdeviceplan.LinkApplyLive,
	)
	appendLoopbackLink(
		t,
		&desiredInput,
		&desiredPlan,
		"link-restart",
		"link-uid-restart",
		"package-c",
		"package-d",
		1500,
		clabernetesdeviceplan.LinkApplyRestart,
	)
	transition, err := clabernetesdirectruntime.EvaluateConnectivityTransition(
		baseInput,
		basePlan,
		desiredInput,
		desiredPlan,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !transition.Changed ||
		transition.RequiredMode != clabernetesdeviceplan.LinkApplyRestart {
		t.Fatalf("mixed connectivity transition = %#v", transition)
	}
}

func TestConnectivityRevisionAllowsUnchangedNonLiveLink(t *testing.T) {
	t.Parallel()

	baseInput, basePlan := connectivityTestInputAndPlan(t)
	desiredInput, desiredPlan := connectivityTestInputAndPlan(t)
	setLoopbackLink(t, &baseInput, &basePlan, 1500)
	setLoopbackLink(t, &desiredInput, &desiredPlan, 1500)
	for index := range basePlan.Interfaces {
		basePlan.Interfaces[index].LinkApplyMode = clabernetesdeviceplan.LinkApplyRecreate
		desiredPlan.Interfaces[index].LinkApplyMode = clabernetesdeviceplan.LinkApplyRecreate
	}
	appendLoopbackLink(
		t,
		&baseInput,
		&basePlan,
		"link-b",
		"link-uid-b",
		"package-c",
		"package-d",
		1500,
		clabernetesdeviceplan.LinkApplyLive,
	)
	appendLoopbackLink(
		t,
		&desiredInput,
		&desiredPlan,
		"link-b",
		"link-uid-b",
		"package-c",
		"package-d",
		9000,
		clabernetesdeviceplan.LinkApplyLive,
	)
	if _, err := clabernetesdirectruntime.NewConnectivityRevision(
		baseInput,
		basePlan,
		desiredInput,
		desiredPlan,
	); err != nil {
		t.Fatalf("NewConnectivityRevision() rejected independent Live Link: %v", err)
	}
}

func TestConnectivityRevisionDecodeRejectsUnknownAndTrailingJSON(t *testing.T) {
	t.Parallel()

	baseInput, basePlan := connectivityTestInputAndPlan(t)
	revision, err := clabernetesdirectruntime.NewConnectivityRevision(
		baseInput,
		basePlan,
		baseInput,
		basePlan,
	)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := revision.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	unknown := append([]byte{}, raw[:len(raw)-1]...)
	unknown = append(unknown, []byte(`,"unknown":true}`)...)
	for _, invalid := range [][]byte{unknown, append(append([]byte{}, raw...), []byte(`{}`)...)} {
		if _, err = clabernetesdirectruntime.DecodeConnectivityRevision(invalid); err == nil {
			t.Fatalf("DecodeConnectivityRevision() accepted %s", invalid)
		}
	}
}

func TestConnectivityHelperAppliesProjectedLiveRevisionWithoutRestart(t *testing.T) {
	t.Parallel()

	baseInput, basePlan := connectivityTestInputAndPlan(t)
	desiredInput, desiredPlan := connectivityTestInputAndPlan(t)
	setLoopbackLink(t, &desiredInput, &desiredPlan, 9000)
	initial, err := clabernetesdirectruntime.NewConnectivityRevision(
		baseInput,
		basePlan,
		baseInput,
		basePlan,
	)
	if err != nil {
		t.Fatal(err)
	}
	desired, err := clabernetesdirectruntime.NewConnectivityRevision(
		baseInput,
		basePlan,
		desiredInput,
		desiredPlan,
	)
	if err != nil {
		t.Fatal(err)
	}
	initialRaw, err := initial.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	desiredRaw, err := desired.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	revisionPath := filepath.Join(root, "revision.json")
	writeConnectivityRevisionFile(t, revisionPath, initialRaw)
	state := filepath.Join(root, "state")
	operations := &fakeLinkOperations{pairSignal: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- clabernetesdirectruntime.RunConnectivityWithLifecycleOperations(
			ctx,
			baseInput,
			basePlan,
			clabernetesdirectruntime.ConnectivityOptions{
				StateDirectory:           state,
				PodUID:                   "pod-uid-a",
				ConnectivityRevisionPath: revisionPath,
				RevisionPollInterval:     5 * time.Millisecond,
			},
			operations,
			nil,
		)
	}()
	t.Cleanup(cancel)
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for clabernetesdirectruntime.ConnectivityReady(basePlan, state) != nil {
		select {
		case runErr := <-errCh:
			t.Fatalf("connectivity helper exited before cold readiness: %v", runErr)
		case <-deadline.C:
			t.Fatal("connectivity helper did not publish cold readiness")
		case <-time.After(5 * time.Millisecond):
		}
	}
	writeConnectivityRevisionFile(t, revisionPath, desiredRaw)
	select {
	case <-operations.pairSignal:
	case runErr := <-errCh:
		t.Fatalf("connectivity helper exited before applying revision: %v", runErr)
	case <-deadline.C:
		t.Fatal("connectivity helper did not apply projected revision")
	}
	for clabernetesdirectruntime.ConnectivityReadyWithRevision(
		basePlan,
		state,
		revisionPath,
	) != nil {
		select {
		case runErr := <-errCh:
			t.Fatalf("connectivity helper exited before revision readiness: %v", runErr)
		case <-deadline.C:
			t.Fatal("connectivity helper did not publish revision readiness")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	if runErr := <-errCh; runErr != nil {
		t.Fatal(runErr)
	}
	if len(operations.pairs) != 1 ||
		!strings.HasPrefix(operations.pairs[0], "package-a/package-b/9000/") {
		t.Fatalf("live revision veth operations = %#v", operations.pairs)
	}
}

func writeConnectivityRevisionFile(t *testing.T, destination string, raw []byte) {
	t.Helper()
	temporary := destination + ".next"
	if err := os.WriteFile(temporary, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temporary, destination); err != nil {
		t.Fatal(err)
	}
}

func setVXLANLink(
	t *testing.T,
	input *clabernetesdeviceplan.Input,
	plan *clabernetesdeviceplan.Plan,
	peerNodeID,
	peerTransport string,
	tunnelID,
	mtu int,
) {
	t.Helper()
	input.Interfaces = []clabernetesdeviceplan.InterfaceInput{{
		ID: "link-a/a", NodeID: "node-a", Name: "requested-a", LinkID: "link-uid-a",
		PeerNodeID: peerNodeID, PeerInterface: "requested-b", PeerTransport: peerTransport,
		Connectivity: "vxlan", TunnelID: tunnelID, MTU: mtu,
	}}
	digest, err := input.Digest()
	if err != nil {
		t.Fatal(err)
	}
	plan.InputDigest = digest
	plan.Interfaces = []clabernetesdeviceplan.InterfacePlan{{
		ID: "link-a/a", NodeID: "node-a", NamespaceOwnerID: "container-a",
		Name: "package-a", LinkID: "link-uid-a", PeerNodeID: peerNodeID,
		PeerInterface: "requested-b", PeerTransport: peerTransport,
		Connectivity: "vxlan", TunnelID: tunnelID, MTU: mtu,
		LinkApplyMode: clabernetesdeviceplan.LinkApplyLive, RequiredAtStart: true,
	}}
	plan.Actions = []clabernetesdeviceplan.Action{{
		ID: "wait/link-a/a", Phase: clabernetesdeviceplan.PhasePreStart,
		Target: clabernetesdeviceplan.ActionTarget{
			NodeID: "node-a", ContainerID: "container-a", NamespaceOwnerID: "container-a",
		},
		Kind: clabernetesdeviceplan.ActionWaitInterface,
		WaitInterface: &clabernetesdeviceplan.WaitInterfaceAction{
			InterfaceID: "link-a/a", TimeoutSeconds: 30,
		},
	}}
}

func setSlurpeethLink(
	t *testing.T,
	input *clabernetesdeviceplan.Input,
	plan *clabernetesdeviceplan.Plan,
	peerNodeID,
	peerTransport string,
	tunnelID,
	mtu int,
) {
	t.Helper()
	input.Interfaces = []clabernetesdeviceplan.InterfaceInput{{
		ID: "link-a/a", NodeID: "node-a", Name: "requested-a", LinkID: "link-uid-a",
		PeerNodeID: peerNodeID, PeerInterface: "requested-b", PeerTransport: peerTransport,
		Connectivity: "slurpeeth", TunnelID: tunnelID, MTU: mtu,
	}}
	digest, err := input.Digest()
	if err != nil {
		t.Fatal(err)
	}
	plan.InputDigest = digest
	plan.Interfaces = []clabernetesdeviceplan.InterfacePlan{{
		ID: "link-a/a", NodeID: "node-a", NamespaceOwnerID: "container-a",
		Name: "package-a", LinkID: "link-uid-a", PeerNodeID: peerNodeID,
		PeerInterface: "requested-b", PeerTransport: peerTransport,
		Connectivity: "slurpeeth", TunnelID: tunnelID, MTU: mtu,
		LinkApplyMode: clabernetesdeviceplan.LinkApplyLive, RequiredAtStart: true,
	}}
	plan.Actions = []clabernetesdeviceplan.Action{{
		ID: "wait/link-a/a", Phase: clabernetesdeviceplan.PhasePreStart,
		Target: clabernetesdeviceplan.ActionTarget{
			NodeID: "node-a", ContainerID: "container-a", NamespaceOwnerID: "container-a",
		},
		Kind: clabernetesdeviceplan.ActionWaitInterface,
		WaitInterface: &clabernetesdeviceplan.WaitInterfaceAction{
			InterfaceID: "link-a/a", TimeoutSeconds: 30,
		},
	}}
}

func setLoopbackLink(
	t *testing.T,
	input *clabernetesdeviceplan.Input,
	plan *clabernetesdeviceplan.Plan,
	mtu int,
) {
	t.Helper()
	input.Interfaces = nil
	plan.Interfaces = nil
	plan.Actions = nil
	appendLoopbackLink(
		t,
		input,
		plan,
		"link-a",
		"link-uid-a",
		"package-a",
		"package-b",
		mtu,
		clabernetesdeviceplan.LinkApplyLive,
	)
}

func appendLoopbackLink(
	t *testing.T,
	input *clabernetesdeviceplan.Input,
	plan *clabernetesdeviceplan.Plan,
	idPrefix,
	linkID,
	leftName,
	rightName string,
	mtu int,
	linkApplyMode clabernetesdeviceplan.LinkApplyMode,
) {
	t.Helper()
	input.Interfaces = append(input.Interfaces,
		clabernetesdeviceplan.InterfaceInput{
			ID: idPrefix + "/a", NodeID: "node-a", Name: "requested-a", LinkID: linkID,
			PeerNodeID: "node-a", PeerInterface: "requested-b",
			Connectivity: "loopback", MTU: mtu,
		},
		clabernetesdeviceplan.InterfaceInput{
			ID: idPrefix + "/b", NodeID: "node-a", Name: "requested-b", LinkID: linkID,
			PeerNodeID: "node-a", PeerInterface: "requested-a",
			Connectivity: "loopback", MTU: mtu,
		},
	)
	digest, err := input.Digest()
	if err != nil {
		t.Fatal(err)
	}
	plan.InputDigest = digest
	plan.Interfaces = append(plan.Interfaces,
		clabernetesdeviceplan.InterfacePlan{
			ID: idPrefix + "/a", NodeID: "node-a", NamespaceOwnerID: "container-a",
			Name: leftName, LinkID: linkID, PeerNodeID: "node-a",
			PeerInterface: "requested-b", Connectivity: "loopback", MTU: mtu,
			LinkApplyMode: linkApplyMode, RequiredAtStart: true,
		},
		clabernetesdeviceplan.InterfacePlan{
			ID: idPrefix + "/b", NodeID: "node-a", NamespaceOwnerID: "container-a",
			Name: rightName, LinkID: linkID, PeerNodeID: "node-a",
			PeerInterface: "requested-a", Connectivity: "loopback", MTU: mtu,
			LinkApplyMode: linkApplyMode, RequiredAtStart: true,
		},
	)
	for _, intf := range plan.Interfaces[len(plan.Interfaces)-2:] {
		plan.Actions = append(plan.Actions, clabernetesdeviceplan.Action{
			ID: "wait/" + intf.ID, Phase: clabernetesdeviceplan.PhasePreStart,
			Target: clabernetesdeviceplan.ActionTarget{
				NodeID: "node-a", ContainerID: "container-a", NamespaceOwnerID: "container-a",
			},
			Kind: clabernetesdeviceplan.ActionWaitInterface,
			WaitInterface: &clabernetesdeviceplan.WaitInterfaceAction{
				InterfaceID: intf.ID, TimeoutSeconds: 30,
			},
		})
	}
}

func setSamePodLink(
	t *testing.T,
	input *clabernetesdeviceplan.Input,
	plan *clabernetesdeviceplan.Plan,
	rightNodeID string,
) {
	t.Helper()
	input.Nodes = append(input.Nodes, clabernetesdeviceplan.NodeInput{
		ID: rightNodeID, Name: "router-b", Kind: "package-kind",
		GroupOwner: "node-a",
		Definition: []byte(`{"kind":"package-kind","image":"example/device:1"}`),
	})
	input.Images = append(input.Images, clabernetesdeviceplan.ImageInput{
		NodeID: rightNodeID, Role: "device", SourceReference: "example/device:1",
		DigestReference: "example/device@sha256:aaaaaaaa",
		Platform:        clabernetesdeviceplan.Platform{OS: "linux", Architecture: "amd64"},
	})
	input.Interfaces = []clabernetesdeviceplan.InterfaceInput{
		{
			ID: "link-a/a", NodeID: "node-a", Name: "requested-a", LinkID: "link-uid-a",
			PeerNodeID: rightNodeID, PeerInterface: "requested-b",
			Connectivity: "same-pod", MTU: 1500,
		},
		{
			ID: "link-a/b", NodeID: rightNodeID, Name: "requested-b", LinkID: "link-uid-a",
			PeerNodeID: "node-a", PeerInterface: "requested-a",
			Connectivity: "same-pod", MTU: 1500,
		},
	}
	digest, err := input.Digest()
	if err != nil {
		t.Fatal(err)
	}
	plan.InputDigest = digest
	plan.Nodes = append(plan.Nodes, clabernetesdeviceplan.NodePlan{
		ID: rightNodeID, Name: "router-b", Kind: "package-kind",
		ContainerIDs: []string{"container-b"}, ReadinessContainerIDs: []string{"container-b"},
	})
	plan.Containers = append(plan.Containers, clabernetesdeviceplan.ContainerPlan{
		ID: "container-b", NodeID: rightNodeID, NamespaceOwnerID: "container-b",
		Image:       "example/device:1",
		ImageDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Required:    true,
	})
	plan.Interfaces = []clabernetesdeviceplan.InterfacePlan{
		{
			ID: "link-a/a", NodeID: "node-a", NamespaceOwnerID: "container-a",
			Name: "package-a", LinkID: "link-uid-a", PeerNodeID: rightNodeID,
			PeerInterface: "requested-b", Connectivity: "same-pod", MTU: 1500,
			LinkApplyMode: clabernetesdeviceplan.LinkApplyLive, RequiredAtStart: true,
		},
		{
			ID: "link-a/b", NodeID: rightNodeID, NamespaceOwnerID: "container-b",
			Name: "package-b", LinkID: "link-uid-a", PeerNodeID: "node-a",
			PeerInterface: "requested-a", Connectivity: "same-pod", MTU: 1500,
			LinkApplyMode: clabernetesdeviceplan.LinkApplyLive, RequiredAtStart: true,
		},
	}
	plan.Actions = nil
	for index, intf := range plan.Interfaces {
		containerID := "container-a"
		if index == 1 {
			containerID = "container-b"
		}
		plan.Actions = append(plan.Actions, clabernetesdeviceplan.Action{
			ID: "wait/" + intf.ID, Phase: clabernetesdeviceplan.PhasePreStart,
			Target: clabernetesdeviceplan.ActionTarget{
				NodeID: intf.NodeID, ContainerID: containerID,
				NamespaceOwnerID: intf.NamespaceOwnerID,
			},
			Kind: clabernetesdeviceplan.ActionWaitInterface,
			WaitInterface: &clabernetesdeviceplan.WaitInterfaceAction{
				InterfaceID: intf.ID, TimeoutSeconds: 30,
			},
		})
	}
}

func connectivityTestInputAndPlan(
	t *testing.T,
) (clabernetesdeviceplan.Input, clabernetesdeviceplan.Plan) {
	t.Helper()
	compatibility := clabernetesdeviceplan.Compatibility{
		ContainerlabModule:  clabernetesdeviceplan.ContainerlabModulePath,
		ContainerlabVersion: "v-test", PlanSchemaVersion: clabernetesdeviceplan.SchemaVersion,
		RegistryDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	input := clabernetesdeviceplan.Input{
		SchemaVersion: clabernetesdeviceplan.SchemaVersion, TopologyName: "lab",
		Compatibility: compatibility,
		Nodes: []clabernetesdeviceplan.NodeInput{{
			ID: "node-a", Name: "router", Kind: "package-kind",
			Definition: []byte(`{"kind":"package-kind","image":"example/device:1"}`),
		}},
		Images: []clabernetesdeviceplan.ImageInput{{
			NodeID: "node-a", Role: "device", SourceReference: "example/device:1",
			DigestReference: "example/device@sha256:aaaaaaaa",
			Platform:        clabernetesdeviceplan.Platform{OS: "linux", Architecture: "amd64"},
		}},
	}
	digest, err := input.Digest()
	if err != nil {
		t.Fatal(err)
	}
	plan := clabernetesdeviceplan.Plan{
		SchemaVersion: clabernetesdeviceplan.SchemaVersion, Compatibility: compatibility,
		InputDigest: digest,
		Planner:     clabernetesdeviceplan.PlannerIdentity{Name: "clabernetes", Revision: "test"},
		Nodes: []clabernetesdeviceplan.NodePlan{{
			ID: "node-a", Name: "router", Kind: "package-kind",
			ContainerIDs: []string{"container-a"}, ReadinessContainerIDs: []string{"container-a"},
		}},
		Containers: []clabernetesdeviceplan.ContainerPlan{{
			ID: "container-a", NodeID: "node-a", NamespaceOwnerID: "container-a",
			Image:       "example/device:1",
			ImageDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Required:    true,
		}},
	}

	return input, plan
}

func setHostLink(
	t *testing.T,
	input *clabernetesdeviceplan.Input,
	plan *clabernetesdeviceplan.Plan,
) {
	t.Helper()
	input.Interfaces = []clabernetesdeviceplan.InterfaceInput{{
		ID:            "link-uid-a/a",
		NodeID:        "node-a",
		Name:          "eth1",
		LinkID:        "link-uid-a",
		LinkName:      "host-link-a",
		PeerInterface: "c9s-host-a",
		Connectivity:  "host",
		MTU:           1450,
	}}
	digest, err := input.Digest()
	if err != nil {
		t.Fatal(err)
	}
	plan.InputDigest = digest
	plan.Interfaces = []clabernetesdeviceplan.InterfacePlan{{
		ID:               "link-uid-a/a",
		NodeID:           "node-a",
		NamespaceOwnerID: "container-a",
		Name:             "eth1",
		LinkID:           "link-uid-a",
		LinkName:         "host-link-a",
		PeerInterface:    "c9s-host-a",
		Connectivity:     "host",
		MTU:              1450,
		LinkApplyMode:    clabernetesdeviceplan.LinkApplyLive,
		RequiredAtStart:  true,
	}}
	plan.Actions = []clabernetesdeviceplan.Action{{
		ID:    "wait/link-uid-a/a",
		Phase: clabernetesdeviceplan.PhasePreStart,
		Target: clabernetesdeviceplan.ActionTarget{
			NodeID: "node-a", ContainerID: "container-a", NamespaceOwnerID: "container-a",
		},
		Kind: clabernetesdeviceplan.ActionWaitInterface,
		WaitInterface: &clabernetesdeviceplan.WaitInterfaceAction{
			InterfaceID: "link-uid-a/a", TimeoutSeconds: 30,
		},
	}}
}
