package deviceplan_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	clabernetesdeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
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
	if digest != clabernetesdeviceplan.Digest(leftJSON) {
		t.Fatalf("input digest = %q, want digest of canonical JSON", digest)
	}

	decoded, err := clabernetesdeviceplan.DecodeInput(leftJSON)
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

	decoded, err := clabernetesdeviceplan.DecodePlan(leftJSON)
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
	if got := decoded.Actions; got[0].Phase != clabernetesdeviceplan.PhasePrepare ||
		got[1].Phase != clabernetesdeviceplan.PhasePostStart {
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
		mutate func(*clabernetesdeviceplan.Plan)
		code   clabernetesdeviceplan.ErrorCode
		field  string
	}{
		{
			name: "unknown container reference",
			mutate: func(plan *clabernetesdeviceplan.Plan) {
				plan.Nodes[0].ContainerIDs = []string{"missing"}
			},
			code:  clabernetesdeviceplan.ErrorInvariant,
			field: "nodes[0].containerIDs",
		},
		{
			name: "mismatched action payload",
			mutate: func(plan *clabernetesdeviceplan.Plan) {
				plan.Actions[0].Kind = clabernetesdeviceplan.ActionFile
			},
			code:  clabernetesdeviceplan.ErrorInvariant,
			field: "actions[0]",
		},
		{
			name: "unknown action kind",
			mutate: func(plan *clabernetesdeviceplan.Plan) {
				plan.Actions[0].Kind = "RunAnything"
			},
			code:  clabernetesdeviceplan.ErrorUnsupported,
			field: "actions[0].kind",
		},
		{
			name: "bad input digest",
			mutate: func(plan *clabernetesdeviceplan.Plan) {
				plan.InputDigest = "latest"
			},
			code:  clabernetesdeviceplan.ErrorInvalidInput,
			field: "inputDigest",
		},
		{
			name: "invalid container port",
			mutate: func(plan *clabernetesdeviceplan.Plan) {
				plan.Containers[0].Ports = []clabernetesdeviceplan.Port{
					{Number: 22, Protocol: "SCTP"},
				}
			},
			code:  clabernetesdeviceplan.ErrorInvalidInput,
			field: "containers[0].ports[0]",
		},
		{
			name: "invalid image digest",
			mutate: func(plan *clabernetesdeviceplan.Plan) {
				plan.Containers[0].ImageDigest = "sha256:not-an-immutable-digest"
			},
			code:  clabernetesdeviceplan.ErrorInvalidInput,
			field: "containers[0].imageDigest",
		},
		{
			name: "invalid file ownership",
			mutate: func(plan *clabernetesdeviceplan.Plan) {
				invalid := int64(-1)
				plan.Files[0].UID = &invalid
			},
			code:  clabernetesdeviceplan.ErrorInvalidInput,
			field: "files[0]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			plan := validPlan(inputDigest)
			tt.mutate(&plan)
			_, normalizeErr := clabernetesdeviceplan.NormalizePlan(plan)
			var planningErr *clabernetesdeviceplan.Error
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
		interfaceSelector clabernetesdeviceplan.ManagementInterfaceSelector
		wantErr           bool
	}{
		{name: "package name", interfaceName: "mgmt0"},
		{
			name:              "pod transport selector",
			interfaceSelector: clabernetesdeviceplan.ManagementInterfacePodTransport,
		},
		{
			name:              "both selections",
			interfaceName:     "mgmt0",
			interfaceSelector: clabernetesdeviceplan.ManagementInterfacePodTransport,
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

			_, normalizeErr := clabernetesdeviceplan.NormalizePlan(plan)
			if tt.wantErr && normalizeErr == nil {
				t.Fatal("NormalizePlan() succeeded for an invalid interface selection")
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
		_, decodeErr := clabernetesdeviceplan.DecodeInput(raw)
		var planningErr *clabernetesdeviceplan.Error
		if !errors.As(decodeErr, &planningErr) ||
			planningErr.Code != clabernetesdeviceplan.ErrorSerialization {
			t.Fatalf("DecodeInput() error = %v, want ErrorSerialization", decodeErr)
		}
	}
}

func TestPlanningErrorDoesNotExposeRejectedValue(t *testing.T) {
	t.Parallel()

	input := validInput()
	input.Payloads[0].Kind = clabernetesdeviceplan.PayloadKind("actual-license-secret")
	_, err := input.CanonicalJSON()
	if err == nil {
		t.Fatal("CanonicalJSON() succeeded for unsupported payload kind")
	}
	if strings.Contains(err.Error(), "actual-license-secret") {
		t.Fatalf("planning error exposed rejected value: %v", err)
	}
}

func validInput() clabernetesdeviceplan.Input {
	return clabernetesdeviceplan.Input{
		SchemaVersion: clabernetesdeviceplan.SchemaVersion,
		TopologyName:  "test-topology",
		Compatibility: testCompatibility(),
		Nodes: []clabernetesdeviceplan.NodeInput{
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
		Images: []clabernetesdeviceplan.ImageInput{
			{
				NodeID:          "node-b",
				SourceReference: "example/linecard:1",
				DigestReference: "example/linecard@sha256:bbbb",
				Platform: clabernetesdeviceplan.Platform{
					OS: "linux", Architecture: "amd64", OSFeatures: []string{"z", "a"},
				},
				Config: clabernetesdeviceplan.ImageConfig{
					Environment: []clabernetesdeviceplan.KeyValue{
						{Name: "Z", Value: "last"},
						{Name: "A", Value: "first"},
					},
				},
			},
			{
				NodeID:          "node-a",
				SourceReference: "example/router:1",
				DigestReference: "example/router@sha256:aaaa",
				Platform: clabernetesdeviceplan.Platform{
					OS: "linux", Architecture: "amd64", OSFeatures: []string{"z", "a"},
				},
				Config: clabernetesdeviceplan.ImageConfig{
					Environment: []clabernetesdeviceplan.KeyValue{
						{Name: "Z", Value: "last"},
						{Name: "A", Value: "first"},
					},
				},
			},
		},
		Payloads: []clabernetesdeviceplan.PayloadInput{
			{
				ID: "payload-b", NodeID: "node-b", Kind: clabernetesdeviceplan.PayloadConfigMap,
				Reference: "lab/linecard:startup.cfg", Destination: "/etc/startup.cfg",
			},
			{
				ID: "payload-a", NodeID: "node-a", Kind: clabernetesdeviceplan.PayloadSecret,
				Reference: "lab/router-license:license.key", Destination: "/etc/license.key",
				Sensitive: true,
			},
		},
		Management: []clabernetesdeviceplan.ManagementInput{
			{NodeID: "node-a", InterfaceName: "mgmt0", IPv4: "10.0.0.10/24"},
		},
		Interfaces: []clabernetesdeviceplan.InterfaceInput{
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

//nolint:funlen // A complete plan fixture makes schema references explicit.
func validPlan(inputDigest string) clabernetesdeviceplan.Plan {
	return clabernetesdeviceplan.Plan{
		SchemaVersion: clabernetesdeviceplan.SchemaVersion,
		Compatibility: testCompatibility(),
		InputDigest:   inputDigest,
		Planner:       clabernetesdeviceplan.PlannerIdentity{Name: "clabernetes", Revision: "test"},
		Nodes: []clabernetesdeviceplan.NodePlan{
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
		Containers: []clabernetesdeviceplan.ContainerPlan{
			{
				ID: "container-b", NodeID: "node-b", NamespaceOwnerID: "container-a",
				Image: "example/linecard:1", Command: []string{"serve", "--foreground"},
				Environment: []clabernetesdeviceplan.KeyValue{
					{Name: "Z", Value: "2"},
					{Name: "A", Value: "1"},
				},
				DNS: clabernetesdeviceplan.DNSConfig{
					Servers: []string{"10.0.0.2", "10.0.0.3"},
				},
				Required: true, MountIDs: []string{"mount-b"},
			},
			{
				ID: "container-a", NodeID: "node-a", NamespaceOwnerID: "container-a",
				Image: "example/router:1", Command: []string{"serve", "--foreground"},
				Environment: []clabernetesdeviceplan.KeyValue{
					{Name: "Z", Value: "2"},
					{Name: "A", Value: "1"},
				},
				DNS: clabernetesdeviceplan.DNSConfig{
					Servers: []string{"10.0.0.2", "10.0.0.3"},
				},
				Required: true, MountIDs: []string{"mount-a"},
			},
		},
		Files: []clabernetesdeviceplan.FilePlan{
			{
				ID: "file-b", NodeID: "node-b", SourceKind: clabernetesdeviceplan.FileSourcePayload,
				SourceReference: "payload-b", ArtifactPath: "payloads/payload-b",
				Destination: "/etc/startup.cfg",
			},
			{
				ID: "file-a", NodeID: "node-a", SourceKind: clabernetesdeviceplan.FileSourcePayload,
				SourceReference: "payload-a", ArtifactPath: "payloads/payload-a",
				Destination: "/etc/license.key", Sensitive: true,
			},
		},
		Volumes: []clabernetesdeviceplan.VolumePlan{
			{ID: "volume-b", NodeID: "node-b", Kind: clabernetesdeviceplan.VolumeArtifacts},
			{ID: "volume-a", NodeID: "node-a", Kind: clabernetesdeviceplan.VolumeArtifacts},
		},
		Mounts: []clabernetesdeviceplan.MountPlan{
			{ID: "mount-b", ContainerID: "container-b", VolumeID: "volume-b", Destination: "/etc"},
			{ID: "mount-a", ContainerID: "container-a", VolumeID: "volume-a", Destination: "/etc"},
		},
		Actions: []clabernetesdeviceplan.Action{
			{
				ID: "post-start", Phase: clabernetesdeviceplan.PhasePostStart, Order: 1,
				Target: clabernetesdeviceplan.ActionTarget{
					NodeID:      "node-a",
					ContainerID: "container-a",
				},
				Kind: clabernetesdeviceplan.ActionExec,
				Exec: &clabernetesdeviceplan.ExecAction{Command: []string{"configure", "apply"}},
			},
			{
				ID: "prepare-file", Phase: clabernetesdeviceplan.PhasePrepare, Order: 1,
				Target: clabernetesdeviceplan.ActionTarget{NodeID: "node-a"},
				Kind:   clabernetesdeviceplan.ActionFile,
				File:   &clabernetesdeviceplan.FileAction{FileID: "file-a"},
			},
		},
		Management: []clabernetesdeviceplan.ManagementPlan{
			{ID: "management-a", NodeID: "node-a", InterfaceName: "mgmt0", IPv4: "10.0.0.10/24"},
		},
		Interfaces: []clabernetesdeviceplan.InterfacePlan{
			{
				ID: "interface-b", NodeID: "node-b", NamespaceOwnerID: "container-a", Name: "eth1",
				LinkID: "link-a", PeerNodeID: "node-a", PeerInterface: "eth1",
				Connectivity: "same-pod",
				MTU:          1500, LinkApplyMode: clabernetesdeviceplan.LinkApplyLive, RequiredAtStart: true,
			},
			{
				ID: "interface-a", NodeID: "node-a", NamespaceOwnerID: "container-a", Name: "eth1",
				LinkID: "link-a", PeerNodeID: "node-b", PeerInterface: "eth1",
				Connectivity: "same-pod",
				MTU:          1500, LinkApplyMode: clabernetesdeviceplan.LinkApplyLive, RequiredAtStart: true,
			},
		},
	}
}

func testCompatibility() clabernetesdeviceplan.Compatibility {
	return clabernetesdeviceplan.Compatibility{
		ContainerlabModule:  "github.com/srl-labs/containerlab",
		ContainerlabVersion: "v0.78.0",
		RegistryDigest:      "sha256:0320f230b9e54f6b5e3a0aaa8b6ee0ffe51bf834bffb7ba5d2200669ed9d7b7e",
		PlanSchemaVersion:   clabernetesdeviceplan.SchemaVersion,
	}
}
