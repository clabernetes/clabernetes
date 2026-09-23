package deviceplan

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestPoolBootstrapBoundsAndPreservesFollowingStream(t *testing.T) {
	var stream bytes.Buffer
	want := PoolBootstrap{Entropy: []byte("private-material")}
	if err := WritePoolBootstrap(&stream, want); err != nil {
		t.Fatal(err)
	}
	stream.WriteString("session follows")
	got, err := readPoolBootstrap(&stream)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Entropy, want.Entropy) || stream.String() != "session follows" {
		t.Fatal("bootstrap consumed or changed the session stream")
	}
	stream.Reset()
	if err = binary.Write(&stream, binary.BigEndian, uint64(maxPoolBootstrapBytes+1)); err != nil {
		t.Fatal(err)
	}
	if _, err = readPoolBootstrap(&stream); err == nil {
		t.Fatal("accepted oversized bootstrap")
	}
}

func TestPoolBootstrapRejectsUnboundMaterial(t *testing.T) {
	bootstrap := testPoolBootstrap()
	content := bootstrap.Payloads["startup"]
	root := t.TempDir()
	if err := stagePoolBootstrap(root, bootstrap); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "payloads", ArtifactNodeDirectory("startup"), "source")
	got, err := os.ReadFile(path) //nolint:gosec // Read the payload under t.TempDir.
	if err != nil || !bytes.Equal(got, content) {
		t.Fatal("payload not staged in its private directory")
	}
	bootstrap.Payloads["startup"] = []byte("changed")
	if err = stagePoolBootstrap(t.TempDir(), bootstrap); err == nil {
		t.Fatal("accepted payload with wrong digest")
	}
	bootstrap.Payloads["startup"] = content
	bootstrap.Payloads["undeclared"] = content
	if err = stagePoolBootstrap(t.TempDir(), bootstrap); err == nil {
		t.Fatal("accepted undeclared payload")
	}
	delete(bootstrap.Payloads, "undeclared")
	bootstrap.Entropy = bytes.Repeat([]byte{43}, EntropySeedBytes)
	if err = stagePoolBootstrap(t.TempDir(), bootstrap); err == nil {
		t.Fatal("accepted wrong request entropy")
	}
}

func testPoolBootstrap() PoolBootstrap {
	seed := bytes.Repeat([]byte{42}, EntropySeedBytes)
	content := []byte("private startup config")
	input := Input{
		SchemaVersion: SchemaVersion, TopologyName: "lab", Compatibility: Compatibility{
			ContainerlabModule: "github.com/srl-labs/containerlab", ContainerlabVersion: "v0.79.0", RegistryDigest: Digest([]byte("registry")), PlanSchemaVersion: SchemaVersion,
		},
		EntropyDigest: Digest(
			seed,
		), Nodes: []NodeInput{{ID: "node", Name: "router", Kind: "linux", Definition: []byte(`{"kind":"linux","image":"alpine:latest"}`)}},
		Payloads: []PayloadInput{
			{
				ID:          "startup",
				NodeID:      "node",
				Kind:        PayloadSecret,
				Reference:   "lab/config:value",
				Digest:      Digest(content),
				Destination: "/config",
				Mode:        0o600,
				Sensitive:   true,
			},
		},
	}

	return PoolBootstrap{
		Input:    input,
		Entropy:  seed,
		Payloads: map[string][]byte{"startup": content},
	}
}
