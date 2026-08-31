//nolint:err113,gocognit,gocyclo // dense fixture-driven tests exercise one boundary end to end.
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

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	clabernetesinternaldirectruntime "github.com/clabernetes/clabernetes/internal/directruntime"
)

type fakeLinkOperations struct {
	sysctls             []string
	pairs               []string
	interfaces          []clabernetesinternaldirectruntime.VethInterface
	deletions           []string
	managementAddresses []string
	managementRoutes    []string
	offloadDisabled     []string
	interpositions      []clabernetesinternaldirectruntime.InterpositionSpec
	interpositionError  error
	fabricSpecs         []clabernetesinternaldirectruntime.FabricEndpointSpec
	fabricUnready       bool
	fabricError         error
	hostSpecs           []clabernetesinternaldirectruntime.HostInterfaceSpec
	hostError           error
	sweepKeepCounts     chan int
	podTransport        string
	pairSignal          chan struct{}
	ensurePairError     error
}

func (f *fakeLinkOperations) DisableTxChecksumOffload(interfaceName string) error {
	f.offloadDisabled = append(f.offloadDisabled, interfaceName)

	return nil
}

func (f *fakeLinkOperations) EnsureInterposition(
	spec clabernetesinternaldirectruntime.InterpositionSpec,
) error {
	if f.interpositionError != nil {
		return f.interpositionError
	}

	f.interpositions = append(f.interpositions, spec)

	return nil
}

func (f *fakeLinkOperations) EnsureFabricEndpoint(
	spec clabernetesinternaldirectruntime.FabricEndpointSpec,
) (clabernetesinternaldirectruntime.FabricEndpointResult, error) {
	if f.fabricError != nil {
		return clabernetesinternaldirectruntime.FabricEndpointResult{}, f.fabricError
	}

	f.fabricSpecs = append(f.fabricSpecs, spec)

	if f.fabricUnready {
		return clabernetesinternaldirectruntime.FabricEndpointResult{
			Reason: "peer transport is not yet resolvable",
		}, nil
	}

	return clabernetesinternaldirectruntime.FabricEndpointResult{Ready: true}, nil
}

func (f *fakeLinkOperations) EnsureHostInterface(
	spec clabernetesinternaldirectruntime.HostInterfaceSpec,
) error {
	if f.hostError != nil {
		return f.hostError
	}

	f.hostSpecs = append(f.hostSpecs, spec)

	return nil
}

func (f *fakeLinkOperations) SweepTransportState(_ string, keepOwners []string) error {
	if f.sweepKeepCounts != nil {
		select {
		case f.sweepKeepCounts <- len(keepOwners):
		default:
		}
	}

	return nil
}

