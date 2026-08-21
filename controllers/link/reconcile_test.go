package link //nolint:testpackage // tests exercise the unexported reconcile status transition

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetescontrollers "github.com/clabernetes/clabernetes/controllers"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	apimachinerymeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	ctrlruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcileClearsRejectedLinkAllocation(t *testing.T) {
	tests := []struct {
		name      string
		links     []clabernetesapisv1alpha1.Link
		nodes     []clabernetesapisv1alpha1.Node
		target    string
		errorPart string
		reason    string
	}{
		{
			name: "invalid",
			links: []clabernetesapisv1alpha1.Link{
				reconcileTestLink("bad-link", "srl1", "e1-1", "srl1", "e1-1", 7),
			},
			target:    "bad-link",
			errorPart: "to itself",
			reason:    "InvalidSpec",
		},
		{
			name: "endpoint-conflict",
			links: []clabernetesapisv1alpha1.Link{
				reconcileTestLink("a-winner", "srl1", "e1-1", "srl2", "e1-1", 1),
				reconcileTestLink("z-loser", "srl1", "e1-1", "srl3", "e1-1", 7),
			},
			nodes: []clabernetesapisv1alpha1.Node{
				reconcileTestNode("srl1"),
				reconcileTestNode("srl2"),
				reconcileTestNode("srl3"),
			},
			target:    "z-loser",
			errorPart: "a-winner",
			reason:    "EndpointConflict",
		},
		{
			name: "unresolved-endpoint",
			links: []clabernetesapisv1alpha1.Link{
				reconcileTestLink("unresolved", "srl1", "e1-1", "missing", "e1-1", 7),
			},
			nodes:     []clabernetesapisv1alpha1.Node{reconcileTestNode("srl1")},
			target:    "unresolved",
			errorPart: "missing",
			reason:    "EndpointsUnresolved",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			scheme := apimachineryruntime.NewScheme()

			err := clabernetesapisv1alpha1.AddToScheme(scheme)
			if err != nil {
				t.Fatalf("failed adding api scheme: %s", err)
			}

			objects := make(
				[]ctrlruntimeclient.Object,
				0,
				len(testCase.links)+len(testCase.nodes),
			)
			for idx := range testCase.links {
				objects = append(objects, &testCase.links[idx])
			}

			for idx := range testCase.nodes {
				objects = append(objects, &testCase.nodes[idx])
			}

			client := ctrlruntimefake.NewClientBuilder().
				WithScheme(scheme).
				WithStatusSubresource(&clabernetesapisv1alpha1.Link{}).
				WithObjects(objects...).
				Build()

			controller := &Controller{
				BaseController: &clabernetescontrollers.BaseController{
					Log:    &claberneteslogging.FakeInstance{},
					Client: client,
				},
				apiReader: client,
			}

			_, err = controller.Reconcile(
				context.Background(),
				ctrlruntime.Request{NamespacedName: apimachinerytypes.NamespacedName{
					Namespace: "clabernetes",
					Name:      testCase.target,
				}},
			)
			if err != nil {
				t.Fatalf("reconcile failed: %s", err)
			}

			actual := &clabernetesapisv1alpha1.Link{}

			err = client.Get(
				context.Background(),
				apimachinerytypes.NamespacedName{
					Namespace: "clabernetes",
					Name:      testCase.target,
				},
				actual,
			)
			if err != nil {
				t.Fatalf("failed getting reconciled link: %s", err)
			}

			if actual.Status.TunnelID != 0 {
				t.Fatalf(
					"expected rejected link allocation cleared, got %d",
					actual.Status.TunnelID,
				)
			}

			if !strings.Contains(actual.Status.Error, testCase.errorPart) {
				t.Fatalf(
					"expected status error containing %q, got %q",
					testCase.errorPart,
					actual.Status.Error,
				)
			}

			condition := apimachinerymeta.FindStatusCondition(
				actual.Status.Conditions,
				clabernetesapisv1alpha1.LinkConditionAccepted,
			)
			if condition == nil || condition.Status != metav1.ConditionFalse ||
				condition.Reason != testCase.reason ||
				!strings.Contains(condition.Message, testCase.errorPart) {
				t.Fatalf("rejected Link Accepted condition = %#v", condition)
			}
		})
	}
}

