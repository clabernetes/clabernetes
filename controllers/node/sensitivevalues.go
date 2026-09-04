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

// screenSensitiveValues merges sensitive value sets, dropping empty values and every value that
// already appears verbatim in the declared Node text. The artifact guards reject any plan or
// planner input that contains a sensitive value; a value the user also wrote into a definition
// (an SSH probe password that doubles as the device username, for example) would otherwise
// reject every plan for that Node while protecting nothing, because the definition is stored in
// plain text anyway. Values absent from the declared text keep their full protection.
func screenSensitiveValues(declared [][]byte, sets ...[][]byte) [][]byte {
	result := [][]byte{}

	for _, set := range sets {
		for _, value := range set {
			if len(value) == 0 || declaredTextContains(declared, value) {
				continue
			}

			result = append(result, value)
		}
	}

	return result
}

func declaredTextContains(declared [][]byte, value []byte) bool {
	for _, text := range declared {
		if bytes.Contains(text, value) {
			return true
		}
	}

	return false
}
