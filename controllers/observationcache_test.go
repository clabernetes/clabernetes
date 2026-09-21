package controllers_test

import (
	"context"
	"testing"

	clabernetescontrollers "github.com/clabernetes/clabernetes/controllers"
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	clientgoworkqueue "k8s.io/client-go/util/workqueue"
	ctrlruntimeevent "sigs.k8s.io/controller-runtime/pkg/event"
	ctrlruntimehandler "sigs.k8s.io/controller-runtime/pkg/handler"
	ctrlruntimereconcile "sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestObservationCacheRejectsInvalidatedInFlightWork(t *testing.T) {
	t.Parallel()
	cache := &clabernetescontrollers.ObservationCache[string]{}
	key := apimachinerytypes.NamespacedName{Namespace: "lab", Name: "node"}
	_, before, _ := cache.Load(key)
	cache.Invalidate(key)
	_, after, _ := cache.Load(key)
	cache.Store(key, before, "stale")
	if _, _, valid := cache.Load(key); valid {
		t.Fatal("accepted a snapshot invalidated while reconciling")
	}
	cache.Store(key, after, "current")
	if value, _, valid := cache.Load(key); !valid || value != "current" {
		t.Fatal("lost current snapshot")
	}
}

func TestObservationHandlerDistinguishesStatusFromDrift(t *testing.T) {
	t.Parallel()
	request := ctrlruntimereconcile.Request{
		NamespacedName: apimachinerytypes.NamespacedName{Name: "owner"},
	}
	queue := clientgoworkqueue.NewTypedRateLimitingQueue(
		clientgoworkqueue.DefaultTypedControllerRateLimiter[ctrlruntimereconcile.Request](),
	)
	defer queue.ShutDown()
	invalidations := 0
	handler := clabernetescontrollers.ObserveWith(
		ctrlruntimehandler.Funcs{
			UpdateFunc: func(_ context.Context, _ ctrlruntimeevent.UpdateEvent, q clientgoworkqueue.TypedRateLimitingInterface[ctrlruntimereconcile.Request]) {
				q.Add(request)
			},
		},
		func(key apimachinerytypes.NamespacedName) {
			if key != request.NamespacedName {
				t.Fatal("invalidated wrong owner")
			}
			invalidations++
		},
	)
	old := &k8scorev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "device", UID: "uid", ResourceVersion: "1"},
	}
	current := old.DeepCopy()
	current.ResourceVersion = "2"
	current.Status.Phase = k8scorev1.PodRunning
	handler.Update(
		context.Background(),
		ctrlruntimeevent.UpdateEvent{ObjectOld: old, ObjectNew: current},
		queue,
	)
	if invalidations != 0 || queue.Len() != 1 {
		t.Fatal("status should enqueue observation without discarding desired state")
	}
	current.Spec.NodeName = "changed-worker"
	handler.Update(
		context.Background(),
		ctrlruntimeevent.UpdateEvent{ObjectOld: old, ObjectNew: current},
		queue,
	)
	if invalidations != 1 {
		t.Fatal("spec drift did not invalidate snapshot")
	}
}
