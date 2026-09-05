package directruntime

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
)

const (
	// meshPeerResyncTicks bounds how many revision ticks pass between two full mesh peer state
	// convergences while the directory is unchanged: a device flushing the namespace's
	// neighbor tables is healed within this many ticks rather than only on the next
	// membership change.
	meshPeerResyncTicks = 30
	// interpositionBootTicks is the window after the cold pass during which the sidecar-owned
	// interposition state (legs, tunnel endpoint, routes, rules, sysctls, translation, clamp)
	// is re-asserted on every tick: devices displace shared namespace state while they boot.
	interpositionBootTicks = 60
	// interpositionResyncTicks paces that re-assertion afterwards. Re-asserting on every tick
	// costs each Pod hundreds of netlink and sysctl operations per second for state a booted
	// device leaves alone, which does not scale to thousands of Pods; a displaced state now
	// heals within this many ticks, and a directory change still triggers a pass at once.
	interpositionResyncTicks = 10
)

// peerDirectoryReader is the sidecar's view of the mounted directory: it re-parses the shard
// files only when their fingerprint (size and modification time of every candidate file)
// changes, so an unchanged tick costs a handful of stat calls rather than a directory parse.
// The kubelet swaps the projected volume atomically, so any content change moves the
// fingerprint.
type peerDirectoryReader struct {
	root string

	mutex       sync.Mutex
	fingerprint string
	peers       []PeerIdentity
	loaded      bool
}

func newPeerDirectoryReader(root string) *peerDirectoryReader {
	return &peerDirectoryReader{root: root}
}

// load returns the current peer set and whether it differs from the previous load. The first
// load always reports a change. A read or parse failure keeps the last-known peers (best
// effort, exactly like the hosts realization) and reports it on the sidecar's log stream.
func (r *peerDirectoryReader) load() ([]PeerIdentity, bool) {
	if r == nil {
		return nil, false
	}

	r.mutex.Lock()
	defer r.mutex.Unlock()

	fingerprint := r.currentFingerprint()
	if r.loaded && fingerprint == r.fingerprint {
		return r.peers, false
	}

	peers, err := ReadPeerDirectory(r.root, os.ReadFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "c9s: peer directory partially unreadable: %v\n", err)

		// A directory that cannot be read whole keeps the last-known peers: dropping the
		// unreadable part would tear down working peer state over a transient projection
		// fault. The fingerprint is still recorded so the next successful rewrite is seen.
		if r.loaded {
			r.fingerprint = fingerprint

			return r.peers, false
		}
	}

	r.fingerprint = fingerprint
	r.peers = peers
	r.loaded = true

	return peers, true
}

func (r *peerDirectoryReader) currentFingerprint() string {
	parts := make([]string, 0, PeerDirectoryShardCount+1)

	for _, path := range peerDirectoryFiles(r.root) {
		info, err := os.Stat(path)
		if err != nil {
			parts = append(parts, "-")

			continue
		}

		parts = append(
			parts,
			strconv.FormatInt(info.Size(), 10)+"@"+strconv.FormatInt(info.ModTime().UnixNano(), 10),
		)
	}

	return strings.Join(parts, ",")
}
