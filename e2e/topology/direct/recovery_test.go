package direct_test

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
)

const (
	recoveryHostInterface = "rc9se2e0"
	recoveryPollInterval  = 5 * time.Second
)

// TestMultiWorkerRecoveryDirect is the task-scoped multi-worker recovery suite: every Link
// flavor is realized in one namespace, cross-worker traffic is proven, and the lab must then
// survive a partial update, forced Pod deletion, rescheduling onto another worker, a manager
// restart. Cleanup must leave no owned worker state behind: connectivity is Pod-scoped.
func TestMultiWorkerRecoveryDirect(t *testing.T) {
	workers := schedulableWorkers(t)
	if len(workers) < 2 {
		t.Skipf("multi-worker recovery needs at least 2 schedulable workers, have %d", len(workers))
	}

	workerA, workerB := workers[0], workers[1]

	testName := "direct-recovery"
	namespace := clabernetestesthelper.NewTestNamespace(testName)
	clabernetestesthelper.KubectlCreateNamespace(t, namespace)

	defer func() {
		if !*clabernetestesthelper.SkipCleanup {
			t.Logf("deleting namespace %q used in test %q", namespace, testName)
			clabernetestesthelper.KubectlDeleteNamespace(t, namespace)

			waitForHostInterfaceRemoval(t, workers)
		}
	}()

	applyManifest(t, namespace, recoveryManifest(workerA, workerB))

	nodeNames := []string{"lin1", "lin2", "lin3", "lin4"}
	for _, nodeName := range nodeNames {
		waitForDirectNodeReady(t, namespace, nodeName)
	}

	lin1 := observeDevicePod(t, namespace, "lin1")
	lin2 := observeDevicePod(t, namespace, "lin2")
	lin3 := observeDevicePod(t, namespace, "lin3")

	// Cross-worker traffic across both accepted connectivity flavors: the controller realizes
	// each of them inside the Pod on the preserved underlay; the device only ever sees plain legs.
	waitForDeviceCommand(t, namespace, lin1,
		[]string{"ping", "-c", "2", "-W", "2", "10.10.0.1"}, " 0% packet loss")
	waitForDeviceCommand(t, namespace, lin1,
		[]string{"ping", "-c", "2", "-W", "2", "10.10.1.1"}, " 0% packet loss")

	// Loopback and same-Pod (grouped) endpoints share one network namespace, so the kernel
	// answers their addresses locally; interface realization is the observable contract.
	for _, iface := range []string{"eth3", "eth4"} {
		waitForDeviceCommand(t, namespace, lin1,
			[]string{"ip", "-br", "link", "show", "dev", iface}, iface)
	}

	for _, iface := range []string{"eth1", "eth2"} {
		waitForDeviceCommand(t, namespace, lin3,
			[]string{"ip", "-br", "link", "show", "dev", iface}, iface)
	}

	// The host Link's Pod-side leg lands in lin2's namespace; the host-side leg carries the
	// declared interface name in the hosting worker's namespace.
	waitForDeviceCommand(t, namespace, lin2,
		[]string{"ip", "-br", "link", "show", "dev", "eth3"}, "eth3")
	requireHostInterfaceOnSomeWorker(t, workers)

	t.Log("partial update: rolling only lin2")

	patchNode(t, namespace, "lin2",
		`{"spec":{"env":{"RECOVERY_STEP":"partial-update"}}}`)
	waitForDevicePodReplacement(t, namespace, "lin2", lin2.podName)
	waitForDirectNodeReady(t, namespace, "lin2")

	unchanged := observeDevicePod(t, namespace, "lin1")
	if unchanged.podName != lin1.podName {
		t.Fatalf(
			"partial update of lin2 rolled lin1: %q -> %q",
			lin1.podName,
			unchanged.podName,
		)
	}

	waitForDeviceCommand(t, namespace, lin1,
		[]string{"ping", "-c", "2", "-W", "2", "10.10.0.1"}, " 0% packet loss")

	t.Log("forced Pod deletion: lin1")

	forceDeletePod(t, namespace, lin1.podName)
	waitForDevicePodReplacement(t, namespace, "lin1", lin1.podName)
	waitForDirectNodeReady(t, namespace, "lin1")
	lin1 = observeDevicePod(t, namespace, "lin1")
	waitForDeviceCommand(t, namespace, lin1,
		[]string{"ping", "-c", "2", "-W", "2", "10.10.0.1"}, " 0% packet loss")

	t.Logf("rescheduling: moving lin1 from %q to %q", workerA, workerB)

	patchLauncherProfile(t, namespace, "recovery-worker-a",
		fmt.Sprintf(
			`{"spec":{"scheduling":{"nodeSelector":{"kubernetes.io/hostname":%q}}}}`,
			workerB,
		))
	waitForDevicePodReplacement(t, namespace, "lin1", lin1.podName)
	waitForDirectNodeReady(t, namespace, "lin1")
	lin1 = observeDevicePod(t, namespace, "lin1")

	if node := devicePodWorker(t, namespace, lin1.podName); node != workerB {
		t.Fatalf("rescheduled lin1 Pod runs on %q, want %q", node, workerB)
	}

	waitForDeviceCommand(t, namespace, lin1,
		[]string{"ping", "-c", "2", "-W", "2", "10.10.0.1"}, " 0% packet loss")

	t.Log("controller restart")

	restartManager(t)
	lin2 = observeDevicePod(t, namespace, "lin2")
	forceDeletePod(t, namespace, lin2.podName)
	waitForDevicePodReplacement(t, namespace, "lin2", lin2.podName)
	waitForDirectNodeReady(t, namespace, "lin2")
	waitForDeviceCommand(t, namespace, lin1,
		[]string{"ping", "-c", "2", "-W", "2", "10.10.0.1"}, " 0% packet loss")
}

