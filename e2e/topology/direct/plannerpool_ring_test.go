package direct_test

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	k8scorev1 "k8s.io/api/core/v1"
)

func poolRingManifest(t *testing.T) string {
	t.Helper()
	definition, err := os.ReadFile("test-fixtures/planner-pool-ring.clab.yaml")
	if err != nil {
		t.Fatal(err)
	}

	return `apiVersion: c9s.run/v1alpha1
kind: Topology
metadata:
  name: planner-pool-ring
spec:
  expose:
    exposeType: ClusterIP
    disableAutoExpose: true
  deployment:
    resources:
      default:
        requests:
          cpu: 10m
          memory: 16Mi
  definition:
    containerlab: |
      ` + strings.ReplaceAll(strings.TrimSpace(string(definition)), "\n", "\n      ") + "\n"
}

func waitForPoolRingTopology(t *testing.T, namespace string, count int) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for {
		var topology clabernetesapisv1alpha1.Topology
		raw := poolKubectl(t, "get", "topology", "planner-pool-ring", "-n", namespace, "-o", "json")
		if err := json.Unmarshal(raw, &topology); err != nil {
			t.Fatal(err)
		}
		if topology.Status.TopologyReady &&
			topology.Status.ObservedGeneration == topology.Generation &&
			topology.Status.NodeCount == count &&
			topology.Status.ReadyNodeCount == count &&
			topology.Status.LinkCount == count {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ring Topology did not converge: %+v", topology.Status)
		}
		time.Sleep(time.Second)
	}
}

func poolRingConnectivity(t *testing.T, namespace string, pods []k8scorev1.Pod) (int, int) {
	t.Helper()
	const count = 200
	var links clabernetesapisv1alpha1.LinkList
	if err := json.Unmarshal(poolKubectl(t, "get", "links", "-n", namespace, "-o", "json"), &links); err != nil {
		t.Fatal(err)
	}
	if len(links.Items) != count {
		t.Fatalf("got %d Links, want %d", len(links.Items), count)
	}
	placement := map[string]string{}
	for _, pod := range pods {
		placement[pod.Labels["c9s.run/name"]] = pod.Spec.NodeName
	}
	crossWorker := 0
	for _, link := range links.Items {
		accepted := false
		for _, condition := range link.Status.Conditions {
			if condition.Type == clabernetesapisv1alpha1.LinkConditionAccepted &&
				condition.Status == "True" && condition.ObservedGeneration == link.Generation {
				accepted = true
			}
		}
		if !accepted || link.Status.WireID == 0 {
			t.Fatalf("link %s has no accepted wire: %+v", link.Name, link.Status)
		}
		if placement[link.Spec.EndpointA.NodeName] != placement[link.Spec.EndpointB.NodeName] {
			crossWorker++
		}
	}
	if crossWorker == 0 {
		t.Fatal("ring must exercise links across Kubernetes workers")
	}
	// Every node probes both adjacent links, using the data interface explicitly so the
	// management mesh cannot accidentally satisfy the check. All 200 links run both ways.
	for _, pod := range pods {
		poolRingProbe(t, namespace, pod, count)
	}
	t.Logf("verified %d links bidirectionally, including %d cross-worker links", count, crossWorker)

	return count, crossWorker
}

func poolRingProbe(t *testing.T, namespace string, pod k8scorev1.Pod, count int) {
	t.Helper()
	name := pod.Labels["c9s.run/name"]
	index, err := strconv.Atoi(strings.TrimPrefix(name, "bb-"))
	if err != nil {
		t.Fatal(err)
	}
	container := ""
	for _, candidate := range pod.Spec.Containers {
		if strings.Contains(candidate.Image, "busybox") {
			container = candidate.Name
		}
	}
	if container == "" {
		t.Fatalf("no BusyBox container in %s", pod.Name)
	}
	script := fmt.Sprintf(
		"ping -q -c 2 -i 0.1 -W 2 -I eth1 10.200.%d.1 && ping -q -c 2 -i 0.1 -W 2 -I eth2 10.200.%d.0",
		index,
		(index+count-1)%count,
	)
	output := poolKubectl(
		t,
		"exec",
		"-n",
		namespace,
		pod.Name,
		"-c",
		container,
		"--",
		"sh",
		"-c",
		script,
	)
	if strings.Count(string(output), " 0% packet loss") != 2 {
		t.Fatalf("data-link packet loss at %s: %s", name, output)
	}
	t.Logf("verified both data interfaces on %s", name)
}
