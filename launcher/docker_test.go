package launcher //nolint:testpackage // tests cover unexported docker daemon config helpers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
)

func TestGetProxiesConfig(t *testing.T) { //nolint:paralleltest // mutates env vars
	tests := []struct {
		name     string
		env      map[string]string
		expected *daemonProxiesConfig
	}{
		{
			name:     "no-proxy-env",
			env:      map[string]string{},
			expected: nil,
		},
		{
			name: "uppercase",
			env: map[string]string{
				clabernetesconstants.HTTPProxyEnv:  "http://proxy.example.com:8080",
				clabernetesconstants.HTTPSProxyEnv: "http://proxy.example.com:8080",
				clabernetesconstants.NoProxyEnv:    "localhost,10.0.0.0/8",
			},
			expected: &daemonProxiesConfig{
				HTTPProxy:  "http://proxy.example.com:8080",
				HTTPSProxy: "http://proxy.example.com:8080",
				NoProxy:    "localhost,10.0.0.0/8",
			},
		},
		{
			name: "lowercase",
			env: map[string]string{
				clabernetesconstants.HTTPSProxyEnvLower: "http://proxy.example.com:8080",
				clabernetesconstants.NoProxyEnvLower:    "localhost",
			},
			expected: &daemonProxiesConfig{
				HTTPSProxy: "http://proxy.example.com:8080",
				NoProxy:    "localhost",
			},
		},
		{
			name: "no-proxy-alone-is-ignored",
			env: map[string]string{
				clabernetesconstants.NoProxyEnv: "localhost",
			},
			expected: nil,
		},
	}

	proxyEnvKeys := []string{
		clabernetesconstants.HTTPProxyEnv,
		clabernetesconstants.HTTPProxyEnvLower,
		clabernetesconstants.HTTPSProxyEnv,
		clabernetesconstants.HTTPSProxyEnvLower,
		clabernetesconstants.NoProxyEnv,
		clabernetesconstants.NoProxyEnvLower,
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { //nolint:paralleltest // mutates env vars
			for _, key := range proxyEnvKeys {
				t.Setenv(key, "")

				err := os.Unsetenv(key)
				if err != nil {
					t.Fatalf("failed unsetting env var %q, err: %s", key, err)
				}
			}

			for key, value := range tt.env {
				t.Setenv(key, value)
			}

			got := getProxiesConfig()

			switch {
			case tt.expected == nil:
				if got != nil {
					t.Fatalf("expected nil proxies config, got %+v", got)
				}
			case got == nil:
				t.Fatalf("expected proxies config %+v, got nil", tt.expected)
			case *got != *tt.expected:
				t.Fatalf("expected proxies config %+v, got %+v", tt.expected, got)
			}
		})
	}
}

func TestParseContainerReadiness(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		state     string
		wantReady bool
		wantErr   bool
	}{
		{name: "running without healthcheck", state: `{"Running":true}`, wantReady: true},
		{
			name:      "running and healthy",
			state:     `{"Running":true,"Health":{"Status":"healthy"}}`,
			wantReady: true,
		},
		{
			name:  "healthcheck starting",
			state: `{"Running":true,"Health":{"Status":"starting"}}`,
		},
		{
			name:  "healthcheck unhealthy",
			state: `{"Running":true,"Health":{"Status":"unhealthy"}}`,
		},
		{name: "paused", state: `{"Running":true,"Paused":true}`},
		{name: "restarting", state: `{"Running":true,"Restarting":true}`},
		{name: "dead", state: `{"Running":true,"Dead":true}`},
		{name: "not running", state: `{"Running":false}`},
		{name: "invalid state", state: `{`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotReady, err := parseContainerReadiness([]byte(tt.state))

			if (err != nil) != tt.wantErr {
				t.Fatalf("parseContainerReadiness() error = %v, wantErr %t", err, tt.wantErr)
			}

			if gotReady != tt.wantReady {
				t.Fatalf(
					"parseContainerReadiness() ready = %t, want %t",
					gotReady,
					tt.wantReady,
				)
			}
		})
	}
}

func TestResolveComponentContainers(t *testing.T) {
	t.Parallel()

	lineCard := componentContainerInspect("line-card-id", "srsim-1", "clab")
	cpm := componentContainerInspect("cpm-id", "srsim-a", "container:line-card-id")

	resolved, err := resolveComponentContainers("srsim", []*dockerContainerInspect{cpm, lineCard})
	if err != nil {
		t.Fatal(err)
	}

	if resolved.primaryContainerID != "line-card-id" {
		t.Fatalf(
			"primary component container = %q, want line-card-id",
			resolved.primaryContainerID,
		)
	}

	if len(resolved.containerIDs) != 2 ||
		resolved.containerIDs["srsim-1"] != "line-card-id" ||
		resolved.containerIDs["srsim-a"] != "cpm-id" {
		t.Fatalf("resolved component containers = %+v", resolved.containerIDs)
	}
}

