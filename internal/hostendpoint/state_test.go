//nolint:gocyclo,noinlineerr,testpackage,wsl_v5 // Identity tests use compact fail-fast assertions.
package hostendpoint

import (
	"context"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	k8scorev1 "k8s.io/api/core/v1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestKubernetesStateResolvesGroupedHostEndpointByImmutableIdentity(t *testing.T) {
	t.Parallel()
	primary := hostEndpointTestNode("lab", "primary", "primary-uid", "")
	secondary := hostEndpointTestNode(
		"lab",
		"secondary",
		"secondary-uid",
		"container:primary",
	)
	pod := hostEndpointTestPod("lab", "primary-pod", "pod-uid", "primary", "worker-a")
	link := hostEndpointTestLink(
		"lab",
		"host-link",
		"link-uid",
		"secondary",
		"secondary-uid",
		"eth3",
		"host3",
	)
	client := hostEndpointTestClient(t, primary, secondary, pod, link)
	state := KubernetesState{Client: client}
	desired, err := state.DesiredForNode(context.Background(), "worker-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(desired) != 1 {
		t.Fatalf("expected one desired host endpoint, got %#v", desired)
	}
	endpoint := desired[0]
	if endpoint.Node.Name != "secondary" || endpoint.Node.UID != "secondary-uid" ||
		endpoint.PodInterface != "eth3" || endpoint.HostInterface != "host3" ||
		endpointPod(endpoint).UID != "pod-uid" {
		t.Fatalf("unexpected grouped host endpoint: %#v", endpoint)
	}
	expected, err := state.ExpectedForPod(
		context.Background(),
		"worker-a",
		testIdentity("lab", "primary-pod", "pod-uid"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(expected) != 1 || endpointPod(expected[0]) != (ObjectIdentity{}) {
		t.Fatalf("Pod-authorized endpoint was not wire-safe: %#v", expected)
	}
	if err = state.MarkPending(
		context.Background(),
		"worker-a",
		testIdentity("lab", "primary-pod", "pod-uid"),
		expected[0],
	); err != nil {
		t.Fatal(err)
	}
	actual := &clabernetesapisv1alpha1.Link{}
	if err = client.Get(
		context.Background(),
		apimachinerytypes.NamespacedName{Namespace: "lab", Name: "host-link"},
		actual,
	); err != nil {
		t.Fatal(err)
	}
	if actual.GetAnnotations()[AppliedNodeAnnotation] != "worker-a" ||
		actual.GetAnnotations()[AppliedPodUIDAnnotation] != "pod-uid" {
		t.Fatalf("immutable host ownership was not recorded: %#v", actual.GetAnnotations())
	}
	actual.Spec.EndpointA.InterfaceName = "eth4"
	if err = client.Update(context.Background(), actual); err != nil {
		t.Fatal(err)
	}
	if err = state.MarkPending(
		context.Background(),
		"worker-a",
		testIdentity("lab", "primary-pod", "pod-uid"),
		expected[0],
	); err == nil {
		t.Fatal("stale endpoint intent was accepted after the Link changed")
	}
}

func TestKubernetesStateSelectsStableWorkerScopedHostInterfaceOwner(t *testing.T) {
	t.Parallel()
	winnerNode := hostEndpointTestNode("lab", "winner", "winner-node-uid", "")
	loserNode := hostEndpointTestNode("lab", "loser", "loser-node-uid", "")
	winnerPod := hostEndpointTestPod(
		"lab",
		"winner-pod",
		"winner-pod-uid",
		"winner",
		"worker-a",
	)
	loserPod := hostEndpointTestPod(
		"lab",
		"loser-pod",
		"loser-pod-uid",
		"loser",
		"worker-a",
	)
	winnerLink := hostEndpointTestLink(
		"lab",
		"a-host-link",
		"winner-link-uid",
		"winner",
		"winner-node-uid",
		"eth1",
		"shared-host",
	)
	loserLink := hostEndpointTestLink(
		"lab",
		"z-host-link",
		"loser-link-uid",
		"loser",
		"loser-node-uid",
		"eth1",
		"shared-host",
	)
	state := KubernetesState{Client: hostEndpointTestClient(
		t,
		winnerNode,
		loserNode,
		winnerPod,
		loserPod,
		winnerLink,
		loserLink,
	)}
	desired, err := state.DesiredForNode(context.Background(), "worker-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(desired) != 1 || desired[0].Link.Name != "a-host-link" ||
		endpointPod(desired[0]).UID != "winner-pod-uid" {
		t.Fatalf("worker-scoped collision winner = %#v", desired)
	}
	winner, err := state.ExpectedForPod(
		context.Background(),
		"worker-a",
		testIdentity("lab", "winner-pod", "winner-pod-uid"),
	)
	if err != nil {
		t.Fatal(err)
	}
	loser, err := state.ExpectedForPod(
		context.Background(),
		"worker-a",
		testIdentity("lab", "loser-pod", "loser-pod-uid"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(winner) != 1 || len(loser) != 0 {
		t.Fatalf("collision authorization = winner %#v, loser %#v", winner, loser)
	}
	operations := &fakeOperations{}
	daemon := &Daemon{NodeName: "worker-a", State: state, Operations: operations}
	err = daemon.Reconcile(context.Background(), ReconcileRequest{
		SchemaVersion: SchemaVersion,
		Pod:           testIdentity("lab", "loser-pod", "loser-pod-uid"),
		Endpoints: []Endpoint{{
			Link:          testIdentity("lab", "z-host-link", "loser-link-uid"),
			Node:          testIdentity("lab", "loser", "loser-node-uid"),
			HostInterface: "shared-host",
			PodInterface:  "eth1",
			MTU:           1450,
		}},
	}, 1)
	if err == nil || len(operations.events) != 0 {
		t.Fatalf(
			"losing collision request mutated host state: err=%v events=%#v",
			err,
			operations.events,
		)
	}
}

func TestDaemonRecoversHostEndpointAfterForcedPodDeletionAndReschedule(t *testing.T) {
	t.Parallel()
	node := hostEndpointTestNode("lab", "router", "node-uid", "")
	oldPod := hostEndpointTestPod("lab", "router-old", "old-pod-uid", "router", "worker-a")
	link := hostEndpointTestLink(
		"lab",
		"host-link",
		"link-uid",
		"router",
		"node-uid",
		"eth1",
		"host1",
	)
	client := hostEndpointTestClient(t, node, oldPod, link)
	state := KubernetesState{Client: client}
	oldDesired, err := state.DesiredForNode(context.Background(), "worker-a")
	if err != nil || len(oldDesired) != 1 {
		t.Fatalf("old desired endpoint = %#v, err=%v", oldDesired, err)
	}
	oldOwnership := ownershipFor(oldDesired[0], endpointPod(oldDesired[0]))
	if err = client.Delete(context.Background(), oldPod); err != nil {
		t.Fatal(err)
	}
	newPod := hostEndpointTestPod("lab", "router-new", "new-pod-uid", "router", "worker-a")
	if err = client.Create(context.Background(), newPod); err != nil {
		t.Fatal(err)
	}
	newExpected, err := state.ExpectedForPod(
		context.Background(),
		"worker-a",
		testIdentity("lab", "router-new", "new-pod-uid"),
	)
	if err != nil || len(newExpected) != 1 {
		t.Fatalf("rescheduled endpoint = %#v, err=%v", newExpected, err)
	}
	operations := &fakeOperations{owned: []OwnedEndpoint{{
		HostInterface: "host1", Ownership: oldOwnership,
	}}}
	daemon := &Daemon{NodeName: "worker-a", State: state, Operations: operations}
	if err = daemon.Reconcile(context.Background(), ReconcileRequest{
		SchemaVersion: SchemaVersion,
		Pod:           testIdentity("lab", "router-new", "new-pod-uid"),
		Endpoints:     newExpected,
	}, 1); err != nil {
		t.Fatal(err)
	}
	if len(operations.deleted) != 1 || operations.deleted[0].Ownership != oldOwnership ||
		len(operations.ensured) != 1 {
		t.Fatalf(
			"reschedule recovery = deleted %#v, ensured %#v",
			operations.deleted,
			operations.ensured,
		)
	}
}

func TestKubernetesStateReleasesOnlyThisDaemonFinalizers(t *testing.T) {
	t.Parallel()
	owned := &clabernetesapisv1alpha1.Link{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   "lab",
			Name:        "former-host",
			UID:         "former-link-uid",
			Finalizers:  []string{clabernetesapisv1alpha1.LinkHostEndpointFinalizer},
			Annotations: map[string]string{AppliedNodeAnnotation: "worker-a"},
		},
		Spec: clabernetesapisv1alpha1.LinkSpec{
			EndpointA: clabernetesapisv1alpha1.LinkEndpointSpec{
				NodeName: "router-a", InterfaceName: "eth1",
			},
			EndpointB: clabernetesapisv1alpha1.LinkEndpointSpec{
				NodeName: "router-b", InterfaceName: "eth1",
			},
		},
	}
	other := owned.DeepCopy()
	other.Name = "other-worker"
	other.UID = "other-link-uid"
	other.Annotations = map[string]string{AppliedNodeAnnotation: "worker-b"}
	client := hostEndpointTestClient(t, owned, other)
	state := KubernetesState{Client: client}
	links, err := state.FinalizingLinks(context.Background(), "worker-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || links[0].Identity.Name != "former-host" {
		t.Fatalf("daemon received another worker's finalizer: %#v", links)
	}
	if err = state.RemoveFinalizer(context.Background(), "worker-a", links[0].Identity); err != nil {
		t.Fatal(err)
	}
	actual := &clabernetesapisv1alpha1.Link{}
	if err = client.Get(
		context.Background(),
		apimachinerytypes.NamespacedName{Namespace: "lab", Name: "former-host"},
		actual,
	); err != nil {
		t.Fatal(err)
	}
	if len(actual.GetFinalizers()) != 0 || len(actual.GetAnnotations()) != 0 {
		t.Fatalf("released Link retained daemon ownership: %#v", actual.ObjectMeta)
	}
}

func TestKubernetesStateNormalHostLinkDeletionBecomesFinalizable(t *testing.T) {
	t.Parallel()
	link := hostEndpointTestLink(
		"lab",
		"deleting-host",
		"deleting-link-uid",
		"router",
		"node-uid",
		"eth1",
		"host1",
	)
	now := metav1.Now()
	link.DeletionTimestamp = &now
	link.Annotations = map[string]string{
		AppliedNodeAnnotation: "worker-a", AppliedPodUIDAnnotation: "pod-uid",
	}
	client := hostEndpointTestClient(t, link)
	state := KubernetesState{Client: client}
	links, err := state.FinalizingLinks(context.Background(), "worker-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || links[0].Identity.UID != "deleting-link-uid" {
		t.Fatalf("deleting host Link is not finalizable: %#v", links)
	}
	if err = state.RemoveFinalizer(context.Background(), "worker-a", links[0].Identity); err != nil {
		t.Fatal(err)
	}
	actual := &clabernetesapisv1alpha1.Link{}
	err = client.Get(
		context.Background(),
		apimachinerytypes.NamespacedName{Namespace: "lab", Name: "deleting-host"},
		actual,
	)
	if err != nil && !apimachineryerrors.IsNotFound(err) {
		t.Fatal(err)
	}
	if err == nil && len(actual.GetFinalizers()) != 0 {
		t.Fatalf("deleting host Link retained its finalizer: %#v", actual.GetFinalizers())
	}
}

func hostEndpointTestClient(
	t *testing.T,
	objects ...ctrlruntimeclient.Object,
) ctrlruntimeclient.Client {
	t.Helper()
	scheme := apimachineryruntime.NewScheme()
	if err := clabernetesapisv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := k8scorev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	return ctrlruntimefake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&clabernetesapisv1alpha1.Link{}).
		WithIndex(&k8scorev1.Pod{}, "spec.nodeName", func(object ctrlruntimeclient.Object) []string {
			pod, ok := object.(*k8scorev1.Pod)
			if !ok {
				return nil
			}
			if pod.Spec.NodeName == "" {
				return nil
			}

			return []string{pod.Spec.NodeName}
		}).
		WithObjects(objects...).
		Build()
}

func hostEndpointTestNode(
	namespace,
	name,
	uid,
	networkMode string,
) *clabernetesapisv1alpha1.Node {
	return &clabernetesapisv1alpha1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace, Name: name, UID: apimachinerytypes.UID(uid),
		},
		Spec: clabernetesapisv1alpha1.NodeSpec{
			NodeDefinition: clabernetesapisv1alpha1.NodeDefinition{NetworkMode: networkMode},
		},
	}
}

func hostEndpointTestPod(
	namespace,
	name,
	uid,
	workload,
	worker string,
) *k8scorev1.Pod {
	return &k8scorev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			UID:       apimachinerytypes.UID(uid),
			Labels:    map[string]string{clabernetesconstants.LabelDirectWorkload: workload},
		},
		Spec: k8scorev1.PodSpec{NodeName: worker},
	}
}

