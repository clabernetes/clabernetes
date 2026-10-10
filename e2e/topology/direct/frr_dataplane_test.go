package direct_test

import (
	"testing"

	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
)

// TestDirectFrrDataplane proves the FRRouting kind boots as a direct device Pod with
// containerlab's generated FRR configuration. The kind bind-mounts /etc/frr over the image's
// defaults, so the wire interface must carry the address the startup configuration declared, the
// routing daemons must form an OSPF adjacency across the fabric link, and the OSPF-learned
// loopback route must carry real traffic -- the same contract containerlab deploys with.
func TestDirectFrrDataplane(t *testing.T) {
	t.Parallel()

	testName := "topology-direct-frr"

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

	clabernetestesthelper.KubectlFileOp(
		t,
		clabernetestesthelper.Apply,
		namespace,
		"test-fixtures/40-frr-dataplane.yaml",
	)

	for _, nodeName := range []string{"frr1", "frr2"} {
		waitForDirectNodeReady(t, namespace, nodeName)
	}

	// The bind-mounted frr.conf is the node's running configuration: zebra must have applied the
	// declared interface address to the fabric-attached wire interface.
	for nodeName, address := range map[string]string{
		"frr1": "192.168.0.0/31",
		"frr2": "192.168.0.1/31",
	} {
		waitForDeviceCommand(
			t,
			namespace,
			observeDevicePod(t, namespace, nodeName),
			[]string{"ip", "-br", "addr", "show", "eth1"},
			address,
		)
	}

	// Dataplane across the Link: FRR forwards in the kernel, so the wire itself must carry the
	// traffic the configuration describes.
	for source, destination := range map[string]string{
		"frr1": "192.168.0.1",
		"frr2": "192.168.0.0",
	} {
		waitForDeviceCommand(
			t,
			namespace,
			observeDevicePod(t, namespace, source),
			[]string{"ping", "-c", "2", "-W", "2", destination},
			" 0% packet loss",
		)
	}

	// The routing daemons must run and converge: the OSPF adjacency forms across the fabric
	// link and installs the peer's loopback as an OSPF route in the kernel FIB.
	for _, nodeName := range []string{"frr1", "frr2"} {
		waitForDeviceCommand(
			t,
			namespace,
			observeDevicePod(t, namespace, nodeName),
			[]string{"vtysh", "-c", "show ip ospf neighbor"},
			"Full",
		)
	}

	for source, loopback := range map[string]string{
		"frr1": "10.0.0.2",
		"frr2": "10.0.0.1",
	} {
		waitForDeviceCommand(
			t,
			namespace,
			observeDevicePod(t, namespace, source),
			[]string{"ping", "-c", "2", "-W", "2", loopback},
			" 0% packet loss",
		)
	}

	waitForWorkerArtifactCollection(t, namespace)
}
