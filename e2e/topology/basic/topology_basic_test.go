package basic_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"testing"
	"time"

	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
	clabernetestesthelpersuite "github.com/clabernetes/clabernetes/testhelper/suite"
)

func TestMain(m *testing.M) {
	clabernetestesthelper.Flags()

	os.Exit(m.Run())
}

func TestContainerlabBasic(t *testing.T) {
	t.Parallel()

	testName := "topology-basic"

	namespace := clabernetestesthelper.NewTestNamespace(testName)

	steps := clabernetestesthelpersuite.Steps{
		{
			// the topology compiles to Node/Link/NodeProfile objects and the Node controller
			// realizes those -- this asserts the compile pipeline plus the expose/fabric
			// services (the device Pods themselves are covered behaviorally in e2e/topology/direct)
			Index:       10,
			Description: "Create a simple containerlab topology with just one node",
			AssertObjects: map[string][]clabernetestesthelpersuite.AssertObject{
				"topology": {
					{
						Name: testName,
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeTopology,
						},
					},
				},
				"node.c9s.run": {
					{
						Name: "srl1",
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeNode,
						},
					},
				},
				"nodeprofile": {
					{
						Name:           testName,
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{},
					},
				},
				"service": {
					{
						Name: "srl1",
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeExposeService,
						},
					},
					{
						Name: "srl1-wire",
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeFabricService,
						},
					},
				},
			},
		},
		{
			// this step we add a second node to topo and actually configure some links this time.
			Index:       20,
			Description: "Add a node and connect them",
			AssertObjects: map[string][]clabernetestesthelpersuite.AssertObject{
				"topology": {
					{
						Name: testName,
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeTopology,
						},
					},
				},
				"node.c9s.run": {
					{
						Name: "srl1",
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeNode,
						},
					},
					{
						Name: "srl2",
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeNode,
						},
					},
				},
				"link": {
					{
						Name: "srl1-e1-1-srl2-e1-1",
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeLink,
						},
					},
					{
						Name: "srl1-e1-3-host-eth13",
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeLink,
						},
					},
				},
				"service": {
					{
						Name: "srl1",
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeExposeService,
						},
					},
					{
						Name: "srl1-wire",
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeFabricService,
						},
					},
					{
						Name: "srl2-wire",
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeFabricService,
						},
					},
				},
			},
		},
	}

	clabernetestesthelpersuite.Run(t, testName, steps, namespace)
}

func TestSRLinuxDNSFromManagementNamespace(t *testing.T) {
	testName := "topology-srlinux-dns"
	namespace := clabernetestesthelper.NewTestNamespace(testName)

	clabernetestesthelper.KubectlCreateNamespace(t, namespace)

	defer func() {
		if !*clabernetestesthelper.SkipCleanup {
			t.Logf("deleting namespace %q used in test %q", namespace, testName)
			clabernetestesthelper.KubectlDeleteNamespace(t, namespace)
		}
	}()

	clabernetestesthelper.KubectlFileOp(
		t,
		clabernetestesthelper.Apply,
		namespace,
		"test-fixtures/20-apply.yaml",
	)

	for _, nodeName := range []string{"srl1", "srl2"} {
		clabernetestesthelper.KubectlWaitForCreate(
			t,
			"nodes.c9s.run",
			namespace,
			nodeName,
		)
		waitForNodeReady(t, namespace, nodeName)
	}

	srLinuxNodes := getSRLinuxNodeNames(t, namespace)
	if len(srLinuxNodes) < 2 {
		t.Skipf("topology has fewer than two SR Linux nodes: %v", srLinuxNodes)
	}

	waitForSRLinuxRemotePing(t, namespace, srLinuxNodes[0], srLinuxNodes[1])
}

