package deviceplan_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	"github.com/google/go-cmp/cmp"
)

func TestCanonicalInputIsDeterministicAndDoesNotMutateCaller(t *testing.T) {
	t.Parallel()

	left := validInput()
	right := validInput()
	slices.Reverse(right.Nodes)
	slices.Reverse(right.Images)
	slices.Reverse(right.Images[1].Platform.OSFeatures)
	slices.Reverse(right.Images[1].Config.Environment)
	slices.Reverse(right.Payloads)
	slices.Reverse(right.Interfaces)
	original := right.Images[1].Config.Environment[0].Name

	leftJSON, err := left.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}

	rightJSON, err := right.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(string(leftJSON), string(rightJSON)); diff != "" {
		t.Fatalf("canonical input differs (-left +right):\n%s", diff)
	}

	if right.Images[1].Config.Environment[0].Name != original {
		t.Fatal("CanonicalJSON() mutated caller-owned input")
	}

	digest, err := left.Digest()
	if err != nil {
		t.Fatal(err)
	}

	if digest != clabernetesinternaldeviceplan.Digest(leftJSON) {
		t.Fatalf("input digest = %q, want digest of canonical JSON", digest)
	}

	decoded, err := clabernetesinternaldeviceplan.DecodeInput(leftJSON)
	if err != nil {
		t.Fatal(err)
	}

	decodedJSON, err := decoded.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(leftJSON, decodedJSON) {
		t.Fatalf("input round trip changed canonical JSON:\n%s\n%s", leftJSON, decodedJSON)
	}
}

func TestCanonicalPlanOrdersSetsButPreservesCommandsAndDNS(t *testing.T) {
	t.Parallel()

	input := validInput()

	inputDigest, err := input.Digest()
	if err != nil {
		t.Fatal(err)
	}

	left := validPlan(inputDigest)
	right := validPlan(inputDigest)
	slices.Reverse(right.Nodes)
	slices.Reverse(right.Containers)
	slices.Reverse(right.Files)
	slices.Reverse(right.Volumes)
	slices.Reverse(right.Mounts)
	slices.Reverse(right.Actions)
	slices.Reverse(right.Interfaces)
	slices.Reverse(right.Containers[1].Environment)
	slices.Reverse(right.Containers[1].MountIDs)

	leftJSON, err := left.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}

	rightJSON, err := right.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(string(leftJSON), string(rightJSON)); diff != "" {
		t.Fatalf("canonical plan differs (-left +right):\n%s", diff)
	}

	decoded, err := clabernetesinternaldeviceplan.DecodePlan(leftJSON)
	if err != nil {
		t.Fatal(err)
	}

	if got, want := decoded.Containers[0].Command, []string{"serve", "--foreground"}; !reflect.DeepEqual(
		got,
		want,
	) {
		t.Fatalf("ordered command = %#v, want %#v", got, want)
	}

	if got, want := decoded.Containers[0].DNS.Servers, []string{"10.0.0.2", "10.0.0.3"}; !reflect.DeepEqual(
		got,
		want,
	) {
		t.Fatalf("ordered DNS servers = %#v, want %#v", got, want)
	}

	if got := decoded.Actions; got[0].Phase != clabernetesinternaldeviceplan.PhasePrepare ||
		got[1].Phase != clabernetesinternaldeviceplan.PhasePostStart {
		t.Fatalf("actions are not ordered by lifecycle phase: %#v", got)
	}
}

