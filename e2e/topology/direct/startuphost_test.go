package direct_test

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesinternaldirectpod "github.com/clabernetes/clabernetes/internal/directpod"
	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
	k8scorev1 "k8s.io/api/core/v1"
)

// TestStartupPerHostAdmission exercises a deliberately slow boot and links to later peers.
//
//nolint:gocognit,gocyclo // Follow one sequential queue, boot, and policy-edit scenario.
func TestStartupPerHostAdmission(t *testing.T) {
	if os.Getenv("STARTUP_HOST_E2E") == "" {
		t.Skip("STARTUP_HOST_E2E is not set")
	}
	host := os.Getenv("STARTUP_HOST_E2E_NODE")
	if host == "" {
		t.Fatal("STARTUP_HOST_E2E_NODE must select a test host")
	}
	namespace := clabernetestesthelper.NewTestNamespace("startup-host")
	clabernetestesthelper.KubectlCreateNamespace(t, namespace)
	defer func() {
		if t.Failed() {
			clabernetestesthelper.DumpNamespaceDiagnostics(t, namespace)
		}
		if !*clabernetestesthelper.SkipCleanup {
			clabernetestesthelper.KubectlDeleteNamespace(t, namespace)
		}
	}()
	manifest := fmt.Sprintf(`apiVersion: c9s.run/v1alpha1
kind: Topology
metadata:
  name: admission
spec:
  rollout:
    batchSize: 100
    maxConcurrentPerHost: 1
  deployment:
    scheduling:
      nodeSelector:
        kubernetes.io/hostname: %s
    persistence:
      enabled: false
    resources:
      default:
        requests:
          cpu: 20m
          memory: 64Mi
  expose:
    exposeType: None
  statusProbes:
    enabled: true
    probeConfiguration:
      startupSeconds: 300
      tcpProbeConfiguration:
        port: 8080
  definition:
    containerlab: |
      name: admission
      topology:
        defaults:
          kind: linux
          image: docker.io/library/busybox:1.37.0-musl
          entrypoint: sh
          cmd: "-c 'sleep 25; httpd -f -p 8080'"
        nodes:
          node-a: {}
          node-b: {}
          node-c: {}
          node-d: {}
        links:
          - endpoints: [node-a:eth1, node-b:eth1]
          - endpoints: [node-b:eth2, node-c:eth1]
          - endpoints: [node-c:eth2, node-d:eth1]
          - endpoints: [node-d:eth2, node-a:eth2]
`, host)
	poolApply(t, namespace, manifest)
	deadline := time.Now().Add(8 * time.Minute)
	sawQueued := false
	sawInitialStatus := false
	for {
		pods := poolPods(t, namespace, "c9s.run/direct-workload")
		booting, started, queued := 0, 0, 0
		for _, pod := range pods.Items {
			if pod.Spec.NodeName != "" && pod.Spec.NodeName != host {
				t.Fatalf("unexpected host %s", pod.Spec.NodeName)
			}
			granted := pod.Annotations[clabernetesinternaldirectpod.StartupAdmittedAnnotation] == string(
				pod.UID,
			)
			primaryStarted := false
			for _, status := range pod.Status.ContainerStatuses {
				if status.Name == pod.Annotations[clabernetesinternaldirectpod.KubectlDefaultContainerAnnotation] {
					primaryStarted = status.Started != nil && *status.Started
				}
			}
			switch {
			case primaryStarted:
				started++
			case granted:
				booting++
			default:
				queued++
			}
			for _, condition := range pod.Status.Conditions {
				if condition.Type == k8scorev1.PodReady &&
					condition.Status == k8scorev1.ConditionTrue &&
					!granted {
					t.Fatal("Pod ran without host admission")
				}
			}
		}
		if len(pods.Items) == 4 && started == 0 && !sawInitialStatus {
			var topology clabernetesapisv1alpha1.Topology
			raw := poolKubectl(t, "get", "topology", "admission", "-n", namespace, "-o", "json")
			if err := json.Unmarshal(raw, &topology); err != nil {
				t.Fatal(err)
			}
			if topology.Status.NodeCount != 4 || topology.Status.ReadyNodeCount != 0 ||
				topology.Status.TopologyReady ||
				topology.Status.ObservedGeneration != topology.Generation {
				t.Fatalf(
					"initial zero-valued aggregate status was not persisted: %#v",
					topology.Status,
				)
			}
			sawInitialStatus = true
		}
		if booting > 1 {
			t.Fatalf("host cap exceeded: %d booting", booting)
		}
		sawQueued = sawQueued || (queued > 0 && booting == 1)
		t.Logf("started=%d booting=%d queued=%d", started, booting, queued)
		if started == 4 {
			if !sawInitialStatus {
				t.Fatal("test never observed initial aggregate status")
			}
			if !sawQueued {
				t.Fatal("test never observed a waiting device")
			}

			break
		}
		if time.Now().After(deadline) {
			t.Fatal("startup admission timed out")
		}
		time.Sleep(5 * time.Second)
	}
	// Links to Pods admitted later must eventually converge with no device restarts.
	poolKubectl(
		t,
		"wait",
		"topology/admission",
		"-n",
		namespace,
		"--for=jsonpath={.status.topologyReady}=true",
		"--timeout=180s",
	)
	for _, pod := range poolPods(t, namespace, "c9s.run/direct-workload").Items {
		for _, status := range pod.Status.ContainerStatuses {
			if status.RestartCount != 0 {
				t.Fatalf("%s restarted", pod.Name)
			}
		}
	}
	// A policy edit must retain the Pod templates and identities.
	before := poolKubectl(
		t,
		"get",
		"pods",
		"-n",
		namespace,
		"-o",
		"jsonpath={range .items[*]}{.metadata.uid}{\"\\n\"}{end}",
	)
	poolKubectl(
		t,
		"patch",
		"topology",
		"admission",
		"-n",
		namespace,
		"--type=merge",
		"-p",
		`{"spec":{"rollout":{"maxConcurrentPerHost":0}}}`,
	)
	time.Sleep(10 * time.Second)
	after := poolKubectl(
		t,
		"get",
		"pods",
		"-n",
		namespace,
		"-o",
		"jsonpath={range .items[*]}{.metadata.uid}{\"\\n\"}{end}",
	)
	if strings.TrimSpace(string(before)) != strings.TrimSpace(string(after)) {
		t.Fatal("policy edit replaced Pods")
	}
}
