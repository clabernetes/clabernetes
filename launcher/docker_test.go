package launcher //nolint:testpackage // tests cover unexported docker daemon config helpers

import (
	"os"
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
