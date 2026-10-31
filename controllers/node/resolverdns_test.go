//nolint:noinlineerr // Resolver boundary tests are compact assertions.
package node //nolint:testpackage // tests exercise the controller resolver boundary

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
)

func TestReadResolverServersParsesUniqueNameservers(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "resolv.conf")
	content := "search lab.svc\nnameserver 10.96.0.10\nnameserver 10.96.0.10\n" +
		"nameserver 192.0.2.53\noptions ndots:5\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	servers, err := readResolverServers(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"10.96.0.10", "192.0.2.53"}; !reflect.DeepEqual(servers, want) {
		t.Fatalf("resolver servers = %#v, want %#v", servers, want)
	}
}

func TestReadResolverServersRejectsEmptyConfiguration(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "resolv.conf")
	if err := os.WriteFile(path, []byte("search lab.svc\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := readResolverServers(path); err == nil {
		t.Fatal("readResolverServers() accepted a resolver file without nameservers")
	}
}

func TestWithResolverServersCompletesEveryInput(t *testing.T) {
	t.Parallel()

	values := []clabernetesinternaldeviceplan.ManagementInput{
		{NodeID: "node-a", IPv4: "192.0.2.10/24"},
		{
			NodeID: "node-b",
			DNS:    clabernetesinternaldeviceplan.DNSConfig{Servers: []string{"192.0.2.53"}},
		},
	}

	completed := withResolverServers(values, []string{"10.96.0.10"})
	if len(completed) != len(values) {
		t.Fatalf("completed inputs = %#v", completed)
	}
	for index := range completed {
		if want := []string{"10.96.0.10"}; !reflect.DeepEqual(
			completed[index].ResolverServers,
			want,
		) {
			t.Fatalf("input %d resolver servers = %#v", index, completed[index].ResolverServers)
		}
		// Declared DNS intent is untouched; only the completion input is added.
		if values[index].NodeID != completed[index].NodeID ||
			!reflect.DeepEqual(values[index].DNS, completed[index].DNS) {
			t.Fatalf("input %d declared intent changed: %#v", index, completed[index])
		}
	}
}
