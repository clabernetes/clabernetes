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
			// the topology compiles to Node/Link/LauncherProfile objects and the Node controller
			// realizes those -- so this asserts the whole compile + realize pipeline including
			// the (unprefixed! the namespace is the topology boundary) deployment and services
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
				"launcherprofile": {
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
						Name: "srl1-vx",
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeFabricService,
						},
					},
				},
				"deployment": {
					{
						Name: "srl1",
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeDeployment,
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
						Name: "srl1-vx",
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeFabricService,
						},
					},
					{
						Name: "srl2-vx",
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeFabricService,
						},
					},
				},
				"deployment": {
					{
						Name: "srl1",
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeDeployment,
						},
					},
					{
						Name: "srl2",
						NormalizeFuncs: []func(t *testing.T, objectData []byte) []byte{
							clabernetestesthelper.NormalizeDeployment,
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

	srLinuxNodes := getSRLinuxNodeNames(t, namespace)
	if len(srLinuxNodes) < 2 {
		t.Skipf("topology has fewer than two SR Linux nodes: %v", srLinuxNodes)
	}

	waitForSRLinuxRemotePing(t, namespace, srLinuxNodes[0], srLinuxNodes[1])
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
		pollInterval = 3 * time.Second
		timeout      = 5 * time.Minute
	)

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	var lastOutput []byte

	for {
		remoteDNSName, err := getRemoteNodeDNSName(t, namespace, remoteNode)
		if err != nil {
			lastOutput = []byte(err.Error())
		} else if remoteDNSName != "" {
			command := []string{
				"exec",
				"--namespace",
				namespace,
				"deployment/" + sourceNode,
				"-c",
				sourceNode,
				"--",
				"sh",
				"-ec",
				`source="$1"
remote="$2"
container_id="$(docker ps --quiet --filter "label=clab-node-name=${source}")"
test -n "${container_id}"
docker exec "${container_id}" ip netns exec srbase-mgmt ping -c 1 -W 5 "${remote}"`,
				"launcher",
				sourceNode,
				remoteDNSName,
			}

			cmd := exec.CommandContext( //nolint:gosec
				t.Context(),
				"kubectl",
				command...,
			)

			output, pingErr := cmd.CombinedOutput()
			if pingErr == nil && strings.TrimSpace(string(output)) != "" {
				return
			}

			lastOutput = output
		}

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
	}
}

func getRemoteNodeDNSName(t *testing.T, namespace, nodeName string) (string, error) {
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
		return "", err
	}

	var podList struct {
		Items []struct {
			Status struct {
				PodIP string `json:"podIP"`
			} `json:"status"`
		} `json:"items"`
	}

	err = json.Unmarshal(output, &podList)
	if err != nil {
		return "", err
	}

	if len(podList.Items) == 0 || podList.Items[0].Status.PodIP == "" {
		return "", nil
	}

	podIP := strings.ReplaceAll(podList.Items[0].Status.PodIP, ".", "-")

	return fmt.Sprintf(
		"%s.%s.pod.cluster.local",
		podIP,
		namespace,
	), nil
}
