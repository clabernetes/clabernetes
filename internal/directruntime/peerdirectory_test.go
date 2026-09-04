package directruntime_test

import (
	"bytes"
	"errors"
	"net/netip"
	"os"
	"strings"
	"testing"

	clabernetesinternaldirectruntime "github.com/clabernetes/clabernetes/internal/directruntime"
)

var errReadFailure = errors.New("read failure")

func TestRenderPeerDirectoryShardsPlacesEachPeerInItsStableShard(t *testing.T) {
	t.Parallel()

	peers := []clabernetesinternaldirectruntime.PeerIdentity{
		{Name: "leaf1", IPv4: "172.80.80.11", Pod: "10.244.1.5"},
		{Name: "leaf2", IPv4: "172.80.80.12", Pod: "10.244.2.9"},
		{Name: "spine1", IPv4: "172.80.80.21"},
		{Name: "gnmic", IPv4: "172.80.80.41", Aliases: []string{"collector"}},
	}

	shards, err := clabernetesinternaldirectruntime.RenderPeerDirectoryShards(peers)
	if err != nil {
		t.Fatal(err)
	}

	if len(shards) != clabernetesinternaldirectruntime.PeerDirectoryShardCount {
		t.Fatalf("rendered %d shards, want %d",
			len(shards), clabernetesinternaldirectruntime.PeerDirectoryShardCount)
	}

	seen := map[string]int{}

	for shard, content := range shards {
		parsed, parseErr := clabernetesinternaldirectruntime.ParsePeerDirectory(content)
		if parseErr != nil {
			t.Fatalf("shard %d: %v", shard, parseErr)
		}

		for _, peer := range parsed {
			if clabernetesinternaldirectruntime.PeerDirectoryShard(peer.Name) != shard {
				t.Fatalf("peer %q rendered into shard %d", peer.Name, shard)
			}

			seen[peer.Name] = shard

			if peer.Name == "leaf1" && peer.Pod != "10.244.1.5" {
				t.Fatalf("leaf1 lost its Pod address: %#v", peer)
			}
		}
	}

	if len(seen) != len(peers) {
		t.Fatalf("rendered peers = %v, want every peer once", seen)
	}

	// Shard assignment is a function of the name alone, so it never moves with membership.
	again, err := clabernetesinternaldirectruntime.RenderPeerDirectoryShards(peers[:1])
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(again[seen["leaf1"]], shards[seen["leaf1"]]) {
		t.Fatal("leaf1 shard bytes changed with a different membership of other shards")
	}
}

func TestReadPeerDirectoryMergesShardsAndTheLegacyFile(t *testing.T) {
	t.Parallel()

	render := func(peers ...clabernetesinternaldirectruntime.PeerIdentity) []byte {
		content, err := clabernetesinternaldirectruntime.RenderPeerDirectory(peers)
		if err != nil {
			t.Fatal(err)
		}

		return content
	}

	files := map[string][]byte{
		"/dir/peers-0.json": render(
			clabernetesinternaldirectruntime.PeerIdentity{Name: "r2", IPv4: "172.20.20.12"},
		),
		"/dir/peers-5.json": render(
			clabernetesinternaldirectruntime.PeerIdentity{
				Name: "r1", IPv4: "172.20.20.11", Pod: "10.244.0.5",
			},
		),
		"/dir/peers-6.json": []byte(""),
		"/dir/peers-7.json": []byte("{not json"),
		// The legacy single file still resolves during the transition, without overriding a
		// shard entry of the same name.
		"/dir/peers.json": render(
			clabernetesinternaldirectruntime.PeerIdentity{Name: "r1", IPv4: "10.9.9.9"},
			clabernetesinternaldirectruntime.PeerIdentity{Name: "legacy", IPv4: "172.20.20.99"},
		),
	}

	readFile := func(path string) ([]byte, error) {
		content, ok := files[path]
		if !ok {
			return nil, os.ErrNotExist
		}

		return content, nil
	}

	peers, err := clabernetesinternaldirectruntime.ReadPeerDirectory("/dir", readFile)
	if err == nil || !strings.Contains(err.Error(), "peers-7.json") {
		t.Fatalf("malformed shard must be reported, got %v", err)
	}

	if len(peers) != 3 || peers[0].Name != "legacy" || peers[1].Name != "r1" ||
		peers[1].IPv4 != "172.20.20.11" || peers[1].Pod != "10.244.0.5" ||
		peers[2].Name != "r2" {
		t.Fatalf("merged peers = %#v", peers)
	}

	// Absence of every file is no error and no peers.
	peers, err = clabernetesinternaldirectruntime.ReadPeerDirectory(
		"/empty",
		func(string) ([]byte, error) { return nil, os.ErrNotExist },
	)
	if err != nil || len(peers) != 0 {
		t.Fatalf("empty directory = %#v, %v", peers, err)
	}

	// Any other read failure is reported.
	_, err = clabernetesinternaldirectruntime.ReadPeerDirectory(
		"/broken",
		func(string) ([]byte, error) { return nil, errReadFailure },
	)
	if err == nil {
		t.Fatal("read failures must be reported")
	}
}

func TestManagementMeshMACDerivesFromTheManagementIPv4Address(t *testing.T) {
	t.Parallel()

	mac, err := clabernetesinternaldirectruntime.ManagementMeshMAC(
		netip.MustParseAddr("172.80.80.11"),
	)
	if err != nil || mac.String() != "06:c9:ac:50:50:0b" {
		t.Fatalf("ManagementMeshMAC() = %v, %v", mac, err)
	}

	mapped, err := clabernetesinternaldirectruntime.ManagementMeshMAC(
		netip.MustParseAddr("::ffff:172.80.80.11"),
	)
	if err != nil || mapped.String() != mac.String() {
		t.Fatalf("ManagementMeshMAC(mapped) = %v, %v", mapped, err)
	}

	if _, err = clabernetesinternaldirectruntime.ManagementMeshMAC(
		netip.MustParseAddr("fd00::1"),
	); err == nil {
		t.Fatal("an IPv6 address has no derived identity")
	}
}
