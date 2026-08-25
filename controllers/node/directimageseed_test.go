package node

import (
	"strings"
	"testing"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
)

func TestDeclaredTopologyImagesMatchCold(t *testing.T) {
	t.Parallel()

	nodeID := "node-a"

	declared := []clabernetesinternaldeviceplan.ImageInput{{
		NodeID:          nodeID,
		SourceReference: "ghcr.io/example/device:1",
		DigestReference: "ghcr.io/example/device@sha256:" + strings.Repeat("a", 64),
	}}
	cold := []clabernetesinternaldeviceplan.ImageInput{{
		NodeID:          nodeID,
		Role:            "image",
		SourceReference: "ghcr.io/example/device:1",
		DigestReference: "ghcr.io/example/device@sha256:" + strings.Repeat("a", 64),
	}}

	if !declaredTopologyImagesMatchCold(declared, cold) {
		t.Fatal("expected topology-declared image to match cold input")
	}

	changed := []clabernetesinternaldeviceplan.ImageInput{{
		NodeID:          nodeID,
		SourceReference: "ghcr.io/example/device:2",
		DigestReference: "ghcr.io/example/device@sha256:" + strings.Repeat("b", 64),
	}}
	if declaredTopologyImagesMatchCold(changed, cold) {
		t.Fatal("expected changed topology image to reject cold input")
	}
}
