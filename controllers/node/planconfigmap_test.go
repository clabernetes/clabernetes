//nolint:nlreturn,noinlineerr,wsl_v5 // Ownership tests are clearer as compact assertions.
package node //nolint:testpackage // tests exercise the plan artifact's ownership boundary

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	k8scorev1 "k8s.io/api/core/v1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

//nolint:gocyclo // One test verifies all fields of the persisted security boundary.
func TestPlanConfigMapRenderIsImmutableContentAddressedAndSecretFree(t *testing.T) {
	t.Parallel()

	node := planTestNode(strings.Repeat("long-node-name-", 6))
	artifact := PlanArtifact{
		Plan: []byte(
			`{"schemaVersion":"v1alpha1","secrets":[{"name":"license","key":"license.key"}]}`,
		),
		NormalizedInputs: []byte(`{"node":"router","licenseRef":"lab/license:license.key"}`),
		SensitiveValues:  [][]byte{[]byte("actual-license-contents")},
	}
	reconciler := &PlanConfigMapReconciler{}

	configMap, identity, err := reconciler.Render(node, artifact)
	if err != nil {
		t.Fatal(err)
	}
	if configMap.Immutable == nil || !*configMap.Immutable {
		t.Fatal("plan ConfigMap is not immutable")
	}
	if len(configMap.GetName()) > 63 || !strings.Contains(configMap.GetName(), "-plan-") {
		t.Fatalf(
			"plan ConfigMap name %q is not a bounded content-addressed name",
			configMap.GetName(),
		)
	}
	if identity.Name != configMap.GetName() ||
		identity.PlanDigest != digestArtifact(artifact.Plan) ||
		identity.InputDigest != digestArtifact(artifact.NormalizedInputs) {
		t.Fatalf("plan identity = %#v, ConfigMap = %#v", identity, configMap)
	}
	if got, want := configMap.Data, map[string]string{planDataKey: string(artifact.Plan)}; !reflect.DeepEqual(
		got,
		want,
	) {
		t.Fatalf("ConfigMap data = %#v, want %#v", got, want)
	}
	rendered := configMap.String()
	for _, forbidden := range []string{"actual-license-contents", string(artifact.NormalizedInputs)} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("plan ConfigMap persisted forbidden value %q: %s", forbidden, rendered)
		}
	}
	if got := configMap.Annotations[planDigestAnnotation]; got != identity.PlanDigest {
		t.Fatalf("plan digest annotation = %q, want %q", got, identity.PlanDigest)
	}
	if got := configMap.Annotations[planInputDigestAnnotation]; got != identity.InputDigest {
		t.Fatalf("input digest annotation = %q, want %q", got, identity.InputDigest)
	}
	if len(configMap.OwnerReferences) != 1 || configMap.OwnerReferences[0].UID != node.GetUID() ||
		configMap.OwnerReferences[0].Controller == nil || !*configMap.OwnerReferences[0].Controller {
		t.Fatalf(
			"plan ConfigMap owner = %#v, want controller Node UID %q",
			configMap.OwnerReferences,
			node.GetUID(),
		)
	}
}

//nolint:tparallel // Cases reuse a base Node and verify mutations sequentially.
func TestPlanConfigMapRejectsInvalidOrSensitiveArtifacts(t *testing.T) {
	t.Parallel()

	node := planTestNode("router")
	tests := []struct {
		name       string
		reconciler PlanConfigMapReconciler
		artifact   PlanArtifact
		mutateNode func(*clabernetesapisv1alpha1.Node)
	}{
		{name: "empty plan", artifact: PlanArtifact{NormalizedInputs: []byte(`{}`)}},
		{
			name:     "invalid plan JSON",
			artifact: PlanArtifact{Plan: []byte(`{`), NormalizedInputs: []byte(`{}`)},
		},
		{
			name:     "invalid input JSON",
			artifact: PlanArtifact{Plan: []byte(`{}`), NormalizedInputs: []byte(`{`)},
		},
		{
			name:       "oversized plan",
			reconciler: PlanConfigMapReconciler{MaxPlanBytes: 1},
			artifact:   PlanArtifact{Plan: []byte(`{}`), NormalizedInputs: []byte(`{}`)},
		},
		{
			name:       "oversized inputs",
			reconciler: PlanConfigMapReconciler{MaxInputBytes: 1},
			artifact:   PlanArtifact{Plan: []byte(`{}`), NormalizedInputs: []byte(`{}`)},
		},
		{
			name:       "negative ceiling",
			reconciler: PlanConfigMapReconciler{MaxPlanBytes: -1},
			artifact:   PlanArtifact{Plan: []byte(`{}`), NormalizedInputs: []byte(`{}`)},
		},
		{
			name: "secret in plan",
			artifact: PlanArtifact{
				Plan:             []byte(`{"password":"secret-value"}`),
				NormalizedInputs: []byte(`{}`),
				SensitiveValues:  [][]byte{[]byte("secret-value")},
			},
		},
		{
			name: "secret in inputs",
			artifact: PlanArtifact{
				Plan:             []byte(`{}`),
				NormalizedInputs: []byte(`{"password":"secret-value"}`),
				SensitiveValues:  [][]byte{[]byte("secret-value")},
			},
		},
		{
			name:       "missing Node UID",
			artifact:   PlanArtifact{Plan: []byte(`{}`), NormalizedInputs: []byte(`{}`)},
			mutateNode: func(node *clabernetesapisv1alpha1.Node) { node.UID = "" },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			testNode := node.DeepCopy()
			if test.mutateNode != nil {
				test.mutateNode(testNode)
			}
			_, _, err := test.reconciler.Render(testNode, test.artifact)
			if !errors.Is(err, ErrInvalidPlanArtifact) {
				t.Fatalf("Render() error = %v, want ErrInvalidPlanArtifact", err)
			}
			if err != nil && strings.Contains(err.Error(), "secret-value") {
				t.Fatalf("Render() error leaked sensitive value: %v", err)
			}
		})
	}
}

