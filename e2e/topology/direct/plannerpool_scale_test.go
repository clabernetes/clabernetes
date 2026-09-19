package direct_test

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
	k8scorev1 "k8s.io/api/core/v1"
)

// TestPlannerPool200LightNodes is an opt-in capacity experiment using idle Linux nodes.
// It retains the management mesh but adds no explicit Links, traffic generators or PVCs.
func TestPlannerPool200LightNodes(t *testing.T) {
	testPlannerPoolScale(t, false)
}

// TestPlannerPool200LinkedNodes compiles a containerlab topology file into a 200-node ring
// and checks traffic in both directions on every declared data link.
func TestPlannerPool200LinkedNodes(t *testing.T) {
	testPlannerPoolScale(t, true)
}

//nolint:gocognit,gocyclo // Keep the benchmark setup, timing and validation in one opt-in scenario.
func testPlannerPoolScale(t *testing.T, linked bool) {
	t.Helper()
	if os.Getenv("PLANNER_POOL_SCALE_E2E") == "" {
		t.Skip("PLANNER_POOL_SCALE_E2E is not set")
	}
	const count = 200
	managerNamespace := os.Getenv("PLANNER_POOL_NAMESPACE")
	if managerNamespace == "" {
		managerNamespace = "c9s-e2e"
	}
	before := poolPodUIDs(t, managerNamespace)
	if len(before) == 0 {
		t.Fatal("no ready planner pool workers")
	}
	namespace := clabernetestesthelper.NewTestNamespace("planner-pool-200")
	clabernetestesthelper.KubectlCreateNamespace(t, namespace)
	defer func() {
		if os.Getenv("PLANNER_POOL_SCALE_KEEP") == "" && !*clabernetestesthelper.SkipCleanup {
			clabernetestesthelper.KubectlDeleteNamespace(t, namespace)
		}
	}()
	poolKubectl(t, "label", "namespace", namespace, "c9s.run/scale-test=planner-pool-200")
	var manifest strings.Builder
	if linked {
		manifest.WriteString(poolRingManifest(t))
	} else {
		poolApply(t, namespace, `apiVersion: c9s.run/v1alpha1
kind: NodeProfile
metadata:
  name: light
spec:
  resources:
    requests:
      cpu: 10m
      memory: 16Mi
  expose:
    exposeType: ClusterIP
    disableAutoExpose: true
`)
		for index := range count {
			fmt.Fprintf(&manifest, `---
apiVersion: c9s.run/v1alpha1
kind: Node
metadata:
  name: bb-%03d
spec:
  kind: linux
  image: docker.io/library/busybox:1.37.0-musl
  entrypoint: sleep
  cmd: "2147483647"
  profileRef:
    name: light
`, index)
		}
	}
	started := time.Now()
	poolApply(t, namespace, manifest.String())
	allPlanned := time.Duration(0)
	for {
		var nodes clabernetesapisv1alpha1.NodeList
		raw := poolKubectl(t, "get", "nodes.c9s.run", "-n", namespace, "-o", "json")
		if err := json.Unmarshal(raw, &nodes); err != nil {
			t.Fatal(err)
		}
		planned, ready := 0, 0
		for _, node := range nodes.Items {
			if node.Status.PlanDigest != "" {
				planned++
			}
			if node.Status.Readiness == "ready" {
				ready++
			}
		}
		elapsed := time.Since(started)
		t.Logf("namespace=%s elapsed=%s planned=%d/%d ready=%d/%d",
			namespace, elapsed.Round(time.Second), planned, count, ready, count)
		if planned == count && allPlanned == 0 {
			allPlanned = elapsed
		}
		if ready == count {
			break
		}
		if elapsed > 15*time.Minute {
			t.Fatalf(
				"startup deadline: planned=%d ready=%d; inspect namespace %s",
				planned,
				ready,
				namespace,
			)
		}
		time.Sleep(5 * time.Second)
	}
	allReady := time.Since(started)
	if after := poolPodUIDs(t, managerNamespace); !reflect.DeepEqual(before, after) {
		t.Fatalf("pool workers changed during scale test: before=%v after=%v", before, after)
	}
	var pods k8scorev1.PodList
	if err := json.Unmarshal(poolKubectl(t, "get", "pods", "-n", namespace, "-o", "json"), &pods); err != nil {
		t.Fatal(err)
	}
	if len(pods.Items) != count {
		t.Fatalf(
			"expected %d device Pods and no disposable planners, found %d",
			count,
			len(pods.Items),
		)
	}
	placement := map[string]int{}
	for _, pod := range pods.Items {
		placement[pod.Spec.NodeName]++
		assertPoolPodNoRestarts(t, pod)
		for _, container := range pod.Spec.Containers {
			if strings.Contains(container.Image, "busybox") {
				if container.Resources.Requests.Cpu().MilliValue() != 10 ||
					container.Resources.Requests.Memory().Value() != 16<<20 {
					t.Fatalf("light requests not applied to %s", pod.Name)
				}
			}
		}
	}
	for _, pod := range poolPods(t, managerNamespace, "c9s.run/planner-pool").Items {
		assertPoolPodNoRestarts(t, pod)
	}
	var links, crossWorkerLinks int
	// Preserve phase timings even if a later data-plane assertion fails.
	defer func() {
		if path := os.Getenv("PLANNER_POOL_SCALE_REPORT"); path != "" {
			report, err := json.MarshalIndent(map[string]any{
				"namespace": namespace, "nodes": count, "startedAt": started.UTC(),
				"passed": !t.Failed(), "linkedTopology": linked,
				"allPlannedSeconds": allPlanned.Seconds(), "allReadySeconds": allReady.Seconds(),
				"placement": placement, "plannerWorkers": before,
				"links": links, "crossWorkerLinks": crossWorkerLinks,
			}, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(path, report, 0o600); err != nil { //nolint:gosec // The test operator explicitly chooses this local report destination.
				t.Fatal(err)
			}
		}
	}()
	t.Logf("all planned=%s all ready=%s placement=%v workers=%v",
		allPlanned.Round(time.Second), allReady.Round(time.Second), placement, before)
	if linked {
		links, crossWorkerLinks = poolRingConnectivity(t, namespace, pods.Items)
	}
	for _, pod := range poolPods(t, namespace, "c9s.run/direct-workload").Items {
		assertPoolPodNoRestarts(t, pod)
	}
}

func assertPoolPodNoRestarts(t *testing.T, pod k8scorev1.Pod) {
	t.Helper()
	for _, statuses := range [][]k8scorev1.ContainerStatus{pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses} {
		for _, status := range statuses {
			if status.RestartCount != 0 {
				t.Fatalf(
					"%s/%s container %s restarted %d times",
					pod.Namespace,
					pod.Name,
					status.Name,
					status.RestartCount,
				)
			}
		}
	}
}
