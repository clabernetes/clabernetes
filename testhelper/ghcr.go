package testhelper

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// CreateGHCRPullSecret creates a kubernetes.io/dockerconfigjson Secret holding only the
// runner's ghcr.io credentials so restricted-image conformance suites can pull private vendor
// images without leaking unrelated registry auth into the cluster.
func CreateGHCRPullSecret(t *testing.T, namespace, secretName string) {
	t.Helper()

	configPath := ghcrDockerConfigPath(t)

	configData, err := os.ReadFile(configPath) //nolint:gosec // path is the runner Docker config.
	if err != nil {
		t.Fatalf("read Docker config %q: %v", configPath, err)
	}

	var config struct {
		Auths map[string]json.RawMessage `json:"auths"`
	}

	err = json.Unmarshal(configData, &config)
	if err != nil {
		t.Fatalf("decode Docker config %q: %v", configPath, err)
	}

	ghcrAuth, ok := config.Auths["ghcr.io"]
	if !ok {
		t.Fatalf("Docker config %q has no ghcr.io credentials", configPath)
	}

	minimalConfig, err := json.Marshal(struct {
		Auths map[string]json.RawMessage `json:"auths"`
	}{
		Auths: map[string]json.RawMessage{"ghcr.io": ghcrAuth},
	})
	if err != nil {
		t.Fatalf("encode GHCR Docker config: %v", err)
	}

	minimalConfigPath := filepath.Join(t.TempDir(), "config.json")

	err = os.WriteFile(
		minimalConfigPath,
		minimalConfig,
		0o600,
	)
	if err != nil {
		t.Fatalf("write GHCR Docker config: %v", err)
	}

	cmd := exec.CommandContext( //nolint:gosec // kubectl arguments are test-controlled.
		t.Context(),
		"kubectl",
		"create",
		"secret",
		"generic",
		secretName,
		"--namespace",
		namespace,
		"--type=kubernetes.io/dockerconfigjson",
		"--from-file=.dockerconfigjson="+minimalConfigPath,
	)

	Execute(t, cmd)
}

func ghcrDockerConfigPath(t *testing.T) string {
	t.Helper()

	if dockerConfigDir := os.Getenv("DOCKER_CONFIG"); dockerConfigDir != "" {
		return filepath.Join(dockerConfigDir, "config.json")
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("resolve home directory for Docker config: %v", err)
	}

	return filepath.Join(homeDir, ".docker", "config.json")
}
