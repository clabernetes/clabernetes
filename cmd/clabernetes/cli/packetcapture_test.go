package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	clabernetescmdclabernetescli "github.com/clabernetes/clabernetes/cmd/clabernetes/cli"
	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	clabernetesinternaldirectruntime "github.com/clabernetes/clabernetes/internal/directruntime"
)

// writeCapturePlan writes a matching device input and plan for one Node with two loopback
// interfaces and returns their paths.
func writeCapturePlan(t *testing.T) (string, string) {
	t.Helper()

	compatibility := clabernetesinternaldeviceplan.Compatibility{
		ContainerlabModule:  clabernetesinternaldeviceplan.ContainerlabModulePath,
		ContainerlabVersion: "v-test",
		PlanSchemaVersion:   clabernetesinternaldeviceplan.SchemaVersion,
		RegistryDigest:      "sha256:" + strings.Repeat("a", 64),
	}
	input := clabernetesinternaldeviceplan.Input{
		SchemaVersion: clabernetesinternaldeviceplan.SchemaVersion, TopologyName: "capture",
		Compatibility: compatibility,
		Nodes: []clabernetesinternaldeviceplan.NodeInput{{
			ID: "lin1-uid", Name: "lin1", Kind: "linux",
			Definition: []byte(`{"kind":"linux","image":"example/linux:1"}`),
		}},
		Images: []clabernetesinternaldeviceplan.ImageInput{{
			NodeID: "lin1-uid", Role: "device", SourceReference: "example/linux:1",
			DigestReference: "example/linux@sha256:aaaaaaaa",
			Platform: clabernetesinternaldeviceplan.Platform{
				OS: "linux", Architecture: "amd64",
			},
		}},
	}

	inputDigest, err := input.Digest()
	if err != nil {
		t.Fatal(err)
	}

	plan := clabernetesinternaldeviceplan.Plan{
		SchemaVersion: clabernetesinternaldeviceplan.SchemaVersion,
		Compatibility: compatibility, InputDigest: inputDigest,
		Planner: clabernetesinternaldeviceplan.PlannerIdentity{
			Name: "clabernetes", Revision: "capture-test",
		},
		Nodes: []clabernetesinternaldeviceplan.NodePlan{{
			ID: "lin1-uid", Name: "lin1", Kind: "linux",
			ContainerIDs: []string{"lin1"}, ReadinessContainerIDs: []string{"lin1"},
		}},
		Containers: []clabernetesinternaldeviceplan.ContainerPlan{{
			ID: "lin1", NodeID: "lin1-uid", NamespaceOwnerID: "lin1",
			Image: "example/linux:1", Required: true,
		}},
	}
	for _, names := range [][2]string{{"eth1", "eth2"}, {"eth2", "eth1"}} {
		plan.Interfaces = append(plan.Interfaces, clabernetesinternaldeviceplan.InterfacePlan{
			ID: "loop/" + names[0], NodeID: "lin1-uid", NamespaceOwnerID: "lin1",
			Name: names[0], LinkID: "loop", PeerNodeID: "lin1-uid", PeerInterface: names[1],
			Connectivity: clabernetesinternaldeviceplan.ConnectivityLoopback, MTU: 1500,
			LinkApplyMode: clabernetesinternaldeviceplan.LinkApplyLive,
		})
	}

	directory := t.TempDir()
	inputPath := filepath.Join(directory, "input.json")
	planPath := filepath.Join(directory, "plan.json")

	for path, value := range map[string]any{inputPath: input, planPath: plan} {
		raw, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}

		if err = os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	return inputPath, planPath
}

// runPacketCapture runs the shipped entrypoint. Callers do not run in parallel: urfave/cli
// applies its process-global help flag on every run.
func runPacketCapture(t *testing.T, arguments ...string) (string, string, error) {
	t.Helper()

	var stdout, stderr bytes.Buffer

	app := clabernetescmdclabernetescli.Entrypoint()
	app.Writer, app.ErrWriter = &stdout, &stderr

	err := app.Run(append(
		[]string{"manager", "node-runtime", "packet-capture"},
		arguments...,
	))

	return stdout.String(), stderr.String(), err
}

