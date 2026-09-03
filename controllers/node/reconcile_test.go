//nolint:noinlineerr,wsl_v5 // Reconciler tests use compact fail-fast assertions.
package node //nolint:testpackage // tests exercise unexported reconciliation helpers

import (
	"context"
	"errors"
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
	); service != nil {
		t.Fatalf("empty exposed ports rendered service %#v", service)
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
