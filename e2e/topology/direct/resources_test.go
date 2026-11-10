package direct_test

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
	k8sappsv1 "k8s.io/api/apps/v1"
	k8scorev1 "k8s.io/api/core/v1"
	sigsyaml "sigs.k8s.io/yaml"
)

// TestDirectGlobalKindResourceUpdates proves that an exact alias in the global kind map supplies
// the shipped default to both Deployment and Pod specs and that editing it reaches the live Pod.
func TestDirectGlobalKindResourceUpdates(t *testing.T) {
	namespace := clabernetestesthelper.NewTestNamespace("direct-kind-resources")
	managerNamespace := os.Getenv("PLANNER_POOL_NAMESPACE")
	if managerNamespace == "" {
		managerNamespace = "c9s-e2e"
	}

	previousSRL := srlKindResources(t, managerNamespace)
	clabernetestesthelper.KubectlCreateNamespace(t, namespace)

	defer func() {
		if t.Failed() {
			clabernetestesthelper.DumpNamespaceDiagnostics(t, namespace)
		}

		restoreSRLKindResources(t, managerNamespace, previousSRL)

		if !*clabernetestesthelper.SkipCleanup {
			clabernetestesthelper.KubectlDeleteNamespace(t, namespace)
		}
	}()

	setSRLKindMemoryRequest(t, managerNamespace, "2Gi")
	clabernetestesthelper.KubectlFileOp(
		t,
		clabernetestesthelper.Apply,
		namespace,
		"test-fixtures/15-global-kind-resources.yaml",
	)
	waitForDirectNodeReady(t, namespace, "srl1")
	observation := observeDevicePod(t, namespace, "srl1")
	assertContainerResourceRequests(t, namespace, "srl1", observation.containerName, "2Gi")

	setSRLKindMemoryRequest(t, managerNamespace, "2304Mi")
	waitForContainerResourceRequests(t, namespace, "srl1", observation.containerName, "2304Mi")
	waitForDirectNodeReady(t, namespace, "srl1")
}

func srlKindResources(t *testing.T, managerNamespace string) string {
	t.Helper()

	cmd := exec.CommandContext( //nolint:gosec
		t.Context(), "kubectl", "get", "config.c9s.run", "clabernetes",
		"--namespace", managerNamespace,
		"-o", "jsonpath={.spec.deployment.resourcesByKind.srl}",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("reading previous global srl resources: %v: %s", err, output)
	}

	return strings.TrimSpace(string(output))
}

func restoreSRLKindResources(t *testing.T, managerNamespace, previous string) {
	t.Helper()

	operation := "remove"
	arguments := []string{
		"patch", "config.c9s.run", "clabernetes", "--namespace", managerNamespace,
	}
	if previous == "" {
		arguments = append(arguments,
			"--type=json",
			"--patch", `[{"op":"remove","path":"/spec/deployment/resourcesByKind/srl"}]`,
		)
	} else {
		operation = "restore"
		arguments = append(arguments,
			"--type=merge",
			"--patch", fmt.Sprintf(
				`{"spec":{"deployment":{"resourcesByKind":{"srl":%s}}}}`,
				previous,
			),
		)
	}
	cmd := exec.CommandContext(t.Context(), "kubectl", arguments...) //nolint:gosec
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Errorf("restoring previous global srl resources by %s: %v: %s", operation, err, output)
	}
}

func setSRLKindMemoryRequest(t *testing.T, managerNamespace, memory string) {
	t.Helper()

	patch := fmt.Sprintf(
		`{"spec":{"deployment":{"resourcesByKind":{"srl":{"requests":{"cpu":"2","memory":%q}}}}}}`,
		memory,
	)
	cmd := exec.CommandContext( //nolint:gosec
		t.Context(), "kubectl", "patch", "config.c9s.run", "clabernetes",
		"--namespace", managerNamespace, "--type=merge", "--patch", patch,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("setting global srl resources: %v: %s", err, output)
	}
}

func assertContainerResourceRequests(
	t *testing.T,
	namespace, nodeName, containerName, wantMemory string,
) {
	t.Helper()

	deploymentResources := deploymentResourceRequests(t, namespace, nodeName, containerName)
	podResources := podResourceRequests(t, namespace, nodeName, containerName)
	for source, resources := range map[string]k8scorev1.ResourceRequirements{
		"Deployment": deploymentResources,
		"Pod":        podResources,
	} {
		if got := resources.Requests.Cpu().String(); got != "2" {
			t.Errorf("%s CPU request = %q, want 2", source, got)
		}
		if got := resources.Requests.Memory().String(); got != wantMemory {
			t.Errorf("%s memory request = %q, want %s", source, got, wantMemory)
		}
	}
}

func waitForContainerResourceRequests(
	t *testing.T,
	namespace, nodeName, containerName, wantMemory string,
) {
	t.Helper()

	deadline := time.Now().Add(directNodeReadyTimeout)
	var lastDeployment, lastPod k8scorev1.ResourceRequirements
	for time.Now().Before(deadline) {
		lastDeployment = deploymentResourceRequests(t, namespace, nodeName, containerName)
		lastPod = podResourceRequests(t, namespace, nodeName, containerName)
		if lastDeployment.Requests.Cpu().String() == "2" &&
			lastDeployment.Requests.Memory().String() == wantMemory &&
			lastPod.Requests.Cpu().String() == "2" &&
			lastPod.Requests.Memory().String() == wantMemory {
			return
		}

		time.Sleep(directPollInterval)
	}

	t.Fatalf(
		"global resource update did not converge: Deployment = %#v, Pod = %#v; want CPU 2/memory %s",
		lastDeployment,
		lastPod,
		wantMemory,
	)
}

func deploymentResourceRequests(
	t *testing.T,
	namespace, nodeName, containerName string,
) k8scorev1.ResourceRequirements {
	t.Helper()

	raw := clabernetestesthelper.KubectlGetOp(t, "deployment", namespace, nodeName)
	deployment := &k8sappsv1.Deployment{}
	if err := sigsyaml.Unmarshal(raw, deployment); err != nil {
		t.Fatalf("decoding Deployment for %s: %v", nodeName, err)
	}
	for _, container := range deployment.Spec.Template.Spec.Containers {
		if container.Name == containerName {
			return *container.Resources.DeepCopy()
		}
	}

	t.Fatalf("Deployment %s has no primary container %q", nodeName, containerName)

	return k8scorev1.ResourceRequirements{}
}

func podResourceRequests(
	t *testing.T,
	namespace, nodeName, containerName string,
) k8scorev1.ResourceRequirements {
	t.Helper()

	pods := listPods(t, namespace, "c9s.run/direct-workload="+nodeName)
	if len(pods.Items) != 1 {
		return k8scorev1.ResourceRequirements{}
	}

	podName := pods.Items[0].Metadata.Name
	raw := clabernetestesthelper.KubectlGetOp(t, "pod", namespace, podName)
	pod := &k8scorev1.Pod{}
	if err := sigsyaml.Unmarshal(raw, pod); err != nil {
		t.Fatalf("decoding Pod %s: %v", podName, err)
	}
	for _, container := range pod.Spec.Containers {
		if container.Name == containerName {
			return *container.Resources.DeepCopy()
		}
	}

	t.Fatalf("Pod %s has no primary container %q", podName, containerName)

	return k8scorev1.ResourceRequirements{}
}