func waitForNodeReady(t *testing.T, namespace, nodeName string) {
	t.Helper()

	cmd := exec.CommandContext( //nolint:gosec
		t.Context(),
		"kubectl",
		"wait",
		"--for=jsonpath={.status.readiness}=ready",
		"--timeout=12m",
		"--namespace",
		namespace,
		"node.c9s.run/"+nodeName,
	)

	clabernetestesthelper.Execute(t, cmd)
}

func getSRLinuxNodeNames(t *testing.T, namespace string) []string {
	t.Helper()

	cmd := exec.CommandContext( //nolint:gosec
		t.Context(),
		"kubectl",
		"get",
		"nodes.c9s.run",
		"--namespace",
		namespace,
		"-o",
		"json",
	)

	output := clabernetestesthelper.Execute(t, cmd)

	var nodeList struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Kind string `json:"kind"`
			} `json:"spec"`
		} `json:"items"`
	}

	err := json.Unmarshal(output, &nodeList)
	if err != nil {
		t.Fatalf("failed decoding Node resources: %s", err)
	}

	srLinuxNodes := make([]string, 0, len(nodeList.Items))
	for _, node := range nodeList.Items {
		switch strings.ToLower(node.Spec.Kind) {
		case "srl", "nokia_srlinux":
			srLinuxNodes = append(srLinuxNodes, node.Metadata.Name)
		}
	}

	sort.Strings(srLinuxNodes)

	return srLinuxNodes
}

func waitForSRLinuxRemotePing(t *testing.T, namespace, sourceNode, remoteNode string) {
	t.Helper()

	const (
		pollInterval    = 3 * time.Second
		confirmInterval = 20 * time.Second
		timeout         = 5 * time.Minute
	)

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	// The per-node fabric Service is headless, so Service DNS resolves directly to the remote
	// device Pod instead of relying on the cluster's optional per-Pod DNS record mode.
	remoteDNSName := fmt.Sprintf("%s-wire.%s.svc.cluster.local", remoteNode, namespace)

	var lastOutput []byte

	consecutive := 0

	for {
		podName, containerName, targetErr := getDevicePodTarget(t, namespace, sourceNode)
		if targetErr != nil || podName == "" {
			lastOutput = []byte("device Pod for " + sourceNode + " is not observable yet")

			select {
			case <-t.Context().Done():
				t.Fatalf("DNS lookup canceled: %s", strings.TrimSpace(string(lastOutput)))
			case <-deadline.C:
				t.Fatalf(
					"timed out waiting for SR Linux DNS ping %s -> %s: %s",
					sourceNode,
					remoteNode,
					strings.TrimSpace(string(lastOutput)),
				)
			case <-time.After(pollInterval):
			}

			continue
		}

		command := []string{
			"exec",
			"--namespace",
			namespace,
			podName,
			"-c",
			containerName,
			"--",
			"ip",
			"netns",
			"exec",
			"srbase-mgmt",
			"ping",
			"-c",
			"1",
			"-W",
			"5",
			remoteDNSName,
		}

		cmd := exec.CommandContext( //nolint:gosec
			t.Context(),
			"kubectl",
			command...,
		)

		output, pingErr := cmd.CombinedOutput()
		if pingErr == nil && strings.TrimSpace(string(output)) != "" {
			// A very early ping can win a race against the device populating its own
			// management resolver state, so a single success is not steady-state proof.
			consecutive++
			if consecutive >= 2 {
				return
			}
		} else {
			consecutive = 0
		}

		lastOutput = output

		wait := pollInterval
		if consecutive == 1 {
			// Leave the early-boot race window before confirming steady-state resolution.
			wait = confirmInterval
		}

		select {
		case <-t.Context().Done():
			t.Fatalf("DNS lookup canceled: %s", strings.TrimSpace(string(lastOutput)))
		case <-deadline.C:
			dumpSRLinuxDNSDiagnostics(t, namespace, sourceNode)
			t.Fatalf(
				"timed out waiting for SR Linux DNS ping %s -> %s: %s",
				sourceNode,
				remoteNode,
				strings.TrimSpace(string(lastOutput)),
			)
		case <-time.After(wait):
		}
	}
}