func TestPlanConfigMapEnsureIsIdempotentAndRejectsImmutableConflict(t *testing.T) {
	t.Parallel()

	scheme := planTestScheme(t)
	node := planTestNode("router")
	client := ctrlruntimefake.NewClientBuilder().WithScheme(scheme).WithObjects(node).Build()
	reconciler := &PlanConfigMapReconciler{Client: client}
	artifact := PlanArtifact{Plan: []byte(`{"plan":1}`), NormalizedInputs: []byte(`{"input":1}`)}

	created, identity, err := reconciler.Ensure(context.Background(), node, artifact)
	if err != nil {
		t.Fatal(err)
	}
	existing, secondIdentity, err := reconciler.Ensure(context.Background(), node, artifact)
	if err != nil {
		t.Fatal(err)
	}
	if identity != secondIdentity || existing.GetUID() != created.GetUID() {
		t.Fatalf(
			"idempotent Ensure identities differ: first=%#v second=%#v",
			identity,
			secondIdentity,
		)
	}

	// Build a separate client containing a forged object at the content-addressed name.
	forged := created.DeepCopy()
	forged.ResourceVersion = ""
	forged.UID = ""
	forged.Data = map[string]string{planDataKey: `{"forged":true}`}
	forgedClient := ctrlruntimefake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(node, forged).
		Build()
	forgedReconciler := &PlanConfigMapReconciler{Client: forgedClient}
	_, _, err = forgedReconciler.Ensure(context.Background(), node, artifact)
	if !errors.Is(err, ErrPlanArtifactConflict) {
		t.Fatalf("Ensure() error = %v, want ErrPlanArtifactConflict", err)
	}
}

func TestPlanConfigMapGarbageCollectionIsUIDSafe(t *testing.T) {
	t.Parallel()

	scheme := planTestScheme(t)
	node := planTestNode("router")
	client := ctrlruntimefake.NewClientBuilder().WithScheme(scheme).WithObjects(node).Build()
	reconciler := &PlanConfigMapReconciler{Client: client}

	oldPlan, _, err := reconciler.Ensure(context.Background(), node, PlanArtifact{
		Plan: []byte(`{"revision":1}`), NormalizedInputs: []byte(`{"revision":1}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	newPlan, _, err := reconciler.Ensure(context.Background(), node, PlanArtifact{
		Plan: []byte(`{"revision":2}`), NormalizedInputs: []byte(`{"revision":2}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	foreign := &k8scorev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "foreign-plan",
			Namespace: node.GetNamespace(),
			Labels: map[string]string{
				clabernetesconstants.LabelComponent: planComponentLabelValue,
				planOwnerUIDLabel:                   string(node.GetUID()),
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: clabernetesapisv1alpha1.SchemeGroupVersion.String(),
				Kind:       "Node",
				Name:       "old-router",
				UID:        apimachinerytypes.UID("different-node-uid"),
				Controller: boolPointer(true),
			}},
		},
	}
	if err = client.Create(context.Background(), foreign); err != nil {
		t.Fatal(err)
	}

	if err = reconciler.GarbageCollect(context.Background(), node, newPlan.GetName()); err != nil {
		t.Fatal(err)
	}
	if err = client.Get(context.Background(), ctrlruntimeclient.ObjectKeyFromObject(oldPlan), &k8scorev1.ConfigMap{}); !apimachineryerrors.IsNotFound(
		err,
	) {
		t.Fatalf("superseded owned plan still exists: %v", err)
	}
	for _, retained := range []*k8scorev1.ConfigMap{newPlan, foreign} {
		if err = client.Get(context.Background(), ctrlruntimeclient.ObjectKeyFromObject(retained), &k8scorev1.ConfigMap{}); err != nil {
			t.Fatalf("UID-safe garbage collection removed %q: %v", retained.GetName(), err)
		}
	}
}

func planTestNode(name string) *clabernetesapisv1alpha1.Node {
	return &clabernetesapisv1alpha1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "lab",
			UID:       apimachinerytypes.UID("router-node-uid"),
		},
	}
}

func planTestScheme(t *testing.T) *apimachineryruntime.Scheme {
	t.Helper()
	scheme := apimachineryruntime.NewScheme()
	for _, add := range []func(*apimachineryruntime.Scheme) error{
		clabernetesapisv1alpha1.AddToScheme,
		k8scorev1.AddToScheme,
	} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	return scheme
}

func boolPointer(value bool) *bool {
	return &value
}
