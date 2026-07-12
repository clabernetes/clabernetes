package config

import (
	"os"
	"regexp"

	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
)

var managedLauncherReleaseImage = regexp.MustCompile(
	`^ghcr\.io/srl-labs/clabernetes/clabernetes-launcher:` +
		`v?[0-9]+\.[0-9]+\.[0-9]+(?:[-+][a-zA-Z0-9._-]+)?$`,
)

// ResolveLauncherImage returns the launcher image compatible with the running manager. Released
// charts historically persisted their default launcher tag in the Config singleton, so blindly
// honoring that value creates manager/launcher version skew on upgrade. Keep nonstandard images as
// explicit user overrides, but move old official release tags with the manager-bundled launcher.
// The persisted value is deliberately left unchanged so rolling the manager back also rolls the
// launcher back through the older manager's existing behavior.
func ResolveLauncherImage(configuredImage string) string {
	bundledImage := os.Getenv(clabernetesconstants.CompatibleLauncherImageEnv)
	if bundledImage == "" {
		bundledImage = os.Getenv(clabernetesconstants.LauncherImageEnv)
	}

	if configuredImage == "" {
		return bundledImage
	}

	if bundledImage == "" || configuredImage == bundledImage {
		return configuredImage
	}

	if managedLauncherReleaseImage.MatchString(configuredImage) {
		return bundledImage
	}

	return configuredImage
}
