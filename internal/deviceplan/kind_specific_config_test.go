package deviceplan_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
)

// cumulusBreakoutInput builds a single-node planning input for the real nvidia_cumulusvx kind
// with kind-specific config: the port layout c9s must carry from the topology to the imported
// kind untouched.
func cumulusBreakoutInput(t *testing.T) clabernetesinternaldeviceplan.Input {
	t.Helper()

	const image = "vrnetlab/nvidia_cumulus-vx:5.16.1"

	input := singleNodeInput("nvidia_cumulusvx", image)
	input.Nodes[0].Definition = []byte(`{` +
		`"kind":"nvidia_cumulusvx",` +
		`"image":"` + image + `",` +
		`"kind-specific-config":{"port-count":4,"breakouts":[{"port":"1","channels":4}]}` +
		`}`)

	return input
}

func TestKindSpecificConfigReachesImportedKind(t *testing.T) {
	t.Parallel()

	input := cumulusBreakoutInput(t)

	plan, err := (clabernetesinternaldeviceplan.Adapter{
		Registry: clabernetesinternaldeviceplan.NewContainerlabRegistry(),
		Revision: "kind-specific-config-v1",
	}).Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	if len(plan.Nodes) != 1 {
		t.Fatalf("planned nodes = %d, want 1", len(plan.Nodes))
	}

	foundPortsConf := false
	for _, file := range plan.Files {
		if strings.HasSuffix(file.ArtifactPath, "config/ports.conf") {
			foundPortsConf = true
		}
	}

	if !foundPortsConf {
		t.Fatalf(
			"breakout layout produced no ports.conf artifact, files = %#v",
			plan.Files,
		)
	}
}

func TestKindSpecificConfigUnknownKeyIsRejected(t *testing.T) {
	t.Parallel()

	input := singleNodeInput("linux", "alpine:latest")
	input.Nodes[0].Definition = []byte(`{"kind":"linux","image":"alpine:latest",` +
		`"kind-specific-config":{"port-count":4}}`)

	_, err := (clabernetesinternaldeviceplan.Adapter{
		Registry: clabernetesinternaldeviceplan.NewContainerlabRegistry(),
		Revision: "kind-specific-config-v1",
	}).Plan(context.Background(), input)

	var planningErr *clabernetesinternaldeviceplan.Error
	if !errors.As(err, &planningErr) ||
		planningErr.Code != clabernetesinternaldeviceplan.ErrorInvalidInput ||
		planningErr.Field != "definition.kind-specific-config" {
		t.Fatalf("kind-specific config error = %#v, %v", planningErr, err)
	}

	if !strings.Contains(err.Error(), `kind "linux" does not support key "port-count"`) {
		t.Fatalf("unknown key error = %s", err)
	}
}

func TestKindSpecificConfigInvalidValueIsRejected(t *testing.T) {
	t.Parallel()

	input := cumulusBreakoutInput(t)
	input.Nodes[0].Definition = []byte(`{` +
		`"kind":"nvidia_cumulusvx",` +
		`"image":"vrnetlab/nvidia_cumulus-vx:5.16.1",` +
		`"kind-specific-config":{"port-count":"many","breakouts":[{"port":"1","channels":4}]}` +
		`}`)

	_, err := (clabernetesinternaldeviceplan.Adapter{
		Registry: clabernetesinternaldeviceplan.NewContainerlabRegistry(),
		Revision: "kind-specific-config-v1",
	}).Plan(context.Background(), input)

	var planningErr *clabernetesinternaldeviceplan.Error
	if !errors.As(err, &planningErr) ||
		planningErr.Code != clabernetesinternaldeviceplan.ErrorInvalidInput {
		t.Fatalf("kind-specific config error = %#v, %v", planningErr, err)
	}

	if !strings.Contains(err.Error(), "cannot unmarshal !!str `many` into int") {
		t.Fatalf("mistyped value error = %s", err)
	}
}

func TestKindSpecificConfigReachesNokiaSrsimKind(t *testing.T) {
	t.Parallel()

	const image = "nokia_srsim:25.10.R1"

	input := singleNodeInput("nokia_srsim", image)
	input.Nodes[0].Type = "sr-1-92s"
	input.Nodes[0].Definition = []byte(`{` +
		`"kind":"nokia_srsim","type":"sr-1-92s","image":"` + image + `",` +
		`"kind-specific-config":{"config-mode":"classic","gen-component-config":false}` +
		`}`)

	plan, err := (clabernetesinternaldeviceplan.Adapter{
		Registry: clabernetesinternaldeviceplan.NewContainerlabRegistry(),
		Revision: "kind-specific-config-v1",
	}).Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	if len(plan.Nodes) != 1 {
		t.Fatalf("planned nodes = %d, want 1", len(plan.Nodes))
	}

	// The sros kind translates config-mode into its generated configuration template, not into
	// a container environment variable: the classic template is what the plan must carry.
	found := false
	for _, file := range plan.Files {
		if file.SourceKind == clabernetesinternaldeviceplan.FileSourceGenerator {
			found = true
		}
	}

	if !found {
		t.Fatalf("the srsim kind generated no configuration artifacts; files = %#v", plan.Files)
	}
}
