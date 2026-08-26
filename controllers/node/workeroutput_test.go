package node //nolint:testpackage // tests exercise unexported worker-artifact ownership details.

import (
	"context"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	k8scorev1 "k8s.io/api/core/v1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestGarbageCollectWorkerArtifactsRemovesSupersededCaches(t *testing.T) {
	t.Parallel()

	node := &clabernetesapisv1alpha1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "router",
			Namespace: "lab",
			UID:       apimachinerytypes.UID("uid-router"),
		},
	}

	controller := true
	blockOwnerDeletion := true
	owner := metav1.OwnerReference{
		APIVersion:         clabernetesapisv1alpha1.SchemeGroupVersion.String(),
		Kind:               "Node",
		Name:               node.GetName(),
		UID:                node.GetUID(),
		Controller:         &controller,
		BlockOwnerDeletion: &blockOwnerDeletion,
	}

	staleOutput := &k8scorev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "router-images-stale000000",
			Namespace: node.GetNamespace(),
			Labels: map[string]string{
				clabernetesconstants.LabelComponent: workerOutputComponentLabelValue,
				planOwnerUIDLabel:                   string(node.GetUID()),
			},
			OwnerReferences: []metav1.OwnerReference{owner},
		},
	}
	staleInput := &k8scorev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "router-plan-input-stale000000",
			Namespace: node.GetNamespace(),
			Labels: map[string]string{
				clabernetesconstants.LabelComponent: plannerInputComponentLabelValue,
				planOwnerUIDLabel:                   string(node.GetUID()),
			},
			OwnerReferences: []metav1.OwnerReference{owner},
		},
	}
	currentInput := &k8scorev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "router-plan-input-current000",
			Namespace: node.GetNamespace(),
			Labels: map[string]string{
				clabernetesconstants.LabelComponent: plannerInputComponentLabelValue,
				planOwnerUIDLabel:                   string(node.GetUID()),
			},
			OwnerReferences: []metav1.OwnerReference{owner},
		},
	}

	scheme := plannerTestScheme(t)
	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(node, staleOutput, staleInput, currentInput).
		Build()

	reconciler := &Reconciler{Client: client}
	keep := map[string]bool{currentInput.GetName(): true}

	if err := reconciler.garbageCollectWorkerArtifacts(context.Background(), node, keep); err != nil {
		t.Fatalf("garbageCollectWorkerArtifacts() error = %v", err)
	}

	for _, name := range []string{staleOutput.GetName(), staleInput.GetName()} {
		configMap := &k8scorev1.ConfigMap{}

		err := client.Get(
			context.Background(),
			apimachinerytypes.NamespacedName{Namespace: node.GetNamespace(), Name: name},
			configMap,
		)
		if !apimachineryerrors.IsNotFound(err) {
			t.Fatalf("stale ConfigMap %q still exists: %v", name, err)
		}
	}

	retained := &k8scorev1.ConfigMap{}
	if err := client.Get(
		context.Background(),
		apimachinerytypes.NamespacedName{
			Namespace: node.GetNamespace(),
			Name:      currentInput.GetName(),
		},
		retained,
	); err != nil {
		t.Fatalf("current input ConfigMap was deleted: %v", err)
	}
}
