package node //nolint:testpackage // tests exercise unexported reconciliation helpers

import (
	"context"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	k8scorev1 "k8s.io/api/core/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func persistentVolumeClaimTestReconciler(
	t *testing.T,
	objects ...*k8scorev1.PersistentVolumeClaim,
) (*Reconciler, *clabernetesapisv1alpha1.Node) {
	t.Helper()

	scheme := nodeReconcileTestScheme(t)
	node := nodeReconcileTestNode()

	builder := ctrlruntimefake.NewClientBuilder().WithScheme(scheme).WithObjects(node)
	for _, object := range objects {
		builder = builder.WithObjects(object)
	}

	return &Reconciler{
		Log:    &claberneteslogging.FakeInstance{},
		Client: builder.Build(),
		PersistentVolumeClaimReconciler: NewPersistentVolumeClaimReconciler(
			&claberneteslogging.FakeInstance{},
			clabernetesconfig.GetFakeManager,
		),
	}, node
}

func persistenceTestProfile(reclaim string) *ResolvedProfile {
	return &ResolvedProfile{
		Persistence: clabernetesapisv1alpha1.Persistence{
			Enabled: true,
			Reclaim: reclaim,
		},
	}
}

func getReconciledClaim(
	t *testing.T,
	reconciler *Reconciler,
	node *clabernetesapisv1alpha1.Node,
) *k8scorev1.PersistentVolumeClaim {
	t.Helper()

	claim := &k8scorev1.PersistentVolumeClaim{}

	err := reconciler.Client.Get(
		context.Background(),
		apimachinerytypes.NamespacedName{Namespace: node.GetNamespace(), Name: node.GetName()},
		claim,
	)
	if err != nil {
		t.Fatalf("fetching reconciled claim: %s", err)
	}

	return claim
}

func TestReconcilePersistentVolumeClaimDefaultReclaimOwnsClaim(t *testing.T) {
	t.Parallel()

	reconciler, node := persistentVolumeClaimTestReconciler(t)

	name, err := reconciler.reconcilePersistentVolumeClaim(
		context.Background(),
		node,
		persistenceTestProfile(""),
	)
	if err != nil {
		t.Fatalf("reconciling claim: %s", err)
	}

	if name != node.GetName() {
		t.Fatalf("claim name = %q", name)
	}

	claim := getReconciledClaim(t, reconciler, node)

	if !ownedByUID(claim, node.GetUID()) {
		t.Fatalf("default-reclaim claim is not owned by its node: %#v", claim.OwnerReferences)
	}
}

func TestReconcilePersistentVolumeClaimRetainOmitsOwnership(t *testing.T) {
	t.Parallel()

	reconciler, node := persistentVolumeClaimTestReconciler(t)

	_, err := reconciler.reconcilePersistentVolumeClaim(
		context.Background(),
		node,
		persistenceTestProfile(clabernetesapisv1alpha1.PersistenceReclaimRetain),
	)
	if err != nil {
		t.Fatalf("reconciling retained claim: %s", err)
	}

	claim := getReconciledClaim(t, reconciler, node)

	if len(claim.OwnerReferences) != 0 {
		t.Fatalf("retained claim carries owner references: %#v", claim.OwnerReferences)
	}
}

func TestReconcilePersistentVolumeClaimAdoptsRetainedClaim(t *testing.T) {
	t.Parallel()

	retained := &k8scorev1.PersistentVolumeClaim{}
	retained.Name = "srl1"
	retained.Namespace = "clabernetes"
	retained.Spec = NewPersistentVolumeClaimReconciler(
		&claberneteslogging.FakeInstance{},
		clabernetesconfig.GetFakeManager,
	).Render(nodeReconcileTestNode(), persistenceTestProfile(""), nil).Spec

	reconciler, node := persistentVolumeClaimTestReconciler(t, retained)

	name, err := reconciler.reconcilePersistentVolumeClaim(
		context.Background(),
		node,
		persistenceTestProfile(clabernetesapisv1alpha1.PersistenceReclaimRetain),
	)
	if err != nil {
		t.Fatalf("adopting retained claim: %s", err)
	}

	if name != "srl1" {
		t.Fatalf("adopted claim name = %q", name)
	}

	claim := getReconciledClaim(t, reconciler, node)

	if len(claim.OwnerReferences) != 0 {
		t.Fatalf("adopted retained claim gained owner references: %#v", claim.OwnerReferences)
	}
}

func TestReconcilePersistentVolumeClaimRejectsIncompatibleRetainedStorageClass(t *testing.T) {
	t.Parallel()

	foreignClass := "foreign-storage"
	retained := &k8scorev1.PersistentVolumeClaim{}
	retained.Name = "srl1"
	retained.Namespace = "clabernetes"
	retained.Spec.StorageClassName = &foreignClass

	reconciler, node := persistentVolumeClaimTestReconciler(t, retained)

	profile := persistenceTestProfile(clabernetesapisv1alpha1.PersistenceReclaimRetain)
	profile.Persistence.StorageClassName = "declared-storage"

	_, err := reconciler.reconcilePersistentVolumeClaim(context.Background(), node, profile)
	if err == nil {
		t.Fatal("adoption accepted an incompatible storage class")
	}
}

func TestReconcilePersistentVolumeClaimReclaimTransitionRemovesOwnership(t *testing.T) {
	t.Parallel()

	reconciler, node := persistentVolumeClaimTestReconciler(t)

	if _, err := reconciler.reconcilePersistentVolumeClaim(
		context.Background(),
		node,
		persistenceTestProfile(""),
	); err != nil {
		t.Fatalf("creating owned claim: %s", err)
	}

	if len(getReconciledClaim(t, reconciler, node).OwnerReferences) != 1 {
		t.Fatal("owned claim was not created with an owner reference")
	}

	if _, err := reconciler.reconcilePersistentVolumeClaim(
		context.Background(),
		node,
		persistenceTestProfile(clabernetesapisv1alpha1.PersistenceReclaimRetain),
	); err != nil {
		t.Fatalf("transitioning claim to Retain: %s", err)
	}

	claim := getReconciledClaim(t, reconciler, node)

	if len(claim.OwnerReferences) != 0 {
		t.Fatalf("Retain transition kept owner references: %#v", claim.OwnerReferences)
	}
}