func TestPlanningSchemaFailsClosed(t *testing.T) {
	t.Parallel()

	input := validInput()

	inputDigest, err := input.Digest()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(*clabernetesinternaldeviceplan.Plan)
		code   clabernetesinternaldeviceplan.ErrorCode
		field  string
	}{
		{
			name: "unknown container reference",
			mutate: func(plan *clabernetesinternaldeviceplan.Plan) {
				plan.Nodes[0].ContainerIDs = []string{"missing"}
			},
			code:  clabernetesinternaldeviceplan.ErrorInvariant,
			field: "nodes[0].containerIDs",
		},
		{
			name: "mismatched action payload",
			mutate: func(plan *clabernetesinternaldeviceplan.Plan) {
				plan.Actions[0].Kind = clabernetesinternaldeviceplan.ActionFile
			},
			code:  clabernetesinternaldeviceplan.ErrorInvariant,
			field: "actions[0]",
		},
		{
			name: "unknown action kind",
			mutate: func(plan *clabernetesinternaldeviceplan.Plan) {
				plan.Actions[0].Kind = "RunAnything"
			},
			code:  clabernetesinternaldeviceplan.ErrorUnsupported,
			field: "actions[0].kind",
		},
		{
			name: "bad input digest",
			mutate: func(plan *clabernetesinternaldeviceplan.Plan) {
				plan.InputDigest = "latest"
			},
			code:  clabernetesinternaldeviceplan.ErrorInvalidInput,
			field: "inputDigest",
		},
		{
			name: "invalid container port",
			mutate: func(plan *clabernetesinternaldeviceplan.Plan) {
				plan.Containers[0].Ports = []clabernetesinternaldeviceplan.Port{
					{Number: 22, Protocol: "SCTP"},
				}
			},
			code:  clabernetesinternaldeviceplan.ErrorInvalidInput,
			field: "containers[0].ports[0]",
		},
		{
			name: "invalid image digest",
			mutate: func(plan *clabernetesinternaldeviceplan.Plan) {
				plan.Containers[0].ImageDigest = "sha256:not-an-immutable-digest"
			},
			code:  clabernetesinternaldeviceplan.ErrorInvalidInput,
			field: "containers[0].imageDigest",
		},
		{
			name: "invalid file ownership",
			mutate: func(plan *clabernetesinternaldeviceplan.Plan) {
				invalid := int64(-1)
				plan.Files[0].UID = &invalid
			},
			code:  clabernetesinternaldeviceplan.ErrorInvalidInput,
			field: "files[0]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			plan := validPlan(inputDigest)
			tt.mutate(&plan)
			_, normalizeErr := clabernetesinternaldeviceplan.NormalizePlan(plan)

			var planningErr *clabernetesinternaldeviceplan.Error
			if !errors.As(normalizeErr, &planningErr) {
				t.Fatalf("NormalizePlan() error = %v, want structured planning error", normalizeErr)
			}

			if planningErr.Code != tt.code || planningErr.Field != tt.field {
				t.Fatalf(
					"NormalizePlan() error = %#v, want code %q field %q",
					planningErr,
					tt.code,
					tt.field,
				)
			}
		})
	}
}

func TestManagementPlanAcceptsOneGenericInterfaceSelection(t *testing.T) {
	t.Parallel()

	inputDigest, err := validInput().Digest()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name              string
		interfaceName     string
		interfaceSelector clabernetesinternaldeviceplan.ManagementInterfaceSelector
		wantErr           bool
	}{
		{name: "package name", interfaceName: "mgmt0"},
		{
			name:              "pod transport selector",
			interfaceSelector: clabernetesinternaldeviceplan.ManagementInterfacePodTransport,
		},
		{
			name:              "both selections",
			interfaceName:     "mgmt0",
			interfaceSelector: clabernetesinternaldeviceplan.ManagementInterfacePodTransport,
			wantErr:           true,
		},
		{
			name:              "unknown selector",
			interfaceSelector: "FutureSelector",
			wantErr:           true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			plan := validPlan(inputDigest)
			plan.Management[0].InterfaceName = tt.interfaceName
			plan.Management[0].InterfaceSelector = tt.interfaceSelector

			_, normalizeErr := clabernetesinternaldeviceplan.NormalizePlan(plan)
			if tt.wantErr && normalizeErr == nil {
				t.Fatal("NormalizePlan() succeeded for an invalid interface selection")
			}

			if !tt.wantErr && normalizeErr != nil {
				t.Fatalf("NormalizePlan() error = %v", normalizeErr)
			}
		})
	}
}

