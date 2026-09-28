//nolint:noinlineerr,wsl_v5 // Reconciler tests use compact fail-fast assertions.
package node //nolint:testpackage // tests exercise unexported reconciliation helpers

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	k8sappsv1 "k8s.io/api/apps/v1"
	k8scorev1 "k8s.io/api/core/v1"
	k8srbacv1 "k8s.io/api/rbac/v1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	apimachinerymeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	apimachineryschema "k8s.io/apimachinery/pkg/runtime/schema"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type conflictOnceClient struct {
	ctrlruntimeclient.Client

	updateCalls    int
	beforeConflict func(context.Context, ctrlruntimeclient.Object) error
}

type conflictOnceStatusWriter struct {
	ctrlruntimeclient.StatusWriter

	client *conflictOnceClient
}

type countingNodeReader struct {
	ctrlruntimeclient.Reader

	getCalls int
}

func (r *countingNodeReader) Get(
	ctx context.Context,
	key ctrlruntimeclient.ObjectKey,
	obj ctrlruntimeclient.Object,
	opts ...ctrlruntimeclient.GetOption,
) error {
	r.getCalls++

	return r.Reader.Get(ctx, key, obj, opts...)
}

var errInjectedNodeConflict = errors.New("injected conflict")

func (c *conflictOnceClient) Status() ctrlruntimeclient.StatusWriter {
	return &conflictOnceStatusWriter{StatusWriter: c.Client.Status(), client: c}
}

func (w *conflictOnceStatusWriter) Update(
	ctx context.Context,
	obj ctrlruntimeclient.Object,
	opts ...ctrlruntimeclient.SubResourceUpdateOption,
) error {
	c := w.client
	c.updateCalls++
	if c.updateCalls == 1 {
		if c.beforeConflict != nil {
			err := c.beforeConflict(ctx, obj)
			if err != nil {
				return err
			}
		}

		return apimachineryerrors.NewConflict(
			apimachineryschema.GroupResource{Group: "c9s.run", Resource: "nodes"},
			obj.GetName(),
			errInjectedNodeConflict,
		)
	}

	return w.StatusWriter.Update(ctx, obj, opts...)
}

func TestDirectRequeueAfterPacesReadyNodes(t *testing.T) {
	t.Parallel()

	converging := &clabernetesapisv1alpha1.Node{}
	converging.Status.Readiness = clabernetesconstants.NodeStatusNotReady

	if got := directRequeueAfter(converging); got != directRequeueInterval {
		t.Fatalf("converging Node requeues after %s, want %s", got, directRequeueInterval)
	}

	if got := directRequeueAfter(nil); got != directRequeueInterval {
		t.Fatalf("nil Node requeues after %s, want %s", got, directRequeueInterval)
	}

	ready := &clabernetesapisv1alpha1.Node{}
	ready.Status.Readiness = clabernetesconstants.NodeStatusReady

	low := directSteadyRequeueInterval - directSteadyRequeueJitter/2
	high := directSteadyRequeueInterval + directSteadyRequeueJitter/2

	for range 64 {
		got := directRequeueAfter(ready)
		if got < low || got >= high {
			t.Fatalf("ready Node requeues after %s, want within [%s, %s)", got, low, high)
		}
	}
}

func TestUpdateNodeStatusRetriesResourceVersionConflict(t *testing.T) {
	t.Parallel()

	scheme := nodeReconcileTestScheme(t)
	node := nodeReconcileTestNode()
	baseClient := ctrlruntimefake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&clabernetesapisv1alpha1.Node{}).WithObjects(node).Build()
	client := &conflictOnceClient{
		Client: baseClient,
		beforeConflict: func(ctx context.Context, obj ctrlruntimeclient.Object) error {
			current := &clabernetesapisv1alpha1.Node{}

			err := baseClient.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(obj), current)
			if err != nil {
				return err
			}

			current.Status.Readiness = clabernetesconstants.NodeStatusNotReady

			return baseClient.Status().Update(ctx, current)
		},
	}
	apiReader := &countingNodeReader{Reader: baseClient}
	reconciler := &Reconciler{Client: client, apiReader: apiReader}
	desired := clabernetesapisv1alpha1.NodeStatus{
		Readiness: clabernetesconstants.NodeStatusReady,
	}

	err := reconciler.updateNodeStatus(context.Background(), node, desired)
	if err != nil {
		t.Fatalf("updateNodeStatus() failed after retryable conflict: %s", err)
	}

	if client.updateCalls != 2 {
		t.Fatalf("status update calls = %d, want 2", client.updateCalls)
	}

	if apiReader.getCalls != 2 {
		t.Fatalf("direct status reads = %d, want 2", apiReader.getCalls)
	}

	actual := &clabernetesapisv1alpha1.Node{}

	err = baseClient.Get(
		context.Background(),
		ctrlruntimeclient.ObjectKeyFromObject(node),
		actual,
	)
	if err != nil {
		t.Fatal(err)
	}

	if actual.Status.Readiness != clabernetesconstants.NodeStatusReady {
		t.Fatalf("stored readiness = %q, want ready", actual.Status.Readiness)
	}
}

