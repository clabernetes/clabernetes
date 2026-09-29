package node

import (
	"bytes"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
)

// declaredNodeText collects what a user declares in plain text for every Node of a compiled
// planning input: the name, kind, type, and containerlab definition. All of it is readable on
// the Node and Topology objects before any artifact is generated, so nothing in it can be a
// secret the artifact guards need to keep out of a ConfigMap.
func declaredNodeText(input clabernetesinternaldeviceplan.Input) [][]byte {
	result := make([][]byte, 0, len(input.Nodes))

	for _, node := range input.Nodes {
		for _, text := range [][]byte{
			[]byte(node.Name),
			[]byte(node.Kind),
			[]byte(node.Type),
			node.Definition,
		} {
			if len(text) != 0 {
				result = append(result, text)
			}
		}
	}

	return result
}

// screenSensitiveValues merges sensitive value sets, dropping empty values and values already
// present in text known to be public independently of the sensitive source. Callers may use
// declared Node text, or for probe passwords the planner artifacts finalized before probe
// resolution. Other Secret sources must still be screened against the artifacts.
func screenSensitiveValues(publicText [][]byte, sets ...[][]byte) [][]byte {
	result := [][]byte{}

	for _, set := range sets {
		for _, value := range set {
			if len(value) == 0 || publicTextContains(publicText, value) {
				continue
			}

			result = append(result, value)
		}
	}

	return result
}

func publicTextContains(publicText [][]byte, value []byte) bool {
	for _, text := range publicText {
		if bytes.Contains(text, value) {
			return true
		}
	}

	return false
}
