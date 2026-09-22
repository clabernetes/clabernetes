//nolint:testpackage // Exercise admission persistence and informer boundaries.
package node

import (
	"context"
	"fmt"
	"maps"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetescontrollers "github.com/clabernetes/clabernetes/controllers"
	clabernetesinternaldirectpod "github.com/clabernetes/clabernetes/internal/directpod"
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	ctrlruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func admissionTestController(client ctrlruntimeclient.Client, size int32) *Controller {
	return &Controller{
		BaseController: &clabernetescontrollers.BaseController{Client: client},
		reconciler: &Reconciler{configManagerGetter: func() clabernetesconfig.Manager {
			return clabernetesconfig.NewFakeManager(clabernetesconfig.WithRolloutBatchSize(size))
		}},
	}
}

func admissionTestNode(namespace, name string) *clabernetesapisv1alpha1.Node {
	return &clabernetesapisv1alpha1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			UID:       apimachinerytypes.UID(namespace + "-" + name),
		},
	}
}

func admissionTestPod(node *clabernetesapisv1alpha1.Node, ready bool) *k8scorev1.Pod {
	status := k8scorev1.ConditionFalse
	if ready {
		status = k8scorev1.ConditionTrue
	}

	return &k8scorev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      node.Name + "-pod",
			Namespace: node.Namespace,
			Labels:    map[string]string{clabernetesconstants.LabelDirectWorkload: node.Name},
			Annotations: map[string]string{
				clabernetesinternaldirectpod.NodeUIDAnnotation: string(node.UID),
			},
		},
		Status: k8scorev1.PodStatus{
			Conditions: []k8scorev1.PodCondition{
				{Type: k8scorev1.PodReadyToStartContainers, Status: status},
			},
		},
	}
}

func admissionTestAdvance(t *testing.T, c *Controller, want int) {
	t.Helper()
	if _, err := c.reconcileStartupAdmission(t.Context(), ctrlruntime.Request{}); err != nil {
		t.Fatal(err)
	}
	nodes := &clabernetesapisv1alpha1.NodeList{}
	if err := c.Client.List(t.Context(), nodes); err != nil {
		t.Fatal(err)
	}
	got := 0
	for i := range nodes.Items {
		if !startupAdmissionRequired(&nodes.Items[i], 1) {
			got++
		}
	}
	if got != want {
		t.Fatalf("admitted %d Nodes, want %d", got, want)
	}
}

func TestGlobalStartupAdmissionGroupsNamespacesAndRestart(t *testing.T) {
	t.Parallel()
	a := admissionTestNode("a", "primary")
	child := admissionTestNode("a", "secondary")
	child.Spec.NetworkMode = "container:primary"
	b := admissionTestNode("b", "primary")
	later := admissionTestNode("c", "primary")
	held := admissionTestNode("d", "held")
	held.Annotations = map[string]string{clabernetesconstants.AnnotationStartupHold: "topology-uid"}
	ignored := admissionTestNode("a", "ignored")
	ignored.Labels = map[string]string{clabernetesconstants.LabelIgnoreReconcile: "true"}
	missing := admissionTestNode("a", "missing")
	missing.Spec.NetworkMode = "container:absent"
	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(nodeReconcileTestScheme(t)).
		WithStatusSubresource(&k8scorev1.Pod{}).
		WithObjects(a, child, b, later, held, ignored, missing).
		Build()
	c := admissionTestController(client, 2)
	admissionTestAdvance(
		t,
		c,
		3,
	) // Two primary workloads, including one shared-Pod member.
	c = admissionTestController(client, 2) // Admission survives a manager restart.
	admissionTestAdvance(t, c, 3)
	pa, pb := admissionTestPod(a, true), admissionTestPod(b, false)
	pb.Status.Conditions = append(
		pb.Status.Conditions,
		k8scorev1.PodCondition{Type: k8scorev1.PodReady, Status: k8scorev1.ConditionTrue},
	)
	for _, pod := range []*k8scorev1.Pod{pa, pb} {
		if err := client.Create(t.Context(), pod); err != nil {
			t.Fatal(err)
		}
	}
	admissionTestAdvance(t, c, 3) // Application Ready cannot substitute for the sandbox barrier.
	pb.Status.Conditions[0].Status = k8scorev1.ConditionTrue
	if err := client.Status().Update(t.Context(), pb); err != nil {
		t.Fatal(err)
	}
	admissionTestAdvance(
		t,
		c,
		4,
	) // No application Ready condition on pa: cross-batch links cannot deadlock.
	if !startupAdmissionRequired(held, 2) {
		t.Fatal("Topology-held workload bypassed local admission")
	}
	if startupAdmissionRequired(later, 0) {
		t.Fatal("disabling the global limit must release pending Nodes")
	}
	recreated := a.DeepCopy()
	recreated.UID = "new-uid"
	recreated.Annotations = map[string]string{
		clabernetesconstants.AnnotationStartupAdmitted: string(a.UID),
	}
	if !startupAdmissionRequired(recreated, 2) {
		t.Fatal("old UID admission reused for recreated Node")
	}
}

