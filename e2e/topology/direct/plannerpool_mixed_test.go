package direct_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
	k8scorev1 "k8s.io/api/core/v1"
)

// TestPlannerPoolMixedVendorLinks exercises a topology file, parallel data links, distributed
// SR-SIM components, and a second SR Linux/cEOS link added only after baseline traffic succeeds.
//
//nolint:gocyclo // Keep the baseline and link-add assertions in one sequential scenario.
func TestPlannerPoolMixedVendorLinks(t *testing.T) {
	if os.Getenv("PLANNER_POOL_MIXED_E2E") == "" {
		t.Skip("PLANNER_POOL_MIXED_E2E is not set")
	}
	license := os.Getenv("SRSIM_LICENSE")
	if strings.TrimSpace(license) == "" {
		t.Fatal("the mixed-vendor test requires SRSIM_LICENSE")
	}
	managerNamespace := os.Getenv("PLANNER_POOL_NAMESPACE")
	if managerNamespace == "" {
		managerNamespace = "c9s-e2e"
	}
	workers := poolPodUIDs(t, managerNamespace)
	if len(workers) == 0 {
		t.Fatal("no ready planner pool workers")
	}
	namespace := clabernetestesthelper.NewTestNamespace("planner-pool-mixed")
	clabernetestesthelper.KubectlCreateNamespace(t, namespace)
	t.Logf("mixed-vendor namespace: %s; planner workers: %v", namespace, workers)
	defer func() {
		if t.Failed() {
			clabernetestesthelper.DumpNamespaceDiagnostics(t, namespace)
		}
		if os.Getenv("PLANNER_POOL_MIXED_KEEP") == "" && !*clabernetestesthelper.SkipCleanup {
			clabernetestesthelper.KubectlDeleteNamespace(t, namespace)
		}
	}()
	clabernetestesthelper.CreateGHCRPullSecret(t, namespace, "pool-registry")
	licensePath := filepath.Join(t.TempDir(), "license.txt")
	if err := os.WriteFile(licensePath, []byte(license), 0o600); err != nil { //nolint:gosec // Path is inside t.TempDir.
		t.Fatal(err)
	}
	poolKubectl(t, "create", "configmap", "srsim-license", "-n", namespace,
		"--from-file=license.txt="+licensePath)

	started := time.Now()
	poolApply(t, namespace, poolMixedManifest(t, false))
	before := waitForPoolMixedReady(t, namespace, 12)
	t.Logf("all six Nodes and twelve Links ready in %s", time.Since(started))
	baselineWires := poolMixedWires(t, namespace, before)
	probes := poolMixedProbes()
	poolMixedCheckTraffic(t, namespace, probes)
	t.Logf("baseline healthy: all twelve data links passed; elapsed %s", time.Since(started))
	srlPlan, ceosPlan := nodePlanDigest(t, namespace, "srl"), nodePlanDigest(t, namespace, "ceos")

	// Change only the file's link inventory. Configure the new data ports after the runtime
	// applies it, so a startup-config change cannot inadvertently force SR Linux to restart.
	added := time.Now()
	poolApply(t, namespace, poolMixedManifest(t, true))
	waitForPlanDigestChange(t, namespace, "srl", srlPlan)
	waitForPlanDigestChange(t, namespace, "ceos", ceosPlan)
	waitForDevicePodReplacement(t, namespace, "ceos", before["ceos"].Name)
	after := waitForPoolMixedReady(t, namespace, 13)
	for name, oldPod := range before {
		changed := oldPod.UID != after[name].UID
		if changed != (name == "ceos") {
			t.Fatalf(
				"unexpected Pod replacement for %s: %s -> %s",
				name,
				oldPod.UID,
				after[name].UID,
			)
		}
	}
	t.Logf("link lifecycle: cEOS recreated, other five Pods retained, in %s", time.Since(added))
	poolMixedConfigureNewPorts(t, namespace)
	probes = append(probes,
		poolMixedProbe{"srl", "10.210.13.1", "10.210.13.2"},
		poolMixedProbe{"ceos", "10.210.13.2", "10.210.13.1"},
	)
	poolMixedCheckTraffic(t, namespace, probes)
	after = waitForPoolMixedReady(t, namespace, 13)
	afterWires := poolMixedWires(t, namespace, after)
	for name, wire := range baselineWires {
		if current := afterWires[name]; current != wire {
			t.Fatalf("existing link %s changed wire ID: %d -> %d", name, wire, current)
		}
	}
	if current := poolPodUIDs(t, managerNamespace); !reflect.DeepEqual(workers, current) {
		t.Fatalf("planner workers changed: %v -> %v", workers, current)
	}
	for _, pod := range poolPods(t, managerNamespace, "c9s.run/planner-pool=clabernetes").Items {
		assertPoolPodNoRestarts(t, pod)
	}
	if pods := poolPods(t, namespace, "app.kubernetes.io/name=clabernetes-planner"); len(
		pods.Items,
	) != 0 {
		t.Fatal("unexpected disposable planner Pods")
	}
	t.Logf(
		"all thirteen links passed, new link in both directions; unchanged pool; total %s",
		time.Since(started),
	)
}