func TestPacketCaptureListsInterfacesByNodeName(t *testing.T) {
	inputPath, planPath := writeCapturePlan(t)

	// The default revision path is not mounted here, so the accepted plan is effective.
	stdout, _, err := runPacketCapture(
		t,
		"--plan", planPath, "--input", inputPath, "--nodeName", "lin1", "--list-interfaces",
	)
	if err != nil {
		t.Fatal(err)
	}

	var interfaces []clabernetesinternaldirectruntime.PacketCaptureInterface
	if err = json.Unmarshal([]byte(stdout), &interfaces); err != nil {
		t.Fatalf("listing is not JSON: %v: %s", err, stdout)
	}

	if len(interfaces) != 2 || interfaces[0].Interface != "eth1" ||
		interfaces[0].PeerNode != "lin1" || interfaces[0].PeerInterface != "eth2" ||
		interfaces[1].Node != "lin1" {
		t.Fatalf("listed interfaces = %#v", interfaces)
	}
}

func TestPacketCaptureRejectsMalformedRequests(t *testing.T) {
	inputPath, planPath := writeCapturePlan(t)
	files := []string{"--plan", planPath, "--input", inputPath}

	for name, testCase := range map[string]struct {
		arguments []string
		reason    string
	}{
		"listing with an interface": {
			arguments: []string{"--list-interfaces", "--interface", "eth1"},
			reason:    "does not accept capture flags: --interface",
		},
		"both selectors": {
			arguments: []string{
				"--nodeID", "lin1-uid", "--nodeName", "lin1", "--interface", "eth1",
				"--packetLimit", "1",
			},
			reason: "not both",
		},
		"no selector": {
			arguments: []string{"--interface", "eth1", "--packetLimit", "1"},
			reason:    "requires --nodeID or --nodeName",
		},
		"pcap with two interfaces": {
			arguments: []string{
				"--nodeName", "lin1", "--interface", "eth1", "--interface", "eth2",
				"--packetLimit", "1",
			},
			reason: "use --format pcapng",
		},
		"explicit revision that is missing": {
			arguments: []string{
				"--nodeName", "lin1", "--interface", "eth1", "--packetLimit", "1",
				"--connectivityRevision", filepath.Join(t.TempDir(), "revision.json"),
			},
			reason: "opening connectivity revision",
		},
	} {
		stdout, _, err := runPacketCapture(t, append(files, testCase.arguments...)...)
		if err == nil || !strings.Contains(err.Error(), testCase.reason) || stdout != "" {
			t.Errorf("%s: error = %v, stdout %q, want %q", name, err, stdout, testCase.reason)
		}
	}
}

func TestPacketCaptureDeniesForeignInterfaceBeforeCapturing(t *testing.T) {
	inputPath, planPath := writeCapturePlan(t)

	stdout, stderr, err := runPacketCapture(
		t,
		"--plan", planPath, "--input", inputPath, "--nodeName", "lin1",
		"--interface", "eth1", "--interface", "eth0", "--format", "pcapng",
		"--duration", "1s",
	)
	if err == nil || stdout != "" {
		t.Fatalf("foreign interface capture = %v, stdout %d bytes", err, len(stdout))
	}

	var record clabernetesinternaldirectruntime.PacketCaptureAuditRecord
	if err = json.Unmarshal([]byte(stderr), &record); err != nil {
		t.Fatalf("audit is not one JSON record: %v: %s", err, stderr)
	}

	if record.Status != "Denied" || record.NodeID != "lin1-uid" ||
		len(record.Interfaces) != 2 || record.Interfaces[1].Name != "eth0" {
		t.Fatalf("denied audit = %#v", record)
	}
}

func TestPacketCaptureDefaultsToTheSidecarMounts(t *testing.T) {
	if _, err := os.Stat(clabernetesinternaldirectruntime.ConnectivityInputPath); err == nil {
		t.Skip("the sidecar input is mounted on this machine")
	}

	_, _, err := runPacketCapture(t, "--nodeName", "lin1", "--list-interfaces")
	if err == nil ||
		!strings.Contains(err.Error(), clabernetesinternaldirectruntime.ConnectivityInputPath) {
		t.Fatalf("default input path error = %v", err)
	}
}