func TestResolveComponentContainersRejectsInvalidGroups(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		containers []*dockerContainerInspect
		wantError  string
	}{
		{
			name: "missing-node-name-label",
			containers: []*dockerContainerInspect{
				componentContainerInspect("component-id", "", "clab"),
			},
			wantError: "has no \"clab-node-name\" label",
		},
		{
			name: "duplicate-component-name",
			containers: []*dockerContainerInspect{
				componentContainerInspect("first-id", "srsim-a", "clab"),
				componentContainerInspect("second-id", "srsim-a", "container:first-id"),
			},
			wantError: "multiple component containers named \"srsim-a\"",
		},
		{
			name: "multiple-netns-owners",
			containers: []*dockerContainerInspect{
				componentContainerInspect("first-id", "srsim-1", "clab"),
				componentContainerInspect("second-id", "srsim-a", "clab"),
			},
			wantError: "multiple network namespace owners",
		},
		{
			name: "missing-netns-owner",
			containers: []*dockerContainerInspect{
				componentContainerInspect("first-id", "srsim-1", "container:other"),
				componentContainerInspect("second-id", "srsim-a", "container:first-id"),
			},
			wantError: "no network namespace owner found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := resolveComponentContainers("srsim", tt.containers)
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("resolveComponentContainers() error = %v, want %q", err, tt.wantError)
			}
		})
	}
}

func componentContainerInspect(id, nodeName, networkMode string) *dockerContainerInspect {
	container := &dockerContainerInspect{ID: id}
	container.Config.Labels = map[string]string{containerlabNodeNameLabel: nodeName}
	container.HostConfig.NetworkMode = networkMode

	return container
}

func TestWriteNodeStatus(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "status")

	err := writeNodeStatus(path, false)
	if err != nil {
		t.Fatal(err)
	}

	status, err := os.ReadFile(path) //nolint:gosec // path is created inside t.TempDir.
	if err != nil {
		t.Fatal(err)
	}

	if len(status) != 0 {
		t.Fatalf("unhealthy status file = %q, want empty", status)
	}

	err = writeNodeStatus(path, true)
	if err != nil {
		t.Fatal(err)
	}

	status, err = os.ReadFile(path) //nolint:gosec // path is created inside t.TempDir.
	if err != nil {
		t.Fatal(err)
	}

	if string(status) != clabernetesconstants.NodeStatusHealthy {
		t.Fatalf(
			"healthy status file = %q, want %q",
			status,
			clabernetesconstants.NodeStatusHealthy,
		)
	}
}

func TestEnsureKubeAPINotProxied(t *testing.T) { //nolint:paralleltest // mutates env vars
	tests := []struct {
		name            string
		httpsProxy      string
		noProxy         string
		apiHost         string
		expectedNoProxy string
	}{
		{
			name:            "appends-api-host",
			httpsProxy:      "http://proxy.example.com:8080",
			noProxy:         "localhost",
			apiHost:         "10.96.0.1",
			expectedNoProxy: "localhost,10.96.0.1",
		},
		{
			name:            "sets-when-empty",
			httpsProxy:      "http://proxy.example.com:8080",
			noProxy:         "",
			apiHost:         "10.96.0.1",
			expectedNoProxy: "10.96.0.1",
		},
		{
			name:            "already-present",
			httpsProxy:      "http://proxy.example.com:8080",
			noProxy:         "localhost,10.96.0.1,example.com",
			apiHost:         "10.96.0.1",
			expectedNoProxy: "localhost,10.96.0.1,example.com",
		},
		{
			name:            "no-proxy-configured-no-op",
			httpsProxy:      "",
			noProxy:         "localhost",
			apiHost:         "10.96.0.1",
			expectedNoProxy: "localhost",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { //nolint:paralleltest // mutates env vars
			for _, key := range []string{
				clabernetesconstants.HTTPProxyEnv,
				clabernetesconstants.HTTPProxyEnvLower,
				clabernetesconstants.HTTPSProxyEnvLower,
			} {
				t.Setenv(key, "")

				err := os.Unsetenv(key)
				if err != nil {
					t.Fatalf("failed unsetting env var %q, err: %s", key, err)
				}
			}

			t.Setenv(clabernetesconstants.HTTPSProxyEnv, tt.httpsProxy)
			t.Setenv(clabernetesconstants.NoProxyEnv, tt.noProxy)
			t.Setenv(clabernetesconstants.NoProxyEnvLower, tt.noProxy)
			t.Setenv("KUBERNETES_SERVICE_HOST", tt.apiHost)

			ensureKubeAPINotProxied()

			for _, key := range []string{
				clabernetesconstants.NoProxyEnv,
				clabernetesconstants.NoProxyEnvLower,
			} {
				if got := os.Getenv(key); got != tt.expectedNoProxy {
					t.Fatalf("expected %s %q, got %q", key, tt.expectedNoProxy, got)
				}
			}
		})
	}
}
