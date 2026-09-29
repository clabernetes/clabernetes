//nolint:testpackage // Exercises the private config conversion without creating a cluster Secret.
package testhelper

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGHCRDockerConfigCredentialStore(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DOCKER_CONFIG", dir)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	config := `{"auths":{"ghcr.io":{},"other.example":{"auth":"unrelated"}},"credsStore":"mock"}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	helper := `#!/bin/sh
read server
test "$server" = ghcr.io || exit 1
printf '%s\n' '{"Username":"test-user","Secret":"test-token"}'
`
	//nolint:gosec // The fake credential helper must be executable by the test.
	if err := os.WriteFile(filepath.Join(dir, "docker-credential-mock"), []byte(helper), 0o700); err != nil {
		t.Fatal(err)
	}

	minimal, err := ghcrDockerConfig(t)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Auths map[string]struct {
			Auth string `json:"auth"`
		} `json:"auths"`
	}
	if err = json.Unmarshal(minimal, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Auths) != 1 {
		t.Fatalf("pull Secret has %d registries, want only ghcr.io", len(result.Auths))
	}
	auth, err := base64.StdEncoding.DecodeString(result.Auths["ghcr.io"].Auth)
	if err != nil {
		t.Fatal(err)
	}
	if string(auth) != "test-user:test-token" {
		t.Fatal("pull Secret does not contain the credential helper's GHCR entry")
	}
}