func TestManagementPlanInterpositionContract(t *testing.T) {
	t.Parallel()

	inputDigest, err := validInput().Digest()
	if err != nil {
		t.Fatal(err)
	}

	validContract := func() *clabernetesinternaldeviceplan.ManagementInterposition {
		return &clabernetesinternaldeviceplan.ManagementInterposition{
			DeviceInterface: "eth0",
			DeviceMAC:       "00:1c:73:c9:50:31",
			TransportCIDRs:  []string{"10.96.0.0/12", "10.244.0.0/16"},
			InboundPorts: []clabernetesinternaldeviceplan.ManagementPortMap{
				{Protocol: "tcp", PodPort: 22, DevicePort: 22},
			},
		}
	}

	tests := []struct {
		name    string
		mutate  func(*clabernetesinternaldeviceplan.ManagementPlan)
		wantErr bool
	}{
		{
			name:   "valid interposed entry",
			mutate: func(_ *clabernetesinternaldeviceplan.ManagementPlan) {},
		},
		{
			name: "contract without interposed selector",
			mutate: func(entry *clabernetesinternaldeviceplan.ManagementPlan) {
				entry.InterfaceSelector = clabernetesinternaldeviceplan.ManagementInterfacePodTransport
			},
			wantErr: true,
		},
		{
			name: "interposed selector without contract",
			mutate: func(entry *clabernetesinternaldeviceplan.ManagementPlan) {
				entry.Interposition = nil
			},
			wantErr: true,
		},
		{
			name: "missing allocated identity",
			mutate: func(entry *clabernetesinternaldeviceplan.ManagementPlan) {
				entry.IPv4 = ""
			},
			wantErr: true,
		},
		{
			name: "missing gateway",
			mutate: func(entry *clabernetesinternaldeviceplan.ManagementPlan) {
				entry.IPv4Gateway = ""
			},
			wantErr: true,
		},
		{
			name: "invalid device MAC",
			mutate: func(entry *clabernetesinternaldeviceplan.ManagementPlan) {
				entry.Interposition.DeviceMAC = "not-a-mac"
			},
			wantErr: true,
		},
		{
			name: "invalid transport CIDR",
			mutate: func(entry *clabernetesinternaldeviceplan.ManagementPlan) {
				entry.Interposition.TransportCIDRs = []string{"10.96.0.0"}
			},
			wantErr: true,
		},
		{
			name: "invalid inbound protocol",
			mutate: func(entry *clabernetesinternaldeviceplan.ManagementPlan) {
				entry.Interposition.InboundPorts = []clabernetesinternaldeviceplan.ManagementPortMap{
					{Protocol: "sctp", PodPort: 22, DevicePort: 22},
				}
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			plan := validPlan(inputDigest)
			plan.Management[0].InterfaceName = ""
			plan.Management[0].InterfaceSelector = clabernetesinternaldeviceplan.ManagementInterfaceInterposed
			plan.Management[0].IPv4 = "172.20.20.11/24"
			plan.Management[0].IPv4Gateway = "172.20.20.1"
			plan.Management[0].Interposition = validContract()
			tt.mutate(&plan.Management[0])

			_, normalizeErr := clabernetesinternaldeviceplan.NormalizePlan(plan)
			if tt.wantErr && normalizeErr == nil {
				t.Fatal("NormalizePlan() succeeded for an invalid interposition contract")
			}

			if !tt.wantErr && normalizeErr != nil {
				t.Fatalf("NormalizePlan() error = %v", normalizeErr)
			}
		})
	}
}

func TestDecodeRejectsUnknownAndTrailingJSON(t *testing.T) {
	t.Parallel()

	inputJSON, err := validInput().CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}

	var object map[string]any
	if err = json.Unmarshal(inputJSON, &object); err != nil {
		t.Fatal(err)
	}

	object["futureField"] = true

	withUnknown, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}

	for _, raw := range [][]byte{withUnknown, append(slices.Clone(inputJSON), []byte(` {}`)...)} {
		_, decodeErr := clabernetesinternaldeviceplan.DecodeInput(raw)

		var planningErr *clabernetesinternaldeviceplan.Error
		if !errors.As(decodeErr, &planningErr) ||
			planningErr.Code != clabernetesinternaldeviceplan.ErrorSerialization {
			t.Fatalf("DecodeInput() error = %v, want ErrorSerialization", decodeErr)
		}
	}
}

func TestPlanningErrorDoesNotExposeRejectedValue(t *testing.T) {
	t.Parallel()

	input := validInput()
	input.Payloads[0].Kind = clabernetesinternaldeviceplan.PayloadKind("actual-license-secret")

	_, err := input.CanonicalJSON()
	if err == nil {
		t.Fatal("CanonicalJSON() succeeded for unsupported payload kind")
	}

	if strings.Contains(err.Error(), "actual-license-secret") {
		t.Fatalf("planning error exposed rejected value: %v", err)
	}
}