func TestUpdateNodeStatusDropsStatusProjectedFromStaleGeneration(t *testing.T) {
	t.Parallel()

	scheme := nodeReconcileTestScheme(t)
	staleNode := nodeReconcileTestNode()
	staleNode.Generation = 1
	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&clabernetesapisv1alpha1.Node{}).
		WithObjects(staleNode).
		Build()
	reconciler := &Reconciler{Client: client, apiReader: client}

	currentNode := &clabernetesapisv1alpha1.Node{}
	if err := client.Get(
		context.Background(),
		ctrlruntimeclient.ObjectKeyFromObject(staleNode),
		currentNode,
	); err != nil {
		t.Fatal(err)
	}

	currentNode.Generation = 2
	currentNode.Spec.Image = "ghz.dev/clabernetes/example:2"
	if err := client.Update(context.Background(), currentNode); err != nil {
		t.Fatal(err)
	}

	currentNode.Status.Readiness = clabernetesconstants.NodeStatusNotReady
	currentNode.Status.Conditions = []metav1.Condition{{
		Type:               clabernetesapisv1alpha1.NodeConditionPlanApplied,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: currentNode.GetGeneration(),
		Reason:             directPlanPendingReason,
		Message:            directPlanPendingMessage,
	}}
	if err := client.Status().Update(context.Background(), currentNode); err != nil {
		t.Fatal(err)
	}

	staleDesired := clabernetesapisv1alpha1.NodeStatus{
		Readiness: clabernetesconstants.NodeStatusReady,
		Conditions: []metav1.Condition{{
			Type:               clabernetesapisv1alpha1.NodeConditionPlanApplied,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: staleNode.GetGeneration(),
		}},
	}

	if err := reconciler.updateNodeStatus(
		context.Background(),
		staleNode,
		staleDesired,
	); err != nil {
		t.Fatalf("stale status projection should be ignored, got %s", err)
	}

	actual := &clabernetesapisv1alpha1.Node{}
	if err := client.Get(
		context.Background(),
		ctrlruntimeclient.ObjectKeyFromObject(staleNode),
		actual,
	); err != nil {
		t.Fatal(err)
	}

	condition := apimachinerymeta.FindStatusCondition(
		actual.Status.Conditions,
		clabernetesapisv1alpha1.NodeConditionPlanApplied,
	)
	if actual.Status.Readiness != clabernetesconstants.NodeStatusNotReady ||
		condition == nil || condition.Status != metav1.ConditionFalse ||
		condition.ObservedGeneration != currentNode.GetGeneration() {
		t.Fatalf("stale generation overwrote current status: %#v", actual.Status)
	}
}

func TestRenderDirectFabricServicePublishesCurrentPodBeforeReadiness(t *testing.T) {
	t.Parallel()

	node := nodeReconcileTestNode()
	service := NewServiceReconciler(
		&claberneteslogging.FakeInstance{},
		clabernetesconfig.GetFakeManager,
	).RenderDirectFabricService(node, node.GetName())

	if service.Spec.ClusterIP != k8scorev1.ClusterIPNone ||
		!service.Spec.PublishNotReadyAddresses {
		t.Fatalf(
			"direct fabric service does not provide headless early discovery: %+v",
			service.Spec,
		)
	}
	if service.Spec.Selector[clabernetesconstants.LabelTopologyNode] != node.GetName() {
		t.Fatalf("direct fabric selector = %v", service.Spec.Selector)
	}
}

