//nolint:testpackage // Exercise namespace reconciliation and its event boundary.
package node

import (
	"maps"
	"testing"

	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesinternaldirectpod "github.com/clabernetes/clabernetes/internal/directpod"
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestNamespacePeerDirectoryTracksAddressesAndLastNodeDeletion(t *testing.T) {
	t.Parallel()
	node := planInputTestNode("router", "uid-router", "linux", "busybox")
	node.Spec.ProfileRef = nil
	pod := &k8scorev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "router-pod", Namespace: node.Namespace,
			Labels: map[string]string{clabernetesconstants.LabelDirectWorkload: node.Name},
			Annotations: map[string]string{
				clabernetesinternaldirectpod.NodeUIDAnnotation: string(node.UID),
			},
		},
		Status: k8scorev1.PodStatus{PodIP: "10.244.0.1"},
	}
	client := ctrlruntimefake.NewClientBuilder().WithScheme(nodeReconcileTestScheme(t)).
		WithObjects(node, pod).WithStatusSubresource(pod).Build()
	r := newPeerDirectoryReconciler(client)
	request := ctrlruntime.Request{
		NamespacedName: ctrlruntimeclient.ObjectKey{Namespace: node.Namespace},
	}
	reconcile := func() {
		t.Helper()
		if _, err := r.reconcileNamespacePeerDirectory(t.Context(), request); err != nil {
			t.Fatal(err)
		}
	}
	reconcile()
	want := map[string]string{node.Name: pod.Status.PodIP}
	versions := peerDirectoryShardVersions(t, client, node.Namespace, want)
	reconcile()
	if !maps.Equal(versions, peerDirectoryShardVersions(t, client, node.Namespace, want)) {
		t.Fatal("unchanged namespace rewrote the peer directory")
	}
	pod.Status.PodIP = "10.244.0.2"
	if err := client.Status().Update(t.Context(), pod); err != nil {
		t.Fatal(err)
	}
	reconcile()
	peerDirectoryShardVersions(
		t,
		client,
		node.Namespace,
		map[string]string{node.Name: pod.Status.PodIP},
	)
	if err := client.Delete(t.Context(), node); err != nil {
		t.Fatal(err)
	}
	reconcile()
	peerDirectoryShardVersions(t, client, node.Namespace, nil)
	// A bad profile on another Node must not stop valid namespace updates.
	node.ResourceVersion = ""
	if err := client.Create(t.Context(), node); err != nil {
		t.Fatal(err)
	}
	invalid := planInputTestNode("invalid", "invalid-uid", "linux", "busybox")
	invalid.Spec.ProfileRef = &k8scorev1.LocalObjectReference{Name: "missing-profile"}
	if err := client.Create(t.Context(), invalid); err != nil {
		t.Fatal(err)
	}
	reconcile()
	peerDirectoryShardVersions(
		t,
		client,
		node.Namespace,
		map[string]string{node.Name: pod.Status.PodIP},
	)
	request.Namespace = "unrelated"
	reconcile()
	configMaps := &k8scorev1.ConfigMapList{}
	if err := client.List(t.Context(), configMaps, ctrlruntimeclient.InNamespace(request.Namespace)); err != nil {
		t.Fatal(err)
	}
	if len(configMaps.Items) != 0 {
		t.Fatal("empty unrelated namespace acquired peer directory resources")
	}
}
