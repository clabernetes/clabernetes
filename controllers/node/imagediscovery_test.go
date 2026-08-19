package node

import (
	"context"
	"testing"

	clabernetesdeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	k8scorev1 "k8s.io/api/core/v1"
	k8snetworkingv1 "k8s.io/api/networking/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestImageDiscoveryReconcilerUsesLockedDownWorkerWithExplicitSeedMetadata(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	node := planTestNode("router")
	input := validInput()
	inputDigest, err := input.Digest()
	if err != nil {
		t.Fatal(err)
	}
	discovery := clabernetesdeviceplan.ImageDiscovery{
		SchemaVersion: clabernetesdeviceplan.SchemaVersion,
		Compatibility: input.Compatibility, InputDigest: inputDigest,
		Planner: clabernetesdeviceplan.PlannerIdentity{Name: "clabernetes", Revision: "planner-v1"},
		Images: []clabernetesdeviceplan.ImageRequirement{{
			NodeID: "node-a", Role: "device", SourceReference: "example/device:1",
		}},
	}
	framed, err := clabernetesdeviceplan.EncodeImageWorkerOutput(discovery)
	if err != nil {
		t.Fatal(err)
	}
	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(plannerTestScheme(t)).
		WithObjects(node).
		Build()
	reconciler := &ImageDiscoveryReconciler{
		Client: client,
		ReadLogs: func(context.Context, string, string, string) ([]byte, error) {
			return framed, nil
		},
	}
	attempt := ImageDiscoveryAttempt{
		Node: node, Input: input, Image: "example/c9s@sha256:abc",
		PlannerRevision: "planner-v1", DeadlineSeconds: 60,
	}
	first, err := reconciler.Reconcile(ctx, attempt)
	if err != nil {
		t.Fatal(err)
	}
	policies := &k8snetworkingv1.NetworkPolicyList{}
	if err = client.List(ctx, policies, ctrlruntimeclient.InNamespace(node.GetNamespace())); err != nil {
		t.Fatal(err)
	}
	pods := &k8scorev1.PodList{}
	if err = client.List(ctx, pods, ctrlruntimeclient.InNamespace(node.GetNamespace())); err != nil {
		t.Fatal(err)
	}
	if first.State != PlannerStatePending || len(policies.Items) != 1 || len(pods.Items) != 0 {
		t.Fatalf(
			"first image-discovery state=%#v policies=%d Pods=%d",
			first,
			len(policies.Items),
			len(pods.Items),
		)
	}
	second, err := reconciler.Reconcile(ctx, attempt)
	if err != nil {
		t.Fatal(err)
	}
	pod := &k8scorev1.Pod{}
	if err = client.Get(ctx, plannerObjectKey(node.GetNamespace(), second.PodName), pod); err != nil {
		t.Fatal(err)
	}
	if len(pod.Spec.Containers) != 1 || len(pod.Spec.Containers[0].Args) == 0 ||
		pod.Spec.Containers[0].Args[0] != plannerWorkerImages {
		t.Fatalf("image discovery worker command = %#v", pod.Spec.Containers)
	}
	pod.Status.Phase = k8scorev1.PodSucceeded
	if err = client.Status().Update(ctx, pod); err != nil {
		t.Fatal(err)
	}
	completed, err := reconciler.Reconcile(ctx, attempt)
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != PlannerStateSucceeded || completed.Discovery == nil ||
		len(completed.Discovery.Images) != 1 || completed.Discovery.Images[0].Role != "device" {
		t.Fatalf("completed discovery = %#v", completed)
	}

	upgradedAttempt := attempt
	upgradedAttempt.Image = "example/c9s@sha256:def"
	upgraded, err := reconciler.Reconcile(ctx, upgradedAttempt)
	if err != nil {
		t.Fatal(err)
	}
	if upgraded.PodName == completed.PodName {
		t.Fatalf("runtime-image upgrade retained immutable worker name %q", upgraded.PodName)
	}
	upgraded, err = reconciler.Reconcile(ctx, upgradedAttempt)
	if err != nil {
		t.Fatal(err)
	}
	upgradedPod := &k8scorev1.Pod{}
	if err = client.Get(
		ctx,
		plannerObjectKey(node.GetNamespace(), upgraded.PodName),
		upgradedPod,
	); err != nil {
		t.Fatal(err)
	}
	if got := upgradedPod.Spec.Containers[0].Image; got != upgradedAttempt.Image {
		t.Fatalf("upgraded worker image = %q, want %q", got, upgradedAttempt.Image)
	}
}