// recoveryManifest renders the recovery lab: two standalone linux Nodes pinned to distinct
// workers, one grouped pair, and one Link of every flavor (vxlan, slurpeeth, loopback,
// same-Pod, host).
func recoveryManifest(workerA, workerB string) string {
	return fmt.Sprintf(`---
apiVersion: c9s.run/v1alpha1
kind: LauncherProfile
metadata:
  name: recovery-worker-a
spec:
  expose:
    disableAutoExpose: true
  scheduling:
    nodeSelector:
      kubernetes.io/hostname: %s
---
apiVersion: c9s.run/v1alpha1
kind: LauncherProfile
metadata:
  name: recovery-worker-b
spec:
  expose:
    disableAutoExpose: true
  scheduling:
    nodeSelector:
      kubernetes.io/hostname: %s
---
apiVersion: c9s.run/v1alpha1
kind: LauncherProfile
metadata:
  name: recovery-shared
spec:
  expose:
    disableAutoExpose: true
---
apiVersion: c9s.run/v1alpha1
kind: Node
metadata:
  name: lin1
spec:
  kind: linux
  image: ghcr.io/srl-labs/network-multitool
  launcherProfileRef:
    name: recovery-worker-a
  exec:
    - ip address add 10.10.0.0/31 dev eth1
    - ip address add 10.10.1.0/31 dev eth2
    - ip address add 10.10.2.0/31 dev eth3
    - ip address add 10.10.2.1/31 dev eth4
---
apiVersion: c9s.run/v1alpha1
kind: Node
metadata:
  name: lin2
spec:
  kind: linux
  image: ghcr.io/srl-labs/network-multitool
  launcherProfileRef:
    name: recovery-worker-b
  exec:
    - ip address add 10.10.0.1/31 dev eth1
    - ip address add 10.10.1.1/31 dev eth2
---
apiVersion: c9s.run/v1alpha1
kind: Node
metadata:
  name: lin3
spec:
  kind: linux
  image: ghcr.io/srl-labs/network-multitool
  launcherProfileRef:
    name: recovery-shared
  exec:
    - ip address add 10.10.3.0/31 dev eth1
---
apiVersion: c9s.run/v1alpha1
kind: Node
metadata:
  name: lin4
spec:
  kind: linux
  image: ghcr.io/srl-labs/network-multitool
  network-mode: container:lin3
  launcherProfileRef:
    name: recovery-shared
  # The grouped secondary shares lin3's network namespace, so it must not race the primary's
  # image services for their listen ports.
  entrypoint: sleep
  cmd: infinity
  exec:
    - ip address add 10.10.3.1/31 dev eth2
---
apiVersion: c9s.run/v1alpha1
kind: Link
metadata:
  name: recovery-vxlan
spec:
  endpointA:
    nodeName: lin1
    interfaceName: eth1
  endpointB:
    nodeName: lin2
    interfaceName: eth1
  connectivity: vxlan
---
apiVersion: c9s.run/v1alpha1
kind: Link
metadata:
  name: recovery-slurpeeth
spec:
  endpointA:
    nodeName: lin1
    interfaceName: eth2
  endpointB:
    nodeName: lin2
    interfaceName: eth2
  connectivity: slurpeeth
---
apiVersion: c9s.run/v1alpha1
kind: Link
metadata:
  name: recovery-loopback
spec:
  endpointA:
    nodeName: lin1
    interfaceName: eth3
  endpointB:
    nodeName: lin1
    interfaceName: eth4
---
apiVersion: c9s.run/v1alpha1
kind: Link
metadata:
  name: recovery-same-pod
spec:
  endpointA:
    nodeName: lin3
    interfaceName: eth1
  endpointB:
    nodeName: lin4
    interfaceName: eth2
---
apiVersion: c9s.run/v1alpha1
kind: Link
metadata:
  name: recovery-host
spec:
  endpointA:
    nodeName: lin2
    interfaceName: eth3
  endpointB:
    nodeName: host
    interfaceName: %s
`, workerA, workerB, recoveryHostInterface)
}