func poolMixedManifest(t *testing.T, added bool) string {
	t.Helper()
	raw, err := os.ReadFile("test-fixtures/planner-pool-mixed.clab.yaml")
	if err != nil {
		t.Fatal(err)
	}
	definition := string(raw)
	for _, image := range []struct{ variable, fallback string }{
		{"SRL_IMAGE", "ghcr.io/clab-labs/srlinux:25.10.1"},
		{"CEOS_IMAGE", "ghcr.io/clab-labs/ceos:4.33.1F"},
		{"SRSIM_IMAGE", "ghcr.io/clab-labs/nokia_srsim:26.7.R1"},
	} {
		if override := os.Getenv(image.variable); override != "" {
			definition = strings.ReplaceAll(definition, image.fallback, override)
		}
	}
	if added {
		definition += "    - endpoints: [\"srl:e1-5\", \"ceos:eth5\"]\n"
	}

	return `apiVersion: c9s.run/v1alpha1
kind: Topology
metadata:
  name: planner-pool-mixed
spec:
  statusProbes:
    enabled: true
  expose:
    exposeType: ClusterIP
    disableAutoExpose: true
  imagePull:
    pullSecrets: [pool-registry]
  deployment:
    scheduling:
      affinity:
        podAntiAffinity:
          preferredDuringSchedulingIgnoredDuringExecution:
            - weight: 100
              podAffinityTerm:
                topologyKey: kubernetes.io/hostname
                labelSelector:
                  matchExpressions:
                    - key: c9s.run/direct-workload
                      operator: Exists
    filesFromConfigMap:
      sros:
        - filePath: /opt/nokia/sros/license.txt
          configMapName: srsim-license
          configMapPath: license.txt
  definition:
    containerlab: |
      ` + strings.ReplaceAll(strings.TrimSpace(definition), "\n", "\n      ") + "\n"
}

func waitForPoolMixedReady(t *testing.T, namespace string, linkCount int) map[string]k8scorev1.Pod {
	t.Helper()
	for _, name := range []string{"srl", "ceos", "sros", "mt-srl", "mt-ceos", "mt-sros"} {
		waitForDirectNodeReady(t, namespace, name)
	}
	deadline := time.Now().Add(time.Minute)
	for {
		var topology clabernetesapisv1alpha1.Topology
		raw := poolKubectl(
			t,
			"get",
			"topology",
			"planner-pool-mixed",
			"-n",
			namespace,
			"-o",
			"json",
		)
		if err := json.Unmarshal(raw, &topology); err != nil {
			t.Fatal(err)
		}
		if topology.Status.TopologyReady &&
			topology.Status.ObservedGeneration == topology.Generation &&
			topology.Status.NodeCount == 6 &&
			topology.Status.ReadyNodeCount == 6 &&
			topology.Status.LinkCount == linkCount {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("mixed Topology did not converge: %+v", topology.Status)
		}
		time.Sleep(time.Second)
	}
	result := map[string]k8scorev1.Pod{}
	for _, pod := range poolPods(t, namespace, "c9s.run/direct-workload").Items {
		if pod.DeletionTimestamp != nil {
			continue
		}
		assertPoolMixedPodReady(t, pod)
		result[pod.Labels["c9s.run/direct-workload"]] = pod
	}
	if len(result) != 6 || len(result["sros"].Spec.Containers) != 2 {
		t.Fatalf("expected six workloads including two SR-SIM component containers: %+v", result)
	}

	return result
}

func assertPoolMixedPodReady(t *testing.T, pod k8scorev1.Pod) {
	t.Helper()
	assertPoolPodNoRestarts(t, pod)
	if !localHelperStarted(pod) {
		t.Fatalf("helper not started in %s", pod.Name)
	}
	for _, status := range append(pod.Status.ContainerStatuses, pod.Status.InitContainerStatuses...) {
		if status.Name == "clabwire" || strings.HasPrefix(status.Name, "node-") {
			if !status.Ready || status.State.Running == nil {
				t.Fatalf("%s/%s is not running and ready", pod.Name, status.Name)
			}
		}
	}
}

func poolMixedWires(t *testing.T, namespace string, pods map[string]k8scorev1.Pod) map[string]int {
	t.Helper()
	var links clabernetesapisv1alpha1.LinkList
	if err := json.Unmarshal(poolKubectl(t, "get", "links", "-n", namespace, "-o", "json"), &links); err != nil {
		t.Fatal(err)
	}
	result, seen := map[string]int{}, map[int]bool{}
	crossWorker := 0
	for _, link := range links.Items {
		accepted := false
		for _, condition := range link.Status.Conditions {
			if condition.Type == clabernetesapisv1alpha1.LinkConditionAccepted &&
				condition.Status == "True" && condition.ObservedGeneration == link.Generation {
				accepted = true
			}
		}
		if !accepted || link.Status.WireID == 0 || seen[link.Status.WireID] {
			t.Fatalf("link %s has no unique accepted wire: %+v", link.Name, link.Status)
		}
		seen[link.Status.WireID] = true
		result[link.Name] = link.Status.WireID
		if pods[link.Spec.EndpointA.NodeName].Spec.NodeName != pods[link.Spec.EndpointB.NodeName].Spec.NodeName {
			crossWorker++
		}
	}
	if crossWorker == 0 {
		t.Fatal("mixed-vendor test must exercise cross-worker links")
	}
	t.Logf("%d accepted unique wires, %d across Kubernetes workers", len(result), crossWorker)

	return result
}