func TestRenderDirectAliasService(t *testing.T) {
	t.Parallel()

	node := nodeReconcileTestNode()
	service := NewServiceReconciler(
		&claberneteslogging.FakeInstance{},
		clabernetesconfig.GetFakeManager,
	).RenderDirectAliasService(node, node.GetName(), "srl1-alt")

	if service.GetName() != "srl1-alt" {
		t.Fatalf("alias service name = %q, want srl1-alt", service.GetName())
	}

	if service.Spec.ClusterIP != k8scorev1.ClusterIPNone ||
		!service.Spec.PublishNotReadyAddresses || len(service.Spec.Ports) != 0 {
		t.Fatalf("alias service is not a portless headless name binding: %+v", service.Spec)
	}

	if service.Spec.Selector[clabernetesconstants.LabelTopologyNode] != node.GetName() {
		t.Fatalf("alias selector = %v", service.Spec.Selector)
	}

	labels := service.GetLabels()
	if labels[clabernetesconstants.LabelTopologyServiceType] !=
		clabernetesconstants.TopologyServiceTypeAlias ||
		labels[clabernetesconstants.LabelTopologyNode] != node.GetName() {
		t.Fatalf("alias service labels = %v", labels)
	}
}

func TestRenderDirectExposeServiceTypes(t *testing.T) {
	t.Parallel()

	node := nodeReconcileTestNode()
	exposedPorts := &clabernetesapisv1alpha1.NodeExposedPorts{
		Ports: []clabernetesapisv1alpha1.NodeExposedPort{{
			DestinationPort: 22,
			ExposePort:      22,
			Protocol:        string(k8scorev1.ProtocolTCP),
		}},
	}
	reconciler := NewServiceReconciler(
		&claberneteslogging.FakeInstance{},
		clabernetesconfig.GetFakeManager,
	)

	tests := []struct {
		name        string
		exposeType  string
		serviceType k8scorev1.ServiceType
		headless    bool
		nilService  bool
	}{
		{name: "built-in default", serviceType: k8scorev1.ServiceTypeLoadBalancer},
		{
			name:        "load balancer",
			exposeType:  "LoadBalancer",
			serviceType: k8scorev1.ServiceTypeLoadBalancer,
		},
		{name: "cluster ip", exposeType: "ClusterIP", serviceType: k8scorev1.ServiceTypeClusterIP},
		{
			name:        "headless",
			exposeType:  "Headless",
			serviceType: k8scorev1.ServiceTypeClusterIP,
			headless:    true,
		},
		{name: "none", exposeType: "None", nilService: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			service := reconciler.RenderDirectExposeService(
				node,
				node.GetName(),
				&ResolvedProfile{ExposeType: test.exposeType},
				exposedPorts,
				nil,
			)
			if test.nilService {
				if service != nil {
					t.Fatalf("service = %#v, want nil", service)
				}

				return
			}

			if service == nil || service.Spec.Type != test.serviceType ||
				serviceIsHeadless(service) != test.headless {
				t.Fatalf(
					"service = %#v, want type %q headless=%t",
					service,
					test.serviceType,
					test.headless,
				)
			}
		})
	}

	if service := reconciler.RenderDirectExposeService(
		node,
		node.GetName(),
		&ResolvedProfile{ExposeType: "ClusterIP"},
		&clabernetesapisv1alpha1.NodeExposedPorts{},
		nil,
	); service != nil {
		t.Fatalf("empty exposed ports rendered service %#v", service)
	}
}