func TestGlobalStartupAdmissionAdoptsExistingAndIgnoresStalePodUID(t *testing.T) {
	t.Parallel()
	a, b, c := admissionTestNode("a", "a"), admissionTestNode("b", "b"), admissionTestNode("c", "c")
	stale := admissionTestPod(b, true)
	stale.Annotations[clabernetesinternaldirectpod.NodeUIDAnnotation] = "deleted-node"
	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(nodeReconcileTestScheme(t)).
		WithObjects(a, b, c, admissionTestPod(a, true), stale).
		Build()
	controller := admissionTestController(client, 1)
	admissionTestAdvance(
		t,
		controller,
		2,
	) // Existing ready a is adopted; b is the one new admission.
	admissionTestAdvance(t, controller, 2) // Stale Pod identity must not release c.
	if err := client.Delete(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	admissionTestAdvance(t, controller, 2) // Deleting the blocked workload releases c.
}

func TestGlobalStartupAdmissionCacheLagDoesNotRefillBatch(t *testing.T) {
	t.Parallel()
	objects := make([]ctrlruntimeclient.Object, 0, 5)
	for i := range 5 {
		objects = append(objects, admissionTestNode("lab", fmt.Sprintf("n%d", i)))
	}
	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(nodeReconcileTestScheme(t)).
		WithObjects(objects...).
		Build()
	patches := 0
	stale := interceptor.NewClient(client, interceptor.Funcs{
		List: func(ctx context.Context, c ctrlruntimeclient.WithWatch, list ctrlruntimeclient.ObjectList, opts ...ctrlruntimeclient.ListOption) error {
			if err := c.List(ctx, list, opts...); err != nil {
				return err
			}
			if nodes, ok := list.(*clabernetesapisv1alpha1.NodeList); ok {
				for i := range nodes.Items {
					delete(
						nodes.Items[i].Annotations,
						clabernetesconstants.AnnotationStartupAdmitted,
					)
				}
			}

			return nil
		},
		Patch: func(ctx context.Context, c ctrlruntimeclient.WithWatch, obj ctrlruntimeclient.Object, patch ctrlruntimeclient.Patch, opts ...ctrlruntimeclient.PatchOption) error {
			patches++

			return c.Patch(ctx, obj, patch, opts...)
		},
	})
	c := admissionTestController(stale, 2)
	for range 3 {
		if _, err := c.reconcileStartupAdmission(t.Context(), ctrlruntime.Request{}); err != nil {
			t.Fatal(err)
		}
	}
	if patches != 2 {
		t.Fatalf("cache lag caused %d admission writes, want 2", patches)
	}
	// Durable state, not this controller's overlay, is sufficient after a restart.
	admissionTestAdvance(t, admissionTestController(client, 2), 2)
}

func TestGlobalStartupAdmissionResumesPartialGroup(t *testing.T) {
	t.Parallel()
	a, child, b := admissionTestNode(
		"a",
		"primary",
	), admissionTestNode(
		"a",
		"secondary",
	), admissionTestNode(
		"b",
		"primary",
	)
	child.Spec.NetworkMode = "container:primary"
	a.Annotations = map[string]string{clabernetesconstants.AnnotationStartupAdmitted: string(a.UID)}
	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(nodeReconcileTestScheme(t)).
		WithObjects(a, child, b).
		Build()
	admissionTestAdvance(t, admissionTestController(client, 1), 2)
}

func TestDirectMetadataExcludesStartupAdmission(t *testing.T) {
	t.Parallel()
	r := &Reconciler{configManagerGetter: clabernetesconfig.GetFakeManager}
	node := admissionTestNode("lab", "device")
	node.Annotations = map[string]string{"example.com/user": "preserved"}
	beforeLabels, beforeAnnotations := r.directMetadata(node)
	node.Annotations[clabernetesconstants.AnnotationStartupAdmitted] = string(node.UID)
	node.Annotations[clabernetesconstants.AnnotationStartupHold] = "topology-uid"
	afterLabels, afterAnnotations := r.directMetadata(node)
	if !maps.Equal(beforeLabels, afterLabels) || !maps.Equal(beforeAnnotations, afterAnnotations) {
		t.Fatalf(
			"admission changed workload metadata: labels=%v annotations=%v",
			afterLabels,
			afterAnnotations,
		)
	}
	if node.Annotations[clabernetesconstants.AnnotationStartupAdmitted] != string(node.UID) {
		t.Fatal("filter mutated Node admission state")
	}
}

func TestIdleStartupAdmissionUsesSlowWatchdog(t *testing.T) {
	t.Parallel()
	client := ctrlruntimefake.NewClientBuilder().WithScheme(nodeReconcileTestScheme(t)).Build()
	controller := admissionTestController(client, 0)
	result, err := controller.reconcileStartupAdmission(t.Context(), ctrlruntime.Request{})
	if err != nil || result.RequeueAfter != directRequeueInterval {
		t.Fatalf("idle admission: result=%v err=%v", result, err)
	}
}
