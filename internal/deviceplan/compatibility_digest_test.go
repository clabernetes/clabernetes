package deviceplan_test

import (
	"testing"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
)

// TestCompatibilityFixtureMatchesLiveRegistry pins the committed plan-input fixture digest
// against the live registry: bumping containerlab changes the digest, and the fixture must be
// refreshed in the same commit.
func TestCompatibilityFixtureMatchesLiveRegistry(t *testing.T) {
	compat, err := clabernetesinternaldeviceplan.LiveCompatibility(
		clabernetesinternaldeviceplan.NewContainerlabRegistry(),
	)
	if err != nil {
		t.Fatal(err)
	}

	fixture := testCompatibility()

	if compat.RegistryDigest != fixture.RegistryDigest {
		t.Fatalf(
			"fixture digest %s does not match the live registry digest %s",
			fixture.RegistryDigest,
			compat.RegistryDigest,
		)
	}

	if compat.ContainerlabVersion != fixture.ContainerlabVersion {
		t.Fatalf(
			"fixture version %s does not match the linked module version %s",
			fixture.ContainerlabVersion,
			compat.ContainerlabVersion,
		)
	}
}
