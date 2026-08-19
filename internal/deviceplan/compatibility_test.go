package deviceplan_test

import (
	"errors"
	"testing"

	clabernetesdeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
)

func TestCompatibilityComesFromExplicitModuleAndLiveRegistry(t *testing.T) {
	t.Parallel()

	compatibility, err := clabernetesdeviceplan.CompatibilityForRegistry(nil, "v-test")
	if err != nil {
		t.Fatal(err)
	}
	if compatibility.ContainerlabModule != clabernetesdeviceplan.ContainerlabModulePath ||
		compatibility.ContainerlabVersion != "v-test" || compatibility.RegistryDigest == "" ||
		compatibility.PlanSchemaVersion != clabernetesdeviceplan.SchemaVersion {
		t.Fatalf("live compatibility = %#v", compatibility)
	}
	synthetic, err := clabernetesdeviceplan.CompatibilityForRegistry(
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
	compatibility, err := clabernetesdeviceplan.CompatibilityForRegistry(registry, "v-test")
	if err != nil {
		t.Fatal(err)
	}
	compatibility.RegistryDigest = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	err = clabernetesdeviceplan.ValidateCompatibility(registry, compatibility, "v-test")
	var planningErr *clabernetesdeviceplan.Error
	if !errors.As(err, &planningErr) || planningErr.Code != clabernetesdeviceplan.ErrorInvariant ||
		planningErr.Behavior != "imported-registry" {
		t.Fatalf("Plan() error = %#v, want imported-registry Invariant", err)
	}
}
