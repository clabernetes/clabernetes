//nolint:testpackage // Verify admission cache and persistence boundaries.
package node

import (
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetesinternaldirectpod "github.com/clabernetes/clabernetes/internal/directpod"
	k8sappsv1 "k8s.io/api/apps/v1"
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

//nolint:gocyclo // Exercise the sequential admission, cache lag, and restart boundaries.
func TestStartupHostAdmission(t *testing.T) {
	t.Parallel()
	topology := &clabernetesapisv1alpha1.Topology{
		ObjectMeta: metav1.ObjectMeta{Name: "lab", Namespace: "a", UID: "lab-uid"},
		Spec: clabernetesapisv1alpha1.TopologySpec{
			Rollout: &clabernetesapisv1alpha1.TopologyRollout{MaxConcurrentPerHost: 1},
		},
	}
	nodes := make([]clabernetesapisv1alpha1.Node, 0, 5)
	var pods []k8scorev1.Pod
	objects := make([]ctrlruntimeclient.Object, 0, 11)
	objects = append(objects, topology)
	for i, location := range []struct{ namespace, host string }{{"a", "host1"}, {"a", "host1"}, {"b", "host1"}, {"c", "host1"}, {"a", "host2"}} {
		node := admissionTestNode(location.namespace, string(rune('a'+i)))
		if node.Namespace == "a" {
			node.OwnerReferences = []metav1.OwnerReference{
				*metav1.NewControllerRef(topology, clabernetesapisv1alpha1.SchemeGroupVersion.WithKind("Topology")),
			}
		}
		pod := admissionTestPod(node, true)
		pod.UID = apimachinerytypes.UID(node.Name + "-pod-uid")
		pod.Spec.NodeName = location.host
		pod.Spec.InitContainers = []k8scorev1.Container{
			{Name: clabernetesinternaldirectpod.StartupGateContainerName},
		}
		pod.Annotations[clabernetesinternaldirectpod.KubectlDefaultContainerAnnotation] = "device"
		nodes = append(nodes, *node)
		pods = append(pods, *pod)
		objects = append(objects, node, pod)
	}
	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(nodeReconcileTestScheme(t)).
		WithObjects(objects...).
		WithStatusSubresource(&k8scorev1.Pod{}).
		Build()
	controller := func(limit int32) *Controller {
		c := admissionTestController(client, 0)
		c.reconciler.configManagerGetter = func() clabernetesconfig.Manager {
			return clabernetesconfig.NewFakeManager(
				clabernetesconfig.WithRolloutMaxConcurrentPerHost(limit),
			)
		}

		return c
	}
	advance := func(c *Controller, snapshot []k8scorev1.Pod, want int) []k8scorev1.Pod {
		t.Helper()
		if err := c.admitStartupHosts(t.Context(), nodes, snapshot); err != nil {
			t.Fatal(err)
		}
		list := &k8scorev1.PodList{}
		if err := client.List(t.Context(), list); err != nil {
			t.Fatal(err)
		}
		got := 0
		for _, pod := range list.Items {
			if pod.Annotations[clabernetesinternaldirectpod.StartupAdmittedAnnotation] == string(
				pod.UID,
			) {
				got++
			}
		}
		if got != want {
			t.Fatalf("admitted %d, want %d", got, want)
		}

		return list.Items
	}
	snapshot := &k8scorev1.PodList{}
	if err := client.List(t.Context(), snapshot); err != nil {
		t.Fatal(err)
	}
	pods = snapshot.Items
	c := controller(2)
	stale := make([]k8scorev1.Pod, len(pods))
	for i := range pods {
		stale[i] = *pods[i].DeepCopy()
	}
	current := advance(
		c,
		pods,
		3,
	) // Topology cap=1, global cap=2, separate host progresses.
	advance(c, stale, 3) // Stale informer cannot over-admit.
	// Losing the owner of a booting Pod must not free a Topology slot.
	if err := controller(0).admitStartupHosts(t.Context(), nodes[1:], current); err != nil {
		t.Fatal(err)
	}
	missingOwner := &k8scorev1.PodList{}
	if err := client.List(t.Context(), missingOwner); err != nil {
		t.Fatal(err)
	}
	for _, pod := range missingOwner.Items {
		if pod.Name == "b-pod" &&
			pod.Annotations[clabernetesinternaldirectpod.StartupAdmittedAnnotation] != "" {
			t.Fatal("missing owner released an occupied slot")
		}
	}
	current = advance(controller(2), current, 3) // Persisted grants survive restart.
	for i := range current {
		if current[i].Name != "a-pod" {
			continue
		}
		started := true
		current[i].Status.ContainerStatuses = []k8scorev1.ContainerStatus{
			{Name: "device", Started: &started},
		}
		// PodReady deliberately remains false: later link peers must not hold this slot.
		if err := client.Status().Update(t.Context(), &current[i]); err != nil {
			t.Fatal(err)
		}
	}
	current = advance(controller(2), current, 4)
	// Removing limits releases waiting Pods without replacing them or revoking grants.
	topology.Spec.Rollout.MaxConcurrentPerHost = 0
	if err := client.Update(t.Context(), topology); err != nil {
		t.Fatal(err)
	}
	advance(controller(0), current, 5)
}

func TestStartupHostGateDoesNotRebootExistingDeployment(t *testing.T) {
	t.Parallel()
	r := admissionTestController(nil, 0).reconciler
	r.configManagerGetter = func() clabernetesconfig.Manager {
		return clabernetesconfig.NewFakeManager(
			clabernetesconfig.WithRolloutMaxConcurrentPerHost(50),
		)
	}
	existing := &k8sappsv1.Deployment{}
	enabled, err := r.startupHostGate(t.Context(), admissionTestNode("a", "one"), existing)
	if err != nil || enabled {
		t.Fatalf("existing workload gained gate: %v %v", enabled, err)
	}
	enabled, err = r.startupHostGate(t.Context(), admissionTestNode("a", "two"), nil)
	if err != nil || !enabled {
		t.Fatalf("new workload missing gate: %v %v", enabled, err)
	}
	existing.Spec.Template.Spec.InitContainers = []k8scorev1.Container{
		{Name: clabernetesinternaldirectpod.StartupGateContainerName},
	}
	r.configManagerGetter = func() clabernetesconfig.Manager { return clabernetesconfig.NewFakeManager() }
	enabled, err = r.startupHostGate(t.Context(), admissionTestNode("a", "one"), existing)
	if err != nil || !enabled {
		t.Fatalf("disabling limit changed existing template: %v %v", enabled, err)
	}
}
