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
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
	k8scorev1 "k8s.io/api/core/v1"
)

// TestPlannerPool200LightNodes is an opt-in capacity experiment using idle Linux nodes.
// It retains the management mesh but adds no explicit Links, traffic generators or PVCs.
func TestPlannerPool200LightNodes(t *testing.T) {
	testPlannerPoolScale(t, false, 0, false)
}

// TestPlannerPool200BatchedNodes measures a batched Topology with a /22 management mesh and no PVCs.
func TestPlannerPool200BatchedNodes(t *testing.T) {
	testPlannerPoolScale(t, false, 100, false)
}

// TestPlannerPool200LinkedNodes compiles a containerlab topology file into a 200-node ring
// and checks traffic in both directions on every declared data link.
func TestPlannerPool200LinkedNodes(t *testing.T) {
	testPlannerPoolScale(t, true, 0, false)
}

// TestPlannerPool200GlobalBatchedLinkedNodes exercises the global limit without a Topology.
func TestPlannerPool200GlobalBatchedLinkedNodes(t *testing.T) {
	testPlannerPoolScale(t, true, 100, true)
}

//nolint:gocognit,gocyclo // Keep the benchmark setup, timing and validation in one opt-in scenario.
func testPlannerPoolScale(t *testing.T, linked bool, batchSize int, standalone bool) {
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
	if standalone {
		poolSetGlobalBatchSize(t, managerNamespace, batchSize)
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
	switch {
	case standalone:
		manifest.WriteString(poolStandaloneRingManifest(count))
	case batchSize > 0:
		manifest.WriteString(poolBatchedScaleManifest(count, batchSize))
	case linked:
		manifest.WriteString(poolRingManifest(t))
	default:
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
			if standalone && node.Status.PlanDigest != "" &&
				node.Annotations[clabernetesconstants.AnnotationStartupAdmitted] != string(
					node.UID,
				) {
				t.Fatalf("Node %s planned without global admission", node.Name)
			}
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
			if batchSize == 0 || standalone {
				break
			}
			var topology clabernetesapisv1alpha1.Topology
			if err := json.Unmarshal(poolKubectl(t, "get", "topology", "busybox", "-n", namespace, "-o", "json"), &topology); err != nil {
				t.Fatal(err)
			}
			if topology.Status.TopologyReady && topology.Status.ReadyNodeCount == count &&
				topology.Status.ObservedGeneration == topology.Generation {
				break
			}
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
	expectedMemory := int64(16 << 20)
	if batchSize > 0 {
		expectedMemory = 64 << 20
		var pvcs k8scorev1.PersistentVolumeClaimList
		if err := json.Unmarshal(poolKubectl(t, "get", "pvc", "-n", namespace, "-o", "json"), &pvcs); err != nil {
			t.Fatal(err)
		}
		if len(pvcs.Items) != 0 {
			t.Fatalf("expected no PVCs, found %d", len(pvcs.Items))
		}
	}
	for _, pod := range pods.Items {
		placement[pod.Spec.NodeName]++
		assertPoolPodNoRestarts(t, pod)
		for _, container := range pod.Spec.Containers {
			if strings.Contains(container.Image, "busybox") {
				if container.Resources.Requests.Cpu().MilliValue() != 10 ||
					container.Resources.Requests.Memory().Value() != expectedMemory {
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
				"passed": !t.Failed(), "linkedTopology": linked && !standalone,
				"standaloneNodesAndLinks": standalone,
				"batchSize":               batchSize,
				"allPlannedSeconds":       allPlanned.Seconds(), "allReadySeconds": allReady.Seconds(),
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
		if !standalone {
			waitForPoolRingTopology(t, namespace, count)
		}
		links, crossWorkerLinks = poolRingConnectivity(t, namespace, pods.Items)
	}
	for _, pod := range poolPods(t, namespace, "c9s.run/direct-workload").Items {
		assertPoolPodNoRestarts(t, pod)
	}
}

func poolBatchedScaleManifest(count, batchSize int) string {
	var manifest strings.Builder
	fmt.Fprintf(&manifest, `apiVersion: c9s.run/v1alpha1
kind: Topology
metadata:
  name: busybox
spec:
  rollout:
    batchSize: %d
  expose:
    exposeType: None
    disableAutoExpose: true
  deployment:
    persistence:
      enabled: false
    resources:
      default:
        requests:
          cpu: 10m
          memory: 64Mi
  definition:
    containerlab: |
      name: busybox
      mgmt:
        ipv4-subnet: 172.30.0.0/22
        ipv4-gw: 172.30.0.1
      topology:
        defaults:
          kind: linux
          image: docker.io/library/busybox:1.37.0-musl
          entrypoint: sleep
          cmd: "2147483647"
        nodes:
`, batchSize)
	for index := range count {
		address := index + 2
		fmt.Fprintf(
			&manifest,
			"          bb-%03d:\n            mgmt-ipv4: 172.30.%d.%d\n",
			index,
			address/256,
			address%256,
		)
	}

	return manifest.String()
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
