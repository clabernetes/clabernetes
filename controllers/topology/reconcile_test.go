package topology //nolint:testpackage // tests exercise the unexported conforms helpers

import (
	"context"
	"errors"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	k8sappsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcileCompileFailureDoesNotMutateLegacyObjects(t *testing.T) {
	t.Parallel()

	scheme := apimachineryruntime.NewScheme()
	for _, addToScheme := range []func(*apimachineryruntime.Scheme) error{
		clabernetesapisv1alpha1.AddToScheme,
		k8sappsv1.AddToScheme,
	} {
		err := addToScheme(scheme)
		if err != nil {
			t.Fatalf("adding scheme: %v", err)
		}
	}

	topology := &clabernetesapisv1alpha1.Topology{
		ObjectMeta: metav1.ObjectMeta{
			Name: "invalid", Namespace: "clabernetes", UID: "topology-uid",
		},
	}
	topology.Spec.Definition.Containerlab = `
name: invalid
topology:
  nodes:
    n1:
      kind: linux
      image: alpine
      unsupported-setting: true
`
	controller := true
	legacy := &k8sappsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
		Name: "invalid-n1", Namespace: topology.GetNamespace(),
		OwnerReferences: []metav1.OwnerReference{{
			APIVersion: clabernetesapisv1alpha1.SchemeGroupVersion.String(),
			Kind:       "Topology", Name: topology.GetName(), UID: topology.GetUID(),
			Controller: &controller,
		}},
	}}
	client := ctrlruntimefake.NewClientBuilder().WithScheme(scheme).WithObjects(legacy).Build()
	reconciler := &Reconciler{Log: &claberneteslogging.FakeInstance{}, Client: client}

	err := reconciler.Reconcile(context.Background(), topology)

	unsupported := &UnsupportedFeaturesError{}
	if !errors.As(err, &unsupported) {
		t.Fatalf("Reconcile() error = %v, want UnsupportedFeaturesError", err)
	}

	actual := &k8sappsv1.Deployment{}

	err = client.Get(
		context.Background(),
		ctrlruntimeclient.ObjectKeyFromObject(legacy),
		actual,
	)
	if err != nil {
		t.Fatalf("compile failure deleted legacy workload: %v", err)
	}
}

// the api server drops empty omitempty fields on storage, so a compiled node whose (always
// non-nil) merged ports/binds/env are empty reads back with those fields nil -- the conforms
// check must treat that as equal or every reconcile sees phantom drift and updates forever.
func TestEmittedObjectConformsEmptyVsAbsent(t *testing.T) {
	rendered := &clabernetesapisv1alpha1.Node{
		Spec: clabernetesapisv1alpha1.NodeSpec{
			NodeDefinition: clabernetesapisv1alpha1.NodeDefinition{
				Kind:  "nokia_srlinux",
				Image: "ghcr.io/nokia/srlinux:latest",
				Ports: []string{},
				Binds: []string{},
				Env:   map[string]string{},
			},
		},
	}

	existing := &clabernetesapisv1alpha1.Node{
		Spec: clabernetesapisv1alpha1.NodeSpec{
			NodeDefinition: clabernetesapisv1alpha1.NodeDefinition{
				Kind:  "nokia_srlinux",
				Image: "ghcr.io/nokia/srlinux:latest",
			},
		},
	}

	if !emittedObjectConforms(existing, rendered) {
		t.Fatal("expected empty vs absent spec fields to conform, got drift")
	}
}

func TestEmittedObjectConformsDetectsSpecDrift(t *testing.T) {
	rendered := &clabernetesapisv1alpha1.Node{
		Spec: clabernetesapisv1alpha1.NodeSpec{
			NodeDefinition: clabernetesapisv1alpha1.NodeDefinition{
				Kind:  "nokia_srlinux",
				Image: "ghcr.io/nokia/srlinux:25.3.1",
			},
		},
	}

	existing := &clabernetesapisv1alpha1.Node{
		Spec: clabernetesapisv1alpha1.NodeSpec{
			NodeDefinition: clabernetesapisv1alpha1.NodeDefinition{
				Kind:  "nokia_srlinux",
				Image: "ghcr.io/nokia/srlinux:latest",
			},
		},
	}

	if emittedObjectConforms(existing, rendered) {
		t.Fatal("expected image drift to be detected, got conforms")
	}
}

