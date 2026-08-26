//nolint:testpackage // dense fixture-driven tests exercise one boundary end to end.
package node

import (
	"context"
	"errors"
	"strings"
	"testing"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestPlannerInputConfigMapIsCanonicalImmutableAndSecretFree(t *testing.T) {
	t.Parallel()

	node := planTestNode(strings.Repeat("long-node-name-", 6))
	input := validInput()

	canonical, err := input.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}

	configMap, digest, err := (&PlannerInputConfigMapReconciler{}).Render(
		node,
		PlannerInputArtifact{
			CanonicalInput: canonical, SensitiveValues: [][]byte{[]byte("actual-secret")},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if configMap.Immutable == nil || !*configMap.Immutable || len(configMap.GetName()) > 63 ||
		configMap.Data[plannerInputKey] != string(canonical) ||
		configMap.Annotations[planInputDigestAnnotation] != digest {
		t.Fatalf("planner input ConfigMap = %#v, digest = %q", configMap, digest)
	}

	if strings.Contains(configMap.String(), "actual-secret") {
		t.Fatal("planner input ConfigMap persisted sensitive validation data")
	}
}

func TestPlannerInputConfigMapRejectsNonCanonicalOrSensitiveInput(t *testing.T) {
	t.Parallel()

	node := planTestNode("router")
	input := validInput()

	canonical, err := input.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name     string
		artifact PlannerInputArtifact
	}{
		{name: "noncanonical", artifact: PlannerInputArtifact{CanonicalInput: append([]byte(" \n"), canonical...)}},
		{name: "sensitive", artifact: PlannerInputArtifact{CanonicalInput: canonical, SensitiveValues: [][]byte{[]byte("node-a")}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, _, renderErr := (&PlannerInputConfigMapReconciler{}).Render(node, test.artifact)
			if !errors.Is(renderErr, ErrInvalidPlannerInput) {
				t.Fatalf("Render() error = %v, want ErrInvalidPlannerInput", renderErr)
			}
		})
	}
}

func TestPlannerInputConfigMapEnsureIsIdempotent(t *testing.T) {
	t.Parallel()

	node := planTestNode("router")

	canonical, err := validInput().CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}

	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(planTestScheme(t)).
		WithObjects(node).
		Build()
	reconciler := &PlannerInputConfigMapReconciler{Client: client}
	artifact := PlannerInputArtifact{CanonicalInput: canonical}

	first, firstDigest, err := reconciler.Ensure(context.Background(), node, artifact)
	if err != nil {
		t.Fatal(err)
	}

	second, secondDigest, err := reconciler.Ensure(context.Background(), node, artifact)
	if err != nil {
		t.Fatal(err)
	}

	if first.GetUID() != second.GetUID() || firstDigest != secondDigest {
		t.Fatalf(
			"idempotent planner inputs differ: %s/%s and %s/%s",
			first.GetUID(),
			firstDigest,
			second.GetUID(),
			secondDigest,
		)
	}
}

func validInput() clabernetesinternaldeviceplan.Input {
	return clabernetesinternaldeviceplan.Input{
		SchemaVersion: clabernetesinternaldeviceplan.SchemaVersion,
		TopologyName:  "lab-a",
		Compatibility: clabernetesinternaldeviceplan.Compatibility{
			ContainerlabModule: "github.com/srl-labs/containerlab", ContainerlabVersion: "v0.78.0",
			RegistryDigest:    "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			PlanSchemaVersion: clabernetesinternaldeviceplan.SchemaVersion,
		},
		Nodes: []clabernetesinternaldeviceplan.NodeInput{{
			ID: "node-a", Name: "router", Kind: "synthetic-registry-entry",
			Definition: []byte(`{"kind":"synthetic-registry-entry","image":"example/device:1"}`),
		}},
		Images: []clabernetesinternaldeviceplan.ImageInput{{
			NodeID: "node-a", SourceReference: "example/device:1",
			DigestReference: "example/device@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			Platform: clabernetesinternaldeviceplan.Platform{
				OS:           "linux",
				Architecture: "amd64",
			},
		}},
	}
}
