//nolint:gocyclo // dense fixture-driven tests exercise one boundary end to end.
package node //nolint:testpackage // tests exercise the stable mutable ownership boundary

import (
	"context"
	"errors"
	"strings"
	"testing"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	clabernetesinternaldirectpod "github.com/clabernetes/clabernetes/internal/directpod"
	clabernetesinternaldirectruntime "github.com/clabernetes/clabernetes/internal/directruntime"
	k8scorev1 "k8s.io/api/core/v1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestConnectivityRevisionConfigMapRetainsGenericLifecycleActionState(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	node := planTestNode("router")
	revision := testConnectivityRevision("a", "b")
	client := ctrlruntimefake.NewClientBuilder().WithScheme(planTestScheme(t)).Build()
	reconciler := &ConnectivityRevisionConfigMapReconciler{Client: client}

	configMap, err := reconciler.Ensure(ctx, node, revision)
	if err != nil {
		t.Fatal(err)
	}

	want := directConnectivityLifecycleAction{
		Mode: clabernetesinternaldeviceplan.LinkApplyRestart, PlanDigest: revision.DesiredPlanDigest,
		AffectedNodeIDs: []string{"node-b", "node-a", "node-a"},
	}

	configMap, err = reconciler.RecordLifecycleAction(ctx, node, configMap, want)
	if err != nil {
		t.Fatal(err)
	}

	got := directConnectivityLifecycleActionFrom(configMap, revision.DesiredPlanDigest)
	if got.Mode != want.Mode || got.PlanDigest != want.PlanDigest ||
		len(got.AffectedNodeIDs) != 2 || got.AffectedNodeIDs[0] != "node-a" ||
		got.AffectedNodeIDs[1] != "node-b" {
		t.Fatalf("recorded lifecycle action = %#v", got)
	}

	configMap.Annotations[directRestartBaselineAnnotation] = `{"planDigest":"retained"}`
	if err = client.Update(ctx, configMap); err != nil {
		t.Fatal(err)
	}
	// A replacement reconciler has no process-local memory from the controller that recorded the
	// action; the Kubernetes artifact remains the complete recovery source.
	reconciler = &ConnectivityRevisionConfigMapReconciler{Client: client}

	configMap, err = reconciler.Ensure(ctx, node, revision)
	if err != nil {
		t.Fatal(err)
	}

	if configMap.Annotations[directRestartBaselineAnnotation] == "" ||
		directConnectivityLifecycleActionFrom(
			configMap,
			revision.DesiredPlanDigest,
		).Mode != want.Mode {
		t.Fatalf("idempotent revision ensure dropped lifecycle state: %#v", configMap.Annotations)
	}
}

