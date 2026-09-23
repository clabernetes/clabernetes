package direct_test

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
	k8scorev1 "k8s.io/api/core/v1"
)

func TestPlannerPoolDelayedPeerStartup(t *testing.T) {
	if os.Getenv("PLANNER_POOL_E2E") == "" {
		t.Skip("PLANNER_POOL_E2E is not set")
	}
	namespace := clabernetestesthelper.NewTestNamespace("planner-pool-delayed-peer")
	clabernetestesthelper.KubectlCreateNamespace(t, namespace)
	defer func() {
		if t.Failed() {
			clabernetestesthelper.DumpNamespaceDiagnostics(t, namespace)
		}
		if !*clabernetestesthelper.SkipCleanup {
			clabernetestesthelper.KubectlDeleteNamespace(t, namespace)
		}
	}()
	var manifest strings.Builder
	for _, name := range []string{"early", "late"} {
		label := ""
		if name == "late" {
			label = "  labels:\n    c9s.run/ignoreReconcile: 'true'\n"
		}
		fmt.Fprintf(&manifest, `---
apiVersion: c9s.run/v1alpha1
kind: Node
metadata:
  name: %s
%sspec:
  kind: linux
  image: docker.io/library/busybox:1.37.0-musl
  entrypoint: sleep
  cmd: "2147483647"
`, name, label)
	}
	manifest.WriteString(`---
apiVersion: c9s.run/v1alpha1
kind: Link
metadata:
  name: delayed
spec:
  endpointA:
    nodeName: early
    interfaceName: eth1
  endpointB:
    nodeName: late
    interfaceName: eth1
`)
	poolApply(t, namespace, manifest.String())
	waitForPoolLocalStartup(t, namespace)
	if pods := poolPods(t, namespace, "c9s.run/name=late"); len(pods.Items) != 0 {
		t.Fatal("delayed peer unexpectedly deployed")
	}
	poolKubectl(t, "label", "node.c9s.run/late", "-n", namespace, "c9s.run/ignoreReconcile-")
	waitForDirectNodeReady(t, namespace, "early")
	waitForDirectNodeReady(t, namespace, "late")
	for _, pod := range poolPods(t, namespace, "c9s.run/direct-workload").Items {
		assertPoolPodNoRestarts(t, pod)
	}
	t.Log(
		"application started with its local endpoint while the peer was absent; both nodes converged without restarting",
	)
}

func waitForPoolLocalStartup(t *testing.T, namespace string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		pods := poolPods(t, namespace, "c9s.run/name=early")
		if len(pods.Items) == 1 && localHelperStarted(pods.Items[0]) {
			pod := pods.Items[0]
			assertPoolPodNoRestarts(t, pod)
			for _, status := range pod.Status.InitContainerStatuses {
				if status.Name == "clabwire" && status.Ready {
					t.Fatal("helper ready before peer exists")
				}
			}
			appRunning := false
			for _, status := range pod.Status.ContainerStatuses {
				if status.State.Running != nil {
					appRunning = true
				}
			}
			if appRunning {
				// Application startup must follow creation of its data endpoint, even with no peer Pod.
				poolKubectl(
					t,
					"exec",
					"-n",
					namespace,
					pod.Name,
					"-c",
					pod.Spec.Containers[0].Name,
					"--",
					"ip",
					"link",
					"show",
					"eth1",
				)

				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("application remained blocked on absent remote peer")
		}
		time.Sleep(time.Second)
	}
}

func localHelperStarted(pod k8scorev1.Pod) bool {
	for _, status := range pod.Status.InitContainerStatuses {
		if status.Name == "clabwire" && status.Started != nil && *status.Started {
			return true
		}
	}

	return false
}