func (f *fakeLinkOperations) ResolvePodTransportInterface(podAddress string) (string, error) {
	if podAddress == "" {
		return "", errors.New("Pod transport address is empty")
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
		clabernetesinternaldirectruntime.VethInterface{Name: left, PeerName: right, Owner: owner},
		clabernetesinternaldirectruntime.VethInterface{Name: right, PeerName: left, Owner: owner},
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
) ([]clabernetesinternaldirectruntime.VethInterface, error) {
	result := []clabernetesinternaldirectruntime.VethInterface{}

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
	if err := clabernetesinternaldirectruntime.RunConnectivity(ctx, input, plan, state); err != nil {
		t.Fatal(err)
	}

	if err := clabernetesinternaldirectruntime.ConnectivityReady(plan, state); err != nil {
		t.Fatal(err)
	}

	plan.Planner.Revision = "changed"
	if err := clabernetesinternaldirectruntime.ConnectivityReady(plan, state); err == nil {
		t.Fatal("ConnectivityReady() accepted a marker for another plan")
	}
}

func TestConnectivityAppliesPackageSysctlsDeterministicallyBeforeReadiness(t *testing.T) {
	t.Parallel()

	input, plan := connectivityTestInputAndPlan(t)
	plan.Containers[0].Security.Sysctls = []clabernetesinternaldeviceplan.KeyValue{
		{Name: "net.ipv6.conf.all.disable_ipv6", Value: "0"},
		{Name: "net.ipv4.ip_forward", Value: "1"},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	operations := &fakeLinkOperations{}

	state := t.TempDir()
	if err := clabernetesinternaldirectruntime.RunConnectivityWithOperations(
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

	if err := clabernetesinternaldirectruntime.ConnectivityReady(plan, state); err != nil {
		t.Fatal(err)
	}
}

func TestConnectivityRejectsConflictingNetworkNamespaceSysctls(t *testing.T) {
	t.Parallel()

	_, plan := connectivityTestInputAndPlan(t)
	plan.Containers[0].RuntimeID = "runtime-a"
	plan.Containers[0].Security.Sysctls = []clabernetesinternaldeviceplan.KeyValue{
		{Name: "net.ipv4.ip_forward", Value: "1"},
	}
	second := plan.Containers[0]
	second.ID = "container-b"
	second.RuntimeID = "runtime-b"
	second.Security.Sysctls = []clabernetesinternaldeviceplan.KeyValue{
		{Name: "net.ipv4.ip_forward", Value: "0"},
	}
	plan.Containers = append(plan.Containers, second)
	plan.Nodes[0].ContainerIDs = append(plan.Nodes[0].ContainerIDs, second.ID)

	plan.Nodes[0].ReadinessContainerIDs = append(
		plan.Nodes[0].ReadinessContainerIDs,
		second.ID,
	)
	if err := clabernetesinternaldirectruntime.ValidatePlanCapabilities(plan); err == nil ||
		!strings.Contains(err.Error(), "conflicting network-namespace sysctl") {
		t.Fatalf("ValidatePlanCapabilities() error = %v", err)
	}
}

func TestConnectivityRejectsUnsafeNetworkNamespaceSysctlName(t *testing.T) {
	t.Parallel()

	_, plan := connectivityTestInputAndPlan(t)

	plan.Containers[0].Security.Sysctls = []clabernetesinternaldeviceplan.KeyValue{
		{Name: "../kernel.hostname", Value: "unexpected"},
	}
	if err := clabernetesinternaldirectruntime.ValidatePlanCapabilities(plan); err == nil ||
		!strings.Contains(err.Error(), "sysctl name is invalid") {
		t.Fatalf("ValidatePlanCapabilities() error = %v", err)
	}
}

func TestImportedApplicationRuntimeUsesItsCurrentNetworkNamespace(t *testing.T) {
	t.Parallel()

	input, plan := connectivityTestInputAndPlan(t)
	plan.Containers[0].RuntimeID = "runtime-a"

	runtime, err := clabernetesinternaldirectruntime.NewImportedApplicationRuntime(
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
	input.Interfaces = []clabernetesinternaldeviceplan.InterfaceInput{{
		ID: "interface-a", NodeID: "node-a", Name: "eth1", LinkID: "link-a",
		Connectivity: "unknown-transport", WireID: 1,
	}}

	inputDigest, err := input.Digest()
	if err != nil {
		t.Fatal(err)
	}

	plan.InputDigest = inputDigest
	plan.Interfaces = []clabernetesinternaldeviceplan.InterfacePlan{{
		ID: "interface-a", NodeID: "node-a", NamespaceOwnerID: "container-a",
		Name: "eth1", LinkID: "link-a", Connectivity: "unknown-transport", WireID: 1,
		LinkApplyMode: clabernetesinternaldeviceplan.LinkApplyLive,
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err = clabernetesinternaldirectruntime.RunConnectivity(ctx, input, plan, t.TempDir()); err == nil {
		t.Fatal("RunConnectivity() accepted unimplemented interface realization")
	}
}

func TestHostConnectivityRealizesWorkerLegBeforeReadiness(t *testing.T) {
	t.Parallel()

	input, plan := connectivityTestInputAndPlan(t)
	setHostLink(t, &input, &plan)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	operations := &fakeLinkOperations{}

	state := t.TempDir()
	if err := clabernetesinternaldirectruntime.RunConnectivityWithLifecycleOperations(
		ctx,
		input,
		plan,
		clabernetesinternaldirectruntime.ConnectivityOptions{
			StateDirectory: state,
			PodNamespace:   "lab",
			PodName:        "router-pod",
			PodUID:         "pod-uid-a",
		},
		operations,
		nil,
	); err != nil {
		t.Fatal(err)
	}

	if len(operations.hostSpecs) != 1 {
		t.Fatalf("host Link operations = %#v", operations.hostSpecs)
	}

	spec := operations.hostSpecs[0]
	if spec.InterfaceName != "eth1" || spec.HostInterface != "c9s-host-a" || spec.MTU != 1450 ||
		spec.OwnerPrefix == "" || !strings.HasPrefix(spec.Owner, spec.OwnerPrefix) {
		t.Fatalf("host Link spec = %#v", spec)
	}

	if err := clabernetesinternaldirectruntime.ConnectivityReady(plan, state); err != nil {
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

	err := clabernetesinternaldirectruntime.RunConnectivityWithLifecycleOperations(
		ctx,
		input,
		plan,
		clabernetesinternaldirectruntime.ConnectivityOptions{
			StateDirectory: state,
			PodNamespace:   "lab",
			PodName:        "router-pod",
			PodUID:         "pod-uid-a",
		},
		&fakeLinkOperations{hostError: errors.New("collision with foreign host interface")},
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "collision with foreign host interface") {
		t.Fatalf("RunConnectivityWithLifecycleOperations() error = %v", err)
	}

	if err = clabernetesinternaldirectruntime.ConnectivityReady(plan, state); err == nil {
		t.Fatal("failed host endpoint published connectivity readiness")
	}
}

func TestHostConnectivityLiveRemovalSweepsTransportState(t *testing.T) {
	t.Parallel()

	baseInput, basePlan := connectivityTestInputAndPlan(t)
	setHostLink(t, &baseInput, &basePlan)
	desiredInput, desiredPlan := connectivityTestInputAndPlan(t)

	initialRevision, err := clabernetesinternaldirectruntime.NewConnectivityRevision(
		baseInput,
		basePlan,
		baseInput,
		basePlan,
	)
	if err != nil {
		t.Fatal(err)
	}

	desiredRevision, err := clabernetesinternaldirectruntime.NewConnectivityRevision(
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

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	operations := &fakeLinkOperations{sweepKeepCounts: make(chan int, 16)}

	go func() {
		errCh <- clabernetesinternaldirectruntime.RunConnectivityWithLifecycleOperations(
			ctx,
			baseInput,
			basePlan,
			clabernetesinternaldirectruntime.ConnectivityOptions{
				StateDirectory:           t.TempDir(),
				PodNamespace:             "lab",
				PodName:                  "router-pod",
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
	case keep := <-operations.sweepKeepCounts:
		if keep != 1 {
			t.Fatalf("initial transport sweep kept %d owners, want 1", keep)
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
		case keep := <-operations.sweepKeepCounts:
			if keep != 0 {
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
			t.Fatal("connectivity helper did not sweep the removed host Link state")
		}
	}
}

func TestConnectivityHelperRestartRecoversEveryLinkFlavor(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		flavor    string
		configure func(*testing.T, *clabernetesinternaldeviceplan.Input, *clabernetesinternaldeviceplan.Plan)
	}{
		{
			name: "loopback", flavor: "local",
			configure: func(
				t *testing.T,
				input *clabernetesinternaldeviceplan.Input,
				plan *clabernetesinternaldeviceplan.Plan,
			) {
				t.Helper()
				setLoopbackLink(t, input, plan, 1500)
			},
		},
		{
			name: "same-pod", flavor: "local",
			configure: func(
				t *testing.T,
				input *clabernetesinternaldeviceplan.Input,
				plan *clabernetesinternaldeviceplan.Plan,
			) {
				t.Helper()
				setSamePodLink(t, input, plan, "node-b-uid")
			},
		},
		{
			name: "wire", flavor: "fabric",
			configure: func(
				t *testing.T,
				input *clabernetesinternaldeviceplan.Input,
				plan *clabernetesinternaldeviceplan.Plan,
			) {
				t.Helper()
				setWireLink(t, input, plan, "peer-node-uid", "peer-wire", 73, 1450)
			},
		},
		{name: "host", flavor: "host", configure: setHostLink},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			input, plan := connectivityTestInputAndPlan(t)
			testCase.configure(t, &input, &plan)
			state := t.TempDir()
			operations := &fakeLinkOperations{}
			run := func() error {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()

				options := clabernetesinternaldirectruntime.ConnectivityOptions{
					StateDirectory:   state,
					PodNamespace:     "lab",
					PodName:          "router-pod",
					PodUID:           "pod-uid-a",
					FilterOperations: &fakeTransportFilterOperations{},
				}

				return clabernetesinternaldirectruntime.RunConnectivityWithLifecycleOperations(
					ctx, input, plan, options, operations, nil,
				)
			}

			switch testCase.flavor {
			case "local":
				operations.ensurePairError = errors.New("injected local Link failure")
			case "fabric":
				operations.fabricError = errors.New("injected fabric endpoint failure")
			case "host":
				operations.hostError = errors.New("injected host endpoint failure")
			}

			if err := run(); err == nil {
				t.Fatal("partial failure was accepted")
			}

			if err := clabernetesinternaldirectruntime.ConnectivityReady(plan, state); err == nil {
				t.Fatal("partial failure published connectivity readiness")
			}

			operations.ensurePairError = nil
			operations.fabricError = nil
			operations.hostError = nil

			for restart := range 2 {
				if err := run(); err != nil {
					t.Fatalf("helper run %d: %v", restart+1, err)
				}
			}

			if err := clabernetesinternaldirectruntime.ConnectivityReady(plan, state); err != nil {
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
			case "fabric":
				if len(operations.fabricSpecs) != 2 ||
					!reflect.DeepEqual(operations.fabricSpecs[0], operations.fabricSpecs[1]) {
					t.Fatalf("helper restart fabric specs = %#v", operations.fabricSpecs)
				}
			case "host":
				if len(operations.hostSpecs) != 2 ||
					!reflect.DeepEqual(operations.hostSpecs[0], operations.hostSpecs[1]) {
					t.Fatalf("helper restart host specs = %#v", operations.hostSpecs)
				}
			}
		})
	}
}

// TestFabricConnectivityRealizesPodLocalEndpointBeforeReadiness proves cross-Pod endpoints are
// realized inside the Pod on the preserved underlay: the spec must carry the plan interface
// name, the allocated wire id, and the peer transport identity.
func TestFabricConnectivityRealizesPodLocalEndpointBeforeReadiness(t *testing.T) {
	t.Parallel()

	input, plan := connectivityTestInputAndPlan(t)
	setWireLink(t, &input, &plan, "peer-node-uid", "peer-wire", 73, 1450)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	operations := &fakeLinkOperations{}
	filter := &fakeTransportFilterOperations{}

	state := t.TempDir()
	if err := clabernetesinternaldirectruntime.RunConnectivityWithLifecycleOperations(
		ctx,
		input,
		plan,
		clabernetesinternaldirectruntime.ConnectivityOptions{
			StateDirectory:   state,
			PodNamespace:     "lab",
			PodName:          "router-pod",
			PodUID:           "pod-uid-a",
			PodAddress:       "10.244.0.12",
			FilterOperations: filter,
		},
		operations,
		nil,
	); err != nil {
		t.Fatal(err)
	}

	if len(operations.fabricSpecs) != 1 {
		t.Fatalf("fabric operations = %#v", operations.fabricSpecs)
	}

	if len(filter.specs) == 0 || len(filter.specs[0].UDPPorts) != 1 ||
		filter.specs[0].UDPPorts[0] != 14790 {
		t.Fatalf("wire plan must assert the fabric transport accept, got %#v", filter.specs)
	}

	spec := operations.fabricSpecs[0]
	if spec.InterfaceName == "" || spec.WireID != 73 || spec.MTU != 1450 ||
		spec.PeerTransport != "peer-wire" || spec.PodAddress != "10.244.0.12" ||
		spec.OwnerPrefix == "" || !strings.HasPrefix(spec.Owner, spec.OwnerPrefix) {
		t.Fatalf("fabric endpoint spec = %#v", spec)
	}

	if err := clabernetesinternaldirectruntime.ConnectivityReady(plan, state); err != nil {
		t.Fatal(err)
	}
}

// TestFabricConnectivityStaysUnreadyUntilPeerResolves holds readiness back while the endpoint
// transport still waits on its peer.
func TestFabricConnectivityStaysUnreadyUntilPeerResolves(t *testing.T) {
	t.Parallel()

	input, plan := connectivityTestInputAndPlan(t)
	setWireLink(t, &input, &plan, "peer-node-uid", "peer-wire", 73, 1450)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	state := t.TempDir()
	if err := clabernetesinternaldirectruntime.RunConnectivityWithLifecycleOperations(
		ctx,
		input,
		plan,
		clabernetesinternaldirectruntime.ConnectivityOptions{
			StateDirectory:   state,
			PodNamespace:     "lab",
			PodName:          "router-pod",
			PodUID:           "pod-uid-a",
			PodAddress:       "10.244.0.12",
			FilterOperations: &fakeTransportFilterOperations{},
		},
		&fakeLinkOperations{fabricUnready: true},
		nil,
	); err != nil {
		t.Fatal(err)
	}

	if err := clabernetesinternaldirectruntime.ConnectivityReady(plan, state); err == nil {
		t.Fatal("pending fabric transport published connectivity readiness")
	}
}

func TestDirectRuntimeRequiresImmutableApplicationImages(t *testing.T) {
	t.Parallel()

	_, plan := connectivityTestInputAndPlan(t)

	plan.Containers[0].ImageDigest = ""
	if err := clabernetesinternaldirectruntime.ValidatePlanCapabilities(plan); err == nil ||
		!strings.Contains(err.Error(), "immutable image digest") {
		t.Fatalf("ValidatePlanCapabilities() error = %v", err)
	}
}

func TestImportedEndpointLifecycleRejectsNonDistinctHostNamespace(t *testing.T) {
	t.Parallel()

	input, plan := connectivityTestInputAndPlan(t)
	plan.Actions = []clabernetesinternaldeviceplan.Action{{
		ID:    "imported-deploy-endpoints/node-a",
		Phase: clabernetesinternaldeviceplan.PhaseInterfaceFixup,
		Target: clabernetesinternaldeviceplan.ActionTarget{
			NodeID: "node-a", ContainerID: "container-a", NamespaceOwnerID: "container-a",
		},
		Kind:                    clabernetesinternaldeviceplan.ActionImportedDeployEndpoints,
		ImportedDeployEndpoints: &clabernetesinternaldeviceplan.ImportedDeployEndpointsAction{},
	}}
	err := clabernetesinternaldirectruntime.RunConnectivityWithOptions(
		context.Background(),
		input,
		plan,
		clabernetesinternaldirectruntime.ConnectivityOptions{
			StateDirectory: t.TempDir(), ArtifactRoot: t.TempDir(), Revision: "test",
			HostNetworkNamespacePath: "/proc/self/ns/net",
		},
	)

	var capabilityErr *clabernetesinternaldeviceplan.Error
	if !errors.As(err, &capabilityErr) ||
		capabilityErr.Code != clabernetesinternaldeviceplan.ErrorUnsupported ||
		capabilityErr.Field != "runtime.networkNamespace" ||
		capabilityErr.Behavior != "host-network-namespace" {
		t.Fatalf("RunConnectivityWithOptions() capability error = %#v, %v", capabilityErr, err)
	}
}

func TestManagementConnectivityAddsPackageSelectedAddressesBeforeReadiness(t *testing.T) {
	t.Parallel()

	input, plan := connectivityTestInputAndPlan(t)
	input.Management = []clabernetesinternaldeviceplan.ManagementInput{{
		NodeID: "node-a", IPv4: "192.0.2.10/24", IPv6: "2001:db8::10/64",
	}}

	digest, err := input.Digest()
	if err != nil {
		t.Fatal(err)
	}

	plan.InputDigest = digest
	plan.Management = []clabernetesinternaldeviceplan.ManagementPlan{{
		ID: "management/node-a", NodeID: "node-a",
		InterfaceSelector: clabernetesinternaldeviceplan.ManagementInterfacePodTransport,
		IPv4:              "192.0.2.10/24", IPv6: "2001:db8::10/64",
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	operations := &fakeLinkOperations{podTransport: "resolved-pod-interface"}

	state := t.TempDir()
	if err = clabernetesinternaldirectruntime.RunConnectivityWithLifecycleOperations(
		ctx,
		input,
		plan,
		clabernetesinternaldirectruntime.ConnectivityOptions{
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

	if err = clabernetesinternaldirectruntime.ConnectivityReady(plan, state); err != nil {
		t.Fatal(err)
	}
}

func TestManagementConnectivityRejectsPodTransportPrefixOverlapBeforeAddressMutation(t *testing.T) {
	t.Parallel()

	input, plan := connectivityTestInputAndPlan(t)
	input.Management = []clabernetesinternaldeviceplan.ManagementInput{{
		NodeID: "node-a", IPv4: "10.244.0.10/16",
	}}

	digest, err := input.Digest()
	if err != nil {
		t.Fatal(err)
	}

	plan.InputDigest = digest
	plan.Management = []clabernetesinternaldeviceplan.ManagementPlan{{
		ID: "management/node-a", NodeID: "node-a",
		InterfaceSelector: clabernetesinternaldeviceplan.ManagementInterfacePodTransport,
		IPv4:              "10.244.0.10/16",
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	operations := &fakeLinkOperations{podTransport: "resolved-pod-interface"}
	err = clabernetesinternaldirectruntime.RunConnectivityWithLifecycleOperations(
		ctx,
		input,
		plan,
		clabernetesinternaldirectruntime.ConnectivityOptions{
			StateDirectory: t.TempDir(), PodAddress: "10.244.2.180",
		},
		operations,
		nil,
	)

	var capabilityErr *clabernetesinternaldeviceplan.Error
	if !errors.As(err, &capabilityErr) ||
		capabilityErr.Code != clabernetesinternaldeviceplan.ErrorUnsupported ||
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
	input.Management = []clabernetesinternaldeviceplan.ManagementInput{{
		NodeID: "node-a", IPv4: "192.0.2.10/24", IPv4Gateway: "192.0.2.1",
	}}

	digest, err := input.Digest()
	if err != nil {
		t.Fatal(err)
	}

	plan.InputDigest = digest
	plan.Management = []clabernetesinternaldeviceplan.ManagementPlan{{
		ID: "management/node-a", NodeID: "node-a", InterfaceName: "eth0",
		IPv4: "192.0.2.10/24", IPv4Gateway: "192.0.2.1",
		Routes: []clabernetesinternaldeviceplan.Route{{
			Destination: "198.51.100.0/24", Gateway: "192.0.2.1", Metric: 7,
		}},
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	operations := &fakeLinkOperations{}
	if err = clabernetesinternaldirectruntime.RunConnectivityWithOperations(
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
	input.Management = []clabernetesinternaldeviceplan.ManagementInput{{
		NodeID: "node-a", IPv4: "192.0.2.10/24",
	}}

	digest, err := input.Digest()
	if err != nil {
		t.Fatal(err)
	}

	plan.InputDigest = digest
	plan.Management = []clabernetesinternaldeviceplan.ManagementPlan{{
		ID: "management/node-a", NodeID: "node-a", InterfaceName: "eth0",
		IPv4: "192.0.2.11/24",
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = clabernetesinternaldirectruntime.RunConnectivityWithOperations(
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
	input.Interfaces = []clabernetesinternaldeviceplan.InterfaceInput{
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

	plan.Interfaces = []clabernetesinternaldeviceplan.InterfacePlan{
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
			LinkApplyMode:    clabernetesinternaldeviceplan.LinkApplyLive,
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
			LinkApplyMode:    clabernetesinternaldeviceplan.LinkApplyLive,
			RequiredAtStart:  true,
		},
	}
	for _, intf := range plan.Interfaces {
		plan.Actions = append(plan.Actions, clabernetesinternaldeviceplan.Action{
			ID: "wait/" + intf.ID, Phase: clabernetesinternaldeviceplan.PhasePreStart,
			Target: clabernetesinternaldeviceplan.ActionTarget{
				NodeID: "node-a", ContainerID: "container-a", NamespaceOwnerID: "container-a",
			},
			Kind: clabernetesinternaldeviceplan.ActionWaitInterface,
			WaitInterface: &clabernetesinternaldeviceplan.WaitInterfaceAction{
				InterfaceID: intf.ID, TimeoutSeconds: 30,
			},
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	operations := &fakeLinkOperations{}

	state := t.TempDir()
	if err = clabernetesinternaldirectruntime.RunConnectivityWithLifecycleOperations(
		ctx,
		input,
		plan,
		clabernetesinternaldirectruntime.ConnectivityOptions{
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

	if err = clabernetesinternaldirectruntime.ConnectivityReady(plan, state); err != nil {
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

	options := clabernetesinternaldirectruntime.ConnectivityOptions{
		StateDirectory: t.TempDir(),
		PodUID:         "pod-uid-a",
	}
	if err := clabernetesinternaldirectruntime.RunConnectivityWithLifecycleOperations(
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
	if err := clabernetesinternaldirectruntime.RunConnectivityWithLifecycleOperations(
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

	options := clabernetesinternaldirectruntime.ConnectivityOptions{
		StateDirectory: t.TempDir(),
		PodUID:         "pod-uid-a",
	}
	if err := clabernetesinternaldirectruntime.RunConnectivityWithLifecycleOperations(
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
		clabernetesinternaldirectruntime.VethInterface{
			Name: "foreign-a", PeerName: "foreign-b", Owner: "foreign-owner",
		},
		clabernetesinternaldirectruntime.VethInterface{
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
	if err = clabernetesinternaldirectruntime.RunConnectivityWithLifecycleOperations(
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
	if err := clabernetesinternaldirectruntime.RunConnectivityWithLifecycleOperations(
		ctx,
		input,
		plan,
		clabernetesinternaldirectruntime.ConnectivityOptions{
			StateDirectory: t.TempDir(), PodUID: "former-pod-uid",
		},
		operations,
		nil,
	); err != nil {
		t.Fatal(err)
	}

	formerOwner := strings.SplitN(operations.pairs[0], "/", 4)[3]

	operations.pairs = nil
	if err := clabernetesinternaldirectruntime.RunConnectivityWithLifecycleOperations(
		ctx,
		input,
		plan,
		clabernetesinternaldirectruntime.ConnectivityOptions{
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

	options := clabernetesinternaldirectruntime.ConnectivityOptions{
		StateDirectory: t.TempDir(), PodUID: "pod-uid-a",
	}
	if err := clabernetesinternaldirectruntime.RunConnectivityWithLifecycleOperations(
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

	if err := clabernetesinternaldirectruntime.RunConnectivityWithLifecycleOperations(
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

	plan.Interfaces = []clabernetesinternaldeviceplan.InterfacePlan{
		{
			ID: "link/a", NodeID: "node-a", NamespaceOwnerID: "container-a",
			Name: "eth1", LinkID: "link-uid", PeerNodeID: "node-a",
			Connectivity: "same-pod", LinkApplyMode: clabernetesinternaldeviceplan.LinkApplyLive,
		},
		{
			ID: "link/b", NodeID: "node-a", NamespaceOwnerID: "container-a",
			Name: "eth2", LinkID: "link-uid", PeerNodeID: "node-a",
			Connectivity: "loopback", LinkApplyMode: clabernetesinternaldeviceplan.LinkApplyLive,
		},
	}
	if err := clabernetesinternaldirectruntime.ValidatePlanCapabilities(plan); err == nil ||
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

	err := clabernetesinternaldirectruntime.RunConnectivityWithLifecycleOperations(
		ctx,
		input,
		plan,
		clabernetesinternaldirectruntime.ConnectivityOptions{
			StateDirectory: state,
			PodUID:         "pod-uid-a",
		},
		&fakeLinkOperations{ensurePairError: conflict},
		nil,
	)
	if !errors.Is(err, conflict) {
		t.Fatalf("RunConnectivityWithLifecycleOperations() error = %v", err)
	}

	if readyErr := clabernetesinternaldirectruntime.ConnectivityReady(plan, state); readyErr == nil {
		t.Fatal("connectivity helper published readiness after an interface conflict")
	}
}

func TestConnectivityRevisionReproducesPlannerVerifiedLiveInterfacePlan(t *testing.T) {
	t.Parallel()

	baseInput, basePlan := connectivityTestInputAndPlan(t)
	desiredInput, desiredPlan := connectivityTestInputAndPlan(t)
	setLoopbackLink(t, &desiredInput, &desiredPlan, 1500)

	revision, err := clabernetesinternaldirectruntime.NewConnectivityRevision(
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

	decoded, err := clabernetesinternaldirectruntime.DecodeConnectivityRevision(raw)
	if err != nil {
		t.Fatal(err)
	}

	appliedInput, appliedPlan, err := clabernetesinternaldirectruntime.ApplyConnectivityRevision(
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

	basePlan.Containers[0].Environment = []clabernetesinternaldeviceplan.KeyValue{{
		Name: "package-created-endpoint-count", Value: "0",
	}}
	desiredPlan.Containers[0].Environment = []clabernetesinternaldeviceplan.KeyValue{{
		Name: "package-created-endpoint-count", Value: "2",
	}}

	revision, err := clabernetesinternaldirectruntime.NewConnectivityRevision(
		baseInput,
		basePlan,
		desiredInput,
		desiredPlan,
	)
	if err != nil {
		t.Fatal(err)
	}

	_, appliedPlan, err := clabernetesinternaldirectruntime.ApplyConnectivityRevision(
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

	_, err = clabernetesinternaldirectruntime.NewConnectivityRevision(
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

	_, err := clabernetesinternaldirectruntime.NewConnectivityRevision(
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
	desiredPlan.Interfaces[0].LinkApplyMode = clabernetesinternaldeviceplan.LinkApplyRecreate

	_, err := clabernetesinternaldirectruntime.NewConnectivityRevision(
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
		desiredPlan.Interfaces[index].LinkApplyMode = clabernetesinternaldeviceplan.LinkApplyRestart
	}

	revision, err := clabernetesinternaldirectruntime.NewConnectivityRevisionForMode(
		baseInput,
		basePlan,
		desiredInput,
		desiredPlan,
		clabernetesinternaldeviceplan.LinkApplyRestart,
	)
	if err != nil {
		t.Fatal(err)
	}

	if revision.MaximumMode != clabernetesinternaldeviceplan.LinkApplyRestart {
		t.Fatalf("Restart revision = %#v", revision)
	}

	if _, _, err = clabernetesinternaldirectruntime.ApplyConnectivityRevision(
		baseInput,
		basePlan,
		revision,
	); err != nil {
		t.Fatal(err)
	}

	for index := range desiredPlan.Interfaces {
		desiredPlan.Interfaces[index].LinkApplyMode = clabernetesinternaldeviceplan.LinkApplyRecreate
	}

	if _, err = clabernetesinternaldirectruntime.NewConnectivityRevisionForMode(
		baseInput,
		basePlan,
		desiredInput,
		desiredPlan,
		clabernetesinternaldeviceplan.LinkApplyRestart,
	); err == nil || !strings.Contains(err.Error(), "Recreate") {
		t.Fatalf("Restart projection accepted Recreate: %v", err)
	}
}

func TestEvaluateConnectivityTransitionUsesMostDisruptiveAffectedPlanMode(t *testing.T) {
	t.Parallel()

	for _, mode := range []clabernetesinternaldeviceplan.LinkApplyMode{
		clabernetesinternaldeviceplan.LinkApplyLive,
		clabernetesinternaldeviceplan.LinkApplyRestart,
		clabernetesinternaldeviceplan.LinkApplyRecreate,
	} {
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()

			baseInput, basePlan := connectivityTestInputAndPlan(t)
			desiredInput, desiredPlan := connectivityTestInputAndPlan(t)
			setLoopbackLink(t, &desiredInput, &desiredPlan, 1500)

			for index := range desiredPlan.Interfaces {
				desiredPlan.Interfaces[index].LinkApplyMode = mode
			}

			desiredPlan.Containers[0].Environment = []clabernetesinternaldeviceplan.KeyValue{{
				Name: "package-created-endpoint-count", Value: "2",
			}}

			transition, err := clabernetesinternaldirectruntime.EvaluateConnectivityTransition(
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

	unchanged, err := clabernetesinternaldirectruntime.EvaluateConnectivityTransition(
		baseInput,
		basePlan,
		baseInput,
		basePlan,
	)
	if err != nil {
		t.Fatal(err)
	}

	if unchanged.Changed || unchanged.RequiredMode != clabernetesinternaldeviceplan.LinkApplyLive {
		t.Fatalf("unchanged connectivity transition = %#v", unchanged)
	}
}

func TestEvaluateConnectivityTransitionUsesRemovedEndpointPlanMode(t *testing.T) {
	t.Parallel()

	for _, mode := range []clabernetesinternaldeviceplan.LinkApplyMode{
		clabernetesinternaldeviceplan.LinkApplyLive,
		clabernetesinternaldeviceplan.LinkApplyRestart,
		clabernetesinternaldeviceplan.LinkApplyRecreate,
	} {
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()

			baseInput, basePlan := connectivityTestInputAndPlan(t)
			desiredInput, desiredPlan := connectivityTestInputAndPlan(t)
			setLoopbackLink(t, &baseInput, &basePlan, 1500)

			for index := range basePlan.Interfaces {
				basePlan.Interfaces[index].LinkApplyMode = mode
			}

			transition, err := clabernetesinternaldirectruntime.EvaluateConnectivityTransition(
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
		clabernetesinternaldeviceplan.LinkApplyLive,
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
		clabernetesinternaldeviceplan.LinkApplyRestart,
	)

	transition, err := clabernetesinternaldirectruntime.EvaluateConnectivityTransition(
		baseInput,
		basePlan,
		desiredInput,
		desiredPlan,
	)
	if err != nil {
		t.Fatal(err)
	}

	if !transition.Changed ||
		transition.RequiredMode != clabernetesinternaldeviceplan.LinkApplyRestart {
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
		basePlan.Interfaces[index].LinkApplyMode = clabernetesinternaldeviceplan.LinkApplyRecreate
		desiredPlan.Interfaces[index].LinkApplyMode = clabernetesinternaldeviceplan.LinkApplyRecreate
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
		clabernetesinternaldeviceplan.LinkApplyLive,
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
		clabernetesinternaldeviceplan.LinkApplyLive,
	)

	if _, err := clabernetesinternaldirectruntime.NewConnectivityRevision(
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

	revision, err := clabernetesinternaldirectruntime.NewConnectivityRevision(
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
		if _, err = clabernetesinternaldirectruntime.DecodeConnectivityRevision(invalid); err == nil {
			t.Fatalf("DecodeConnectivityRevision() accepted %s", invalid)
		}
	}
}

func TestConnectivityHelperAppliesProjectedLiveRevisionWithoutRestart(t *testing.T) {
	t.Parallel()

	baseInput, basePlan := connectivityTestInputAndPlan(t)
	desiredInput, desiredPlan := connectivityTestInputAndPlan(t)
	setLoopbackLink(t, &desiredInput, &desiredPlan, 9000)

	initial, err := clabernetesinternaldirectruntime.NewConnectivityRevision(
		baseInput,
		basePlan,
		baseInput,
		basePlan,
	)
	if err != nil {
		t.Fatal(err)
	}

	desired, err := clabernetesinternaldirectruntime.NewConnectivityRevision(
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
		errCh <- clabernetesinternaldirectruntime.RunConnectivityWithLifecycleOperations(
			ctx,
			baseInput,
			basePlan,
			clabernetesinternaldirectruntime.ConnectivityOptions{
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

	for clabernetesinternaldirectruntime.ConnectivityReady(basePlan, state) != nil {
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

	for clabernetesinternaldirectruntime.ConnectivityReadyWithRevision(
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

func setWireLink(
	t *testing.T,
	input *clabernetesinternaldeviceplan.Input,
	plan *clabernetesinternaldeviceplan.Plan,
	peerNodeID,
	peerTransport string,
	wireID,
	mtu int,
) {
	t.Helper()

	input.Interfaces = []clabernetesinternaldeviceplan.InterfaceInput{{
		ID: "link-a/a", NodeID: "node-a", Name: "requested-a", LinkID: "link-uid-a",
		PeerNodeID: peerNodeID, PeerInterface: "requested-b", PeerTransport: peerTransport,
		Connectivity: "wire", WireID: wireID, MTU: mtu,
	}}

	digest, err := input.Digest()
	if err != nil {
		t.Fatal(err)
	}

	plan.InputDigest = digest
	plan.Interfaces = []clabernetesinternaldeviceplan.InterfacePlan{{
		ID: "link-a/a", NodeID: "node-a", NamespaceOwnerID: "container-a",
		Name: "package-a", LinkID: "link-uid-a", PeerNodeID: peerNodeID,
		PeerInterface: "requested-b", PeerTransport: peerTransport,
		Connectivity: "wire", WireID: wireID, MTU: mtu,
		LinkApplyMode: clabernetesinternaldeviceplan.LinkApplyLive, RequiredAtStart: true,
	}}
	plan.Actions = []clabernetesinternaldeviceplan.Action{{
		ID: "wait/link-a/a", Phase: clabernetesinternaldeviceplan.PhasePreStart,
		Target: clabernetesinternaldeviceplan.ActionTarget{
			NodeID: "node-a", ContainerID: "container-a", NamespaceOwnerID: "container-a",
		},
		Kind: clabernetesinternaldeviceplan.ActionWaitInterface,
		WaitInterface: &clabernetesinternaldeviceplan.WaitInterfaceAction{
			InterfaceID: "link-a/a", TimeoutSeconds: 30,
		},
	}}
}

func setLoopbackLink(
	t *testing.T,
	input *clabernetesinternaldeviceplan.Input,
	plan *clabernetesinternaldeviceplan.Plan,
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
		clabernetesinternaldeviceplan.LinkApplyLive,
	)
}

func appendLoopbackLink(
	t *testing.T,
	input *clabernetesinternaldeviceplan.Input,
	plan *clabernetesinternaldeviceplan.Plan,
	idPrefix,
	linkID,
	leftName,
	rightName string,
	mtu int,
	linkApplyMode clabernetesinternaldeviceplan.LinkApplyMode,
) {
	t.Helper()

	input.Interfaces = append(input.Interfaces,
		clabernetesinternaldeviceplan.InterfaceInput{
			ID: idPrefix + "/a", NodeID: "node-a", Name: "requested-a", LinkID: linkID,
			PeerNodeID: "node-a", PeerInterface: "requested-b",
			Connectivity: "loopback", MTU: mtu,
		},
		clabernetesinternaldeviceplan.InterfaceInput{
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
		clabernetesinternaldeviceplan.InterfacePlan{
			ID: idPrefix + "/a", NodeID: "node-a", NamespaceOwnerID: "container-a",
			Name: leftName, LinkID: linkID, PeerNodeID: "node-a",
			PeerInterface: "requested-b", Connectivity: "loopback", MTU: mtu,
			LinkApplyMode: linkApplyMode, RequiredAtStart: true,
		},
		clabernetesinternaldeviceplan.InterfacePlan{
			ID: idPrefix + "/b", NodeID: "node-a", NamespaceOwnerID: "container-a",
			Name: rightName, LinkID: linkID, PeerNodeID: "node-a",
			PeerInterface: "requested-a", Connectivity: "loopback", MTU: mtu,
			LinkApplyMode: linkApplyMode, RequiredAtStart: true,
		},
	)
	for _, intf := range plan.Interfaces[len(plan.Interfaces)-2:] {
		plan.Actions = append(plan.Actions, clabernetesinternaldeviceplan.Action{
			ID: "wait/" + intf.ID, Phase: clabernetesinternaldeviceplan.PhasePreStart,
			Target: clabernetesinternaldeviceplan.ActionTarget{
				NodeID: "node-a", ContainerID: "container-a", NamespaceOwnerID: "container-a",
			},
			Kind: clabernetesinternaldeviceplan.ActionWaitInterface,
			WaitInterface: &clabernetesinternaldeviceplan.WaitInterfaceAction{
				InterfaceID: intf.ID, TimeoutSeconds: 30,
			},
		})
	}
}

func setSamePodLink(
	t *testing.T,
	input *clabernetesinternaldeviceplan.Input,
	plan *clabernetesinternaldeviceplan.Plan,
	rightNodeID string,
) {
	t.Helper()

	input.Nodes = append(input.Nodes, clabernetesinternaldeviceplan.NodeInput{
		ID: rightNodeID, Name: "router-b", Kind: "package-kind",
		GroupOwner: "node-a",
		Definition: []byte(`{"kind":"package-kind","image":"example/device:1"}`),
	})
	input.Images = append(input.Images, clabernetesinternaldeviceplan.ImageInput{
		NodeID: rightNodeID, Role: "device", SourceReference: "example/device:1",
		DigestReference: "example/device@sha256:aaaaaaaa",
		Platform:        clabernetesinternaldeviceplan.Platform{OS: "linux", Architecture: "amd64"},
	})
	input.Interfaces = []clabernetesinternaldeviceplan.InterfaceInput{
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
	plan.Nodes = append(plan.Nodes, clabernetesinternaldeviceplan.NodePlan{
		ID: rightNodeID, Name: "router-b", Kind: "package-kind",
		ContainerIDs: []string{"container-b"}, ReadinessContainerIDs: []string{"container-b"},
	})
	plan.Containers = append(plan.Containers, clabernetesinternaldeviceplan.ContainerPlan{
		ID: "container-b", NodeID: rightNodeID, NamespaceOwnerID: "container-b",
		Image:       "example/device:1",
		ImageDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Required:    true,
	})
	plan.Interfaces = []clabernetesinternaldeviceplan.InterfacePlan{
		{
			ID: "link-a/a", NodeID: "node-a", NamespaceOwnerID: "container-a",
			Name: "package-a", LinkID: "link-uid-a", PeerNodeID: rightNodeID,
			PeerInterface: "requested-b", Connectivity: "same-pod", MTU: 1500,
			LinkApplyMode: clabernetesinternaldeviceplan.LinkApplyLive, RequiredAtStart: true,
		},
		{
			ID: "link-a/b", NodeID: rightNodeID, NamespaceOwnerID: "container-b",
			Name: "package-b", LinkID: "link-uid-a", PeerNodeID: "node-a",
			PeerInterface: "requested-a", Connectivity: "same-pod", MTU: 1500,
			LinkApplyMode: clabernetesinternaldeviceplan.LinkApplyLive, RequiredAtStart: true,
		},
	}

	plan.Actions = nil
	for index, intf := range plan.Interfaces {
		containerID := "container-a"
		if index == 1 {
			containerID = "container-b"
		}

		plan.Actions = append(plan.Actions, clabernetesinternaldeviceplan.Action{
			ID: "wait/" + intf.ID, Phase: clabernetesinternaldeviceplan.PhasePreStart,
			Target: clabernetesinternaldeviceplan.ActionTarget{
				NodeID: intf.NodeID, ContainerID: containerID,
				NamespaceOwnerID: intf.NamespaceOwnerID,
			},
			Kind: clabernetesinternaldeviceplan.ActionWaitInterface,
			WaitInterface: &clabernetesinternaldeviceplan.WaitInterfaceAction{
				InterfaceID: intf.ID, TimeoutSeconds: 30,
			},
		})
	}
}

func connectivityTestInputAndPlan(
	t *testing.T,
) (clabernetesinternaldeviceplan.Input, clabernetesinternaldeviceplan.Plan) {
	t.Helper()

	compatibility := clabernetesinternaldeviceplan.Compatibility{
		ContainerlabModule:  clabernetesinternaldeviceplan.ContainerlabModulePath,
		ContainerlabVersion: "v-test", PlanSchemaVersion: clabernetesinternaldeviceplan.SchemaVersion,
		RegistryDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	input := clabernetesinternaldeviceplan.Input{
		SchemaVersion: clabernetesinternaldeviceplan.SchemaVersion, TopologyName: "lab",
		Compatibility: compatibility,
		Nodes: []clabernetesinternaldeviceplan.NodeInput{{
			ID: "node-a", Name: "router", Kind: "package-kind",
			Definition: []byte(`{"kind":"package-kind","image":"example/device:1"}`),
		}},
		Images: []clabernetesinternaldeviceplan.ImageInput{{
			NodeID: "node-a", Role: "device", SourceReference: "example/device:1",
			DigestReference: "example/device@sha256:aaaaaaaa",
			Platform: clabernetesinternaldeviceplan.Platform{
				OS:           "linux",
				Architecture: "amd64",
			},
		}},
	}

	digest, err := input.Digest()
	if err != nil {
		t.Fatal(err)
	}

	plan := clabernetesinternaldeviceplan.Plan{
		SchemaVersion: clabernetesinternaldeviceplan.SchemaVersion, Compatibility: compatibility,
		InputDigest: digest,
		Planner: clabernetesinternaldeviceplan.PlannerIdentity{
			Name:     "clabernetes",
			Revision: "test",
		},
		Nodes: []clabernetesinternaldeviceplan.NodePlan{{
			ID: "node-a", Name: "router", Kind: "package-kind",
			ContainerIDs: []string{"container-a"}, ReadinessContainerIDs: []string{"container-a"},
		}},
		Containers: []clabernetesinternaldeviceplan.ContainerPlan{{
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
	input *clabernetesinternaldeviceplan.Input,
	plan *clabernetesinternaldeviceplan.Plan,
) {
	t.Helper()

	input.Interfaces = []clabernetesinternaldeviceplan.InterfaceInput{{
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
	plan.Interfaces = []clabernetesinternaldeviceplan.InterfacePlan{{
		ID:               "link-uid-a/a",
		NodeID:           "node-a",
		NamespaceOwnerID: "container-a",
		Name:             "eth1",
		LinkID:           "link-uid-a",
		LinkName:         "host-link-a",
		PeerInterface:    "c9s-host-a",
		Connectivity:     "host",
		MTU:              1450,
		LinkApplyMode:    clabernetesinternaldeviceplan.LinkApplyLive,
		RequiredAtStart:  true,
	}}
	plan.Actions = []clabernetesinternaldeviceplan.Action{{
		ID:    "wait/link-uid-a/a",
		Phase: clabernetesinternaldeviceplan.PhasePreStart,
		Target: clabernetesinternaldeviceplan.ActionTarget{
			NodeID: "node-a", ContainerID: "container-a", NamespaceOwnerID: "container-a",
		},
		Kind: clabernetesinternaldeviceplan.ActionWaitInterface,
		WaitInterface: &clabernetesinternaldeviceplan.WaitInterfaceAction{
			InterfaceID: "link-uid-a/a", TimeoutSeconds: 30,
		},
	}}
}

type fakeNATOperations struct {
	specs []clabernetesinternaldirectruntime.InterpositionNATSpec
	err   error
}

func (f *fakeNATOperations) EnsureInterpositionNAT(
	spec clabernetesinternaldirectruntime.InterpositionNATSpec,
) error {
	if f.err != nil {
		return f.err
	}

	f.specs = append(f.specs, spec)

	return nil
}

func (f *fakeNATOperations) DeleteInterpositionNAT() error {
	return nil
}

type fakeTransportFilterOperations struct {
	specs []clabernetesinternaldirectruntime.TransportFilterSpec
	err   error
}

func (f *fakeTransportFilterOperations) EnsureTransportFilterAccepts(
	spec clabernetesinternaldirectruntime.TransportFilterSpec,
) error {
	if f.err != nil {
		return f.err
	}

	f.specs = append(f.specs, spec)

	return nil
}

func interposedConnectivityFixture(
	t *testing.T,
) (clabernetesinternaldeviceplan.Input, clabernetesinternaldeviceplan.Plan) {
	t.Helper()

	input, plan := connectivityTestInputAndPlan(t)
	input.Management = []clabernetesinternaldeviceplan.ManagementInput{{
		NodeID: "node-a", IPv4: "172.20.20.11/24", IPv4Gateway: "172.20.20.1",
	}}

	digest, err := input.Digest()
	if err != nil {
		t.Fatal(err)
	}

	plan.InputDigest = digest
	plan.Management = []clabernetesinternaldeviceplan.ManagementPlan{{
		ID: "management/node-a", NodeID: "node-a",
		InterfaceSelector: clabernetesinternaldeviceplan.ManagementInterfaceInterposed,
		IPv4:              "172.20.20.11/24", IPv4Gateway: "172.20.20.1",
		Interposition: &clabernetesinternaldeviceplan.ManagementInterposition{
			DeviceInterface: "eth0",
			DeviceMAC:       "00:1c:73:c9:50:31",
			InboundPorts: []clabernetesinternaldeviceplan.ManagementPortMap{
				{Protocol: "tcp", PodPort: 22, DevicePort: 22},
			},
			Mesh: &clabernetesinternaldeviceplan.ManagementMesh{
				TunnelID:    16_100_007,
				GatewayMAC:  "02:c9:aa:bb:cc:dd",
				PeerService: "c9s-management-mesh",
			},
		},
	}}

	return input, plan
}

func TestInterposedManagementRealizesPodLocallyBeforeReadiness(t *testing.T) {
	t.Parallel()

	input, plan := interposedConnectivityFixture(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	operations := &fakeLinkOperations{}
	nat := &fakeNATOperations{}
	filter := &fakeTransportFilterOperations{}

	state := t.TempDir()
	if err := clabernetesinternaldirectruntime.RunConnectivityWithLifecycleOperations(
		ctx,
		input,
		plan,
		clabernetesinternaldirectruntime.ConnectivityOptions{
			StateDirectory:   state,
			PodAddress:       "10.244.0.12",
			NATOperations:    nat,
			FilterOperations: filter,
		},
		operations,
		nil,
	); err != nil {
		t.Fatal(err)
	}

	if len(filter.specs) == 0 || len(filter.specs[0].UDPPorts) != 1 ||
		filter.specs[0].UDPPorts[0] != 14789 {
		t.Fatalf("mesh plan must assert the mesh transport accept, got %#v", filter.specs)
	}

	if len(operations.interpositions) != 1 {
		t.Fatalf("interposition operations = %#v", operations.interpositions)
	}

	spec := operations.interpositions[0]
	if spec.DeviceInterface != "eth0" ||
		spec.DeviceMAC != "00:1c:73:c9:50:31" ||
		spec.ManagementIPv4 != "172.20.20.11/24" ||
		spec.GatewayIPv4 != "172.20.20.1" ||
		spec.TransportInterface != clabernetesinternaldirectruntime.TransportInterfaceName ||
		spec.RouterInterface != clabernetesinternaldirectruntime.RouterInterfaceName ||
		spec.PodAddress != "10.244.0.12" ||
		spec.MeshTunnelID != 16_100_007 ||
		spec.MeshGatewayMAC != "02:c9:aa:bb:cc:dd" ||
		spec.MeshPeerService != "c9s-management-mesh" {
		t.Fatalf("interposition spec = %#v", spec)
	}

	if len(nat.specs) != 1 {
		t.Fatalf("translation operations = %#v", nat.specs)
	}

	natSpec := nat.specs[0]
	if natSpec.ManagementAddress != "172.20.20.11" ||
		natSpec.ManagementSubnet != "172.20.20.0/24" ||
		natSpec.PodAddress != "10.244.0.12" ||
		len(natSpec.InboundPorts) != 1 || natSpec.InboundPorts[0].DevicePort != 22 {
		t.Fatalf("translation spec = %#v", natSpec)
	}

	// The interposed identity must not be double-realized on the Pod transport interface.
	if len(operations.managementAddresses) != 0 {
		t.Fatalf("interposed identity leaked to Pod transport: %#v", operations.managementAddresses)
	}

	if err := clabernetesinternaldirectruntime.ConnectivityReady(plan, state); err != nil {
		t.Fatal(err)
	}
}

func TestInterposedManagementFailsClosed(t *testing.T) {
	t.Parallel()

	for name, mutate := range map[string]func(
		*clabernetesinternaldirectruntime.ConnectivityOptions,
		*fakeLinkOperations,
		*fakeNATOperations,
	){
		"namespace preparation fails": func(
			_ *clabernetesinternaldirectruntime.ConnectivityOptions,
			operations *fakeLinkOperations,
			_ *fakeNATOperations,
		) {
			operations.interpositionError = errors.New("device leg is unavailable")
		},
		"translation fails": func(
			_ *clabernetesinternaldirectruntime.ConnectivityOptions,
			_ *fakeLinkOperations,
			nat *fakeNATOperations,
		) {
			nat.err = errors.New("translation backend is unavailable")
		},
		"pod address missing": func(
			options *clabernetesinternaldirectruntime.ConnectivityOptions,
			_ *fakeLinkOperations,
			_ *fakeNATOperations,
		) {
			options.PodAddress = ""
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			input, plan := interposedConnectivityFixture(t)

			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			operations := &fakeLinkOperations{}
			nat := &fakeNATOperations{}
			options := clabernetesinternaldirectruntime.ConnectivityOptions{
				StateDirectory: t.TempDir(),
				PodAddress:     "10.244.0.12",
				NATOperations:  nat,
			}
			mutate(&options, operations, nat)

			err := clabernetesinternaldirectruntime.RunConnectivityWithLifecycleOperations(
				ctx,
				input,
				plan,
				options,
				operations,
				nil,
			)
			if err == nil {
				t.Fatal("interposition did not fail closed")
			}

			if err = clabernetesinternaldirectruntime.ConnectivityReady(
				plan,
				options.StateDirectory,
			); err == nil {
				t.Fatal("connectivity readiness was published despite interposition failure")
			}

			if name != "pod address missing" {
				conditions, readErr := os.ReadFile(filepath.Join(
					options.StateDirectory,
					clabernetesinternaldirectruntime.InterpositionConditionsFile,
				))
				if readErr != nil || !strings.Contains(string(conditions), "=False:") {
					t.Fatalf(
						"failed interposition recorded no failing condition: %s err=%v",
						conditions,
						readErr,
					)
				}
			}
		})
	}
}
