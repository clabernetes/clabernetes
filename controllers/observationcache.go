package controllers

import (
	"context"
	"reflect"
	"sync"

	k8scorev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	clientgoworkqueue "k8s.io/client-go/util/workqueue"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimeevent "sigs.k8s.io/controller-runtime/pkg/event"
	ctrlruntimehandler "sigs.k8s.io/controller-runtime/pkg/handler"
	ctrlruntimepredicate "sigs.k8s.io/controller-runtime/pkg/predicate"
	ctrlruntimereconcile "sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// ObservationCache retains successful desired-state work between observation events. Tokens
// prevent a reconcile in flight from publishing a snapshot invalidated by a concurrent event.
// Entries are hints only: callers must check identity and expiry, and rebuild after restart.
type ObservationCache[T any] struct {
	mu      sync.Mutex
	next    uint64
	entries map[apimachinerytypes.NamespacedName]observationEntry[T]
}

type observationEntry[T any] struct {
	token uint64
	value T
	valid bool
}

// Load starts an observation epoch when a key has not been seen before.
func (c *ObservationCache[T]) Load(
	key apimachinerytypes.NamespacedName,
) (T, uint64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[apimachinerytypes.NamespacedName]observationEntry[T])
	}
	entry, exists := c.entries[key]
	if !exists {
		c.next++
		entry.token = c.next
		c.entries[key] = entry
	}

	return entry.value, entry.token, entry.valid
}

// Store succeeds only if no invalidation arrived after Load.
func (c *ObservationCache[T]) Store(
	key apimachinerytypes.NamespacedName,
	token uint64,
	value T,
) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, exists := c.entries[key]; exists && entry.token == token {
		c.entries[key] = observationEntry[T]{token: token, value: value, valid: true}
	}
}

// Invalidate also releases snapshots of deleted objects. The globally increasing token keeps
// deleting and recreating the same key from accepting a previous owner's in-flight result.
func (c *ObservationCache[T]) Invalidate(key apimachinerytypes.NamespacedName) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
}

// DesiredStateChanged excludes resource version, managed fields and ordinary status.
// Service status is an input to exposed addresses and invalidates observations.
// Core payloads do not increment generation consistently, so compare their data
// and ordinary resources' specs.
func DesiredStateChanged(old, current ctrlruntimeclient.Object) bool {
	if old == nil || current == nil || reflect.TypeOf(old) != reflect.TypeOf(current) {
		return true
	}
	if objectMetadataChanged(old, current) {
		return true
	}

	switch value := old.(type) {
	case *k8scorev1.ConfigMap:
		next := current.(*k8scorev1.ConfigMap) //nolint:forcetypeassert // Identical concrete types were checked above.

		return !apiequality.Semantic.DeepEqual(value.Data, next.Data) ||
			!apiequality.Semantic.DeepEqual(value.BinaryData, next.BinaryData) ||
			!apiequality.Semantic.DeepEqual(value.Immutable, next.Immutable)
	case *k8scorev1.Secret:
		next := current.(*k8scorev1.Secret) //nolint:forcetypeassert // Identical concrete types were checked above.

		return value.Type != next.Type ||
			!apiequality.Semantic.DeepEqual(value.Data, next.Data) ||
			!apiequality.Semantic.DeepEqual(value.Immutable, next.Immutable)
	case *k8scorev1.Service:
		next := current.(*k8scorev1.Service) //nolint:forcetypeassert // Identical concrete types were checked above.

		return !apiequality.Semantic.DeepEqual(value.Spec, next.Spec) ||
			!apiequality.Semantic.DeepEqual(value.Status, next.Status)
	default:
		a := reflect.ValueOf(old).Elem().FieldByName("Spec")
		b := reflect.ValueOf(current).Elem().FieldByName("Spec")

		return a.IsValid() && !apiequality.Semantic.DeepEqual(a.Interface(), b.Interface())
	}
}