func validInput() clabernetesinternaldeviceplan.Input {
	return clabernetesinternaldeviceplan.Input{
		SchemaVersion: clabernetesinternaldeviceplan.SchemaVersion,
		TopologyName:  "test-topology",
		Compatibility: testCompatibility(),
		Nodes: []clabernetesinternaldeviceplan.NodeInput{
			{
				ID:         "node-b",
				Name:       "linecard",
				Kind:       syntheticKind,
				GroupOwner: "node-a",
				Definition: json.RawMessage(`{"kind":"future-kind","image":"example/linecard:1"}`),
			},
			{
				ID:         "node-a",
				Name:       "router",
				Kind:       syntheticKind,
				Definition: json.RawMessage(`{"kind":"future-kind","image":"example/router:1"}`),
			},
		},
		Images: []clabernetesinternaldeviceplan.ImageInput{
			{
				NodeID:          "node-b",
				SourceReference: "example/linecard:1",
				DigestReference: "example/linecard@sha256:bbbb",
				Platform: clabernetesinternaldeviceplan.Platform{
					OS: "linux", Architecture: "amd64", OSFeatures: []string{"z", "a"},
				},
				Config: clabernetesinternaldeviceplan.ImageConfig{
					Environment: []clabernetesinternaldeviceplan.KeyValue{
						{Name: "Z", Value: "last"},
						{Name: "A", Value: "first"},
					},
				},
			},
			{
				NodeID:          "node-a",
				SourceReference: "example/router:1",
				DigestReference: "example/router@sha256:aaaa",
				Platform: clabernetesinternaldeviceplan.Platform{
					OS: "linux", Architecture: "amd64", OSFeatures: []string{"z", "a"},
				},
				Config: clabernetesinternaldeviceplan.ImageConfig{
					Environment: []clabernetesinternaldeviceplan.KeyValue{
						{Name: "Z", Value: "last"},
						{Name: "A", Value: "first"},
					},
				},
			},
		},
		Payloads: []clabernetesinternaldeviceplan.PayloadInput{
			{
				ID: "payload-b", NodeID: "node-b", Kind: clabernetesinternaldeviceplan.PayloadConfigMap,
				Reference: "lab/linecard:startup.cfg", Destination: "/etc/startup.cfg",
			},
			{
				ID: "payload-a", NodeID: "node-a", Kind: clabernetesinternaldeviceplan.PayloadSecret,
				Reference: "lab/router-license:license.key", Destination: "/etc/license.key",
				Sensitive: true,
			},
		},
		Management: []clabernetesinternaldeviceplan.ManagementInput{
			{NodeID: "node-a", InterfaceName: "mgmt0", IPv4: "10.0.0.10/24"},
		},
		Interfaces: []clabernetesinternaldeviceplan.InterfaceInput{
			{
				ID: "interface-b", NodeID: "node-b", Name: "eth1", LinkID: "link-1",
				PeerNodeID: "node-a", PeerInterface: "eth1", Connectivity: "same-pod", MTU: 1500,
			},
			{
				ID: "interface-a", NodeID: "node-a", Name: "eth1", LinkID: "link-1",
				PeerNodeID: "node-b", PeerInterface: "eth1", Connectivity: "same-pod", MTU: 1500,
			},
		},
	}
}

