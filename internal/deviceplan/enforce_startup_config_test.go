package deviceplan_test

import (
	"context"
	"strings"
	"testing"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
)

func enforceStartupConfigInput(definition string) clabernetesinternaldeviceplan.Input {
	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Nodes[0].Definition = []byte(definition)

	return input
}

func TestPlanRecordsEnforceStartupConfig(t *testing.T) {
	t.Parallel()

	adapter := clabernetesinternaldeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "planner-v1",
	}

	input := enforceStartupConfigInput(
		`{"kind":"` + syntheticKind + `","image":"example/future:1",` +
			`"startup-config":"set system name router\nset system location lab\n",` +
			`"enforce-startup-config":true}`,
	)

	plan, err := adapter.Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	if len(plan.Nodes) != 1 || !plan.Nodes[0].EnforceStartupConfig {
		t.Fatalf("plan did not record enforce-startup-config: %#v", plan.Nodes)
	}

	// The recorded flag must survive normalization round trips.
	normalized, err := clabernetesinternaldeviceplan.NormalizePlan(*plan)
	if err != nil {
		t.Fatal(err)
	}

	if !normalized.Nodes[0].EnforceStartupConfig {
		t.Fatal("normalization dropped enforce-startup-config")
	}
}

func TestPlanRejectsEnforceWithoutStartupConfig(t *testing.T) {
	t.Parallel()

	adapter := clabernetesinternaldeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "planner-v1",
	}

	input := enforceStartupConfigInput(
		`{"kind":"` + syntheticKind + `","image":"example/future:1",` +
			`"enforce-startup-config":true}`,
	)

	_, err := adapter.Plan(context.Background(), input)
	if err == nil {
		t.Fatal("planning accepted enforce-startup-config without a startup configuration")
	}

	if !strings.Contains(err.Error(), "enforce-startup-config") {
		t.Fatalf("planning error does not identify the field: %v", err)
	}
}

func TestPlanRejectsEnforceWithSuppressStartupConfig(t *testing.T) {
	t.Parallel()

	adapter := clabernetesinternaldeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "planner-v1",
	}

	input := enforceStartupConfigInput(
		`{"kind":"` + syntheticKind + `","image":"example/future:1",` +
			`"startup-config":"set system name router\n",` +
			`"enforce-startup-config":true,"suppress-startup-config":true}`,
	)

	_, err := adapter.Plan(context.Background(), input)
	if err == nil {
		t.Fatal("planning accepted enforce with suppress-startup-config")
	}

	if !strings.Contains(err.Error(), "suppress-startup-config") {
		t.Fatalf("planning error does not identify the conflict: %v", err)
	}
}

func TestPlanOmitsEnforceByDefault(t *testing.T) {
	t.Parallel()

	adapter := clabernetesinternaldeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "planner-v1",
	}

	plan, err := adapter.Plan(
		context.Background(),
		singleNodeInput(syntheticKind, "example/future:1"),
	)
	if err != nil {
		t.Fatal(err)
	}

	if plan.Nodes[0].EnforceStartupConfig {
		t.Fatal("plan recorded enforce-startup-config without a declaration")
	}
}