func TestRenderExposeServiceRequestsRealizedManagementAddress(t *testing.T) {
	t.Parallel()

	exposedPorts := &clabernetesapisv1alpha1.NodeExposedPorts{
		Ports: []clabernetesapisv1alpha1.NodeExposedPort{{
			DestinationPort: 22,
			ExposePort:      22,
			Protocol:        string(k8scorev1.ProtocolTCP),
		}},
	}
	dualStack := &clabernetesapisv1alpha1.NodeDirectManagementStatus{
		IPv4: "10.20.30.57/24",
		IPv6: "2001:db8:20::39/64",
	}
	reconciler := NewServiceReconciler(
		&claberneteslogging.FakeInstance{},
		clabernetesconfig.GetFakeManager,
	)

	tests := []struct {
		name       string
		pinnedIPv4 string
		profile    ResolvedProfile
		management *clabernetesapisv1alpha1.NodeDirectManagementStatus
		want       string
	}{
		{
			name:       "allocated ipv4",
			profile:    ResolvedProfile{UseNodeMgmtIpv4Address: true},
			management: dualStack,
			want:       "10.20.30.57",
		},
		{
			name:       "pinned ipv4",
			pinnedIPv4: "10.20.30.11",
			profile:    ResolvedProfile{UseNodeMgmtIpv4Address: true},
			management: &clabernetesapisv1alpha1.NodeDirectManagementStatus{
				IPv4: "10.20.30.11/24",
			},
			want: "10.20.30.11",
		},
		{
			name:       "allocated ipv6",
			profile:    ResolvedProfile{UseNodeMgmtIpv6Address: true},
			management: dualStack,
			want:       "2001:db8:20::39",
		},
		{
			name: "ipv4 takes precedence",
			profile: ResolvedProfile{
				UseNodeMgmtIpv4Address: true,
				UseNodeMgmtIpv6Address: true,
			},
			management: dualStack,
			want:       "10.20.30.57",
		},
		{
			name:    "selected family has no address",
			profile: ResolvedProfile{UseNodeMgmtIpv4Address: true},
			management: &clabernetesapisv1alpha1.NodeDirectManagementStatus{
				IPv6: "2001:db8:20::39/64",
			},
		},
		{
			name:    "management disabled",
			profile: ResolvedProfile{UseNodeMgmtIpv4Address: true},
		},
		{
			name:       "option disabled",
			management: dualStack,
		},
		{
			name: "cluster ip",
			profile: ResolvedProfile{
				ExposeType:             "ClusterIP",
				UseNodeMgmtIpv4Address: true,
			},
			management: dualStack,
		},
		{
			name:    "invalid address",
			profile: ResolvedProfile{UseNodeMgmtIpv4Address: true},
			management: &clabernetesapisv1alpha1.NodeDirectManagementStatus{
				IPv4: "not-an-address/24",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			node := nodeReconcileTestNode()
			node.Spec.MgmtIPv4 = test.pinnedIPv4

			service := reconciler.RenderDirectExposeService(
				node,
				node.GetName(),
				&test.profile,
				exposedPorts,
				test.management,
			)
			if service == nil {
				t.Fatal("expected expose Service")
			}
			if service.Spec.LoadBalancerIP != test.want {
				t.Fatalf("loadBalancerIP = %q, want %q", service.Spec.LoadBalancerIP, test.want)
			}
		})
	}
}

func TestReconcileExposeServiceFollowsRealizedManagementAddress(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := nodeReconcileTestScheme(t)
	node := nodeReconcileTestNode()
	profile := &ResolvedProfile{UseNodeMgmtIpv4Address: true}
	exposedPorts := &clabernetesapisv1alpha1.NodeExposedPorts{
		Ports: []clabernetesapisv1alpha1.NodeExposedPort{{
			DestinationPort: 22,
			ExposePort:      22,
			Protocol:        string(k8scorev1.ProtocolTCP),
		}},
	}
	serviceReconciler := NewServiceReconciler(
		&claberneteslogging.FakeInstance{},
		clabernetesconfig.GetFakeManager,
	)
	existing := serviceReconciler.RenderExposeService(
		node,
		node.GetName(),
		profile,
		exposedPorts,
		&clabernetesapisv1alpha1.NodeDirectManagementStatus{IPv4: "10.20.30.57/24"},
	)
	existing.OwnerReferences = []metav1.OwnerReference{{UID: node.GetUID()}}

	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(node, existing).
		Build()
	reconciler := &Reconciler{
		Log:               &claberneteslogging.FakeInstance{},
		Client:            client,
		ServiceReconciler: serviceReconciler,
	}

	// A recreated Node is allocated a new address; its Service must request that one instead.
	rendered := serviceReconciler.RenderExposeService(
		node,
		node.GetName(),
		profile,
		exposedPorts,
		&clabernetesapisv1alpha1.NodeDirectManagementStatus{IPv4: "10.20.30.91/24"},
	)
	if _, err := reconciler.reconcileRenderedExposeService(ctx, node, rendered); err != nil {
		t.Fatalf("reconciling expose Service: %s", err)
	}

	actual := &k8scorev1.Service{}
	if err := client.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(existing), actual); err != nil {
		t.Fatal(err)
	}
	if actual.Spec.LoadBalancerIP != "10.20.30.91" {
		t.Fatalf("reconciled loadBalancerIP = %q, want 10.20.30.91", actual.Spec.LoadBalancerIP)
	}
}

