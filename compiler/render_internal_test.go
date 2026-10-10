package compiler

import (
	"strings"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
)

// TestDecodeManifestReturnsContractError pins the decode failure contract: a manifest the c9s
// vocabulary cannot carry must surface as an error carrying the decode-failure message -- not a
// panic and not a silently zero-valued object -- so the reconcile path reports it through the
// same structured failure handling as compile failures.
func TestDecodeManifestReturnsContractError(t *testing.T) {
	err := decodeManifest(
		map[string]any{"spec": "not-a-node-spec"},
		&clabernetesapisv1alpha1.Node{},
	)

	if err == nil {
		t.Fatal("expected an error decoding an incompatible manifest")
	}

	if !strings.Contains(err.Error(), "decoding compiled manifest") {
		t.Fatalf("expected the decode contract message, got %q", err.Error())
	}
}
