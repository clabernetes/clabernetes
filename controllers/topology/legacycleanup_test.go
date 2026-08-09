package topology //nolint:testpackage // tests exercise the unexported migration cleanup

import (
	"context"
	"testing"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	claberneteslogging "github.com/srl-labs/clabernetes/logging"
	k8sappsv1 "k8s.io/api/apps/v1"
	k8scorev1 "k8s.io/api/core/v1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestLegacyCleanupRetainsPersistentVolumeClaims(t *testing.T) {
	scheme := apimachineryruntime.NewScheme()

	for _, addToScheme := range []func(*apimachineryruntime.Scheme) error{
		k8scorev1.AddToScheme,
		k8sappsv1.AddToScheme,
	} {
		err := addToScheme(scheme)
		if err != nil {
			t.Fatalf("failed adding scheme: %s", err)
		}
	}

	topology := &clabernetesapisv1alpha1.Topology{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-lab",
			Namespace: "clabernetes",
			UID:       "topology-uid",
		},
	}
	ownerReference := metav1.OwnerReference{
		APIVersion: clabernetesapisv1alpha1.SchemeGroupVersion.String(),
		Kind:       "Topology",
		Name:       topology.GetName(),
		UID:        topology.GetUID(),
	}

	legacyDeployment := &k8sappsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
		Name:            "my-lab-srl1",
		Namespace:       topology.GetNamespace(),
		OwnerReferences: []metav1.OwnerReference{ownerReference},
	}}
	legacyPVC := &k8scorev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{
		Name:            "my-lab-srl1",
		Namespace:       topology.GetNamespace(),
		OwnerReferences: []metav1.OwnerReference{ownerReference},
	}}

	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(legacyDeployment, legacyPVC).
		Build()
	reconciler := &Reconciler{Log: &claberneteslogging.FakeInstance{}, Client: client}

	err := reconciler.legacyCleanupOwnedObjects(context.Background(), topology)
	if err != nil {
		t.Fatalf("legacy cleanup failed: %s", err)
	}

	key := apimachinerytypes.NamespacedName{
		Namespace: topology.GetNamespace(),
		Name:      "my-lab-srl1",
	}

	err = client.Get(context.Background(), key, &k8sappsv1.Deployment{})
	if !apimachineryerrors.IsNotFound(err) {
		t.Fatalf("expected legacy deployment deleted, got: %v", err)
	}

	err = client.Get(context.Background(), key, &k8scorev1.PersistentVolumeClaim{})
	if err != nil {
		t.Fatalf("expected legacy pvc retained for node adoption, got: %s", err)
	}
}