func TestRenderExposeServiceResolvesApplicationProtocols(t *testing.T) {
	t.Parallel()

	node := nodeReconcileTestNode()
	node.Spec.AppProtocols = []clabernetesapisv1alpha1.NodeAppProtocol{
		{Port: "443/tcp", AppProtocol: ""},
		{Port: "9339/tcp", AppProtocol: "https"},
		{Port: "57400/tcp", AppProtocol: "kubernetes.io/h2c"},
		{Port: "9273/tcp", AppProtocol: "example.com/metrics"},
		// An override is descriptive only and must not select this otherwise absent port.
		{Port: "12345/tcp", AppProtocol: "example.com/unused"},
	}
	exposedPorts := &clabernetesapisv1alpha1.NodeExposedPorts{
		Ports: []clabernetesapisv1alpha1.NodeExposedPort{
			{DestinationPort: 22, ExposePort: 22, Protocol: "TCP"},
			{DestinationPort: 443, ExposePort: 443, Protocol: "TCP"},
			{DestinationPort: 9339, ExposePort: 9339, Protocol: "TCP"},
			{DestinationPort: 57400, ExposePort: 57400, Protocol: "TCP"},
			{DestinationPort: 9273, ExposePort: 9273, Protocol: "TCP"},
			{DestinationPort: 9999, ExposePort: 9999, Protocol: "UDP"},
		},
	}

	service := NewServiceReconciler(
		&claberneteslogging.FakeInstance{},
		clabernetesconfig.GetFakeManager,
	).RenderExposeService(node, node.GetName(), &ResolvedProfile{}, exposedPorts, nil)
	if service == nil {
		t.Fatal("expected expose Service")
	}

	want := map[int32]string{
		22:    "ssh",
		443:   "",
		9339:  "https",
		57400: "kubernetes.io/h2c",
		9273:  "example.com/metrics",
		9999:  "",
	}
	if len(service.Spec.Ports) != len(want) {
		t.Fatalf("Service ports = %#v, want %d entries", service.Spec.Ports, len(want))
	}

	for _, port := range service.Spec.Ports {
		got := ""
		if port.AppProtocol != nil {
			got = *port.AppProtocol
		}
		if got != want[port.Port] {
			t.Errorf("port %d appProtocol = %q, want %q", port.Port, got, want[port.Port])
		}
	}
}

func TestDefaultManagementPortsCarryExactApplicationProtocols(t *testing.T) {
	t.Parallel()

	want := []managementPortDefinition{
		{DestinationPort: 21, Protocol: "TCP", AppProtocol: "ftp"},
		{DestinationPort: 22, Protocol: "TCP", AppProtocol: "ssh"},
		{DestinationPort: 23, Protocol: "TCP", AppProtocol: "telnet"},
		{DestinationPort: 80, Protocol: "TCP", AppProtocol: "http"},
		{DestinationPort: 443, Protocol: "TCP", AppProtocol: "https"},
		{DestinationPort: 830, Protocol: "TCP", AppProtocol: "netconf-ssh"},
		{DestinationPort: 5000, Protocol: "TCP", AppProtocol: "telnet"},
		{DestinationPort: 5900, Protocol: "TCP", AppProtocol: "rfb"},
		{DestinationPort: 6030, Protocol: "TCP", AppProtocol: "c9s.run/gnmi"},
		{DestinationPort: 9339, Protocol: "TCP", AppProtocol: "c9s.run/gnmi"},
		{DestinationPort: 9340, Protocol: "TCP", AppProtocol: "c9s.run/gribi"},
		{DestinationPort: 9559, Protocol: "TCP", AppProtocol: "c9s.run/p4runtime"},
		{DestinationPort: 57400, Protocol: "TCP", AppProtocol: "c9s.run/gnmi"},
		{DestinationPort: 161, Protocol: "UDP", AppProtocol: "snmp"},
	}
	if got := defaultManagementPorts(); !reflect.DeepEqual(got, want) {
		t.Fatalf("default management ports = %#v, want %#v", got, want)
	}

	exposed := defaultExposePorts()
	if len(exposed) != len(want) {
		t.Fatalf("default exposed ports = %#v, want %d entries", exposed, len(want))
	}
	for idx, port := range exposed {
		if port.DestinationPort != want[idx].DestinationPort ||
			port.Protocol != want[idx].Protocol {
			t.Fatalf("default exposed port %d = %#v, want %#v", idx, port, want[idx])
		}
	}
}

