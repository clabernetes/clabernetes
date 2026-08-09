package node //nolint:testpackage // Tests intentionally exercise unexported event index helpers.

import (
	"context"
	"reflect"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetescontrollers "github.com/clabernetes/clabernetes/controllers"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	ctrlruntimereconcile "sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestLauncherProfileReferenceIndex(t *testing.T) {
	node := &clabernetesapisv1alpha1.Node{}
	if values := launcherProfileReferenceIndex(node); values != nil {
		t.Fatalf("expected omitted reference not to be indexed, got %v", values)
	}

	node.Spec.LauncherProfileRef = &k8scorev1.LocalObjectReference{Name: "profile-a"}

	values := launcherProfileReferenceIndex(node)
	if len(values) != 1 || values[0] != "profile-a" {
		t.Fatalf("expected explicit reference index value, got %v", values)
	}
}

func TestLauncherProfileEventEnqueuesOnlyReferencingGroups(t *testing.T) {
	scheme := apimachineryruntime.NewScheme()

	err := clabernetesapisv1alpha1.AddToScheme(scheme)
	if err != nil {
		t.Fatalf("failed adding API scheme: %s", err)
	}

	namespace := "clabernetes"
	primary := &clabernetesapisv1alpha1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "srl1", Namespace: namespace},
		Spec: clabernetesapisv1alpha1.NodeSpec{
			LauncherProfileRef: &k8scorev1.LocalObjectReference{Name: "profile-a"},
		},
	}
	secondary := &clabernetesapisv1alpha1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "sim-a", Namespace: namespace},
		Spec: clabernetesapisv1alpha1.NodeSpec{
			NodeDefinition: clabernetesapisv1alpha1.NodeDefinition{
				NetworkMode: "container:srl1",
			},
			LauncherProfileRef: &k8scorev1.LocalObjectReference{Name: "profile-a"},
		},
	}
	unrelated := &clabernetesapisv1alpha1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "srl2", Namespace: namespace},
		Spec: clabernetesapisv1alpha1.NodeSpec{
			LauncherProfileRef: &k8scorev1.LocalObjectReference{Name: "profile-b"},
		},
	}

	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(primary, secondary, unrelated).
		WithIndex(
			&clabernetesapisv1alpha1.Node{},
			launcherProfileReferenceField,
			launcherProfileReferenceIndex,
		).
		Build()
	controller := &Controller{
		BaseController: &clabernetescontrollers.BaseController{
			Client: client,
			Log:    &claberneteslogging.FakeInstance{},
		},
	}
	profile := &clabernetesapisv1alpha1.LauncherProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "profile-a", Namespace: namespace},
	}

	requests := controller.enqueueLaunchersForLauncherProfile(context.Background(), profile)
	if len(requests) != 1 || requests[0].Name != primary.GetName() {
		t.Fatalf("expected only launcher %q, got %+v", primary.GetName(), requests)
	}
}

func TestLinkUpdateEnqueuesOldAndNewEndpointLaunchers(t *testing.T) {
	scheme := apimachineryruntime.NewScheme()

	err := clabernetesapisv1alpha1.AddToScheme(scheme)
	if err != nil {
		t.Fatalf("failed adding API scheme: %s", err)
	}

	namespace := "clabernetes"

	nodes := make([]ctrlruntimeclient.Object, 0, 4)
	for _, name := range []string{"r1", "r2", "r3", "r4"} {
		nodes = append(nodes, &clabernetesapisv1alpha1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		})
	}

	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(nodes...).
		Build()
	controller := &Controller{
		BaseController: &clabernetescontrollers.BaseController{
			Client: client,
			Log:    &claberneteslogging.FakeInstance{},
		},
	}

	oldLink := controllerTestLink(namespace, "r1", "r2")
	newLink := controllerTestLink(namespace, "r3", "r4")
	requests := controller.enqueueLaunchersForLinkObjects(
		context.Background(),
		oldLink,
		newLink,
	)

	if got := requestNames(requests); !reflect.DeepEqual(got, []string{"r1", "r2", "r3", "r4"}) {
		t.Fatalf("expected former and new endpoint launchers, got %v", got)
	}
}

func TestLinkConnectivityUpdateEnqueuesTerminatingLaunchers(t *testing.T) {
	scheme := apimachineryruntime.NewScheme()

	err := clabernetesapisv1alpha1.AddToScheme(scheme)
	if err != nil {
		t.Fatalf("failed adding API scheme: %s", err)
	}

	namespace := "clabernetes"
	r1 := &clabernetesapisv1alpha1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "r1", Namespace: namespace},
	}
	r2 := &clabernetesapisv1alpha1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "r2", Namespace: namespace},
	}
	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(r1, r2).
		Build()
	controller := &Controller{
		BaseController: &clabernetescontrollers.BaseController{
			Client: client,
			Log:    &claberneteslogging.FakeInstance{},
		},
	}

	oldLink := controllerTestLink(namespace, "r1", "r2")
	newLink := oldLink.DeepCopy()
	newLink.Spec.Connectivity = clabernetesapisv1alpha1.LinkConnectivitySlurpeeth

	requests := controller.enqueueLaunchersForLinkObjects(
		context.Background(),
		oldLink,
		newLink,
	)
	if got := requestNames(requests); !reflect.DeepEqual(got, []string{"r1", "r2"}) {
		t.Fatalf("expected both terminating launchers for connectivity change, got %v", got)
	}
}

func controllerTestLink(
	namespace,
	nodeA,
	nodeB string,
) *clabernetesapisv1alpha1.Link {
	return &clabernetesapisv1alpha1.Link{
		ObjectMeta: metav1.ObjectMeta{Name: "link", Namespace: namespace},
		Spec: clabernetesapisv1alpha1.LinkSpec{
			EndpointA: clabernetesapisv1alpha1.LinkEndpointSpec{
				NodeName:      nodeA,
				InterfaceName: "eth1",
			},
			EndpointB: clabernetesapisv1alpha1.LinkEndpointSpec{
				NodeName:      nodeB,
				InterfaceName: "eth1",
			},
		},
	}
}

func requestNames(requests []ctrlruntimereconcile.Request) []string {
	names := make([]string, len(requests))
	for idx := range requests {
		names[idx] = requests[idx].Name
	}

	return names
}
