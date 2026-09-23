package directruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
)

type applicationHostsOperations struct {
	LaunchOperations

	hosts string
	peers []byte
}

func (o applicationHostsOperations) Hostname() (string, error) { return "device", nil }

func (o applicationHostsOperations) ReadFile(path string) ([]byte, error) {
	if strings.HasSuffix(path, "peers-0.json") {
		return o.peers, nil
	}

	return nil, os.ErrNotExist
}

func (o applicationHostsOperations) UpdateFile(
	_ string, update func([]byte) ([]byte, bool),
) error {
	return o.LaunchOperations.UpdateFile(o.hosts, update)
}

func TestApplicationHostsRefreshAfterReplacementAndPeerAddition(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "hosts")
	operations := applicationHostsOperations{LaunchOperations: newLaunchOperations(), hosts: path}
	plan := clabernetesinternaldeviceplan.Plan{}
	peers := []PeerIdentity{{Name: "first", IPv4: "192.0.2.1"}}
	for _, replaced := range []bool{false, true} {
		if replaced {
			peers = append(peers, PeerIdentity{Name: "added", IPv4: "192.0.2.2"})
		}

		var err error
		operations.peers, err = RenderPeerDirectory(peers)
		if err != nil {
			t.Fatal(err)
		}
		// Model a device replacing its hosts file, independently of the sidecar mount.
		if err = os.WriteFile(path, []byte("127.0.0.1 localhost\n"), 0o600); err != nil {
			t.Fatal(err)
		}

		refreshApplicationHosts(plan, operations)
		refreshApplicationHosts(plan, operations)
		content, err := os.ReadFile(path) //nolint:gosec // Test-owned temporary hosts file.
		if err != nil {
			t.Fatal(err)
		}
		for _, peer := range peers {
			if strings.Count(string(content), peer.IPv4+"\t"+peer.Name+"\t# c9s-peer\n") != 1 {
				t.Fatalf("missing or duplicated peer %s: %s", peer.Name, content)
			}
		}
		if !strings.Contains(string(content), "127.0.0.1 localhost\n") {
			t.Fatalf("device content was lost: %s", content)
		}
	}
}