func TestNoneExposurePrunesOnlyExposeService(t *testing.T) {
	t.Parallel()

	scheme := nodeReconcileTestScheme(t)
	node := nodeReconcileTestNode()
	ownerReferences := []metav1.OwnerReference{{UID: node.GetUID()}}
	expose := &k8scorev1.Service{ObjectMeta: metav1.ObjectMeta{
		Name: node.GetName(), Namespace: node.GetNamespace(), OwnerReferences: ownerReferences,
	}}
	fabric := &k8scorev1.Service{ObjectMeta: metav1.ObjectMeta{
		Name: FabricServiceName(node.GetName()), Namespace: node.GetNamespace(),
		OwnerReferences: ownerReferences,
	}}
	alias := &k8scorev1.Service{ObjectMeta: metav1.ObjectMeta{
		Name: "srl1-alt", Namespace: node.GetNamespace(), OwnerReferences: ownerReferences,
	}}
	client := ctrlruntimefake.NewClientBuilder().WithScheme(scheme).
		WithObjects(node, expose, fabric, alias).Build()
	reconciler := &Reconciler{
		Log:    &claberneteslogging.FakeInstance{},
		Client: client,
	}

	_, err := reconciler.reconcileRenderedExposeService(context.Background(), node, nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name   string
		exists bool
	}{
		{name: node.GetName()},
		{name: FabricServiceName(node.GetName()), exists: true},
		{name: alias.GetName(), exists: true},
	} {
		err = client.Get(
			context.Background(),
			apimachinerytypes.NamespacedName{Namespace: node.GetNamespace(), Name: test.name},
			&k8scorev1.Service{},
		)
		if test.exists && err != nil {
			t.Fatalf("service %q was removed: %s", test.name, err)
		}
		if !test.exists && !apimachineryerrors.IsNotFound(err) {
			t.Fatalf("expose service %q was not removed: %v", test.name, err)
		}
	}
}

func TestServiceNeedsRecreateForHeadlessTransitions(t *testing.T) {
	t.Parallel()

	ordinary := &k8scorev1.Service{Spec: k8scorev1.ServiceSpec{
		Type: k8scorev1.ServiceTypeClusterIP, ClusterIP: "10.96.0.20",
	}}
	headless := &k8scorev1.Service{Spec: k8scorev1.ServiceSpec{
		Type: k8scorev1.ServiceTypeClusterIP, ClusterIP: k8scorev1.ClusterIPNone,
	}}
	loadBalancer := &k8scorev1.Service{Spec: k8scorev1.ServiceSpec{
		Type: k8scorev1.ServiceTypeLoadBalancer, ClusterIP: "10.96.0.21",
	}}

	for _, test := range []struct {
		name     string
		existing *k8scorev1.Service
		rendered *k8scorev1.Service
		expected bool
	}{
		{name: "ordinary to headless", existing: ordinary, rendered: headless, expected: true},
		{name: "headless to ordinary", existing: headless, rendered: ordinary, expected: true},
		{name: "load balancer to ordinary", existing: loadBalancer, rendered: ordinary},
		{name: "ordinary unchanged", existing: ordinary, rendered: ordinary},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if actual := serviceNeedsRecreate(test.existing, test.rendered); actual != test.expected {
				t.Fatalf("serviceNeedsRecreate() = %t, want %t", actual, test.expected)
			}
		})
	}
}

func TestReconcileDirectAliasServicesCreatesAndPrunes(t *testing.T) {
	t.Parallel()

	scheme := nodeReconcileTestScheme(t)
	node := nodeReconcileTestNode()
	node.Spec.Aliases = []string{"srl1-alt", "srl1-extra"}

	client := ctrlruntimefake.NewClientBuilder().WithScheme(scheme).WithObjects(node).Build()
	reconciler := &Reconciler{
		Log:    &claberneteslogging.FakeInstance{},
		Client: client,
		ServiceReconciler: NewServiceReconciler(
			&claberneteslogging.FakeInstance{},
			clabernetesconfig.GetFakeManager,
		),
	}

	err := reconciler.reconcileDirectAliasServices(context.Background(), node, node.GetName())
	if err != nil {
		t.Fatalf("reconciling alias services failed: %s", err)
	}

	for _, alias := range node.Spec.Aliases {
		service := &k8scorev1.Service{}

		err = client.Get(
			context.Background(),
			apimachinerytypes.NamespacedName{Namespace: node.GetNamespace(), Name: alias},
			service,
		)
		if err != nil {
			t.Fatalf("alias service %q was not created: %s", alias, err)
		}

		if !ownedByUID(service, node.GetUID()) {
			t.Fatalf("alias service %q is not owned by its node", alias)
		}
	}

	node.Spec.Aliases = []string{"srl1-alt"}

	err = reconciler.reconcileDirectAliasServices(context.Background(), node, node.GetName())
	if err != nil {
		t.Fatalf("re-reconciling alias services failed: %s", err)
	}

	err = client.Get(
		context.Background(),
		apimachinerytypes.NamespacedName{Namespace: node.GetNamespace(), Name: "srl1-extra"},
		&k8scorev1.Service{},
	)
	if !apimachineryerrors.IsNotFound(err) {
		t.Fatalf("undeclared alias service was not pruned: %v", err)
	}

	err = client.Get(
		context.Background(),
		apimachinerytypes.NamespacedName{Namespace: node.GetNamespace(), Name: "srl1-alt"},
		&k8scorev1.Service{},
	)
	if err != nil {
		t.Fatalf("declared alias service must survive pruning: %s", err)
	}
}

