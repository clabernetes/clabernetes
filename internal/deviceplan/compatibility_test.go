package deviceplan_test

import (
	"errors"
	"testing"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
)

func TestCompatibilityComesFromExplicitModuleAndLiveRegistry(t *testing.T) {
	t.Parallel()

	compatibility, err := clabernetesinternaldeviceplan.CompatibilityForRegistry(nil, "v-test")
	if err != nil {
		t.Fatal(err)
	}

	if compatibility.ContainerlabModule != clabernetesinternaldeviceplan.ContainerlabModulePath ||
		compatibility.ContainerlabVersion != "v-test" || compatibility.RegistryDigest == "" ||
		compatibility.PlanSchemaVersion != clabernetesinternaldeviceplan.SchemaVersion {
		t.Fatalf("live compatibility = %#v", compatibility)
	}

	synthetic, err := clabernetesinternaldeviceplan.CompatibilityForRegistry(
		newSyntheticRegistry(t),
		"v-test",
	)
	if err != nil {
		t.Fatal(err)
	}

	if synthetic.RegistryDigest == compatibility.RegistryDigest {
		t.Fatal("a newly registered kind did not change the live registry identity")
	}
}

func TestCompatibilityValidationRejectsStaleRegistryIdentity(t *testing.T) {
	t.Parallel()

	registry := newSyntheticRegistry(t)

	compatibility, err := clabernetesinternaldeviceplan.CompatibilityForRegistry(registry, "v-test")
	if err != nil {
		t.Fatal(err)
	}

	compatibility.RegistryDigest = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	err = clabernetesinternaldeviceplan.ValidateCompatibility(registry, compatibility, "v-test")

	var planningErr *clabernetesinternaldeviceplan.Error
	if !errors.As(err, &planningErr) ||
		planningErr.Code != clabernetesinternaldeviceplan.ErrorInvariant ||
		planningErr.Behavior != "imported-registry" {
		t.Fatalf("Plan() error = %#v, want imported-registry Invariant", err)
	}
}
