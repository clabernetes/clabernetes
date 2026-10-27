package direct_test

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
)

const runStateTopologyName = "runstate"

func runStateTopology(peerMarker string) string {
	return fmt.Sprintf(`apiVersion: c9s.run/v1alpha1
kind: Topology
metadata:
  name: %s
spec:
  expose:
    exposeType: None
  deployment:
    persistence:
      enabled: false
  definition:
    containerlab: |
      name: %s
      topology:
        defaults:
          kind: linux
          image: ghcr.io/srl-labs/network-multitool
        nodes:
          lin1:
            exec:
              - ip address add 192.168.1.0/31 dev eth1
          lin2:
            env:
              RUNSTATE_MARKER: %s
            exec:
              - ip address add 192.168.1.1/31 dev eth1
        links:
          - endpoints: [lin1:eth1, lin2:eth1]
`, runStateTopologyName, runStateTopologyName, peerMarker)
}

// TestDirectNodeRunStateAndRestart proves the Node run state and restart contract end to end on
// a compiled Topology: a stopped Node keeps its Node, Deployment, and Services with no Pod, its
// links go carrier-down at the peer, a Topology apply updates the stopped Node without starting
// it, starting it boots the updated definition and restores the link, and a restart token
// replaces the Pod exactly once.
func TestDirectNodeRunStateAndRestart(t *testing.T) {
	t.Parallel()

	testName := "topology-direct-runstate"
	namespace := clabernetestesthelper.NewTestNamespace(testName)
	clabernetestesthelper.KubectlCreateNamespace(t, namespace)

	defer func() {
		if t.Failed() {
			clabernetestesthelper.DumpNamespaceDiagnostics(t, namespace)
		}

		if !*clabernetestesthelper.SkipCleanup {
			t.Logf("deleting namespace %q used in test %q", namespace, testName)
			clabernetestesthelper.KubectlDeleteNamespace(t, namespace)
		}
	}()

	applyManifest(t, namespace, runStateTopology("v1"))

	for _, nodeName := range []string{"lin1", "lin2"} {
		waitForDirectNodeReady(t, namespace, nodeName)
	}

	lin1 := observeDevicePod(t, namespace, "lin1")
	waitForDeviceCommand(
		t,
		namespace,
		lin1,
		[]string{"ping", "-c", "2", "-W", "2", "192.168.1.1"},
		" 0% packet loss",
	)

	// Pods carry the containerlab node name next to the topology node label.
	lin2 := observeDevicePod(t, namespace, "lin2")
	for label, want := range map[string]string{
		"c9s.run/originalNodeName": "lin2",
		"c9s.run/topologyNode":     "lin2",
	} {
		path := "{.metadata.labels['" + strings.ReplaceAll(label, ".", `\.`) + "']}"
		if got := kubectlJSONPath(t, namespace, "pod", lin2.podName, path); got != want {
			t.Fatalf("device Pod label %s = %q, want %q", label, got, want)
		}
	}

	// Stop: the Pod goes away, everything else stays, and the peer loses carrier.
	patchNode(t, namespace, "lin2", `{"spec":{"runState":"Stopped"}}`)
	waitForNodeCondition(t, namespace, "lin2", "Stopped", "True")
	assertNoDevicePods(t, namespace, "lin2")

	if replicas := kubectlJSONPath(
		t, namespace, "deployment", "lin2", "{.spec.replicas}",
	); replicas != "0" {
		t.Fatalf("stopped Node Deployment replicas = %q, want 0", replicas)
	}

	for _, service := range []string{"lin2-wire"} {
		kubectlJSONPath(t, namespace, "service", service, "{.metadata.name}")
	}

	waitForDeviceCommand(t, namespace, lin1, []string{"ip", "link", "show", "eth1"}, "NO-CARRIER")

	// Apply a changed definition while lin2 is stopped: the Node is updated, re-planned, and
	// stays stopped.
	stoppedDigest := nodePlanDigest(t, namespace, "lin2")

	applyManifest(t, namespace, runStateTopology("v2"))
	waitForPlanDigestChange(t, namespace, "lin2", stoppedDigest)

	if runState := kubectlJSONPath(
		t, namespace, "node.c9s.run", "lin2", "{.spec.runState}",
	); runState != "Stopped" {
		t.Fatalf("Topology apply changed the run state to %q", runState)
	}

	waitForNodeCondition(t, namespace, "lin2", "Stopped", "True")
	assertNoDevicePods(t, namespace, "lin2")

	if marker := kubectlJSONPath(
		t, namespace, "node.c9s.run", "lin2", "{.spec.env.RUNSTATE_MARKER}",
	); marker != "v2" {
		t.Fatalf("stopped Node was not updated by the apply: env marker %q", marker)
	}

	// Start: the updated definition boots and the link comes back.
	patchNode(t, namespace, "lin2", `{"spec":{"runState":"Running"}}`)
	waitForDirectNodeReady(t, namespace, "lin2")
	waitForNodeCondition(t, namespace, "lin2", "Stopped", "False")

	lin2 = observeDevicePod(t, namespace, "lin2")
	waitForDeviceCommand(t, namespace, lin2, []string{"printenv", "RUNSTATE_MARKER"}, "v2")
	waitForDeviceCommand(t, namespace, lin1, []string{"ip", "link", "show", "eth1"}, "state UP")
	waitForDeviceCommand(
		t,
		namespace,
		lin1,
		[]string{"ping", "-c", "2", "-W", "2", "192.168.1.1"},
		" 0% packet loss",
	)

	// Restart: one Pod replacement per new token, reported through observedRestartToken.
	token := fmt.Sprintf("e2e-%d", time.Now().UnixNano())
	clabernetestesthelper.Execute(t, exec.CommandContext( //nolint:gosec
		t.Context(),
		"kubectl", "annotate", "--namespace", namespace, "node.c9s.run", "lin2",
		"c9s.run/restart-requested="+token, "--overwrite",
	))

	waitForDevicePodReplacement(t, namespace, "lin2", lin2.podName)
	clabernetestesthelper.KubectlWaitForOutput(
		t,
		directNodeReadyTimeout,
		[]string{
			"wait", "node.c9s.run/lin2", "--namespace", namespace,
			"--for=jsonpath={.status.observedRestartToken}=" + token, "--timeout=25s",
		},
		"",
	)
	waitForDirectNodeReady(t, namespace, "lin2")

	restarted := observeDevicePod(t, namespace, "lin2")

	// Removing a handled token does not restart the Node again.
	clabernetestesthelper.Execute(t, exec.CommandContext( //nolint:gosec
		t.Context(),
		"kubectl", "annotate", "--namespace", namespace, "node.c9s.run", "lin2",
		"c9s.run/restart-requested-",
	))
	time.Sleep(runStateSettleWindow)

	if current := observeDevicePod(t, namespace, "lin2"); current.podName != restarted.podName {
		t.Fatalf("removing a handled restart token replaced Pod %q", restarted.podName)
	}

	waitForDeviceCommand(
		t,
		namespace,
		lin1,
		[]string{"ping", "-c", "2", "-W", "2", "192.168.1.1"},
		" 0% packet loss",
	)
}

const runStateSettleWindow = 20 * time.Second

func waitForNodeCondition(t *testing.T, namespace, nodeName, conditionType, status string) {
	t.Helper()

	clabernetestesthelper.KubectlWaitForOutput(
		t,
		directNodeReadyTimeout,
		[]string{
			"wait", "node.c9s.run/" + nodeName, "--namespace", namespace,
			"--for=condition=" + conditionType + "=" + status, "--timeout=25s",
		},
		"",
	)
}

func assertNoDevicePods(t *testing.T, namespace, nodeName string) {
	t.Helper()

	if pods := listPods(t, namespace, "c9s.run/direct-workload="+nodeName); len(pods.Items) != 0 {
		t.Fatalf("stopped Node %q still has %d device Pods", nodeName, len(pods.Items))
	}
}
