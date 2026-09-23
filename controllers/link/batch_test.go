package link //nolint:testpackage // Verify namespace allocation and authoritative API access.

import (
	"context"
	"errors"
	"fmt"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	ctrlruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestNamespaceAllocationUsesTwoListsAndRetainsIDsAfterRestart(t *testing.T) {
	const count = 1900
	objects := make([]ctrlruntimeclient.Object, 0, count*2)
	for index := range count {
		node := reconcileTestNode(fmt.Sprintf("node-%04d", index))
		link := reconcileTestLink(fmt.Sprintf("link-%04d", index), node.Name, "eth1",
			fmt.Sprintf("node-%04d", (index+1)%count), "eth2", 0)
		objects = append(objects, &node, &link)
	}
	controller, client := newLifecycleTestController(t, objects...)
	lists, writes := 0, 0
	counted := interceptor.NewClient(client, interceptor.Funcs{
		Get: func(context.Context, ctrlruntimeclient.WithWatch, ctrlruntimeclient.ObjectKey,
			ctrlruntimeclient.Object, ...ctrlruntimeclient.GetOption,
		) error {
			t.Fatal("namespace allocation performed a per-Link GET")

			return nil
		},
		List: func(ctx context.Context, c ctrlruntimeclient.WithWatch, list ctrlruntimeclient.ObjectList,
			opts ...ctrlruntimeclient.ListOption,
		) error {
			lists++

			return c.List(ctx, list, opts...)
		},
		SubResourceUpdate: func(ctx context.Context, c ctrlruntimeclient.Client, subresource string,
			obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.SubResourceUpdateOption,
		) error {
			writes++

			return c.SubResource(subresource).Update(ctx, obj, opts...)
		},
	})
	controller.apiReader, controller.Client = counted, counted
	request := ctrlruntime.Request{}
	request.Namespace = "clabernetes"
	if _, err := controller.Reconcile(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if lists != 2 || writes != count {
		t.Fatalf("lists=%d writes=%d, want 2 and %d", lists, writes, count)
	}
	links := &clabernetesapisv1alpha1.LinkList{}
	if err := client.List(t.Context(), links); err != nil {
		t.Fatal(err)
	}
	ids := map[int]bool{}
	for _, link := range links.Items {
		if link.Status.WireID < 1 || ids[link.Status.WireID] || rejectionMessage(&link) != "" {
			t.Fatalf("invalid or duplicate allocation: %s %+v", link.Name, link.Status)
		}
		ids[link.Status.WireID] = true
	}
	// No in-memory allocation state is required across passes or controller restarts.
	restarted := &Controller{BaseController: controller.BaseController, apiReader: counted}
	if _, err := restarted.Reconcile(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if lists != 4 || writes != count {
		t.Fatalf("restart lists=%d writes=%d, want 4 and %d", lists, writes, count)
	}
}

func TestNamespaceAllocationRefreshesAfterAmbiguousWrite(t *testing.T) {
	a := reconcileTestNode("a")
	b := reconcileTestNode("b")
	first := reconcileTestLink("first", "a", "eth1", "b", "eth1", 0)
	second := reconcileTestLink("second", "a", "eth2", "b", "eth2", 0)
	third := reconcileTestLink("third", "a", "eth3", "b", "eth3", 0)
	controller, client := newLifecycleTestController(t, &a, &b, &first, &second, &third)
	failed := false
	controller.Client = interceptor.NewClient(
		client,
		interceptor.Funcs{
			SubResourceUpdate: func(ctx context.Context, c ctrlruntimeclient.Client, subresource string,
				obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.SubResourceUpdateOption,
			) error {
				if err := c.SubResource(subresource).Update(ctx, obj, opts...); err != nil {
					return err
				}
				if obj.GetName() == second.Name && !failed {
					failed = true

					return context.DeadlineExceeded // Server persisted the allocation; response was lost.
				}

				return nil
			},
		},
	)
	request := ctrlruntime.Request{}
	request.Namespace = "clabernetes"
	if _, err := controller.Reconcile(t.Context(), request); !errors.Is(
		err,
		context.DeadlineExceeded,
	) {
		t.Fatalf("expected ambiguous write failure, got %v", err)
	}
	if getLifecycleTestLink(t, client, third.Name).Status.WireID != 0 {
		t.Fatal("pass continued after uncertain write")
	}
	if _, err := controller.Reconcile(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	for index, name := range []string{first.Name, second.Name, third.Name} {
		if actual := getLifecycleTestLink(t, client, name).Status.WireID; actual != index+1 {
			t.Fatalf("%s got wire %d, want %d", name, actual, index+1)
		}
	}
}

func TestNamespaceLifecycleDeleteDoesNotRemoveConcurrentRewire(t *testing.T) {
	a := reconcileTestNode("a")
	b := reconcileTestNode("b")
	c := reconcileTestNode("c")
	link := reconcileTestLink("link", "a", "eth1", "b", "eth1", 0)
	controller, client := newLifecycleTestController(t, &a, &b, &c, &link)
	reconcileLifecycleLink(t, controller, link.Name)
	if err := client.Delete(t.Context(), &b); err != nil {
		t.Fatal(err)
	}
	rewired := false
	controller.apiReader = interceptor.NewClient(
		client,
		interceptor.Funcs{
			List: func(ctx context.Context, inner ctrlruntimeclient.WithWatch,
				list ctrlruntimeclient.ObjectList, opts ...ctrlruntimeclient.ListOption,
			) error {
				if err := inner.List(ctx, list, opts...); err != nil {
					return err
				}
				if _, ok := list.(*clabernetesapisv1alpha1.NodeList); ok && !rewired {
					current := getLifecycleTestLink(t, client, link.Name)
					current.Spec.EndpointB.NodeName = c.Name
					if err := inner.Update(ctx, current); err != nil {
						return err
					}
					rewired = true
				}

				return nil
			},
		},
	)
	request := ctrlruntime.Request{}
	request.Namespace = "clabernetes"
	if _, err := controller.Reconcile(t.Context(), request); !apimachineryerrors.IsConflict(err) {
		t.Fatalf("expected delete resourceVersion conflict, got %v", err)
	}
	if current := getLifecycleTestLink(t, client, link.Name); current.Spec.EndpointB.NodeName != c.Name {
		t.Fatal("concurrent rewire was lost")
	}
	if _, err := controller.Reconcile(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	requireBoundLink(t, client, link.Name, a.UID, c.UID)
}

func TestNamespaceAllocationRepairsDuplicateIDsAndReleasesRejectedReservations(t *testing.T) {
	a := reconcileTestNode("a")
	b := reconcileTestNode("b")
	invalid := reconcileTestLink("a-invalid", "a", "eth9", "a", "eth9", 1)
	first := reconcileTestLink("b-first", "a", "eth1", "b", "eth1", 7)
	second := reconcileTestLink("c-second", "a", "eth2", "b", "eth2", 7)
	controller, client := newLifecycleTestController(t, &a, &b, &invalid, &first, &second)
	reconcileLifecycleLink(t, controller, first.Name)
	for name, expected := range map[string]int{invalid.Name: 0, first.Name: 7, second.Name: 1} {
		if actual := getLifecycleTestLink(t, client, name).Status.WireID; actual != expected {
			t.Fatalf("%s wire=%d, want %d", name, actual, expected)
		}
	}
}
