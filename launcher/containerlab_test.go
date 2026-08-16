package launcher //nolint:testpackage // tests cover unexported release archive helpers

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"

	claberneteslogging "github.com/clabernetes/clabernetes/logging"
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
    - endpoints: ["se1:eth3", "se2:eth3"]
`,
			expected: []string{"se1-eth1", "se1-eth2"},
		},
		{
			name: "sanitizes-and-deduplicates",
			rawTopology: `
name: clabernetes-sr1
topology:
  nodes:
    sr1:
      kind: nokia_sros
  links:
    - endpoints: ["sr1:1/1/c1/1", "host:sr1-1/1/c1/1"]
    - endpoints: ["sr1:1/1/c1/2", "host:sr1-1/1/c1/1"]
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
    - endpoints: ["se1:eth1", "host:lo"]
    - endpoints: ["se1:eth2", "host:eth0"]
    - endpoints: ["se1:eth3", "host:docker0"]
    - endpoints: ["se1:eth4", "host:se1-eth4"]
`,
			expected: []string{"se1-eth4"},
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
			expected: []string{},
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

func TestRemoveStaleHostInterfaces(t *testing.T) {
	t.Chdir(t.TempDir())

	rawTopology := `
name: clabernetes-se1
topology:
  nodes:
    se1:
      kind: arrcus_arcos
  links:
    - endpoints: ["se1:eth1", "host:se1-eth1"]
    - endpoints: ["se1:eth2", "host:eth0"]
    - endpoints: ["se1:eth3", "host:missing"]
`

	err := os.WriteFile("topo.clab.yaml", []byte(rawTopology), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	errInterfaceMissing := errors.New("interface missing") //nolint:err113 // test sentinel
	existing := map[string]bool{"se1-eth1": true}

	var calls [][]string

	runner := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		call := append([]string{"ip"}, args...)
		calls = append(calls, call)

		switch {
		case reflect.DeepEqual(args, []string{"link", "show", "dev", "se1-eth1"}):
			if existing["se1-eth1"] {
				return nil, nil
			}

			return nil, errInterfaceMissing
		case reflect.DeepEqual(args, []string{"link", "show", "dev", "missing"}):
			return nil, errInterfaceMissing
		case reflect.DeepEqual(args, []string{"link", "delete", "dev", "se1-eth1"}):
			delete(existing, "se1-eth1")

			return nil, nil
		default:
			t.Fatalf("unexpected command: %v", call)

			return nil, nil
		}
	}

	instance := &clabernetes{
		ctx:        context.Background(),
		logger:     &claberneteslogging.FakeInstance{},
		runCommand: runner,
	}
	instance.removeStaleHostInterfaces()

	expectedCalls := [][]string{
		{"ip", "link", "show", "dev", "se1-eth1"},
		{"ip", "link", "delete", "dev", "se1-eth1"},
		{"ip", "link", "show", "dev", "missing"},
	}
	if !reflect.DeepEqual(calls, expectedCalls) {
		t.Fatalf("expected commands %v, got %v", expectedCalls, calls)
	}

	if existing["se1-eth1"] {
		t.Fatal("expected stale interface to be deleted")
	}
}

func TestRemoveStaleHostInterfacesContinuesAfterDeleteFailure(t *testing.T) {
	t.Chdir(t.TempDir())

	rawTopology := `
name: clabernetes-se1
topology:
  nodes:
    se1:
      kind: arrcus_arcos
  links:
    - endpoints: ["se1:eth1", "host:se1-eth1"]
`

	err := os.WriteFile("topo.clab.yaml", []byte(rawTopology), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	var calls [][]string

	errPermissionDenied := errors.New("permission denied") //nolint:err113 // test sentinel

	runner := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{"ip"}, args...))
		if reflect.DeepEqual(args, []string{"link", "show", "dev", "se1-eth1"}) {
			return nil, nil
		}

		return []byte("delete failed"), errPermissionDenied
	}

	instance := &clabernetes{
		ctx:        context.Background(),
		logger:     &claberneteslogging.FakeInstance{},
		runCommand: runner,
	}
	instance.removeStaleHostInterfaces()

	expectedCalls := [][]string{
		{"ip", "link", "show", "dev", "se1-eth1"},
		{"ip", "link", "delete", "dev", "se1-eth1"},
	}
	if !reflect.DeepEqual(calls, expectedCalls) {
		t.Fatalf("expected cleanup to continue after deletion failure, got %v", calls)
	}
}

func TestRunContainerlabCleansBeforeDeploy(t *testing.T) {
	binDir := t.TempDir()
	workDir := t.TempDir()
	t.Chdir(workDir)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	containerlab := `#!/bin/sh
test -f cleanup.done
`

	err := os.WriteFile(binDir+"/containerlab", []byte(containerlab), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Chmod(binDir+"/containerlab", 0o700) //nolint:gosec // test command must be executable
	if err != nil {
		t.Fatal(err)
	}

	rawTopology := `
name: clabernetes-se1
topology:
  nodes:
    se1:
      kind: arrcus_arcos
  links:
    - endpoints: ["se1:eth1", "host:se1-eth1"]
`

	err = os.WriteFile("topo.clab.yaml", []byte(rawTopology), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	runner := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[1] == "delete" {
			err = os.WriteFile("cleanup.done", []byte("deleted\n"), 0o600)
			if err != nil {
				t.Fatal(err)
			}
		}

		return nil, nil
	}

	instance := &clabernetes{
		ctx:                context.Background(),
		logger:             &claberneteslogging.FakeInstance{},
		containerlabLogger: &claberneteslogging.FakeInstance{},
		runCommand:         runner,
	}

	err = instance.runContainerlab()
	if err != nil {
		t.Fatalf("runContainerlab() returned error: %s", err)
	}
}