func applyManifest(t *testing.T, namespace, manifest string) {
	t.Helper()

	cmd := exec.CommandContext( //nolint:gosec
		t.Context(),
		"kubectl",
		"apply",
		"--namespace",
		namespace,
		"-f",
		"-",
	)
	cmd.Stdin = strings.NewReader(manifest)
	clabernetestesthelper.Execute(t, cmd)
}

func patchNode(t *testing.T, namespace, nodeName, patch string) {
	t.Helper()

	cmd := exec.CommandContext( //nolint:gosec
		t.Context(),
		"kubectl", "patch", "node.c9s.run", nodeName,
		"--namespace", namespace, "--type", "merge", "-p", patch,
	)
	clabernetestesthelper.Execute(t, cmd)
}

func patchLauncherProfile(t *testing.T, namespace, profileName, patch string) {
	t.Helper()

	cmd := exec.CommandContext( //nolint:gosec
		t.Context(),
		"kubectl", "patch", "launcherprofile.c9s.run", profileName,
		"--namespace", namespace, "--type", "merge", "-p", patch,
	)
	clabernetestesthelper.Execute(t, cmd)
}

func forceDeletePod(t *testing.T, namespace, podName string) {
	t.Helper()

	cmd := exec.CommandContext( //nolint:gosec
		t.Context(),
		"kubectl", "delete", "pod", podName,
		"--namespace", namespace, "--grace-period=0", "--force", "--ignore-not-found",
	)
	clabernetestesthelper.Execute(t, cmd)
}

// waitForDevicePodReplacement waits until the Node's single device Pod exists under a new name
// and is fully ready.
func waitForDevicePodReplacement(t *testing.T, namespace, nodeName, previousPodName string) {
	t.Helper()

	deadline := time.Now().Add(directNodeReadyTimeout)
	for time.Now().Before(deadline) {
		pods := listPods(t, namespace, "c9s.run/direct-workload="+nodeName)
		if len(pods.Items) == 1 && pods.Items[0].Metadata.Name != previousPodName {
			ready := true

			for _, status := range pods.Items[0].Status.ContainerStatuses {
				if !status.Ready {
					ready = false

					break
				}
			}

			if ready && len(pods.Items[0].Status.ContainerStatuses) > 0 {
				return
			}
		}

		time.Sleep(recoveryPollInterval)
	}

	t.Fatalf("device Pod for %q was never replaced and ready", nodeName)
}

func devicePodWorker(t *testing.T, namespace, podName string) string {
	t.Helper()

	cmd := exec.CommandContext( //nolint:gosec
		t.Context(),
		"kubectl", "get", "pod", podName,
		"--namespace", namespace, "-o", "jsonpath={.spec.nodeName}",
	)

	return strings.TrimSpace(string(clabernetestesthelper.Execute(t, cmd)))
}

