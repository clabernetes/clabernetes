package direct_test

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"testing"

	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
)

const ixiacNodeName = "ixia-c-one"

// TestIxiac runs the real keysight_ixia-c-one image with no explicit isolation setting and
// verifies the implicit ixia-c family default. The device container must be in its own network
// namespace, the sidecar-owned interfaces must never appear there, and the Node must report
// both container and connectivity readiness.
func TestIxiac(t *testing.T) {
	t.Parallel()

	testName := "ixiac"
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

	// The ixia-c-one image is large; use the runner's GHCR credentials so the test does not
	// depend on anonymous registry rate limits.
	clabernetestesthelper.CreateGHCRPullSecret(t, namespace, "ixia-regcred")

	clabernetestesthelper.KubectlFileOp(
		t,
		clabernetestesthelper.Apply,
		namespace,
		"test-fixtures/60-ixiac.yaml",
	)

	waitForDirectNodeReady(t, namespace, ixiacNodeName)
	waitForNodeCondition(t, namespace, ixiacNodeName, "ConnectivityReady")

	device := observeDevicePod(t, namespace, ixiacNodeName)
	if !strings.Contains(device.image, "ixia-c-one") {
		t.Fatalf("device container runs %q, want the real ixia-c-one image", device.image)
	}

	assertDeviceNetnsDeployment(t, namespace, ixiacNodeName, device)

	// The device process must be in the bind-mounted private namespace, not merely missing a
	// stolen interface: the netns inode of the running process must equal the file's inode.
	waitForDeviceCommand(
		t,
		namespace,
		device,
		[]string{
			"sh",
			"-c",
			`test "$(readlink /proc/self/ns/net)" = "net:[$(stat -Lc %i /var/run/netns/$C9S_POD_UID.ns)]" && echo NETNS_OK`,
		},
		"NETNS_OK",
	)

	// ixiacd claims every interface it can see. In the private namespace it may move its own
	// data ports, but the sidecar transport must never be visible to it.
	waitForDeviceCommand(
		t,
		namespace,
		device,
		[]string{
			"sh",
			"-c",
			`ip -o link show | grep -q 'c9s0' && echo STOLEN || echo ISOLATED`,
		},
		"ISOLATED",
	)

	management := poolMixedManagementAddresses(t, namespace, 1)[ixiacNodeName]
	waitForDeviceCommand(
		t,
		namespace,
		device,
		[]string{"sh", "-c", "ip -o -4 addr show dev eth0"},
		management+"/",
	)

	// The plan declares a network sysctl for this Node. The device process lives in the private
	// namespace, so the declared value has to be visible there; the shared Pod namespace keeps
	// its own value and would never satisfy this read.
	waitForDeviceCommand(
		t,
		namespace,
		device,
		[]string{"sh", "-c", "cat /proc/sys/net/ipv4/ip_default_ttl"},
		"42",
	)
}

func waitForNodeCondition(t *testing.T, namespace, nodeName, condition string) {
	t.Helper()

	clabernetestesthelper.KubectlWaitForOutput(
		t,
		directNodeReadyTimeout,
		[]string{
			"get",
			"node.c9s.run/" + nodeName,
			"--namespace",
			namespace,
			"-o",
			fmt.Sprintf(
				`jsonpath={.status.conditions[?(@.type==%q)].status}`,
				condition,
			),
		},
		"True",
	)
}

type deploymentVolumeMount struct {
	Name             string  `json:"name"`
	MountPath        string  `json:"mountPath"`
	MountPropagation *string `json:"mountPropagation"`
}

type deploymentContainer struct {
	Name         string                  `json:"name"`
	Command      []string                `json:"command"`
	VolumeMounts []deploymentVolumeMount `json:"volumeMounts"`
}

type deploymentVolume struct {
	Name     string `json:"name"`
	HostPath *struct {
		Path string `json:"path"`
	} `json:"hostPath"`
}

// assertDeviceNetnsDeployment proves the renderer emitted the isolation contract and that the
// launch boundary and sidecar both received it. This fails before the runtime is exercised, so a
// green node cannot hide a missing volume or mount.
//
//nolint:gocyclo // one linear assertion over the rendered Deployment shape.
func assertDeviceNetnsDeployment(
	t *testing.T,
	namespace,
	nodeName string,
	device devicePodObservation,
) {
	t.Helper()

	cmd := exec.CommandContext( //nolint:gosec // kubectl arguments are test-controlled.
		t.Context(),
		"kubectl",
		"get",
		"deployment",
		nodeName,
		"--namespace",
		namespace,
		"-o",
		"json",
	)
	output := clabernetestesthelper.Execute(t, cmd)

	var deployment struct {
		Spec struct {
			Template struct {
				Spec struct {
					Volumes        []deploymentVolume    `json:"volumes"`
					InitContainers []deploymentContainer `json:"initContainers"`
					Containers     []deploymentContainer `json:"containers"`
				} `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(output, &deployment); err != nil {
		t.Fatalf("decode Deployment: %v", err)
	}

	podSpec := deployment.Spec.Template.Spec

	netnsVolume := false
	for _, volume := range podSpec.Volumes {
		if volume.Name == "device-netns" && volume.HostPath != nil &&
			volume.HostPath.Path == "/var/run/clabernetes/device-netns" {
			netnsVolume = true
		}
	}
	if !netnsVolume {
		t.Fatal("Deployment has no device-netns hostPath volume")
	}

	netnsInit := false
	sidecar := false
	for _, container := range podSpec.InitContainers {
		switch container.Name {
		case "device-netns-init":
			netnsInit = true
			if len(container.VolumeMounts) != 1 ||
				container.VolumeMounts[0].MountPropagation == nil ||
				*container.VolumeMounts[0].MountPropagation != "Bidirectional" {
				t.Fatalf("device netns init mounts = %#v", container.VolumeMounts)
			}
		case "clabwire":
			sidecar = true
			if !slices.Contains(container.Command, "connectivity") ||
				!hasDeviceNetnsMount(container.VolumeMounts) {
				t.Fatalf("connectivity helper does not mount the device netns: %#v", container)
			}
		}
	}
	if !netnsInit {
		t.Fatal("Deployment has no device-netns-init container")
	}
	if !sidecar {
		t.Fatal("Deployment has no clabwire helper")
	}

	for _, container := range podSpec.Containers {
		if container.Name != device.containerName {
			continue
		}

		if !slices.Contains(container.Command, "--deviceNetns") {
			t.Fatalf("application command = %#v, want --deviceNetns", container.Command)
		}
		if !hasDeviceNetnsMount(container.VolumeMounts) {
			t.Fatalf("application container does not mount the device netns: %#v", container)
		}

		return
	}

	t.Fatalf("application container %q is absent from the Deployment", device.containerName)
}

func hasDeviceNetnsMount(mounts []deploymentVolumeMount) bool {
	for _, mount := range mounts {
		if mount.Name == "device-netns" && mount.MountPath == "/var/run/netns" &&
			mount.MountPropagation != nil &&
			*mount.MountPropagation == "HostToContainer" {
			return true
		}
	}

	return false
}