// ObservationPredicate invalidates desired-state snapshots while retaining status events.
func ObservationPredicate(
	invalidate func(apimachinerytypes.NamespacedName),
) ctrlruntimepredicate.Predicate {
	mark := func(object ctrlruntimeclient.Object) {
		if object != nil {
			invalidate(ctrlruntimeclient.ObjectKeyFromObject(object))
		}
	}

	return ctrlruntimepredicate.Funcs{
		CreateFunc: func(e ctrlruntimeevent.CreateEvent) bool {
			mark(e.Object)

			return true
		},
		UpdateFunc: func(e ctrlruntimeevent.UpdateEvent) bool {
			if DesiredStateChanged(e.ObjectOld, e.ObjectNew) {
				mark(e.ObjectNew)
			}

			return true
		},
		DeleteFunc: func(e ctrlruntimeevent.DeleteEvent) bool {
			mark(e.Object)

			return true
		},
		GenericFunc: func(e ctrlruntimeevent.GenericEvent) bool {
			mark(e.Object)

			return true
		},
	}
}

type invalidatingQueue struct {
	clientgoworkqueue.TypedRateLimitingInterface[ctrlruntimereconcile.Request]

	invalidate func(apimachinerytypes.NamespacedName)
}

func (q invalidatingQueue) Add(request ctrlruntimereconcile.Request) {
	q.invalidate(request.NamespacedName)
	q.TypedRateLimitingInterface.Add(request)
}

// ObserveWith wraps an existing mapping/ownership handler, invalidating precisely its targets
// for input changes. Status-only updates keep the successful desired-state snapshot.
func ObserveWith(
	handler ctrlruntimehandler.EventHandler,
	invalidate func(apimachinerytypes.NamespacedName),
) ctrlruntimehandler.EventHandler {
	return ctrlruntimehandler.Funcs{
		CreateFunc: func(
			ctx context.Context,
			e ctrlruntimeevent.CreateEvent,
			q clientgoworkqueue.TypedRateLimitingInterface[ctrlruntimereconcile.Request],
		) {
			handler.Create(ctx, e, invalidatingQueue{q, invalidate})
		},
		UpdateFunc: func(
			ctx context.Context,
			e ctrlruntimeevent.UpdateEvent,
			q clientgoworkqueue.TypedRateLimitingInterface[ctrlruntimereconcile.Request],
		) {
			if DesiredStateChanged(e.ObjectOld, e.ObjectNew) {
				q = invalidatingQueue{q, invalidate}
			}
			handler.Update(ctx, e, q)
		},
		DeleteFunc: func(
			ctx context.Context,
			e ctrlruntimeevent.DeleteEvent,
			q clientgoworkqueue.TypedRateLimitingInterface[ctrlruntimereconcile.Request],
		) {
			handler.Delete(ctx, e, invalidatingQueue{q, invalidate})
		},
		GenericFunc: func(
			ctx context.Context,
			e ctrlruntimeevent.GenericEvent,
			q clientgoworkqueue.TypedRateLimitingInterface[ctrlruntimereconcile.Request],
		) {
			handler.Generic(ctx, e, invalidatingQueue{q, invalidate})
		},
	}
}

func objectMetadataChanged(old, current ctrlruntimeclient.Object) bool {
	if old.GetUID() != current.GetUID() ||
		old.GetGeneration() != current.GetGeneration() ||
		!apiequality.Semantic.DeepEqual(old.GetLabels(), current.GetLabels()) ||
		!apiequality.Semantic.DeepEqual(old.GetAnnotations(), current.GetAnnotations()) ||
		!apiequality.Semantic.DeepEqual(
			old.GetOwnerReferences(),
			current.GetOwnerReferences(),
		) ||
		!apiequality.Semantic.DeepEqual(
			old.GetDeletionTimestamp(),
			current.GetDeletionTimestamp(),
		) {
		return true
	}

	return false
}