func TestReconcileDirectAliasServicesRefusesForeignServiceCollision(t *testing.T) {
	t.Parallel()

	scheme := nodeReconcileTestScheme(t)
	node := nodeReconcileTestNode()
	node.Spec.Aliases = []string{"taken"}

	foreign := &k8scorev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "taken", Namespace: node.GetNamespace()},
	}
	client := ctrlruntimefake.NewClientBuilder().WithScheme(scheme).
		WithObjects(node, foreign).Build()
	reconciler := &Reconciler{
		Log:    &claberneteslogging.FakeInstance{},
		Client: client,
		ServiceReconciler: NewServiceReconciler(
			&claberneteslogging.FakeInstance{},
			clabernetesconfig.GetFakeManager,
		),
	}

	err := reconciler.reconcileDirectAliasServices(context.Background(), node, node.GetName())
	if err == nil || !strings.Contains(err.Error(), "collides") {
		t.Fatalf("expected alias collision failure, got %v", err)
	}
}

func TestPrepareServiceForUpdatePreservesNodePorts(t *testing.T) {
	existing := &k8scorev1.Service{
		ObjectMeta: metav1.ObjectMeta{ResourceVersion: "7"},
		Spec: k8scorev1.ServiceSpec{
			Type:       k8scorev1.ServiceTypeLoadBalancer,
			ClusterIP:  "10.96.0.20",
			ClusterIPs: []string{"10.96.0.20"},
			Ports: []k8scorev1.ServicePort{{
				Name:     "ssh",
				Protocol: k8scorev1.ProtocolTCP,
				Port:     22,
				NodePort: 30_022,
			}},
		},
	}
	rendered := &k8scorev1.Service{Spec: k8scorev1.ServiceSpec{
		Type: k8scorev1.ServiceTypeLoadBalancer,
		Ports: []k8scorev1.ServicePort{{
			Name:       "ssh",
			Protocol:   k8scorev1.ProtocolTCP,
			Port:       22,
			TargetPort: intstr.FromInt32(60_000),
		}},
	}}

	prepareServiceForUpdate(existing, rendered)

	if rendered.GetResourceVersion() != "7" || rendered.Spec.ClusterIP != "10.96.0.20" {
		t.Fatalf("expected service identity/allocation preserved, got %+v", rendered)
	}

	if rendered.Spec.Ports[0].NodePort != 30_022 {
		t.Fatalf("expected allocated node port preserved, got %+v", rendered.Spec.Ports[0])
	}
}