func TestReconcileUnresolvedLinkDoesNotReserveInterface(t *testing.T) {
	scheme := apimachineryruntime.NewScheme()

	err := clabernetesapisv1alpha1.AddToScheme(scheme)
	if err != nil {
		t.Fatalf("failed adding api scheme: %s", err)
	}

	unresolved := reconcileTestLink(
		"a-unresolved",
		"srl1",
		"e1-1",
		"missing",
		"e1-1",
		0,
	)
	valid := reconcileTestLink("b-valid", "srl1", "e1-1", "srl2", "e1-1", 0)
	srl1 := reconcileTestNode("srl1")
	srl2 := reconcileTestNode("srl2")

	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&clabernetesapisv1alpha1.Link{}).
		WithObjects(&unresolved, &valid, &srl1, &srl2).
		Build()
	controller := &Controller{
		BaseController: &clabernetescontrollers.BaseController{
			Log:    &claberneteslogging.FakeInstance{},
			Client: client,
		},
		apiReader: client,
	}

	_, err = controller.Reconcile(
		context.Background(),
		ctrlruntime.Request{NamespacedName: apimachinerytypes.NamespacedName{
			Namespace: valid.GetNamespace(),
			Name:      valid.GetName(),
		}},
	)
	if err != nil {
		t.Fatalf("reconcile failed: %s", err)
	}

	actual := &clabernetesapisv1alpha1.Link{}

	err = client.Get(
		context.Background(),
		apimachinerytypes.NamespacedName{
			Namespace: valid.GetNamespace(),
			Name:      valid.GetName(),
		},
		actual,
	)
	if err != nil {
		t.Fatalf("failed getting reconciled link: %s", err)
	}

	if actual.Status.Error != "" || actual.Status.TunnelID != 1 {
		t.Fatalf(
			"expected valid Link to allocate despite unresolved conflict, got %+v",
			actual.Status,
		)
	}

	condition := apimachinerymeta.FindStatusCondition(
		actual.Status.Conditions,
		clabernetesapisv1alpha1.LinkConditionAccepted,
	)
	if condition == nil || condition.Status != metav1.ConditionTrue ||
		condition.Reason != "Accepted" || condition.ObservedGeneration != actual.GetGeneration() {
		t.Fatalf("valid Link Accepted condition = %#v", condition)
	}
}

func TestReconcileDeletesLinkWhenBoundEndpointIsDeleted(t *testing.T) {
	link := reconcileTestLink("link", "srl1", "e1-1", "srl2", "e1-1", 0)
	srl1 := reconcileTestNode("srl1")
	srl2 := reconcileTestNode("srl2")
	controller, client := newLifecycleTestController(t, &link, &srl1, &srl2)

	reconcileLifecycleLink(t, controller, link.GetName())
	requireBoundLink(t, client, link.GetName(), srl1.GetUID(), srl2.GetUID())

	err := client.Delete(context.Background(), &srl2)
	if err != nil {
		t.Fatalf("failed deleting endpoint Node: %s", err)
	}

	reconcileLifecycleLink(t, controller, link.GetName())

	actual := &clabernetesapisv1alpha1.Link{}

	err = client.Get(
		context.Background(),
		apimachinerytypes.NamespacedName{
			Namespace: link.GetNamespace(),
			Name:      link.GetName(),
		},
		actual,
	)
	if !apimachineryerrors.IsNotFound(err) {
		t.Fatalf("expected bound Link to be deleted, got error: %v", err)
	}
}

