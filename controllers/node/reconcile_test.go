//nolint:noinlineerr,wsl_v5 // Reconciler tests use compact fail-fast assertions.
package node //nolint:testpackage // tests exercise unexported reconciliation helpers

import (
	"context"
	"errors"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	k8sappsv1 "k8s.io/api/apps/v1"
	k8scorev1 "k8s.io/api/core/v1"
	k8srbacv1 "k8s.io/api/rbac/v1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
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

func TestResolveGroupLauncherProfileReferenceInheritsPrimary(t *testing.T) {
	primary := nodeReconcileTestNode()
	primary.Spec.LauncherProfileRef = &k8scorev1.LocalObjectReference{Name: "group-profile"}
	secondary := nodeReconcileTestNode()
	secondary.Name = "sim-a"

	profileName, err := resolveGroupLauncherProfileReference(
		primary.GetName(),
		[]string{primary.GetName(), secondary.GetName()},
		map[string]*clabernetesapisv1alpha1.Node{
			primary.GetName():   primary,
			secondary.GetName(): secondary,
		},
	)
	if err != nil {
		t.Fatalf("resolveGroupLauncherProfileReference() error = %s", err)
	}

	if profileName != "group-profile" {
		t.Fatalf("expected secondary to inherit %q, got %q", "group-profile", profileName)
	}
}

func TestResolveGroupLauncherProfileReferenceRejectsConflict(t *testing.T) {
	primary := nodeReconcileTestNode()
	primary.Spec.LauncherProfileRef = &k8scorev1.LocalObjectReference{Name: "primary-profile"}
	secondary := nodeReconcileTestNode()
	secondary.Name = "sim-a"
	secondary.Spec.LauncherProfileRef = &k8scorev1.LocalObjectReference{Name: "other-profile"}

	_, err := resolveGroupLauncherProfileReference(
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
