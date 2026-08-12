package launcher //nolint:testpackage // tests cover unexported release archive helpers

import (
	"reflect"
	"testing"
)

func TestContainerlabReleaseTarName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		version  string
		goarch   string
		expected string
		wantErr  bool
	}{
		{
			name:     "amd64",
			version:  "0.76.0",
			goarch:   "amd64",
			expected: "containerlab_0.76.0_linux_amd64.tar.gz",
		},
		{
			name:     "arm64",
			version:  "0.76.0",
			goarch:   "arm64",
			expected: "containerlab_0.76.0_linux_arm64.tar.gz",
		},
		{
			name:    "unsupported",
			version: "0.76.0",
			goarch:  "386",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := containerlabReleaseTarName(tt.version, tt.goarch)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}

				return
			}

			if err != nil {
				t.Fatalf("expected nil error, got %s", err)
			}

			if got != tt.expected {
				t.Fatalf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestTopologyHostInterfaces(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		rawTopology string
		expected    []string
	}{
		{
			name: "simple",
			rawTopology: `
name: clabernetes-se1
topology:
  nodes:
    se1:
      kind: arrcus_arcos
  links:
    - endpoints: ["se1:eth1", "host:se1-eth1"]
    - endpoints: ["se1:eth2", "host:se1-eth2"]
`,
			expected: []string{"se1-eth1", "se1-eth2"},
		},
		{
			name: "sanitizes-slashes",
			rawTopology: `
name: clabernetes-sr1
topology:
  nodes:
    sr1:
      kind: nokia_sros
  links:
    - endpoints: ["sr1:1/1/c1/1", "host:sr1-1/1/c1/1"]
`,
			expected: []string{"sr1-1-1-c1-1"},
		},
		{
			name: "skips-protected-interfaces",
			rawTopology: `
name: clabernetes-se1
topology:
  nodes:
    se1:
      kind: arrcus_arcos
  links:
    - endpoints: ["se1:eth1", "host:eth0"]
    - endpoints: ["se1:eth2", "host:se1-eth2"]
`,
			expected: []string{"se1-eth2"},
		},
		{
			name: "no-links",
			rawTopology: `
name: clabernetes-se1
topology:
  nodes:
    se1:
      kind: arrcus_arcos
`,
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := topologyHostInterfaces(tt.rawTopology)
			if err != nil {
				t.Fatalf("expected nil error, got %s", err)
			}

			if !reflect.DeepEqual(got, tt.expected) {
				t.Fatalf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}