func TestReconcileDeletesLinkWhenBoundEndpointIsReplaced(t *testing.T) {
	link := reconcileTestLink("link", "srl1", "e1-1", "srl2", "e1-1", 0)
	srl1 := reconcileTestNode("srl1")
	srl2 := reconcileTestNode("srl2")
	controller, client := newLifecycleTestController(t, &link, &srl1, &srl2)

	reconcileLifecycleLink(t, controller, link.GetName())
	requireBoundLink(t, client, link.GetName(), srl1.GetUID(), srl2.GetUID())

	err := client.Delete(context.Background(), &srl2)
	if err != nil {
		t.Fatalf("failed deleting original endpoint Node: %s", err)
	}

	replacement := reconcileTestNode("srl2")
	replacement.UID = "srl2-replacement-uid"

	err = client.Create(context.Background(), &replacement)
	if err != nil {
		t.Fatalf("failed creating replacement endpoint Node: %s", err)
	}

	reconcileLifecycleLink(t, controller, link.GetName())

	actual := &clabernetesapisv1alpha1.Link{}

	err = client.Get(
		context.Background(),
		apimachinerytypes.NamespacedName{
			Namespace: link.GetNamespace(),
			Name:      link.GetName(),
		},
		actual,
	)
	if !apimachineryerrors.IsNotFound(err) {
		t.Fatalf("expected Link bound to replaced Node to be deleted, got error: %v", err)
	}
}

func TestReconcileBindsLinkAfterInitiallyMissingEndpointAppears(t *testing.T) {
	link := reconcileTestLink("link", "srl1", "e1-1", "srl2", "e1-1", 0)
	srl1 := reconcileTestNode("srl1")
	controller, client := newLifecycleTestController(t, &link, &srl1)

	reconcileLifecycleLink(t, controller, link.GetName())

	actual := getLifecycleTestLink(t, client, link.GetName())
	if actual.Status.ResolvedEndpoints != nil {
		t.Fatalf(
			"expected never-resolved Link to have no endpoint binding, got %+v",
			actual.Status.ResolvedEndpoints,
		)
	}

	if !strings.Contains(actual.Status.Error, "srl2") {
		t.Fatalf("expected unresolved endpoint error, got %q", actual.Status.Error)
	}

	srl2 := reconcileTestNode("srl2")

	err := client.Create(context.Background(), &srl2)
	if err != nil {
		t.Fatalf("failed creating missing endpoint Node: %s", err)
	}

	reconcileLifecycleLink(t, controller, link.GetName())
	requireBoundLink(t, client, link.GetName(), srl1.GetUID(), srl2.GetUID())
}

func TestReconcileBindsHostEndpointWithoutNodeUID(t *testing.T) {
	link := reconcileTestLink("link", "srl1", "e1-1", "host", "eth1", 0)
	srl1 := reconcileTestNode("srl1")
	controller, client := newLifecycleTestController(t, &link, &srl1)

	reconcileLifecycleLink(t, controller, link.GetName())
	reconcileLifecycleLink(t, controller, link.GetName())

	actual := getLifecycleTestLink(t, client, link.GetName())
	if actual.Status.ResolvedEndpoints == nil {
		t.Fatal("expected host Link endpoint identities to be resolved")
	}

	if actual.Status.ResolvedEndpoints.EndpointA.NodeName != srl1.GetName() ||
		actual.Status.ResolvedEndpoints.EndpointA.UID != srl1.GetUID() {
		t.Fatalf(
			"unexpected Node endpoint binding: %+v",
			actual.Status.ResolvedEndpoints.EndpointA,
		)
	}

	if actual.Status.ResolvedEndpoints.EndpointB.NodeName !=
		clabernetesapisv1alpha1.LinkHostNodeName ||
		actual.Status.ResolvedEndpoints.EndpointB.UID != "" {
		t.Fatalf(
			"expected host endpoint name with no UID, got %+v",
			actual.Status.ResolvedEndpoints.EndpointB,
		)
	}

	if actual.Status.Error != "" || actual.Status.TunnelID != 0 {
		t.Fatalf("expected valid local host Link status, got %+v", actual.Status)
	}

	// Host Link state is Pod-namespace-scoped; no node-local finalizer may gate deletion.
	if slices.Contains(
		actual.GetFinalizers(),
		clabernetesapisv1alpha1.LinkHostEndpointFinalizer,
	) {
		t.Fatalf("host Link carries a daemon-era finalizer: %v", actual.GetFinalizers())
	}
}

