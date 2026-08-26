package directruntime

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseResolverConfigExtractsNameserversAndSearch(t *testing.T) {
	t.Parallel()

	config := parseResolverConfig([]byte(
		"# kubelet-managed\n" +
			"nameserver 10.96.0.10\n" +
			"search lab.svc.cluster.local svc.cluster.local cluster.local\n" +
			"options ndots:5\n",
	))

	if !reflect.DeepEqual(config.Nameservers, []string{"10.96.0.10"}) ||
		!reflect.DeepEqual(config.Search, []string{
			"lab.svc.cluster.local", "svc.cluster.local", "cluster.local",
		}) {
		t.Fatalf("parseResolverConfig() = %#v", config)
	}
}

func TestCaptureResolverConfigPersistsAndSurvivesADeviceRewrite(t *testing.T) {
	t.Parallel()

	stateDirectory := t.TempDir()
	source := filepath.Join(t.TempDir(), "resolv.conf")

	kubelet := "nameserver 10.96.0.10\nsearch lab.svc.cluster.local cluster.local\n"
	if err := os.WriteFile(source, []byte(kubelet), 0o600); err != nil {
		t.Fatal(err)
	}

	captured := captureResolverConfig(source, stateDirectory)
	if captured == nil || !captured.usable() {
		t.Fatalf("capture of a usable system configuration = %#v", captured)
	}

	// A network operating system rewriting the shared file must not defeat a later restart:
	// the persisted capture answers instead.
	if err := os.WriteFile(source, []byte("search localdomain\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	recovered := captureResolverConfig(source, stateDirectory)
	if recovered == nil || !reflect.DeepEqual(*recovered, *captured) {
		t.Fatalf("capture after device rewrite = %#v, want persisted %#v", recovered, captured)
	}
}

func TestCaptureResolverConfigWithoutAnySourceIsAbsent(t *testing.T) {
	t.Parallel()

	if config := captureResolverConfig(
		filepath.Join(t.TempDir(), "missing"), t.TempDir(),
	); config != nil {
		t.Fatalf("capture with no system file and no persisted copy = %#v", config)
	}
}

func TestResolverCandidatesCompleteShortNamesThroughSearchFirst(t *testing.T) {
	t.Parallel()

	short := resolverCandidates(
		"multitool-wire",
		[]string{"lab.svc.cluster.local", "cluster.local"},
	)
	if !reflect.DeepEqual(short, []string{
		"multitool-wire.lab.svc.cluster.local.",
		"multitool-wire.cluster.local.",
		"multitool-wire.",
	}) {
		t.Fatalf("short-name candidates = %#v", short)
	}

	dotted := resolverCandidates("peer.example", []string{"cluster.local"})
	if !reflect.DeepEqual(dotted, []string{
		"peer.example.",
		"peer.example.cluster.local.",
	}) {
		t.Fatalf("dotted-name candidates = %#v", dotted)
	}
}
