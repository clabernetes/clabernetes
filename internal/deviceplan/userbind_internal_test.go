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

func TestAppendUserBindPlanRejectsReservedTarget(t *testing.T) {
	t.Parallel()

	err := appendUserBindPlan(
		&Plan{},
		userBindTestNode(),
		[]PayloadInput{{ID: "payload-1", NodeID: "node-a", Destination: "/etc/hosts"}},
		"container-1",
		0,
		"/etc/hosts:/etc/hosts:ro",
	)
	if err == nil || !strings.Contains(err.Error(), "/etc/hosts") {
		t.Fatalf("reserved bind target error = %v", err)
	}
}

func TestAppendPayloadPlansRejectsReservedDestination(t *testing.T) {
	t.Parallel()

	err := appendPayloadPlans(
		&Plan{},
		userBindTestNode(),
		[]PayloadInput{{ID: "payload-1", NodeID: "node-a", Destination: "/etc/hosts"}},
		"container-1",
	)
	if err == nil || !strings.Contains(err.Error(), "/etc/hosts") {
		t.Fatalf("reserved payload destination error = %v", err)
	}
}

func TestAppendPayloadPlansSkipsMountAlreadyRealizedByUserBind(t *testing.T) {
	t.Parallel()

	plan := &Plan{}
	payloads := []PayloadInput{
		{ID: "payload-1", NodeID: "node-a", Destination: "/data/shared.txt"},
	}

	// A bind whose source and target are the same path ("/data/shared.txt:/data/shared.txt")
	// plans the payload's content at the payload's own destination.
	if err := appendUserBindPlan(
		plan,
		userBindTestNode(),
		payloads,
		"container-1",
		0,
		"/data/shared.txt:/data/shared.txt:ro",
	); err != nil {
		t.Fatalf("appendUserBindPlan() error = %v", err)
	}

	if err := appendPayloadPlans(plan, userBindTestNode(), payloads, "container-1"); err != nil {
		t.Fatalf("appendPayloadPlans() error = %v", err)
	}

	if len(plan.Mounts) != 1 || plan.Mounts[0].Destination != "/data/shared.txt" {
		t.Fatalf("same-path bind must plan exactly one mount, got %#v", plan.Mounts)
	}

	if len(plan.Files) != 1 {
		t.Fatalf("payload file plan must survive the mount de-duplication, got %#v", plan.Files)
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