func TestReconcileUnrelatedNodeDeletionDoesNotAffectBoundLink(t *testing.T) {
	link := reconcileTestLink("link", "srl1", "e1-1", "srl2", "e1-1", 0)
	srl1 := reconcileTestNode("srl1")
	srl2 := reconcileTestNode("srl2")
	unrelated := reconcileTestNode("unrelated")
	controller, client := newLifecycleTestController(t, &link, &srl1, &srl2, &unrelated)

	reconcileLifecycleLink(t, controller, link.GetName())

	before := getLifecycleTestLink(t, client, link.GetName())
	if before.Status.ResolvedEndpoints == nil {
		t.Fatal("expected Link to be bound before unrelated Node deletion")
	}

	beforeResolved := *before.Status.ResolvedEndpoints

	err := client.Delete(context.Background(), &unrelated)
	if err != nil {
		t.Fatalf("failed deleting unrelated Node: %s", err)
	}

	reconcileLifecycleLink(t, controller, link.GetName())

	after := getLifecycleTestLink(t, client, link.GetName())
	if after.Status.ResolvedEndpoints == nil ||
		!reflect.DeepEqual(*after.Status.ResolvedEndpoints, beforeResolved) ||
		after.Status.TunnelID != before.Status.TunnelID ||
		after.Status.Error != before.Status.Error {
		t.Fatalf(
			"expected unrelated Node deletion not to affect Link status, before=%+v after=%+v",
			before.Status,
			after.Status,
		)
	}
}

func TestReconcileAllowsIntentionalEndpointRewire(t *testing.T) {
	link := reconcileTestLink("link", "srl1", "e1-1", "srl2", "e1-1", 0)
	srl1 := reconcileTestNode("srl1")
	srl2 := reconcileTestNode("srl2")
	srl3 := reconcileTestNode("srl3")
	controller, client := newLifecycleTestController(t, &link, &srl1, &srl2, &srl3)

	reconcileLifecycleLink(t, controller, link.GetName())
	requireBoundLink(t, client, link.GetName(), srl1.GetUID(), srl2.GetUID())

	rewired := getLifecycleTestLink(t, client, link.GetName())
	rewired.Spec.EndpointB.NodeName = srl3.GetName()

	err := client.Update(context.Background(), rewired)
	if err != nil {
		t.Fatalf("failed rewiring Link spec: %s", err)
	}

	err = client.Delete(context.Background(), &srl2)
	if err != nil {
		t.Fatalf("failed deleting former endpoint Node: %s", err)
	}

	reconcileLifecycleLink(t, controller, link.GetName())
	requireBoundLink(t, client, link.GetName(), srl1.GetUID(), srl3.GetUID())
}

func TestReconcilePreservesBindingAcrossEndpointConflict(t *testing.T) {
	link := reconcileTestLink("z-link", "srl1", "e1-1", "srl2", "e1-1", 0)
	srl1 := reconcileTestNode("srl1")
	srl2 := reconcileTestNode("srl2")
	controller, client := newLifecycleTestController(t, &link, &srl1, &srl2)

	reconcileLifecycleLink(t, controller, link.GetName())

	before := requireBoundLink(t, client, link.GetName(), srl1.GetUID(), srl2.GetUID())
	beforeResolved := *before.Status.ResolvedEndpoints

	winner := reconcileTestLink("a-link", "srl1", "e1-1", "srl2", "e1-2", 0)

	err := client.Create(context.Background(), &winner)
	if err != nil {
		t.Fatalf("failed creating conflicting Link: %s", err)
	}

	reconcileLifecycleLink(t, controller, link.GetName())

	after := getLifecycleTestLink(t, client, link.GetName())
	if after.Status.ResolvedEndpoints == nil ||
		!reflect.DeepEqual(*after.Status.ResolvedEndpoints, beforeResolved) {
		t.Fatalf(
			"expected endpoint conflict to preserve binding, before=%+v after=%+v",
			before.Status.ResolvedEndpoints,
			after.Status.ResolvedEndpoints,
		)
	}

	if !strings.Contains(after.Status.Error, winner.GetName()) {
		t.Fatalf(
			"expected endpoint conflict error naming %q, got %q",
			winner.GetName(),
			after.Status.Error,
		)
	}
}

