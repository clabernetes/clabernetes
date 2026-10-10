package deviceplan_test

import (
	"errors"
	"testing"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
)

// TestCompatibilityFixtureMatchesLiveRegistry pins the committed plan-input fixture digest
// against the live registry: bumping containerlab changes the digest, and the fixture must be
// refreshed in the same commit.
//
// The live version comes from the build information, which test binaries built with Go 1.26 and
// earlier omit from the module graph (golang/go#76926) -- `make test` injects it via -ldflags.
// Without that injection this test has nothing to compare against, so it skips with a pointer to
// the injection instead of failing unrelated to the change under test.
func TestCompatibilityFixtureMatchesLiveRegistry(t *testing.T) {
	compat, err := clabernetesinternaldeviceplan.LiveCompatibility(
		clabernetesinternaldeviceplan.NewContainerlabRegistry(),
	)
	if err != nil {
		var planningErr *clabernetesinternaldeviceplan.Error
		if errors.As(err, &planningErr) &&
			planningErr.Code == clabernetesinternaldeviceplan.ErrorInvariant &&
			planningErr.Field == "compatibility.module" {
			t.Skipf(
				"skipping: %s -- run make test, which injects the linked containerlab "+
					"version into the test binary via -ldflags",
				err,
			)
		}

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
