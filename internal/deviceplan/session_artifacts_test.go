package deviceplan_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
)

func TestSessionArtifactsMustMatchAcceptedPlan(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"different content", "missing content", "sensitivity downgrade", "duplicate attributes", "oversized attributes"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			adapter := clabernetesinternaldeviceplan.Adapter{
				Registry: newSyntheticRegistry(t),
				Revision: "artifact-validation",
			}
			plan, artifacts, err := adapter.PlanArtifacts(
				context.Background(),
				singleNodeInput(syntheticKind, "example/future:1"),
			)
			if err != nil {
				t.Fatal(err)
			}
			artifact := &artifacts[0]
			switch name {
			case "different content":
				artifact.Data = []byte("substituted content")
				artifact.Digest = clabernetesinternaldeviceplan.Digest(artifact.Data)
			case "missing content":
				artifact.Data = nil
			case "sensitivity downgrade":
				artifact.Sensitive = false
			case "duplicate attributes", "oversized attributes":
				value := []byte("value")
				if name == "oversized attributes" {
					value = make([]byte, clabernetesinternaldeviceplan.MaxPlanArtifactBytes)
				}
				for index := range plan.Files {
					file := &plan.Files[index]
					if file.NodeID != artifact.NodeID ||
						file.ArtifactPath != artifact.ArtifactPath {
						continue
					}
					file.ExtendedAttributes = []clabernetesinternaldeviceplan.ExtendedAttribute{
						{Name: "user.a", Digest: clabernetesinternaldeviceplan.Digest(value)},
						{Name: "user.b", Digest: clabernetesinternaldeviceplan.Digest(value)},
					}
					artifact.Digest = clabernetesinternaldeviceplan.PlanArtifactIdentity(*file)
				}
				artifact.ExtendedAttributes = []clabernetesinternaldeviceplan.ArtifactExtendedAttribute{
					{Name: "user.a", Value: value},
					{Name: "user.b", Value: value},
				}
				if name == "duplicate attributes" {
					artifact.ExtendedAttributes[1].Name = "user.a"
				}
			}
			if _, err = clabernetesinternaldeviceplan.NormalizeSessionArtifacts(*plan, artifacts); err == nil {
				t.Fatal("accepted artifact content that does not satisfy the plan")
			}
		})
	}
}

func BenchmarkNormalizeSessionArtifacts(b *testing.B) {
	plan := validPlan(clabernetesinternaldeviceplan.Digest([]byte("input")))
	artifacts := make([]clabernetesinternaldeviceplan.ArtifactContent, 200)
	for index := range artifacts {
		path := fmt.Sprintf("generated/%d", index)
		data := []byte(strings.Repeat("x", 1024))
		artifacts[index] = clabernetesinternaldeviceplan.ArtifactContent{
			NodeID: "node-a", ArtifactPath: path, Data: data,
			Digest: clabernetesinternaldeviceplan.Digest(data), Sensitive: true,
		}
		plan.Files = append(plan.Files, clabernetesinternaldeviceplan.FilePlan{
			ID: path, NodeID: "node-a", SourceReference: path, ArtifactPath: path,
			SourceKind: clabernetesinternaldeviceplan.FileSourcePlanArtifact,
			Digest:     artifacts[index].Digest, Sensitive: true,
		})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := clabernetesinternaldeviceplan.NormalizeSessionArtifacts(plan, artifacts); err != nil {
			b.Fatal(err)
		}
	}
}

func TestPlanArtifactIdentityIncludesExtendedAttributes(t *testing.T) {
	t.Parallel()

	file := clabernetesinternaldeviceplan.FilePlan{
		Digest: clabernetesinternaldeviceplan.Digest([]byte("same bytes")),
	}
	plain := clabernetesinternaldeviceplan.PlanArtifactIdentity(file)
	file.ExtendedAttributes = []clabernetesinternaldeviceplan.ExtendedAttribute{
		{Name: "user.a", Digest: clabernetesinternaldeviceplan.Digest([]byte("a"))},
	}
	first := clabernetesinternaldeviceplan.PlanArtifactIdentity(file)
	file.ExtendedAttributes[0].Digest = clabernetesinternaldeviceplan.Digest([]byte("b"))
	second := clabernetesinternaldeviceplan.PlanArtifactIdentity(file)
	if first == second || first == plain || second == plain {
		t.Fatal("different artifact metadata aliases the same immutable object")
	}
}