func hostEndpointTestLink(
	namespace,
	name,
	uid,
	nodeName,
	nodeUID,
	podInterface,
	hostInterface string,
) *clabernetesapisv1alpha1.Link {
	return &clabernetesapisv1alpha1.Link{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			UID:       apimachinerytypes.UID(uid),
			Finalizers: []string{
				clabernetesapisv1alpha1.LinkHostEndpointFinalizer,
			},
		},
		Spec: clabernetesapisv1alpha1.LinkSpec{
			EndpointA: clabernetesapisv1alpha1.LinkEndpointSpec{
				NodeName: nodeName, InterfaceName: podInterface,
			},
			EndpointB: clabernetesapisv1alpha1.LinkEndpointSpec{
				NodeName:      clabernetesapisv1alpha1.LinkHostNodeName,
				InterfaceName: hostInterface,
			},
			MTU: 1450,
		},
		Status: clabernetesapisv1alpha1.LinkStatus{
			ResolvedEndpoints: &clabernetesapisv1alpha1.LinkResolvedEndpointsStatus{
				EndpointA: clabernetesapisv1alpha1.LinkResolvedEndpointStatus{
					NodeName: nodeName, UID: apimachinerytypes.UID(nodeUID),
				},
				EndpointB: clabernetesapisv1alpha1.LinkResolvedEndpointStatus{
					NodeName: clabernetesapisv1alpha1.LinkHostNodeName,
				},
			},
		},
	}
}
