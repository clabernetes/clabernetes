//nolint:mnd // protocol and platform literals are clearest inline.
package testhelper

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var errGHCRCredentialsMissing = errors.New("ghcr.io credentials are missing")

// CreateGHCRPullSecret creates a kubernetes.io/dockerconfigjson Secret holding only the
// runner's ghcr.io credentials so restricted-image conformance suites can pull private vendor
// images without leaking unrelated registry auth into the cluster.
func CreateGHCRPullSecret(t *testing.T, namespace, secretName string) {
	t.Helper()

	minimalConfig, err := ghcrDockerConfig(t)
	if err != nil {
		t.Fatal(err)
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

func ghcrDockerConfig(t *testing.T) ([]byte, error) {
	t.Helper()

	configPath := ghcrDockerConfigPath(t)
	configData, err := os.ReadFile(configPath) //nolint:gosec // path is the runner Docker config.
	if err != nil {
		return nil, fmt.Errorf("read Docker config %q: %w", configPath, err)
	}

	var config struct {
		Auths       map[string]json.RawMessage `json:"auths"`
		CredsStore  string                     `json:"credsStore"`
		CredHelpers map[string]string          `json:"credHelpers"`
	}

	err = json.Unmarshal(configData, &config)
	if err != nil {
		return nil, fmt.Errorf("decode Docker config %q: %w", configPath, err)
	}

	ghcrAuth := config.Auths["ghcr.io"]
	var inline struct {
		Auth          string `json:"auth"`
		IdentityToken string `json:"identitytoken"`
		Username      string `json:"username"`
		Password      string `json:"password"`
	}
	if len(ghcrAuth) > 0 {
		if err = json.Unmarshal(ghcrAuth, &inline); err != nil {
			return nil, fmt.Errorf("decode ghcr.io Docker auth: %w", err)
		}
	}
	if inline.Auth == "" && inline.IdentityToken == "" &&
		(inline.Username == "" || inline.Password == "") {
		helper := config.CredHelpers["ghcr.io"]
		if helper == "" {
			helper = config.CredsStore
		}
		if helper == "" {
			return nil, fmt.Errorf("Docker config %q: %w", configPath, errGHCRCredentialsMissing)
		}
		ghcrAuth, err = ghcrAuthFromStore(t, helper)
		if err != nil {
			return nil, err
		}
	}

	minimalConfig, err := json.Marshal(struct {
		Auths map[string]json.RawMessage `json:"auths"`
	}{
		Auths: map[string]json.RawMessage{"ghcr.io": ghcrAuth},
	})
	if err != nil {
		return nil, fmt.Errorf("encode GHCR Docker config: %w", err)
	}

	return minimalConfig, nil
}

func ghcrAuthFromStore(t *testing.T, helper string) (json.RawMessage, error) {
	t.Helper()

	//nolint:gosec // The local Docker config selects the credential helper without a shell.
	cmd := exec.CommandContext(t.Context(), "docker-credential-"+helper, "get")
	cmd.Stdin = strings.NewReader("ghcr.io")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("read ghcr.io from Docker credential helper %q: %w", helper, err)
	}
	var credential struct {
		Username string `json:"Username"`
		Secret   string `json:"Secret"`
	}
	if err = json.Unmarshal(output, &credential); err != nil {
		return nil, fmt.Errorf("decode ghcr.io Docker credential helper response: %w", err)
	}
	if credential.Username == "" || credential.Secret == "" {
		return nil, fmt.Errorf("Docker credential helper %q: %w", helper, errGHCRCredentialsMissing)
	}
	auth := base64.StdEncoding.EncodeToString([]byte(credential.Username + ":" + credential.Secret))
	entry, err := json.Marshal(struct {
		Auth string `json:"auth"`
	}{Auth: auth})
	if err != nil {
		return nil, fmt.Errorf("encode ghcr.io Docker auth: %w", err)
	}

	return entry, nil
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
