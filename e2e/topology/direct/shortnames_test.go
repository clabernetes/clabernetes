package direct_test

import (
	"testing"

	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
)

// singleCharacterNamesTopology links two nodes whose containerlab names are one character long.
// containerlab deploys them on Docker as clab-<lab>-a and clab-<lab>-b; c9s names them a and b.
const singleCharacterNamesTopology = `apiVersion: c9s.run/v1alpha1
kind: Topology
metadata:
  name: short-names
spec:
  expose:
    exposeType: None
  deployment:
    persistence:
      enabled: false
  definition:
    containerlab: |
      name: short-names
      topology:
        defaults:
          kind: linux
          image: ghcr.io/srl-labs/network-multitool
        nodes:
          a:
            exec:
              - ip address add 192.168.1.0/31 dev eth1
          b:
            exec:
              - ip address add 192.168.1.1/31 dev eth1
        links:
          - endpoints: [a:eth1, b:eth1]
`

// TestDirectSingleCharacterNodeNames proves a Topology whose nodes have one-character names
// passes containerlab's deployment conditions on the target worker, comes up, and forwards
// traffic across its link.
func TestDirectSingleCharacterNodeNames(t *testing.T) {
	t.Parallel()

	testName := "topology-direct-short-names"
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

	applyManifest(t, namespace, singleCharacterNamesTopology)

	for _, nodeName := range []string{"a", "b"} {
		waitForDirectNodeReady(t, namespace, nodeName)
	}

	waitForDeviceCommand(
		t,
		namespace,
		observeDevicePod(t, namespace, "a"),
		[]string{"ping", "-c", "2", "-W", "2", "192.168.1.1"},
		" 0% packet loss",
	)
}
