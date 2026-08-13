package basic_test

import (
	"os"
	"os/exec"
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
		"test-fixtures/10-apply.yaml",
	)

	waitForSRLinuxDNS(t, namespace)
}

func waitForSRLinuxDNS(t *testing.T, namespace string) {
	t.Helper()

	const (
		pollInterval = 3 * time.Second
		timeout      = 5 * time.Minute
	)

	command := []string{
		"exec",
		"--namespace",
		namespace,
		"deployment/srl1",
		"-c",
		"srl1",
		"--",
		"sh",
		"-ec",
		`container_id="$(docker ps --quiet --filter label=clab-node-name=srl1)"
test -n "${container_id}"
docker exec "${container_id}" ip netns exec srbase-mgmt getent hosts kubernetes.default.svc.cluster.local`,
	}

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	var lastOutput []byte

	for {
		output, err := exec.CommandContext(t.Context(), "kubectl", command...).CombinedOutput() //nolint:gosec
		if err == nil && strings.TrimSpace(string(output)) != "" {
			return
		}

		lastOutput = output

		select {
		case <-t.Context().Done():
			t.Fatalf("DNS lookup canceled: %s", strings.TrimSpace(string(lastOutput)))
		case <-deadline.C:
			t.Fatalf(
				"timed out waiting for DNS lookup from srbase-mgmt: %s",
				strings.TrimSpace(string(lastOutput)),
			)
		case <-time.After(pollInterval):
		}
	}
}
