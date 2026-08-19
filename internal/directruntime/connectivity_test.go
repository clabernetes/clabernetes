package directruntime_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

type fakeHostEndpointReconciler struct {
	requests      chan claberneteshostendpoint.ReconcileRequest
	err           error
	fabricUnready bool
}

func (f *fakeHostEndpointReconciler) Reconcile(
	_ context.Context,
	request claberneteshostendpoint.ReconcileRequest,
	networkNamespacePath string,
) (
	[]claberneteshostendpoint.FabricStatus,
	*claberneteshostendpoint.ManagementStatus,
	error,
) {
	if networkNamespacePath != "/proc/self/ns/net" {
		return nil, nil, fmt.Errorf("network namespace path = %q", networkNamespacePath)
	}
	if f.requests != nil {
		f.requests <- request
	}
	if f.err != nil {
		return nil, nil, f.err
	}
	statuses := make([]claberneteshostendpoint.FabricStatus, 0, len(request.Fabric))
	for _, endpoint := range request.Fabric {
		statuses = append(statuses, claberneteshostendpoint.FabricStatus{
			LinkUID: endpoint.Link.UID,
			Ready:   !f.fabricUnready,
		})
	}
	var management *claberneteshostendpoint.ManagementStatus
	if request.Management != nil {
		management = &claberneteshostendpoint.ManagementStatus{Ready: true}
	}

	return statuses, management, nil
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
			name: "vxlan", flavor: "fabric",
			configure: func(
				t *testing.T,
				input *clabernetesdeviceplan.Input,
				plan *clabernetesdeviceplan.Plan,
			) {
				setVXLANLink(t, input, plan, "peer-node-uid", "peer-vx", 73, 1450)
			},
		},
		{
			name: "slurpeeth", flavor: "fabric",
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
			operations := &fakeLinkOperations{}
			hostReconciler := &fakeHostEndpointReconciler{
				requests: make(chan claberneteshostendpoint.ReconcileRequest, 8),
			}
			run := func() error {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				options := clabernetesdirectruntime.ConnectivityOptions{
					StateDirectory:         state,
					PodNamespace:           "lab",
					PodName:                "router-pod",
					PodUID:                 "pod-uid-a",
					HostEndpointReconciler: hostReconciler,
				}

				return clabernetesdirectruntime.RunConnectivityWithLifecycleOperations(
					ctx, input, plan, options, operations, nil,
				)
			}

			switch testCase.flavor {
			case "local":
				operations.ensurePairError = errors.New("injected local Link failure")
			case "fabric", "host":
				hostReconciler.err = errors.New("injected daemon endpoint failure")
			}
			if err := run(); err == nil {
				t.Fatal("partial failure was accepted")
			}
			if err := clabernetesdirectruntime.ConnectivityReady(plan, state); err == nil {
				t.Fatal("partial failure published connectivity readiness")
			}
			operations.ensurePairError = nil
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
			case "local":
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
			case "fabric", "host":
				requests := []claberneteshostendpoint.ReconcileRequest{}
				for len(hostReconciler.requests) != 0 {
					requests = append(requests, <-hostReconciler.requests)
				}
				if len(requests) != 3 || !reflect.DeepEqual(requests[1], requests[2]) {
					t.Fatalf("helper restart daemon requests = %#v", requests)
				}
			}
		})
	}
}

// TestFabricConnectivityRequestsDaemonRealizationBeforeReadiness proves cross-Pod endpoints are
// realized through the node-local daemon: the Pod side is a plain veth leg, so the request must
// carry the Link identity, the plan interface name, and the allocated tunnel id.
func TestFabricConnectivityRequestsDaemonRealizationBeforeReadiness(t *testing.T) {
	t.Parallel()

	for _, connectivity := range []string{"vxlan", "slurpeeth"} {
		connectivity := connectivity
		t.Run(connectivity, func(t *testing.T) {
			t.Parallel()

			input, plan := connectivityTestInputAndPlan(t)
			if connectivity == "vxlan" {
				setVXLANLink(t, &input, &plan, "peer-node-uid", "peer-vx", 73, 1450)
			} else {
				setSlurpeethLink(t, &input, &plan, "peer-node-uid", "peer-vx", 73, 1450)
			}
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
			if len(request.Fabric) != 1 || len(request.Endpoints) != 0 {
				t.Fatalf("daemon request = %#v", request)
			}
			endpoint := request.Fabric[0]
			if endpoint.Link.Namespace != "lab" || endpoint.Link.UID == "" ||
				endpoint.PodInterface == "" || endpoint.TunnelID != 73 || endpoint.MTU != 1450 {
				t.Fatalf("fabric endpoint = %#v", endpoint)
			}
			if err := clabernetesdirectruntime.ConnectivityReady(plan, state); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestFabricConnectivityStaysUnreadyUntilDaemonReportsPeer holds readiness back while the
// daemon reports the transport is still waiting on its peer.
func TestFabricConnectivityStaysUnreadyUntilDaemonReportsPeer(t *testing.T) {
	t.Parallel()

	input, plan := connectivityTestInputAndPlan(t)
	setVXLANLink(t, &input, &plan, "peer-node-uid", "peer-vx", 73, 1450)
	reconciler := &fakeHostEndpointReconciler{fabricUnready: true}
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
	if err := clabernetesdirectruntime.ConnectivityReady(plan, state); err == nil {
		t.Fatal("pending fabric transport published connectivity readiness")
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
			StateDirectory:         state,
			PodAddress:             "10.244.0.12",
			HostEndpointReconciler: &fakeHostEndpointReconciler{},
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
