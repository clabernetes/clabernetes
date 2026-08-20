//nolint:gocyclo,testpackage // dense fixture-driven tests exercise one boundary end to end.
package node

import (
	"bytes"
	"context"
	"errors"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	k8scorev1 "k8s.io/api/core/v1"
	k8snetworkingv1 "k8s.io/api/networking/v1"
	k8srbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestPlannerReconcilerCreatesDenyPolicyBeforePodAndValidatesResult(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	node := planTestNode("router")
	input := validInput()
	plan := validPlannerResult(t, input, "planner-v1")

	framed, err := clabernetesinternaldeviceplan.EncodeWorkerOutput(plan)
	if err != nil {
		t.Fatal(err)
	}

	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(plannerTestScheme(t)).
		WithObjects(node).
		Build()
	reconciler := &PlannerReconciler{
		Client: client,
		ReadLogs: func(_ context.Context, namespace, podName, containerName string) ([]byte, error) {
			if namespace != node.GetNamespace() || podName == "" ||
				containerName != plannerContainerName {
				t.Fatalf("unexpected log target %s/%s:%s", namespace, podName, containerName)
			}

			return append([]byte("imported hook log\n"), framed...), nil
		},
	}
	attempt := PlannerAttempt{
		Node: node, Input: input, Image: "example/c9s@sha256:abc",
		PlannerRevision: "planner-v1", DeadlineSeconds: 60,
	}

	first, err := reconciler.Reconcile(ctx, attempt)
	if err != nil {
		t.Fatal(err)
	}

	if first.State != PlannerStatePending {
		t.Fatalf("first planner state = %q, want Pending", first.State)
	}

	policies := &k8snetworkingv1.NetworkPolicyList{}
	if err = client.List(ctx, policies, ctrlruntimeclient.InNamespace(node.GetNamespace())); err != nil {
		t.Fatal(err)
	}

	pods := &k8scorev1.PodList{}
	if err = client.List(ctx, pods, ctrlruntimeclient.InNamespace(node.GetNamespace())); err != nil {
		t.Fatal(err)
	}

	if len(policies.Items) != 1 || len(pods.Items) != 0 {
		t.Fatalf(
			"first reconcile created policies=%d Pods=%d, want 1/0",
			len(policies.Items),
			len(pods.Items),
		)
	}

	second, err := reconciler.Reconcile(ctx, attempt)
	if err != nil {
		t.Fatal(err)
	}

	if second.State != PlannerStatePending {
		t.Fatalf("second planner state = %q, want Pending", second.State)
	}

	pod := &k8scorev1.Pod{}
	if err = client.Get(ctx, plannerObjectKey(node.GetNamespace(), second.PodName), pod); err != nil {
		t.Fatal(err)
	}

	pod.Status.Phase = k8scorev1.PodSucceeded
	if err = client.Status().Update(ctx, pod); err != nil {
		t.Fatal(err)
	}

	completed, err := reconciler.Reconcile(ctx, attempt)
	if err != nil {
		t.Fatal(err)
	}

	if completed.State != PlannerStateSucceeded || completed.Plan == nil ||
		completed.Plan.InputDigest != completed.InputDigest {
		t.Fatalf("completed planner result = %#v", completed)
	}
}

func TestPlannerReconcilerReturnsStructuredFailedWorkerDiagnostic(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer

	workerErr := (clabernetesinternaldeviceplan.Worker{
		Input: bytes.NewBufferString(`{}`), Output: &logs,
	}).Run(context.Background())

	var want *clabernetesinternaldeviceplan.Error
	if !errors.As(workerErr, &want) {
		t.Fatalf("worker error = %#v", workerErr)
	}

	ctx := context.Background()
	node := planTestNode("router-diagnostic")
	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(plannerTestScheme(t)).
		WithObjects(node).
		Build()
	reconciler := &PlannerReconciler{
		Client: client,
		ReadLogs: func(context.Context, string, string, string) ([]byte, error) {
			return logs.Bytes(), nil
		},
	}

	attempt := PlannerAttempt{
		Node: node, Input: validInput(), Image: "example/c9s@sha256:abc",
		PlannerRevision: "planner-v1", DeadlineSeconds: 60,
	}
	if _, err := reconciler.Reconcile(ctx, attempt); err != nil {
		t.Fatal(err)
	}

	result, err := reconciler.Reconcile(ctx, attempt)
	if err != nil {
		t.Fatal(err)
	}

	pod := &k8scorev1.Pod{}
	if err = client.Get(ctx, plannerObjectKey(node.GetNamespace(), result.PodName), pod); err != nil {
		t.Fatal(err)
	}

	pod.Status.Phase = k8scorev1.PodFailed
	if err = client.Status().Update(ctx, pod); err != nil {
		t.Fatal(err)
	}

	_, err = reconciler.Reconcile(ctx, attempt)

	var got *clabernetesinternaldeviceplan.Error
	if !errors.Is(err, ErrPlannerFailed) || !errors.As(err, &got) ||
		got.Code != want.Code || got.Field != want.Field || got.Message != want.Message {
		t.Fatalf("failed worker error = %#v, want structured %#v", err, want)
	}
}

func TestPlannerReconcilerRejectsMismatchedWorkerIdentity(t *testing.T) {
	t.Parallel()

	node := planTestNode("router")
	input := validInput()
	plan := validPlannerResult(t, input, "other-revision")

	framed, err := clabernetesinternaldeviceplan.EncodeWorkerOutput(plan)
	if err != nil {
		t.Fatal(err)
	}

	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(plannerTestScheme(t)).
		WithObjects(node).
		Build()
	reconciler := &PlannerReconciler{
		Client: client,
		ReadLogs: func(context.Context, string, string, string) ([]byte, error) {
			return framed, nil
		},
	}

	attempt := PlannerAttempt{
		Node: node, Input: input, Image: "example/c9s@sha256:abc",
		PlannerRevision: "planner-v1", DeadlineSeconds: 60,
	}
	if _, err = reconciler.Reconcile(context.Background(), attempt); err != nil {
		t.Fatal(err)
	}

	result, err := reconciler.Reconcile(context.Background(), attempt)
	if err != nil {
		t.Fatal(err)
	}

	pod := &k8scorev1.Pod{}
	if err = client.Get(
		context.Background(),
		plannerObjectKey(node.GetNamespace(), result.PodName),
		pod,
	); err != nil {
		t.Fatal(err)
	}

	pod.Status.Phase = k8scorev1.PodSucceeded
	if err = client.Status().Update(context.Background(), pod); err != nil {
		t.Fatal(err)
	}

	_, err = reconciler.Reconcile(context.Background(), attempt)
	if !errors.Is(err, ErrPlannerFailed) {
		t.Fatalf("identity mismatch error = %v, want ErrPlannerFailed", err)
	}
}

func TestPlannerPodSpecComparisonAcceptsOnlyAPIDefaultsAndSchedulingState(t *testing.T) {
	t.Parallel()

	node := planTestNode("router")

	rendered, err := RenderPlannerPod(PlannerPodInput{
		Node: node, Name: "router-plan-abc", Image: "example/c9s@sha256:abc",
		InputConfigMapName: "router-plan-input-abc", InputDigest: "sha256:abc",
		PlannerRevision: "planner-v1", MaxInputBytes: 1 << 20, DeadlineSeconds: 60,
	})
	if err != nil {
		t.Fatal(err)
	}

	observed := rendered.DeepCopy()
	observed.Spec.DNSPolicy = k8scorev1.DNSClusterFirst
	observed.Spec.ServiceAccountName = "default"
	observed.Spec.DeprecatedServiceAccount = "default"
	observed.Spec.SchedulerName = "default-scheduler"
	observed.Spec.NodeName = "worker-a"
	priority := int32(0)
	preemption := k8scorev1.PreemptLowerPriority
	observed.Spec.Priority = &priority
	observed.Spec.PreemptionPolicy = &preemption
	observed.Spec.Tolerations = []k8scorev1.Toleration{{
		Key: "node.kubernetes.io/not-ready", Operator: k8scorev1.TolerationOpExists,
		Effect: k8scorev1.TaintEffectNoExecute, TolerationSeconds: func() *int64 {
			seconds := int64(300)

			return &seconds
		}(),
	}}
	defaultMode := int32(0o644)
	observed.Spec.Volumes[0].ConfigMap.DefaultMode = &defaultMode
	observed.Spec.Containers[0].TerminationMessagePath = "/dev/termination-log"
	observed.Spec.Containers[0].TerminationMessagePolicy = k8scorev1.TerminationMessageReadFile

	observed.ObjectMeta = metav1.ObjectMeta{Name: rendered.GetName()}
	if !plannerPodSpecMatches(rendered, observed) {
		t.Fatalf("API-defaulted planner Pod was rejected: %#v", observed.Spec)
	}

	tampered := observed.DeepCopy()

	tampered.Spec.Containers[0].Image = "example/other@sha256:def"
	if plannerPodSpecMatches(rendered, tampered) {
		t.Fatal("planner Pod with a changed worker image was accepted")
	}

	tampered = observed.DeepCopy()

	tampered.Spec.Containers = append(
		tampered.Spec.Containers,
		k8scorev1.Container{Name: "sidecar"},
	)
	if plannerPodSpecMatches(rendered, tampered) {
		t.Fatal("planner Pod with an extra container was accepted")
	}
}

func TestPlannerObjectNameCoversImmutableExecutionPolicy(t *testing.T) {
	t.Parallel()

	node := planTestNode("router")
	base := PlannerPodInput{
		Node: node, Image: "example/c9s@sha256:abc",
		InputConfigMapName: "router-plan-input-abc", InputDigest: "sha256:abc",
		PlannerRevision: "planner-v1", WorkerCommand: plannerWorkerImages,
		MaxInputBytes: 1 << 20, DeadlineSeconds: 60,
	}

	baseName, err := imageDiscoveryPodName(node.GetName(), base)
	if err != nil {
		t.Fatal(err)
	}

	again, err := imageDiscoveryPodName(node.GetName(), base)
	if err != nil {
		t.Fatal(err)
	}

	if again != baseName {
		t.Fatalf("stable planner object name = %q, want %q", again, baseName)
	}

	changedInputs := []PlannerPodInput{base, base, base, base}
	changedInputs[0].Image = "example/c9s@sha256:def"
	changedInputs[1].PlannerRevision = "planner-v2"
	changedInputs[2].DeadlineSeconds = 120

	changedInputs[3].ImagePullSecrets = []k8scorev1.LocalObjectReference{{Name: "pull-b"}}
	for _, changed := range changedInputs {
		changedName, nameErr := imageDiscoveryPodName(node.GetName(), changed)
		if nameErr != nil {
			t.Fatal(nameErr)
		}

		if changedName == baseName {
			t.Fatalf("immutable execution-policy change retained object name %q", baseName)
		}
	}
}

func validPlannerResult(
	t *testing.T,
	input clabernetesinternaldeviceplan.Input,
	revision string,
) clabernetesinternaldeviceplan.Plan {
	t.Helper()

	digest, err := input.Digest()
	if err != nil {
		t.Fatal(err)
	}

	return clabernetesinternaldeviceplan.Plan{
		SchemaVersion: clabernetesinternaldeviceplan.SchemaVersion,
		Compatibility: input.Compatibility,
		InputDigest:   digest,
		Planner: clabernetesinternaldeviceplan.PlannerIdentity{
			Name:     "clabernetes",
			Revision: revision,
		},
		Nodes: []clabernetesinternaldeviceplan.NodePlan{{
			ID: "node-a", Name: "router", Kind: "synthetic-registry-entry",
			ContainerIDs: []string{
				"node-a/primary",
			}, ReadinessContainerIDs: []string{"node-a/primary"},
		}},
		Containers: []clabernetesinternaldeviceplan.ContainerPlan{{
			ID: "node-a/primary", NodeID: "node-a", NamespaceOwnerID: "node-a/primary",
			Image: "example/device:1", Required: true,
		}},
	}
}

func plannerTestScheme(t *testing.T) *apimachineryruntime.Scheme {
	t.Helper()

	scheme := apimachineryruntime.NewScheme()
	for _, add := range []func(*apimachineryruntime.Scheme) error{
		clabernetesapisv1alpha1.AddToScheme,
		k8scorev1.AddToScheme,
		k8snetworkingv1.AddToScheme,
		k8srbacv1.AddToScheme,
	} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}

	return scheme
}
