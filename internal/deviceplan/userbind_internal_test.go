package deviceplan

import (
	"strings"
	"testing"
)

func userBindTestNode() *EvaluatedNode {
	return &EvaluatedNode{Input: NodeInput{ID: "node-a"}}
}

func TestAppendUserBindPlanMountsPayloadBackedFile(t *testing.T) {
	t.Parallel()

	plan := &Plan{}
	payloads := []PayloadInput{
		{ID: "payload-1", NodeID: "node-a", Destination: "/configs/gnmic/gnmic-config.yml"},
	}

	err := appendUserBindPlan(
		plan,
		userBindTestNode(),
		payloads,
		"container-1",
		0,
		"configs/gnmic/gnmic-config.yml:/gnmic-config.yml:ro",
	)
	if err != nil {
		t.Fatalf("appendUserBindPlan() error = %v", err)
	}

	if len(plan.Mounts) != 1 || plan.Mounts[0].Destination != "/gnmic-config.yml" ||
		plan.Mounts[0].SourcePath != "payloads/payload-1" || !plan.Mounts[0].ReadOnly {
		t.Fatalf("payload-backed bind mount = %#v", plan.Mounts)
	}
}

func TestAppendUserBindPlanMountsDirectoryPayloads(t *testing.T) {
	t.Parallel()

	plan := &Plan{}
	payloads := []PayloadInput{
		{ID: "payload-1", NodeID: "node-a", Destination: "/configs/loki/loki-config.yml"},
		{ID: "payload-2", NodeID: "node-a", Destination: "/configs/loki/runtime.yml"},
		{ID: "payload-3", NodeID: "node-b", Destination: "/configs/loki/other.yml"},
	}

	err := appendUserBindPlan(
		plan,
		userBindTestNode(),
		payloads,
		"container-1",
		1,
		"configs/loki:/etc/loki",
	)
	if err != nil {
		t.Fatalf("appendUserBindPlan() error = %v", err)
	}

	if len(plan.Mounts) != 2 {
		t.Fatalf("directory bind mounts = %#v", plan.Mounts)
	}

	for _, mount := range plan.Mounts {
		if !strings.HasPrefix(mount.Destination, "/etc/loki/") {
			t.Fatalf("directory bind destination = %q", mount.Destination)
		}
	}
}

func TestAppendUserBindPlanFailsClosedWithoutPayloadBacking(t *testing.T) {
	t.Parallel()

	err := appendUserBindPlan(
		&Plan{},
		userBindTestNode(),
		nil,
		"container-1",
		0,
		"configs/unknown.yml:/unknown.yml",
	)
	if err == nil {
		t.Fatal("expected unbacked user bind to fail closed")
	}
}
