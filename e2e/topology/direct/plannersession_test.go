package direct_test

import (
	"encoding/json"
	"testing"
	"time"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetescontrollersnode "github.com/clabernetes/clabernetes/controllers/node"
	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	clabernetesinternaldirectpod "github.com/clabernetes/clabernetes/internal/directpod"
	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
	k8sappsv1 "k8s.io/api/apps/v1"
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestDirectDisposablePlannerArtifacts exercises the isolated worker even when the installation
// uses a warm pool. Its private exec result must become durable artifacts before worker deletion.
//
//nolint:gocognit,gocyclo // Follow one sequential worker transport and persistence scenario.
func TestDirectDisposablePlannerArtifacts(t *testing.T) {
	namespace := clabernetestesthelper.NewTestNamespace("disposable-planner")
	clabernetestesthelper.KubectlCreateNamespace(t, namespace)
	defer func() {
		if t.Failed() {
			clabernetestesthelper.DumpNamespaceDiagnostics(t, namespace)
		}
		if !*clabernetestesthelper.SkipCleanup {
			clabernetestesthelper.KubectlDeleteNamespace(t, namespace)
		}
	}()
	poolApply(t, namespace, `apiVersion: v1
kind: Secret
metadata:
  name: startup
stringData:
  config: |
    disposable-planner-artifact
---
apiVersion: c9s.run/v1alpha1
kind: Node
metadata:
  name: device
spec:
  kind: linux
  image: docker.io/library/busybox:1.37.0-musl
  cmd: "sleep 2147483647"
  startup-config: /tmp/disposable.partial.cfg
  filesFromSecret:
    - filePath: /tmp/disposable.partial.cfg
      secretName: startup
      secretPath: config
`)
	waitForDirectNodeReady(t, namespace, "device")
	// Keep the ordinary Node reconciler from collecting the test-owned planning attempt.
	poolKubectl(t, "label", "node.c9s.run", "device", "-n", namespace,
		"c9s.run/ignoreReconcile=true")
	read := func(resource, name string, result any) {
		t.Helper()
		if err := json.Unmarshal(poolKubectlRetry(t, 3, "get", resource, name,
			"-n", namespace, "-o", "json"), result); err != nil {
			t.Fatal(err)
		}
	}
	var node clabernetesapisv1alpha1.Node
	var deployment k8sappsv1.Deployment
	read("node.c9s.run", "device", &node)
	read("deployment", "device", &deployment)
	references, err := clabernetesinternaldirectpod.DeploymentPlanReferences(&deployment)
	if err != nil {
		t.Fatal(err)
	}
	var planMap, inputMap k8scorev1.ConfigMap
	read("configmap", references.PlanConfigMapName, &planMap)
	read("configmap", references.InputConfigMapName, &inputMap)
	plan, err := clabernetesinternaldeviceplan.DecodePlan([]byte(planMap.Data["plan.json"]))
	if err != nil {
		t.Fatal(err)
	}
	input, err := clabernetesinternaldeviceplan.DecodeInput([]byte(inputMap.Data["input.json"]))
	if err != nil {
		t.Fatal(err)
	}
	var secrets k8scorev1.SecretList
	if err = json.Unmarshal(poolKubectl(t, "get", "secrets", "-n", namespace,
		"-l", "c9s.run/direct-entropy=seed", "-o", "json"), &secrets); err != nil {
		t.Fatal(err)
	}
	entropyName := ""
	for _, secret := range secrets.Items {
		if metav1.IsControlledBy(&secret, &node) {
			entropyName = secret.Name
		}
	}
	if entropyName == "" {
		t.Fatal("device has no owned entropy Secret")
	}
	policy := clabernetescontrollersnode.PlannerPodInput{
		Node: &node, Name: "disposable-check",
		Image:              deployment.Spec.Template.Spec.InitContainers[0].Image,
		InputConfigMapName: references.InputConfigMapName, InputDigest: plan.InputDigest,
		PlannerRevision: plan.Planner.Revision, MaxInputBytes: 4 << 20, DeadlineSeconds: 300,
		EntropySecretName: entropyName, Session: true, Payloads: input.Payloads,
		ImagePullSecrets: deployment.Spec.Template.Spec.ImagePullSecrets,
	}
	pod, err := clabernetescontrollersnode.RenderPlannerPod(policy)
	if err != nil {
		t.Fatal(err)
	}
	networkPolicy, err := clabernetescontrollersnode.RenderPlannerNetworkPolicy(policy)
	if err != nil {
		t.Fatal(err)
	}
	pod.TypeMeta = metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"}
	networkPolicy.TypeMeta = metav1.TypeMeta{
		APIVersion: "networking.k8s.io/v1",
		Kind:       "NetworkPolicy",
	}
	for _, resource := range []any{networkPolicy, pod} {
		raw, marshalErr := json.Marshal(resource)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		poolApply(t, namespace, string(raw))
	}
	deadline := time.Now().Add(5 * time.Minute)
	var cache k8scorev1.ConfigMap
	for {
		raw := poolKubectlRetry(t, 3, "get", "configmap", policy.Name, "-n", namespace,
			"--ignore-not-found", "-o", "json")
		if len(raw) != 0 {
			if err = json.Unmarshal(raw, &cache); err != nil {
				t.Fatal(err)
			}

			break
		}
		if time.Now().After(deadline) {
			t.Fatal("disposable planner did not persist its result")
		}
		time.Sleep(time.Second)
	}
	output := []byte(cache.Data["output"])
	if clabernetesinternaldeviceplan.FrameKind(
		output,
	) == clabernetesinternaldeviceplan.WorkerFrameError {
		diagnostic, decodeErr := clabernetesinternaldeviceplan.DecodeWorkerError(
			output, clabernetesinternaldeviceplan.MaxSessionFrameBytes,
		)
		t.Fatalf("disposable planner failed: %v (decode error: %v)", diagnostic, decodeErr)
	}
	result, err := clabernetesinternaldeviceplan.DecodeCachedSessionResult(
		output, clabernetesinternaldeviceplan.MaxSessionFrameBytes,
	)
	if err != nil {
		t.Fatal(err)
	}
	artifacts := 0
	for _, file := range result.Plan.Files {
		if !clabernetesinternaldeviceplan.PlanArtifactNeedsPayload(file) {
			continue
		}
		var secret k8scorev1.Secret
		read("secret", clabernetescontrollersnode.PlanArtifactObjectName(node.Name,
			clabernetesinternaldeviceplan.PlanArtifactIdentity(file)), &secret)
		if !metav1.IsControlledBy(&secret, &node) || secret.Immutable == nil ||
			!*secret.Immutable ||
			clabernetesinternaldeviceplan.Digest(secret.Data["content"]) != file.Digest {
			t.Fatal("generated artifact differs from the accepted plan")
		}
		artifacts++
	}
	if artifacts == 0 {
		t.Fatal("scenario did not exercise generated content")
	}
	poolKubectl(t, "wait", "--for=delete", "pod/"+policy.Name,
		"-n", namespace, "--timeout=60s")
	t.Logf(
		"disposable exec session persisted %d immutable artifacts and removed its worker",
		artifacts,
	)
}
