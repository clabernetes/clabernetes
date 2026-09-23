package topology //nolint:testpackage // Verify authoritative ownership checks and API request count.

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestChildConflictsUseOneAuthoritativeMetadataListPerKind(t *testing.T) {
	t.Parallel()
	topology := conflictTestTopology()
	rendered := renderedChildren{}
	objects := make([]ctrlruntimeclient.Object, 0, 1001)
	for index := range 1000 {
		node := &clabernetesapisv1alpha1.Node{ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("node-%04d", index), Namespace: topology.GetNamespace(),
			Labels: conflictTestGeneratedLabels(topology),
		}}
		rendered.nodes = append(rendered.nodes, node.DeepCopy())
		if index == 999 {
			node.Labels = nil // Foreign object absent from the stale cache must still block deployment.
		}
		objects = append(objects, node)
	}
	otherNamespace := rendered.nodes[0].DeepCopy()
	otherNamespace.Namespace = "unrelated"
	otherNamespace.Labels = nil
	objects = append(objects, otherNamespace)
	rendered.nodes = append(rendered.nodes, rendered.nodes[0].DeepCopy())
	scheme := conflictTestScheme(t)
	lists := 0
	reader := ctrlruntimefake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(context.Context, ctrlruntimeclient.WithWatch, ctrlruntimeclient.ObjectKey,
				ctrlruntimeclient.Object, ...ctrlruntimeclient.GetOption,
			) error {
				t.Fatal("ownership validation issued a per-child GET")

				return nil
			},
			List: func(ctx context.Context, client ctrlruntimeclient.WithWatch,
				list ctrlruntimeclient.ObjectList, opts ...ctrlruntimeclient.ListOption,
			) error {
				lists++
				if _, ok := list.(*metav1.PartialObjectMetadataList); !ok {
					t.Fatalf("ownership validation fetched complete objects: %T", list)
				}

				return client.List(ctx, list, opts...)
			},
		}).Build()
	reconciler := &Reconciler{
		Client:    ctrlruntimefake.NewClientBuilder().WithScheme(scheme).Build(),
		apiReader: reader,
	}
	conflicts, err := reconciler.findChildResourceConflicts(
		context.Background(),
		topology,
		rendered,
	)
	if err != nil {
		t.Fatal(err)
	}
	if lists != 1 {
		t.Fatalf("metadata LIST calls = %d, want 1 for 1000 Nodes", lists)
	}
	if want := []string{"node/node-0000", "node/node-0999"}; !reflect.DeepEqual(conflicts, want) {
		t.Fatalf("conflicts = %v, want duplicate and foreign children %v", conflicts, want)
	}
}

func TestChildConflictInventoryFailureIsReturned(t *testing.T) {
	t.Parallel()
	expected := context.DeadlineExceeded
	reader := ctrlruntimefake.NewClientBuilder().WithScheme(conflictTestScheme(t)).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(context.Context, ctrlruntimeclient.WithWatch,
				ctrlruntimeclient.ObjectList, ...ctrlruntimeclient.ListOption,
			) error {
				return expected
			},
		}).Build()
	reconciler := &Reconciler{Client: reader, apiReader: reader}
	_, err := reconciler.findChildResourceConflicts(
		context.Background(),
		conflictTestTopology(),
		renderedChildren{
			nodes: []*clabernetesapisv1alpha1.Node{{ObjectMeta: metav1.ObjectMeta{Name: "node"}}},
		},
	)
	if !errors.Is(err, expected) {
		t.Fatalf("inventory error = %v", err)
	}
}