func validPlan(inputDigest string) clabernetesinternaldeviceplan.Plan {
	return clabernetesinternaldeviceplan.Plan{
		SchemaVersion: clabernetesinternaldeviceplan.SchemaVersion,
		Compatibility: testCompatibility(),
		InputDigest:   inputDigest,
		Planner: clabernetesinternaldeviceplan.PlannerIdentity{
			Name:     "clabernetes",
			Revision: "test",
		},
		Nodes: []clabernetesinternaldeviceplan.NodePlan{
			{
				ID: "node-b", Name: "linecard", Kind: syntheticKind,
				ContainerIDs: []string{
					"container-b",
				}, ReadinessContainerIDs: []string{"container-b"},
			},
			{
				ID: "node-a", Name: "router", Kind: syntheticKind,
				ContainerIDs: []string{
					"container-a",
				}, ReadinessContainerIDs: []string{"container-a"},
			},
		},
		Containers: []clabernetesinternaldeviceplan.ContainerPlan{
			{
				ID: "container-b", NodeID: "node-b", NamespaceOwnerID: "container-a",
				Image: "example/linecard:1", Command: []string{"serve", "--foreground"},
				Environment: []clabernetesinternaldeviceplan.KeyValue{
					{Name: "Z", Value: "2"},
					{Name: "A", Value: "1"},
				},
				DNS: clabernetesinternaldeviceplan.DNSConfig{
					Servers: []string{"10.0.0.2", "10.0.0.3"},
				},
				Required: true, MountIDs: []string{"mount-b"},
			},
			{
				ID: "container-a", NodeID: "node-a", NamespaceOwnerID: "container-a",
				Image: "example/router:1", Command: []string{"serve", "--foreground"},
				Environment: []clabernetesinternaldeviceplan.KeyValue{
					{Name: "Z", Value: "2"},
					{Name: "A", Value: "1"},
				},
				DNS: clabernetesinternaldeviceplan.DNSConfig{
					Servers: []string{"10.0.0.2", "10.0.0.3"},
				},
				Required: true, MountIDs: []string{"mount-a"},
			},
		},
		Files: []clabernetesinternaldeviceplan.FilePlan{
			{
				ID: "file-b", NodeID: "node-b", SourceKind: clabernetesinternaldeviceplan.FileSourcePayload,
				SourceReference: "payload-b", ArtifactPath: "payloads/payload-b",
				Destination: "/etc/startup.cfg",
			},
			{
				ID: "file-a", NodeID: "node-a", SourceKind: clabernetesinternaldeviceplan.FileSourcePayload,
				SourceReference: "payload-a", ArtifactPath: "payloads/payload-a",
				Destination: "/etc/license.key", Sensitive: true,
			},
		},
		Volumes: []clabernetesinternaldeviceplan.VolumePlan{
			{ID: "volume-b", NodeID: "node-b", Kind: clabernetesinternaldeviceplan.VolumeArtifacts},
			{ID: "volume-a", NodeID: "node-a", Kind: clabernetesinternaldeviceplan.VolumeArtifacts},
		},
		Mounts: []clabernetesinternaldeviceplan.MountPlan{
			{ID: "mount-b", ContainerID: "container-b", VolumeID: "volume-b", Destination: "/etc"},
			{ID: "mount-a", ContainerID: "container-a", VolumeID: "volume-a", Destination: "/etc"},
		},
		Actions: []clabernetesinternaldeviceplan.Action{
			{
				ID: "post-start", Phase: clabernetesinternaldeviceplan.PhasePostStart, Order: 1,
				Target: clabernetesinternaldeviceplan.ActionTarget{
					NodeID:      "node-a",
					ContainerID: "container-a",
				},
				Kind: clabernetesinternaldeviceplan.ActionExec,
				Exec: &clabernetesinternaldeviceplan.ExecAction{
					Command: []string{"configure", "apply"},
				},
			},
			{
				ID: "prepare-file", Phase: clabernetesinternaldeviceplan.PhasePrepare, Order: 1,
				Target: clabernetesinternaldeviceplan.ActionTarget{NodeID: "node-a"},
				Kind:   clabernetesinternaldeviceplan.ActionFile,
				File:   &clabernetesinternaldeviceplan.FileAction{FileID: "file-a"},
			},
		},
		Management: []clabernetesinternaldeviceplan.ManagementPlan{
			{ID: "management-a", NodeID: "node-a", InterfaceName: "mgmt0", IPv4: "10.0.0.10/24"},
		},
		Interfaces: []clabernetesinternaldeviceplan.InterfacePlan{
			{
				ID: "interface-b", NodeID: "node-b", NamespaceOwnerID: "container-a", Name: "eth1",
				LinkID: "link-a", PeerNodeID: "node-a", PeerInterface: "eth1",
				Connectivity: "same-pod",
				MTU:          1500, LinkApplyMode: clabernetesinternaldeviceplan.LinkApplyLive, RequiredAtStart: true,
			},
			{
				ID: "interface-a", NodeID: "node-a", NamespaceOwnerID: "container-a", Name: "eth1",
				LinkID: "link-a", PeerNodeID: "node-b", PeerInterface: "eth1",
				Connectivity: "same-pod",
				MTU:          1500, LinkApplyMode: clabernetesinternaldeviceplan.LinkApplyLive, RequiredAtStart: true,
			},
		},
	}
}

func testCompatibility() clabernetesinternaldeviceplan.Compatibility {
	return clabernetesinternaldeviceplan.Compatibility{
		ContainerlabModule:  "github.com/srl-labs/containerlab",
		ContainerlabVersion: "v0.78.0",
		RegistryDigest:      "sha256:0320f230b9e54f6b5e3a0aaa8b6ee0ffe51bf834bffb7ba5d2200669ed9d7b7e",
		PlanSchemaVersion:   clabernetesinternaldeviceplan.SchemaVersion,
	}
}