func TestEmittedObjectConformsDetectsOwnerDrift(t *testing.T) {
	controller := true
	topologyUID := apimachinerytypes.UID("topology-uid")
	rendered := &clabernetesapisv1alpha1.Node{
		ObjectMeta: metav1.ObjectMeta{
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: clabernetesapisv1alpha1.SchemeGroupVersion.String(),
				Kind:       "Topology",
				Name:       "lab",
				UID:        topologyUID,
				Controller: &controller,
			}},
		},
	}
	existing := rendered.DeepCopy()
	existing.OwnerReferences = nil

	if emittedObjectConforms(existing, rendered) {
		t.Fatal("expected missing controller owner reference to be detected as drift")
	}
}

func TestReconcileEmittedRestoresDriftAndPrunes(t *testing.T) {
	scheme := apimachineryruntime.NewScheme()

	err := clabernetesapisv1alpha1.AddToScheme(scheme)
	if err != nil {
		t.Fatalf("failed adding clabernetes scheme: %s", err)
	}

	topology := &clabernetesapisv1alpha1.Topology{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "lab",
			Namespace: "clabernetes",
			UID:       "topology-uid",
		},
	}
	generatedLabels := map[string]string{
		clabernetesconstants.LabelApp:           clabernetesconstants.Clabernetes,
		clabernetesconstants.LabelTopologyOwner: topology.GetName(),
	}
	drifted := &clabernetesapisv1alpha1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "srl1",
			Namespace: topology.GetNamespace(),
			Labels:    generatedLabels,
		},
		Spec: clabernetesapisv1alpha1.NodeSpec{
			NodeDefinition: clabernetesapisv1alpha1.NodeDefinition{Image: "drifted"},
		},
		Status: clabernetesapisv1alpha1.NodeStatus{Readiness: "ready"},
	}
	controller := true
	extra := &clabernetesapisv1alpha1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "removed",
			Namespace: topology.GetNamespace(),
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: clabernetesapisv1alpha1.SchemeGroupVersion.String(),
				Kind:       "Topology",
				Name:       topology.GetName(),
				UID:        topology.GetUID(),
				Controller: &controller,
			}},
		},
	}
	unrelated := &clabernetesapisv1alpha1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "direct",
			Namespace: topology.GetNamespace(),
		},
	}

	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(drifted, extra, unrelated).
		Build()
	reconciler := &Reconciler{
		Log:    &claberneteslogging.FakeInstance{},
		Client: client,
	}
	rendered := &clabernetesapisv1alpha1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "srl1",
			Namespace: topology.GetNamespace(),
			Labels:    generatedLabels,
		},
		Spec: clabernetesapisv1alpha1.NodeSpec{
			NodeDefinition: clabernetesapisv1alpha1.NodeDefinition{Image: "expected"},
		},
	}
	existing := map[string]*clabernetesapisv1alpha1.Node{
		drifted.GetName(): drifted,
		extra.GetName():   extra,
	}

	err = reconcileEmitted(
		context.Background(),
		reconciler,
		topology,
		"node",
		[]*clabernetesapisv1alpha1.Node{rendered},
		existing,
		emittedObjectConforms[*clabernetesapisv1alpha1.Node],
		func(existingNode, renderedNode *clabernetesapisv1alpha1.Node) {
			renderedNode.Status = existingNode.Status
		},
	)
	if err != nil {
		t.Fatalf("reconcile emitted failed: %s", err)
	}

	actual := &clabernetesapisv1alpha1.Node{}
	key := ctrlruntimeclient.ObjectKeyFromObject(rendered)

	err = client.Get(context.Background(), key, actual)
	if err != nil {
		t.Fatalf("failed getting reconciled Node: %s", err)
	}

	if actual.Spec.Image != "expected" {
		t.Fatalf("expected spec drift corrected, got image %q", actual.Spec.Image)
	}

	if !metav1.IsControlledBy(actual, topology) {
		t.Fatalf("expected topology controller owner reference, got %+v", actual.OwnerReferences)
	}

	if actual.Status.Readiness != "ready" {
		t.Fatalf("expected Node-owned status preserved, got %+v", actual.Status)
	}

	err = client.Get(
		context.Background(),
		ctrlruntimeclient.ObjectKeyFromObject(extra),
		&clabernetesapisv1alpha1.Node{},
	)
	if err == nil {
		t.Fatal("expected removed generated Node to be pruned")
	}

	err = client.Get(
		context.Background(),
		ctrlruntimeclient.ObjectKeyFromObject(unrelated),
		&clabernetesapisv1alpha1.Node{},
	)
	if err != nil {
		t.Fatalf("expected unrelated directly-authored Node retained: %s", err)
	}
}