func newLifecycleTestController(
	t *testing.T,
	objects ...ctrlruntimeclient.Object,
) (*Controller, ctrlruntimeclient.Client) {
	t.Helper()

	scheme := apimachineryruntime.NewScheme()

	err := clabernetesapisv1alpha1.AddToScheme(scheme)
	if err != nil {
		t.Fatalf("failed adding api scheme: %s", err)
	}

	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&clabernetesapisv1alpha1.Link{}).
		WithObjects(objects...).
		Build()

	return &Controller{
		BaseController: &clabernetescontrollers.BaseController{
			Log:    &claberneteslogging.FakeInstance{},
			Client: client,
		},
		apiReader: client,
	}, client
}

func reconcileLifecycleLink(t *testing.T, controller *Controller, name string) {
	t.Helper()

	_, err := controller.Reconcile(
		context.Background(),
		ctrlruntime.Request{NamespacedName: apimachinerytypes.NamespacedName{
			Namespace: "clabernetes",
			Name:      name,
		}},
	)
	if err != nil {
		t.Fatalf("reconcile failed: %s", err)
	}
}

func getLifecycleTestLink(
	t *testing.T,
	client ctrlruntimeclient.Client,
	name string,
) *clabernetesapisv1alpha1.Link {
	t.Helper()

	actual := &clabernetesapisv1alpha1.Link{}

	err := client.Get(
		context.Background(),
		apimachinerytypes.NamespacedName{
			Namespace: "clabernetes",
			Name:      name,
		},
		actual,
	)
	if err != nil {
		t.Fatalf("failed getting Link %q: %s", name, err)
	}

	return actual
}

func requireBoundLink(
	t *testing.T,
	client ctrlruntimeclient.Client,
	name string,
	expectedUIDA,
	expectedUIDB apimachinerytypes.UID,
) *clabernetesapisv1alpha1.Link {
	t.Helper()

	actual := getLifecycleTestLink(t, client, name)
	if actual.Status.ResolvedEndpoints == nil {
		t.Fatalf("expected Link %q endpoint identities to be bound, got %+v", name, actual.Status)
	}

	if actual.Status.ResolvedEndpoints.EndpointA.NodeName != actual.Spec.EndpointA.NodeName ||
		actual.Status.ResolvedEndpoints.EndpointA.UID != expectedUIDA {
		t.Fatalf(
			"unexpected endpoint A binding: %+v",
			actual.Status.ResolvedEndpoints.EndpointA,
		)
	}

	if actual.Status.ResolvedEndpoints.EndpointB.NodeName != actual.Spec.EndpointB.NodeName ||
		actual.Status.ResolvedEndpoints.EndpointB.UID != expectedUIDB {
		t.Fatalf(
			"unexpected endpoint B binding: %+v",
			actual.Status.ResolvedEndpoints.EndpointB,
		)
	}

	if actual.Status.Error != "" {
		t.Fatalf("expected bound Link %q to be valid, got error %q", name, actual.Status.Error)
	}

	return actual
}

func reconcileTestNode(name string) clabernetesapisv1alpha1.Node {
	return clabernetesapisv1alpha1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "clabernetes",
			UID:       apimachinerytypes.UID(name + "-uid"),
		},
	}
}

func reconcileTestLink(
	name,
	nodeA,
	interfaceA,
	nodeB,
	interfaceB string,
	tunnelID int,
) clabernetesapisv1alpha1.Link {
	return clabernetesapisv1alpha1.Link{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "clabernetes"},
		Spec: clabernetesapisv1alpha1.LinkSpec{
			EndpointA: clabernetesapisv1alpha1.LinkEndpointSpec{
				NodeName:      nodeA,
				InterfaceName: interfaceA,
			},
			EndpointB: clabernetesapisv1alpha1.LinkEndpointSpec{
				NodeName:      nodeB,
				InterfaceName: interfaceB,
			},
		},
		Status: clabernetesapisv1alpha1.LinkStatus{TunnelID: tunnelID},
	}
}
