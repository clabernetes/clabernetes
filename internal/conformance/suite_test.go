package conformance_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	clabernetesinternalconformance "github.com/clabernetes/clabernetes/internal/conformance"
	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
)

func TestSuiteInventoryAcceptsEveryLiveRegistryNameWithoutExpectedKindRows(t *testing.T) {
	registry := clabernetesinternaldeviceplan.NewContainerlabRegistry()

	kinds := registry.GetRegisteredNodeKindNames()
	if len(kinds) == 0 {
		t.Fatal("live imported registry is empty")
	}

	var manifests strings.Builder

	coveredNodes := make([]string, 0, len(kinds))

	for index, kind := range kinds {
		nodeName := fmt.Sprintf("node-%03d", index)

		fmt.Fprintf(
			&manifests,
			`---
apiVersion: c9s.run/v1alpha1
kind: Node
metadata:
  name: node-%03d
spec:
  kind: %s
  image: registry.invalid/live-registry:%03d
`,
			index,
			kind,
			index,
		)

		coveredNodes = append(coveredNodes, nodeName)
	}

	suitePath := writeSuite(t, fmt.Sprintf(`
schemaVersion: v1alpha1
scenarios:
  - id: live-registry
    availability: obtainable
    timeout: 2m
    pollInterval: 1s
    manifest: |
%s
    management:
      - name: management
        nodes: [%s]
        target: deployment/probe
        command: [probe, management]
    dataplane:
      - name: dataplane
        nodes: [%s]
        target: deployment/probe
        command: [probe, dataplane]
`, indent(manifests.String(), 6), strings.Join(coveredNodes, ", "), strings.Join(coveredNodes, ", ")))

	suite, inventories, err := clabernetesinternalconformance.LoadSuite(suitePath, registry)
	if err != nil {
		t.Fatal(err)
	}

	if len(suite.Scenarios) != 1 || len(inventories) != 1 ||
		len(inventories[0].Images) != len(kinds) {
		t.Fatalf("suite=%#v inventories=%#v", suite, inventories)
	}

	for index, image := range inventories[0].Images {
		if registry.Kind(image.Kind) == nil || image.Node != fmt.Sprintf("node-%03d", index) {
			t.Fatalf("inventory[%d] = %#v", index, image)
		}
	}
}

func TestSuiteRejectsUnknownFieldsAndUnrepresentableTopology(t *testing.T) {
	registry := clabernetesinternaldeviceplan.NewContainerlabRegistry()
	kind := registry.GetRegisteredNodeKindNames()[0]

	testCases := []struct {
		name    string
		content string
	}{
		{
			name: "unknown suite field",
			content: `
schemaVersion: v1alpha1
unknown: true
scenarios: []
`,
		},
		{
			name: "unrepresentable topology",
			content: fmt.Sprintf(`
schemaVersion: v1alpha1
scenarios:
  - id: invalid-topology
    availability: obtainable
    manifest: |
      apiVersion: c9s.run/v1alpha1
      kind: Topology
      metadata:
        name: invalid
      spec:
        definition:
          containerlab: |
            name: invalid
            topology:
              nodes:
                n1:
                  kind: %s
                  image: example.invalid/device:1
                  unsupported-input: true
    management:
      - name: management
        nodes: [invalid]
        target: deployment/probe
        command: [probe]
    dataplane:
      - name: dataplane
        nodes: [invalid]
        target: deployment/probe
        command: [probe]
`, kind),
		},
		{
			name: "unknown direct Node field",
			content: fmt.Sprintf(`
schemaVersion: v1alpha1
scenarios:
  - id: invalid-node
    availability: obtainable
    manifest: |
      apiVersion: c9s.run/v1alpha1
      kind: Node
      metadata:
        name: invalid
      spec:
        kind: %s
        image: example.invalid/device:1
        package-owned-future-field: true
    management:
      - name: management
        nodes: [invalid]
        target: deployment/probe
        command: [probe]
    dataplane:
      - name: dataplane
        nodes: [invalid]
        target: deployment/probe
        command: [probe]
`, kind),
		},
		{
			name: "duplicate Node identity",
			content: fmt.Sprintf(`
schemaVersion: v1alpha1
scenarios:
  - id: duplicate-node
    availability: obtainable
    manifest: |
      apiVersion: c9s.run/v1alpha1
      kind: Node
      metadata:
        name: duplicate
      spec:
        kind: %s
        image: example.invalid/device:1
      ---
      apiVersion: c9s.run/v1alpha1
      kind: Node
      metadata:
        name: duplicate
      spec:
        kind: %s
        image: example.invalid/device:2
    management:
      - name: management
        nodes: [duplicate]
        target: deployment/probe
        command: [probe]
    dataplane:
      - name: dataplane
        nodes: [duplicate]
        target: deployment/probe
        command: [probe]
`, kind, kind),
		},
		{
			name: "unknown observation coverage",
			content: fmt.Sprintf(`
schemaVersion: v1alpha1
scenarios:
  - id: unknown-coverage
    availability: obtainable
    manifest: |
      apiVersion: c9s.run/v1alpha1
      kind: Node
      metadata:
        name: device
      spec:
        kind: %s
        image: example.invalid/device:1
    management:
      - name: management
        nodes: [not-device]
        target: deployment/device
        command: [probe]
    dataplane:
      - name: dataplane
        nodes: [device]
        target: deployment/device
        command: [probe]
`, kind),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, _, err := clabernetesinternalconformance.LoadSuite(
				writeSuite(t, testCase.content),
				registry,
			)
			if err == nil {
				t.Fatal("LoadSuite() accepted invalid conformance input")
			}
		})
	}
}

func writeSuite(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "suite.yaml")

	err := os.WriteFile(path, []byte(content), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	return path
}

func indent(value string, spaces int) string {
	prefix := strings.Repeat(" ", spaces)

	lines := strings.Split(strings.TrimSuffix(value, "\n"), "\n")
	for index := range lines {
		lines[index] = prefix + lines[index]
	}

	return strings.Join(lines, "\n")
}