// dumpSRLinuxDNSDiagnostics logs the management resolver chain of one SR Linux node so a CI-only
// resolution failure is diagnosable from the job log: the dnsmasq forwarder config SR Linux
// renders from its `system dns` state, the management netns routing tables, raw TCP reachability
// of both the local forwarder and the cluster DNS Service, and the Pod resolver the runtime
// completed the device DNS from.
func dumpSRLinuxDNSDiagnostics(t *testing.T, namespace, nodeName string) {
	t.Helper()

	podName, containerName, err := getDevicePodTarget(t, namespace, nodeName)
	if err != nil || podName == "" {
		t.Logf("DNS diagnostics skipped: device Pod for %s is not observable: %v", nodeName, err)

		return
	}

	diagnostics := [][]string{
		{"pod resolv.conf", "cat", "/etc/resolv.conf"},
		{"dnsmasq forwarder config", "cat", "/etc/dnsmasq-clab-default.conf"},
		{"dnsmasq process", "pgrep", "-a", "dnsmasq"},
		{
			"management netns resolv.conf",
			"ip", "netns", "exec", "srbase-mgmt", "cat", "/etc/resolv.conf",
		},
		{
			"management netns v4 routes",
			"ip", "netns", "exec", "srbase-mgmt", "ip", "-4", "route", "show", "table", "all",
		},
		{
			"lookup via local dnsmasq forwarder",
			"ip", "netns", "exec", "srbase-mgmt",
			"timeout", "5", "nslookup", "kubernetes.default.svc.cluster.local", "127.0.0.1",
		},
		{
			"lookup direct against cluster DNS",
			"ip", "netns", "exec", "srbase-mgmt",
			"timeout", "5", "nslookup", "kubernetes.default.svc.cluster.local", "10.96.0.10",
		},
		{
			"lookup direct against cluster DNS from Pod namespace",
			"timeout", "5", "nslookup", "kubernetes.default.svc.cluster.local", "10.96.0.10",
		},
	}

	for _, diagnostic := range diagnostics {
		command := append(
			[]string{"exec", "--namespace", namespace, podName, "-c", containerName, "--"},
			diagnostic[1:]...,
		)

		cmd := exec.CommandContext(t.Context(), "kubectl", command...) //nolint:gosec

		output, diagErr := cmd.CombinedOutput()
		t.Logf(
			"DNS diagnostic [%s] (err: %v):\n%s",
			diagnostic[0],
			diagErr,
			strings.TrimSpace(string(output)),
		)
	}
}

// getDevicePodTarget resolves the running device Pod and its device container for one logical
// node: the direct runtime realizes devices as Pod containers, so command intent targets the
// container directly.
func getDevicePodTarget(t *testing.T, namespace, nodeName string) (string, string, error) {
	t.Helper()

	cmd := exec.CommandContext( //nolint:gosec
		t.Context(),
		"kubectl",
		"get",
		"pods",
		"--namespace",
		namespace,
		"--selector",
		"c9s.run/name="+nodeName,
		"-o",
		"json",
	)

	output, err := cmd.Output()
	if err != nil {
		return "", "", err
	}

	var podList struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Containers []struct {
					Name string `json:"name"`
				} `json:"containers"`
			} `json:"spec"`
			Status struct {
				Phase string `json:"phase"`
			} `json:"status"`
		} `json:"items"`
	}

	err = json.Unmarshal(output, &podList)
	if err != nil {
		return "", "", err
	}

	for _, pod := range podList.Items {
		if pod.Status.Phase != "Running" {
			continue
		}

		for _, container := range pod.Spec.Containers {
			if strings.HasPrefix(container.Name, "node-") {
				return pod.Metadata.Name, container.Name, nil
			}
		}
	}

	return "", "", nil
}