func TestConnectivityRevisionConfigMapIsStableMutableAndUIDOwned(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	node := planTestNode(strings.Repeat("long-node-name-", 6))
	first := testConnectivityRevision("a", "b")
	second := testConnectivityRevision("a", "c")
	client := ctrlruntimefake.NewClientBuilder().WithScheme(planTestScheme(t)).Build()
	reconciler := &ConnectivityRevisionConfigMapReconciler{Client: client}

	created, err := reconciler.Ensure(ctx, node, first)
	if err != nil {
		t.Fatal(err)
	}

	if created.Immutable != nil || len(created.GetName()) > kubernetesNameLimit ||
		created.GetName() != connectivityRevisionConfigMapName(
			node.GetName(),
			first.BasePlanDigest,
		) ||
		created.Annotations[connectivityRevisionDesiredAnnotation] != first.DesiredPlanDigest ||
		len(created.OwnerReferences) != 1 || created.OwnerReferences[0].UID != node.GetUID() {
		t.Fatalf("created connectivity revision ConfigMap = %#v", created)
	}

	updated, err := reconciler.Ensure(ctx, node, second)
	if err != nil {
		t.Fatal(err)
	}

	if updated.GetName() != created.GetName() || updated.Data[connectivityRevisionDataKey] ==
		created.Data[connectivityRevisionDataKey] ||
		updated.Annotations[connectivityRevisionDesiredAnnotation] != second.DesiredPlanDigest {
		t.Fatalf("updated connectivity revision ConfigMap = %#v", updated)
	}

	again, err := reconciler.Ensure(ctx, node, second)
	if err != nil {
		t.Fatal(err)
	}

	if again.GetResourceVersion() != updated.GetResourceVersion() {
		t.Fatalf(
			"idempotent Ensure() changed resource version %q -> %q",
			updated.GetResourceVersion(),
			again.GetResourceVersion(),
		)
	}

	nextBase := testConnectivityRevision("d", "d")

	next, err := reconciler.Ensure(ctx, node, nextBase)
	if err != nil {
		t.Fatal(err)
	}

	if next.GetName() == created.GetName() {
		t.Fatalf("different cold plan reused revision ConfigMap %q", next.GetName())
	}

	pod := &k8scorev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "old-device", Namespace: node.GetNamespace(),
			Annotations: map[string]string{
				clabernetesinternaldirectpod.NodeUIDAnnotation: string(node.GetUID()),
			},
		},
		Spec: k8scorev1.PodSpec{
			Containers: []k8scorev1.Container{{Name: "device", Image: "example/device:1"}},
			Volumes: []k8scorev1.Volume{{
				Name: "revision",
				VolumeSource: k8scorev1.VolumeSource{ConfigMap: &k8scorev1.ConfigMapVolumeSource{
					LocalObjectReference: k8scorev1.LocalObjectReference{Name: created.GetName()},
				}},
			}},
		},
	}
	if err = client.Create(ctx, pod); err != nil {
		t.Fatal(err)
	}

	if err = reconciler.GarbageCollect(ctx, node, next.GetName()); err != nil {
		t.Fatal(err)
	}

	if err = client.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(created), &k8scorev1.ConfigMap{}); err != nil {
		t.Fatalf("revision referenced by a remaining Pod was removed: %v", err)
	}

	if err = client.Delete(ctx, pod); err != nil {
		t.Fatal(err)
	}

	if err = reconciler.GarbageCollect(ctx, node, next.GetName()); err != nil {
		t.Fatal(err)
	}

	if err = client.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(created), &k8scorev1.ConfigMap{}); !apimachineryerrors.IsNotFound(
		err,
	) {
		t.Fatalf("superseded connectivity revision still exists: %v", err)
	}
}

func TestConnectivityRevisionConfigMapRejectsDifferentNodeUID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	node := planTestNode("router")
	revision := testConnectivityRevision("a", "b")

	rendered, err := (&ConnectivityRevisionConfigMapReconciler{}).Render(node, revision)
	if err != nil {
		t.Fatal(err)
	}

	rendered.OwnerReferences[0].UID = "former-node-uid"
	rendered.Labels[planOwnerUIDLabel] = "former-node-uid"
	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(planTestScheme(t)).
		WithObjects(rendered).
		Build()
	reconciler := &ConnectivityRevisionConfigMapReconciler{Client: client}

	_, err = reconciler.Ensure(ctx, node, revision)
	if !errors.Is(err, ErrConnectivityRevisionArtifactConflict) {
		t.Fatalf("Ensure() error = %v", err)
	}

	actual := &k8scorev1.ConfigMap{}
	if getErr := client.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(rendered), actual); getErr != nil {
		t.Fatal(getErr)
	}

	if actual.OwnerReferences[0].UID != "former-node-uid" {
		t.Fatalf("Ensure() adopted ConfigMap owned by %q", actual.OwnerReferences[0].UID)
	}
}

func testConnectivityRevision(
	base, desired string,
) clabernetesinternaldirectruntime.ConnectivityRevision {
	return clabernetesinternaldirectruntime.ConnectivityRevision{
		SchemaVersion:     clabernetesinternaldirectruntime.ConnectivityRevisionSchemaVersion,
		BasePlanDigest:    "sha256:" + strings.Repeat(base, 64),
		DesiredPlanDigest: "sha256:" + strings.Repeat(desired, 64),
	}
}
