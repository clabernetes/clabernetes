//nolint:testpackage // exercises the unexported sidecar directory cache directly.
package directruntime

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

type peerDirectoryReaderFixture struct {
	t      *testing.T
	shard  string
	reader *peerDirectoryReader
	stamp  time.Time
}

func newPeerDirectoryReaderFixture(t *testing.T) *peerDirectoryReaderFixture {
	t.Helper()

	root := t.TempDir()

	return &peerDirectoryReaderFixture{
		t:      t,
		shard:  filepath.Join(root, PeerDirectoryShardFileName(2)),
		reader: newPeerDirectoryReader(root),
		stamp:  time.Now(),
	}
}

// write stores one shard with a strictly increasing modification time, so a same-size rewrite
// is distinguishable exactly as the kubelet's atomic projection swap makes it.
func (f *peerDirectoryReaderFixture) write(content []byte) {
	f.t.Helper()

	if err := os.WriteFile(f.shard, content, 0o600); err != nil {
		f.t.Fatal(err)
	}

	f.stamp = f.stamp.Add(2 * time.Second)
	if err := os.Chtimes(f.shard, f.stamp, f.stamp); err != nil {
		f.t.Fatal(err)
	}
}

func (f *peerDirectoryReaderFixture) writePeer(pod string) {
	f.t.Helper()

	content, err := RenderPeerDirectory([]PeerIdentity{
		{Name: "r1", IPv4: "172.20.20.11", Pod: pod},
	})
	if err != nil {
		f.t.Fatal(err)
	}

	f.write(content)
}

// expectLoad asserts one load: whether it reports a change and which Pod address (or none) the
// single peer carries.
func (f *peerDirectoryReaderFixture) expectLoad(step string, wantChanged bool, wantPod string) {
	f.t.Helper()

	peers, changed := f.reader.load()
	if changed != wantChanged {
		f.t.Fatalf("%s: changed = %t, want %t", step, changed, wantChanged)
	}

	switch {
	case wantPod == "" && len(peers) != 0:
		f.t.Fatalf("%s: peers = %#v, want none", step, peers)
	case wantPod != "" && (len(peers) != 1 || peers[0].Pod != wantPod):
		f.t.Fatalf("%s: peers = %#v, want one peer at %s", step, peers, wantPod)
	}
}

func TestPeerDirectoryReaderReparsesOnlyWhenTheProjectionChanges(t *testing.T) {
	t.Parallel()

	fixture := newPeerDirectoryReaderFixture(t)

	// An empty directory is a first, changed load with no peers.
	fixture.expectLoad("first load", true, "")
	fixture.expectLoad("unchanged empty directory", false, "")

	fixture.writePeer("10.244.0.5")
	fixture.expectLoad("load after write", true, "10.244.0.5")
	fixture.expectLoad("unchanged shard", false, "10.244.0.5")

	// A same-size rewrite is caught through the modification time.
	fixture.writePeer("10.244.0.6")
	fixture.expectLoad("load after rewrite", true, "10.244.0.6")

	// A malformed shard keeps the last-known peers and reports no change, and the next good
	// rewrite is picked up again.
	fixture.write([]byte("{"))
	fixture.expectLoad("load after corruption", false, "10.244.0.6")

	fixture.writePeer("10.244.0.7")
	fixture.expectLoad("load after repair", true, "10.244.0.7")
}

func TestPeerDirectoryReaderNilIsInert(t *testing.T) {
	t.Parallel()

	var reader *peerDirectoryReader

	if peers, changed := reader.load(); peers != nil || changed {
		t.Fatal("nil reader must be inert")
	}
}

func TestHostsFileMemoTracksTheFileFingerprint(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "hosts")
	memo := &hostsFileMemo{}

	// Nothing remembered yet: never reported unchanged.
	if memo.unchanged(path) {
		t.Fatal("empty memo reported the file unchanged")
	}

	if err := os.WriteFile(path, []byte("10.0.0.1\tnode\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	memo.remember(path)

	if !memo.unchanged(path) {
		t.Fatal("untouched file reported changed")
	}

	// A rewrite of the same size is still caught through the modification time.
	if err := os.WriteFile(path, []byte("10.0.0.2\tnode\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}

	if memo.unchanged(path) {
		t.Fatal("rewritten file reported unchanged")
	}

	var nilMemo *hostsFileMemo

	nilMemo.remember(path)

	if nilMemo.unchanged(path) {
		t.Fatal("nil memo must never skip a realization")
	}
}
