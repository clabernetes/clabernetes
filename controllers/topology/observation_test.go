//nolint:testpackage // Exercise internal reconciliation and cache boundaries.
package topology

import (
	"context"
	"testing"
	"time"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetescontrollers "github.com/clabernetes/clabernetes/controllers"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestTopologyObservationAvoidsInventoryButInvalidationRepairsDrift(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	topology := conflictTestTopology()
	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(conflictTestScheme(t)).
		WithStatusSubresource(&clabernetesapisv1alpha1.Topology{}).
		WithObjects(topology).
		Build()
	lists := 0
	reader := interceptor.NewClient(
		client,
		interceptor.Funcs{
			List: func(ctx context.Context, client ctrlruntimeclient.WithWatch, list ctrlruntimeclient.ObjectList, options ...ctrlruntimeclient.ListOption) error {
				lists++

				return client.List(ctx, list, options...)
			},
		},
	)
	r := &Reconciler{
		Client:              client,
		apiReader:           reader,
		Log:                 &claberneteslogging.FakeInstance{},
		configManagerGetter: clabernetesconfig.GetFakeManager,
		observations:        &clabernetescontrollers.ObservationCache[*topologyObservation]{},
	}
	if _, err := r.Reconcile(ctx, topology); err != nil {
		t.Fatal(err)
	}
	if lists == 0 {
		t.Fatal("initial reconcile skipped authoritative ownership checks")
	}
	lists = 0
	if _, err := r.Reconcile(ctx, topology); err != nil {
		t.Fatal(err)
	}
	if lists != 0 {
		t.Fatalf("status pass issued %d authoritative inventory LISTs", lists)
	}
	node := &clabernetesapisv1alpha1.Node{}
	if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: topology.Namespace, Name: "frr1"}, node); err != nil {
		t.Fatal(err)
	}
	snapshot, _, valid := r.observations.Load(ctrlruntimeclient.ObjectKeyFromObject(topology))
	if !valid {
		t.Fatal("missing topology observation")
	}
	snapshot.checkedAt = time.Now().Add(-6 * time.Minute)
	if _, err := r.Reconcile(ctx, topology); err != nil {
		t.Fatal(err)
	}
	if lists == 0 {
		t.Fatal("expired observation skipped full ownership validation")
	}
	lists = 0
	original := node.Spec.Image
	node.Spec.Image = "foreign/image"
	if err := client.Update(ctx, node); err != nil {
		t.Fatal(err)
	}
	r.observations.Invalidate(ctrlruntimeclient.ObjectKeyFromObject(topology))
	if _, err := r.Reconcile(ctx, topology); err != nil {
		t.Fatal(err)
	}
	if err := client.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(node), node); err != nil {
		t.Fatal(err)
	}
	if node.Spec.Image != original || lists == 0 {
		t.Fatal("child drift did not trigger full validation and repair")
	}
}