func TestReconcileExposeServiceUpdatesApplicationProtocolAndPreservesNodePort(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := nodeReconcileTestScheme(t)
	node := nodeReconcileTestNode()
	exposedPorts := &clabernetesapisv1alpha1.NodeExposedPorts{
		Ports: []clabernetesapisv1alpha1.NodeExposedPort{{
			DestinationPort: 57400,
			ExposePort:      57400,
			Protocol:        "TCP",
		}},
	}
	serviceReconciler := NewServiceReconciler(
		&claberneteslogging.FakeInstance{},
		clabernetesconfig.GetFakeManager,
	)
	existing := serviceReconciler.RenderExposeService(
		node,
		node.GetName(),
		&ResolvedProfile{},
		exposedPorts,
		nil,
	)
	existing.Spec.Ports[0].AppProtocol = nil
	existing.Spec.Ports[0].NodePort = 30_574
	existing.Spec.ClusterIP = "10.96.0.20"
	existing.Spec.ClusterIPs = []string{"10.96.0.20"}
	existing.OwnerReferences = []metav1.OwnerReference{{UID: node.GetUID()}}

	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(node, existing).
		Build()
	reconciler := &Reconciler{
		Log:               &claberneteslogging.FakeInstance{},
		Client:            client,
		ServiceReconciler: serviceReconciler,
	}

	assertReconciled := func(want *string) {
		t.Helper()

		rendered := serviceReconciler.RenderExposeService(
			node,
			node.GetName(),
			&ResolvedProfile{},
			exposedPorts,
			nil,
		)
		if _, err := reconciler.reconcileRenderedExposeService(ctx, node, rendered); err != nil {
			t.Fatalf("reconciling expose Service: %s", err)
		}

		actual := &k8scorev1.Service{}
		if err := client.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(existing), actual); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(actual.Spec.Ports[0].AppProtocol, want) {
			t.Fatalf(
				"reconciled appProtocol = %v, want %v",
				actual.Spec.Ports[0].AppProtocol,
				want,
			)
		}
		if actual.Spec.Ports[0].NodePort != 30_574 {
			t.Fatalf("reconciled Service lost NodePort: %#v", actual.Spec.Ports[0])
		}
	}

	defaultProtocol := "c9s.run/gnmi"
	assertReconciled(&defaultProtocol)

	node.Spec.AppProtocols = []clabernetesapisv1alpha1.NodeAppProtocol{{
		Port: "57400/tcp", AppProtocol: "kubernetes.io/h2c",
	}}
	overriddenProtocol := "kubernetes.io/h2c"
	assertReconciled(&overriddenProtocol)

	node.Spec.AppProtocols[0].AppProtocol = ""
	assertReconciled(nil)
}

func TestResolveGroupProfileReferenceInheritsPrimary(t *testing.T) {
	primary := nodeReconcileTestNode()
	primary.Spec.ProfileRef = &k8scorev1.LocalObjectReference{Name: "group-profile"}
	secondary := nodeReconcileTestNode()
	secondary.Name = "sim-a"

	profileName, err := resolveGroupProfileReference(
		primary.GetName(),
		[]string{primary.GetName(), secondary.GetName()},
		map[string]*clabernetesapisv1alpha1.Node{
			primary.GetName():   primary,
			secondary.GetName(): secondary,
		},
	)
	if err != nil {
		t.Fatalf("resolveGroupProfileReference() error = %s", err)
	}

	if profileName != "group-profile" {
		t.Fatalf("expected secondary to inherit %q, got %q", "group-profile", profileName)
	}
}

func TestResolveGroupProfileReferenceRejectsConflict(t *testing.T) {
	primary := nodeReconcileTestNode()
	primary.Spec.ProfileRef = &k8scorev1.LocalObjectReference{Name: "primary-profile"}
	secondary := nodeReconcileTestNode()
	secondary.Name = "sim-a"
	secondary.Spec.ProfileRef = &k8scorev1.LocalObjectReference{Name: "other-profile"}

	_, err := resolveGroupProfileReference(
		primary.GetName(),
		[]string{primary.GetName(), secondary.GetName()},
		map[string]*clabernetesapisv1alpha1.Node{
			primary.GetName():   primary,
			secondary.GetName(): secondary,
		},
	)
	if err == nil {
		t.Fatal("expected conflicting group profile references to be rejected")
	}
}

func nodeReconcileTestScheme(t *testing.T) *apimachineryruntime.Scheme {
	t.Helper()

	scheme := apimachineryruntime.NewScheme()

	for _, addToScheme := range []func(*apimachineryruntime.Scheme) error{
		clabernetesapisv1alpha1.AddToScheme,
		k8scorev1.AddToScheme,
		k8sappsv1.AddToScheme,
		k8srbacv1.AddToScheme,
	} {
		err := addToScheme(scheme)
		if err != nil {
			t.Fatalf("failed adding scheme: %s", err)
		}
	}

	return scheme
}

func nodeReconcileTestNode() *clabernetesapisv1alpha1.Node {
	return &clabernetesapisv1alpha1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "srl1",
			Namespace: "clabernetes",
			UID:       apimachinerytypes.UID("node-uid"),
			Labels:    map[string]string{},
		},
		Spec: clabernetesapisv1alpha1.NodeSpec{
			NodeDefinition: clabernetesapisv1alpha1.NodeDefinition{
				Image: "ghcr.io/nokia/srlinux:latest",
			},
		},
	}
}