// schedulableWorkers returns the names of schedulable non-control-plane cluster nodes.
func schedulableWorkers(t *testing.T) []string {
	t.Helper()

	cmd := exec.CommandContext(
		t.Context(),
		"kubectl", "get", "nodes",
		"--selector", "!node-role.kubernetes.io/control-plane",
		"-o", "jsonpath={range .items[*]}{.metadata.name}{\"\\n\"}{end}",
	)

	workers := []string{}

	for worker := range strings.SplitSeq(
		string(clabernetestesthelper.Execute(t, cmd)),
		"\n",
	) {
		if strings.TrimSpace(worker) != "" {
			workers = append(workers, strings.TrimSpace(worker))
		}
	}

	return workers
}

// restartManager deletes the manager Pod wherever the release lives and waits for the
// replacement to become available.
func restartManager(t *testing.T) {
	t.Helper()

	cmd := exec.CommandContext(
		t.Context(),
		"kubectl", "delete", "pod",
		"--all-namespaces", "--selector", "c9s.run/component=manager", "--wait=false",
	)
	clabernetestesthelper.Execute(t, cmd)

	waitForComponentReady(t, "c9s.run/component=manager")
}

func waitForComponentReady(t *testing.T, selector string) {
	t.Helper()

	deadline := time.Now().Add(directNodeReadyTimeout)
	for time.Now().Before(deadline) {
		cmd := exec.CommandContext( //nolint:gosec
			t.Context(),
			"kubectl", "get", "pods",
			"--all-namespaces", "--selector", selector,
			"-o",
			`jsonpath={range .items[*]}{.status.phase}/{.status.containerStatuses[0].ready} {end}`,
		)

		output, err := cmd.CombinedOutput()
		if err == nil {
			states := strings.Fields(string(output))
			allReady := len(states) > 0

			for _, state := range states {
				if state != "Running/true" {
					allReady = false

					break
				}
			}

			if allReady {
				return
			}
		}

		time.Sleep(recoveryPollInterval)
	}

	t.Fatalf("component %q never became ready again", selector)
}

// requireHostInterfaceOnSomeWorker asserts the host Link's host-side interface exists in
// exactly one worker's host network namespace. Worker namespaces are reached through the
// KinD node containers; on other environments the in-device leg is the observable proof.
func requireHostInterfaceOnSomeWorker(t *testing.T, workers []string) {
	t.Helper()

	if !dockerReachesWorkers(t, workers) {
		t.Logf("worker host namespaces are not reachable via docker; skipping host-side check")

		return
	}

	deadline := time.Now().Add(directNodeReadyTimeout)
	for time.Now().Before(deadline) {
		if len(workersWithHostInterface(t, workers)) == 1 {
			return
		}

		time.Sleep(recoveryPollInterval)
	}

	t.Fatalf(
		"host interface %q not on exactly one worker: %v",
		recoveryHostInterface,
		workersWithHostInterface(t, workers),
	)
}

// waitForHostInterfaceRemoval asserts the host Link's host-side interface is swept once the
// task namespace is gone.
func waitForHostInterfaceRemoval(t *testing.T, workers []string) {
	t.Helper()

	if !dockerReachesWorkers(t, workers) {
		return
	}

	deadline := time.Now().Add(directNodeReadyTimeout)
	for time.Now().Before(deadline) {
		if len(workersWithHostInterface(t, workers)) == 0 {
			return
		}

		time.Sleep(recoveryPollInterval)
	}

	t.Errorf(
		"host interface %q still present after cleanup on workers %v",
		recoveryHostInterface,
		workersWithHostInterface(t, workers),
	)
}

func dockerReachesWorkers(t *testing.T, workers []string) bool {
	t.Helper()

	cmd := exec.CommandContext( //nolint:gosec
		t.Context(),
		"docker", "inspect", "--format", "{{.State.Running}}", workers[0],
	)

	output, err := cmd.CombinedOutput()

	return err == nil && strings.TrimSpace(string(output)) == "true"
}

func workersWithHostInterface(t *testing.T, workers []string) []string {
	t.Helper()

	present := []string{}

	for _, worker := range workers {
		cmd := exec.CommandContext( //nolint:gosec
			t.Context(),
			"docker", "exec", worker, "ip", "-br", "link", "show", "dev", recoveryHostInterface,
		)
		if err := cmd.Run(); err == nil {
			present = append(present, worker)
		}
	}

	return present
}
