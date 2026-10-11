package deviceplan

import (
	"context"
	"strings"
	"testing"
)

// internalSingleNodeInput builds one single-node planning input with the live registry identity,
// mirroring the external test helper, for the real imported kinds under test here.
func internalSingleNodeInput(t *testing.T, kind, image, definition string) Input {
	t.Helper()

	compatibility, err := LiveCompatibility(NewContainerlabRegistry())
	if err != nil {
		t.Fatal(err)
	}

	return Input{
		SchemaVersion: SchemaVersion,
		TopologyName:  "test-topology",
		Compatibility: compatibility,
		Nodes: []NodeInput{{
			ID:         "node-a",
			Name:       "router",
			Kind:       kind,
			Type:       "sr-1-92s",
			Definition: []byte(definition),
		}},
		Images: []ImageInput{{
			NodeID: "node-a", SourceReference: image,
			DigestReference: image + "@sha256:" + strings.Repeat("a", 64),
			Platform:        Platform{OS: "linux", Architecture: "amd64"},
		}},
	}
}

// srsimClassicConfigDefinition builds an sr-1-92s definition whose kind-specific config selects
// the classic configuration mode.
func srsimClassicConfigDefinition(image string) string {
	return `{"kind":"nokia_srsim","type":"sr-1-92s","image":"` + image + `",` +
		`"kind-specific-config":{"config-mode":"classic","gen-component-config":false}}`
}

// srsimConfigArtifactPath is the generated configuration artifact whose content the config-mode
// kind-specific config changes.
const srsimConfigArtifactPath = "A/config/cf3/config.cfg"

// TestKindSpecificConfigSelectsSrsimClassicTemplate proves the nokia_srsim kind-specific
// config reaches the imported kind: `config-mode: classic` changes the generated configuration
// artifact relative to the model-driven default, which is what the kind does with the mode.
func TestKindSpecificConfigSelectsSrsimClassicTemplate(t *testing.T) {
	t.Parallel()

	const image = "nokia_srsim:25.10.R1"

	classicDigest := srsimConfigDigest(
		t,
		srsimClassicConfigDefinition(image),
		image,
	)
	defaultDigest := srsimConfigDigest(
		t,
		`{"kind":"nokia_srsim","type":"sr-1-92s","image":"`+image+`"}`,
		image,
	)

	if classicDigest == "" {
		t.Fatal("the classic-mode evaluation produced no configuration artifact")
	}

	if defaultDigest == "" {
		t.Fatal("the model-driven evaluation produced no configuration artifact")
	}

	if classicDigest == defaultDigest {
		t.Fatal(
			"config-mode classic did not change the generated configuration; " +
				"the kind-specific config did not reach the imported kind",
		)
	}
}

// srsimConfigDigest evaluates one srsim definition and returns the digest of its generated
// configuration artifact.
func srsimConfigDigest(t *testing.T, definition, image string) string {
	t.Helper()

	evaluation, err := (Adapter{
		Registry: NewContainerlabRegistry(),
		Revision: "kind-specific-config-v1",
	}).Evaluate(
		context.Background(),
		internalSingleNodeInput(t, "nokia_srsim", image, definition),
	)
	if err != nil {
		t.Fatal(err)
	}

	for _, artifact := range evaluation.Nodes[0].GeneratedArtifacts {
		if artifact.Path == srsimConfigArtifactPath {
			return artifact.Digest
		}
	}

	return ""
}
