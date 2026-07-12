package config_test

import (
	"testing"

	clabernetesconfig "github.com/srl-labs/clabernetes/config"
	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
)

func TestResolveLauncherImage(t *testing.T) {
	const bundledImage = "ghcr.io/srl-labs/clabernetes/clabernetes-launcher:0.7.0"

	t.Setenv(clabernetesconstants.CompatibleLauncherImageEnv, bundledImage)

	for _, testCase := range []struct {
		name       string
		configured string
		expected   string
	}{
		{
			name:     "unset uses bundled",
			expected: bundledImage,
		},
		{
			name:       "current release remains unchanged",
			configured: bundledImage,
			expected:   bundledImage,
		},
		{
			name:       "old chart-managed release follows manager",
			configured: "ghcr.io/srl-labs/clabernetes/clabernetes-launcher:0.6.0",
			expected:   bundledImage,
		},
		{
			name:       "old v-prefixed chart-managed release follows manager",
			configured: "ghcr.io/srl-labs/clabernetes/clabernetes-launcher:v0.6.0-rc.1",
			expected:   bundledImage,
		},
		{
			name:       "custom registry remains pinned",
			configured: "registry.example.test/team/launcher:0.6.0",
			expected:   "registry.example.test/team/launcher:0.6.0",
		},
		{
			name:       "custom official tag remains pinned",
			configured: "ghcr.io/srl-labs/clabernetes/clabernetes-launcher:experiment",
			expected:   "ghcr.io/srl-labs/clabernetes/clabernetes-launcher:experiment",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := clabernetesconfig.ResolveLauncherImage(testCase.configured); got != testCase.expected {
				t.Fatalf("expected %q, got %q", testCase.expected, got)
			}
		})
	}
}

func TestResolveLauncherImageWithoutBundledImage(t *testing.T) {
	t.Setenv(clabernetesconstants.LauncherImageEnv, "")
	t.Setenv(clabernetesconstants.CompatibleLauncherImageEnv, "")

	const configuredImage = "ghcr.io/srl-labs/clabernetes/clabernetes-launcher:0.6.0"

	if got := clabernetesconfig.ResolveLauncherImage(configuredImage); got != configuredImage {
		t.Fatalf("expected %q, got %q", configuredImage, got)
	}
}