type poolMixedProbe struct {
	node, source, target string
}

func poolMixedProbes() []poolMixedProbe {
	return []poolMixedProbe{
		{"mt-srl", "eth1", "10.210.1.1"},
		{"mt-srl", "eth2", "10.210.2.1"},
		{"mt-ceos", "eth1", "10.210.3.1"},
		{"mt-ceos", "eth2", "10.210.4.1"},
		{"mt-sros", "eth1", "10.210.5.1"},
		{"mt-sros", "eth2", "10.210.6.1"},
		{"srl", "10.210.7.1", "10.210.7.2"},
		{"srl", "10.210.8.1", "10.210.8.2"},
		{"ceos", "10.210.9.1", "10.210.9.2"},
		{"mt-srl", "eth3", "10.210.10.2"},
		{"mt-ceos", "eth4", "10.210.11.2"},
		{"mt-sros", "eth4", "10.210.12.2"},
	}
}

func poolMixedCheckTraffic(t *testing.T, namespace string, probes []poolMixedProbe) {
	t.Helper()
	// Let device protocols/ARP converge first; then require a separate five-packet zero-loss
	// check for every data link, without retrying a failed measured check.
	for _, probe := range probes {
		deadline := time.Now().Add(3 * time.Minute)
		for {
			output, err := poolMixedPing(t, namespace, probe, 1)
			if err == nil && strings.Contains(string(output), " 0% packet loss") {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("data link %v did not converge: %v: %s", probe, err, output)
			}
			time.Sleep(3 * time.Second)
		}
	}
	for _, probe := range probes {
		output, err := poolMixedPing(t, namespace, probe, 5)
		if err != nil || !strings.Contains(string(output), " 0% packet loss") {
			t.Fatalf("data link %v lost traffic: %v: %s", probe, err, output)
		}
		t.Logf("data link %v: %s", probe, strings.TrimSpace(string(output)))
	}
}

func poolMixedPing(
	t *testing.T,
	namespace string,
	probe poolMixedProbe,
	count int,
) ([]byte, error) {
	t.Helper()
	command := []string{
		"ping",
		"-I",
		probe.source,
		"-c",
		strconv.Itoa(count),
		"-W",
		"2",
		probe.target,
	}
	if probe.node == "srl" {
		command = append([]string{"ip", "netns", "exec", "srbase-default"}, command...)
	}
	args := append([]string{
		"exec", "-n", namespace, "deployment/" + probe.node, "-c",
		clabernetestesthelper.DirectDeviceContainerName(t, namespace, probe.node), "--",
	}, command...)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	//nolint:gosec // Test-controlled arguments, no shell.
	return exec.CommandContext(ctx, "kubectl", args...).
		CombinedOutput()
}

func poolMixedConfigureNewPorts(t *testing.T, namespace string) {
	t.Helper()
	srl, ceos := observeDevicePod(t, namespace, "srl"), observeDevicePod(t, namespace, "ceos")
	waitForDeviceCommand(t, namespace, srl, []string{
		"bash", "-c",
		`printf 'enter candidate\nset / interface ethernet-1/5 admin-state enable\n` +
			`set / interface ethernet-1/5 subinterface 0 ipv4 admin-state enable\n` +
			`set / interface ethernet-1/5 subinterface 0 ipv4 address 10.210.13.1/30\n` +
			`set / network-instance default interface ethernet-1/5.0\ncommit now\nquit\n' | sr_cli`,
	}, "committed")
	waitForDeviceCommand(t, namespace, ceos,
		[]string{"Cli", "-p", "15", "-c", "show interfaces Ethernet5"}, "Ethernet5")
	deviceCommand(t, namespace, ceos,
		[]string{"Cli", "-p", "15", "-c", "configure\ninterface Ethernet5\nno switchport\nend"})
	// EOS completes routed-port conversion asynchronously; wait before assigning the IP.
	waitForDeviceCommand(
		t,
		namespace,
		ceos,
		[]string{
			"Cli",
			"-p",
			"15",
			"-c",
			"show interfaces Ethernet5 switchport",
		},
		"Switchport: Disabled",
	)
	deviceCommand(t, namespace, ceos, []string{
		"Cli", "-p", "15", "-c",
		"configure\ninterface Ethernet5\nip address 10.210.13.2/30\nno shutdown\nend",
	})
}
