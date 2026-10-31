//nolint:noinlineerr,nlreturn,wsl_v5 // Artifact persistence tests are compact ownership assertions.
package node //nolint:testpackage // tests exercise the generated-artifact object boundary

import (
	"context"
	"errors"
	"strings"
	"testing"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	k8scorev1 "k8s.io/api/core/v1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// artifactTestPlan builds a plan with one regular and one sensitive generated artifact.
func artifactTestPlan(t *testing.T) clabernetesinternaldeviceplan.Plan {
	t.Helper()

	regularContent := []byte("generated\n")
	sensitiveContent := []byte("tls material\n")
	return clabernetesinternaldeviceplan.Plan{
		SchemaVersion: clabernetesinternaldeviceplan.SchemaVersion,
		Files: []clabernetesinternaldeviceplan.FilePlan{
			{
				ID: "generated/public", NodeID: "node-a",
				SourceKind:      clabernetesinternaldeviceplan.FileSourcePlanArtifact,
				ArtifactKind:    clabernetesinternaldeviceplan.ArtifactRegular,
				SourceReference: "generated/public", ArtifactPath: "generated/public.conf",
				Digest: clabernetesinternaldeviceplan.Digest(regularContent), Mode: 0o644,
			},
			{
				ID: "generated/tls", NodeID: "node-a",
				SourceKind:      clabernetesinternaldeviceplan.FileSourcePlanArtifact,
				ArtifactKind:    clabernetesinternaldeviceplan.ArtifactRegular,
				SourceReference: "generated/tls", ArtifactPath: "generated/tls.pem",
				Digest: clabernetesinternaldeviceplan.Digest(sensitiveContent), Mode: 0o600,
				Sensitive: true,
			},
		},
	}
}

func TestPlanArtifactReferencesKeepSensitiveContentOutOfConfigMaps(t *testing.T) {
	t.Parallel()

	node := planTestNode("router")
	references, err := PlanArtifactReferences(node, artifactTestPlan(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(references) != 2 {
		t.Fatalf("generated artifact references = %#v", references)
	}

	byID := map[string]clabernetesinternaldeviceplan.PayloadInput{}
	for _, reference := range references {
		byID[reference.ID] = reference
	}

	public := byID["generated/public"]
	if public.Kind != clabernetesinternaldeviceplan.PayloadConfigMap || public.Sensitive {
		t.Fatalf("non-sensitive artifact reference = %#v", public)
	}
	private := byID["generated/tls"]
	if private.Kind != clabernetesinternaldeviceplan.PayloadSecret || !private.Sensitive {
		t.Fatalf("sensitive artifact reference = %#v", private)
	}
	if !strings.HasPrefix(private.Reference, "lab/") ||
		!strings.HasSuffix(private.Reference, ":"+PlanArtifactKey) {
		t.Fatalf("artifact reference %q is not a same-namespace object key", private.Reference)
	}
}

// TestPlanArtifactReferencesResolveFromThePlanAlone proves a cached plan resolves identical object
// references without carrying artifact bytes again.
func TestPlanArtifactReferencesResolveFromThePlanAlone(t *testing.T) {
	t.Parallel()

	node := planTestNode("router")
	plan := artifactTestPlan(t)
	first, err := PlanArtifactReferences(node, plan)
	if err != nil {
		t.Fatal(err)
	}
	second, err := PlanArtifactReferences(node, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != len(second) {
		t.Fatalf("reference count = %d, want %d", len(second), len(first))
	}
	for index := range first {
		if first[index] != second[index] {
			t.Fatalf("reference %d is not deterministic: %#v", index, second[index])
		}
	}

	names, err := PlanArtifactObjectNames(node, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != len(first) {
		t.Fatalf("object names = %#v", names)
	}
}

func TestPlanArtifactReconcilerPersistsDigestNamedObjects(t *testing.T) {
	t.Parallel()

	node := planTestNode("router")
	plan := artifactTestPlan(t)
	scheme := planTestScheme(t)
	client := ctrlruntimefake.NewClientBuilder().WithScheme(scheme).WithObjects(node).Build()
	reconciler := &PlanArtifactReconciler{Client: client}
	ctx := context.Background()

	artifacts := []clabernetesinternaldeviceplan.ArtifactContent{
		{
			NodeID: "node-a", ArtifactPath: "generated/public.conf",
			Digest: plan.Files[0].Digest, Data: []byte("generated\n"),
		},
		{
			NodeID: "node-a", ArtifactPath: "generated/tls.pem",
			Digest: plan.Files[1].Digest, Data: []byte("tls material\n"), Sensitive: true,
		},
	}
	if err := reconciler.Ensure(ctx, node, artifacts); err != nil {
		t.Fatal(err)
	}
	// A second persist of the same content is idempotent.
	if err := reconciler.Ensure(ctx, node, artifacts); err != nil {
		t.Fatal(err)
	}
	if err := reconciler.Verify(ctx, node, plan); err != nil {
		t.Fatal(err)
	}

	publicName := PlanArtifactObjectName(node.GetName(), plan.Files[0].Digest)
	public := &k8scorev1.ConfigMap{}
	if err := client.Get(ctx, apimachinerytypes.NamespacedName{
		Namespace: "lab", Name: publicName,
	}, public); err != nil {
		t.Fatal(err)
	}
	if public.Immutable == nil || !*public.Immutable ||
		public.Data[PlanArtifactKey] != "generated\n" ||
		!metav1.IsControlledBy(public, node) {
		t.Fatalf("persisted ConfigMap artifact = %#v", public)
	}

	privateName := PlanArtifactObjectName(node.GetName(), plan.Files[1].Digest)
	private := &k8scorev1.Secret{}
	if err := client.Get(ctx, apimachinerytypes.NamespacedName{
		Namespace: "lab", Name: privateName,
	}, private); err != nil {
		t.Fatal(err)
	}
	if string(private.Data[PlanArtifactKey]) != "tls material\n" ||
		!metav1.IsControlledBy(private, node) {
		t.Fatalf("persisted Secret artifact = %#v", private)
	}
}

func TestPlanArtifactReconcilerRejectsConflictingObject(t *testing.T) {
	t.Parallel()

	node := planTestNode("router")
	plan := artifactTestPlan(t)
	scheme := planTestScheme(t)
	forged := &k8scorev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      PlanArtifactObjectName(node.GetName(), plan.Files[1].Digest),
			Namespace: "lab",
		},
		Data: map[string][]byte{PlanArtifactKey: []byte("forged\n")},
	}
	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(node, forged).
		Build()
	reconciler := &PlanArtifactReconciler{Client: client}

	err := reconciler.Ensure(
		context.Background(),
		node,
		[]clabernetesinternaldeviceplan.ArtifactContent{{
			NodeID: "node-a", ArtifactPath: "generated/tls.pem",
			Digest: plan.Files[1].Digest, Data: []byte("tls material\n"), Sensitive: true,
		}},
	)
	if !errors.Is(err, ErrPlanArtifactConflict) {
		t.Fatalf("Ensure() error = %#v, want artifact conflict", err)
	}
}

func TestPlanArtifactVerifyReportsMissingObject(t *testing.T) {
	t.Parallel()

	node := planTestNode("router")
	scheme := planTestScheme(t)
	client := ctrlruntimefake.NewClientBuilder().WithScheme(scheme).WithObjects(node).Build()
	reconciler := &PlanArtifactReconciler{Client: client}

	err := reconciler.Verify(context.Background(), node, artifactTestPlan(t))
	if !errors.Is(err, ErrPlanArtifactMissing) {
		t.Fatalf("Verify() error = %#v, want missing artifact", err)
	}
}

func TestPlanArtifactReconcilerCollectsSupersededObjects(t *testing.T) {
	t.Parallel()

	node := planTestNode("router")
	scheme := planTestScheme(t)
	client := ctrlruntimefake.NewClientBuilder().WithScheme(scheme).WithObjects(node).Build()
	reconciler := &PlanArtifactReconciler{Client: client}
	ctx := context.Background()

	stale := []clabernetesinternaldeviceplan.ArtifactContent{{
		NodeID: "node-a", ArtifactPath: "generated/stale.conf",
		Digest: clabernetesinternaldeviceplan.Digest([]byte("stale\n")),
		Data:   []byte("stale\n"), Sensitive: true,
	}}
	current := []clabernetesinternaldeviceplan.ArtifactContent{{
		NodeID: "node-a", ArtifactPath: "generated/current.conf",
		Digest: clabernetesinternaldeviceplan.Digest([]byte("current\n")),
		Data:   []byte("current\n"), Sensitive: true,
	}}
	if err := reconciler.Ensure(ctx, node, stale); err != nil {
		t.Fatal(err)
	}
	if err := reconciler.Ensure(ctx, node, current); err != nil {
		t.Fatal(err)
	}

	keep := map[string]bool{
		PlanArtifactObjectName(node.GetName(), current[0].Digest): true,
	}
	if err := reconciler.GarbageCollect(ctx, node, keep); err != nil {
		t.Fatal(err)
	}

	staleObject := &k8scorev1.Secret{}
	err := client.Get(ctx, apimachinerytypes.NamespacedName{
		Namespace: "lab",
		Name:      PlanArtifactObjectName(node.GetName(), stale[0].Digest),
	}, staleObject)
	if !apimachineryerrors.IsNotFound(err) {
		t.Fatalf("superseded artifact was not collected: %v", err)
	}

	currentObject := &k8scorev1.Secret{}
	if err = client.Get(ctx, apimachinerytypes.NamespacedName{
		Namespace: "lab",
		Name:      PlanArtifactObjectName(node.GetName(), current[0].Digest),
	}, currentObject); err != nil {
		t.Fatalf("referenced artifact was collected: %v", err)
	}
}
