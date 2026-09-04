package directruntime

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// The peer directory is the namespace-wide map of logical node names onto management
// addresses and onto the Pod currently realizing each node. It rides a fixed set of
// namespace-scoped ConfigMap shards projected into every device Pod, so lab membership changes
// reach running Pods through the kubelet's ConfigMap sync — no Deployment change, no Pod
// restart — and a change to one node rewrites one shard. The launch boundary realizes the
// names into /etc/hosts before the device process starts, and the connectivity sidecar
// re-asserts them and derives the management mesh peer state while the Pod runs.
const (
	// PeerDirectoryConfigMapName is the legacy single namespace-scoped ConfigMap that carried
	// the directory before sharding; the controller removes it.
	PeerDirectoryConfigMapName = "c9s-peer-directory"
	// PeerDirectoryConfigMapKey is the single file key inside every directory ConfigMap.
	PeerDirectoryConfigMapKey = "peers.json"
	// PeerDirectoryShardCount is the fixed number of directory shards. It is fixed so the set
	// of objects a Pod mounts never changes with namespace size: growth never recreates Pods.
	PeerDirectoryShardCount = 8
	// ApplicationPeerDirectoryRoot is where application containers mount the directory.
	ApplicationPeerDirectoryRoot = "/var/lib/clabernetes/peer-directory"
	// ConnectivityPeerDirectoryRoot is where the connectivity sidecar mounts the directory.
	ConnectivityPeerDirectoryRoot = "/var/run/clabernetes/peer-directory"
	// peerDirectorySchemaVersion gates forward-incompatible directory changes.
	peerDirectorySchemaVersion = 1
)

var (
	// errUnsupportedPeerDirectory marks directory content from an incompatible schema.
	errUnsupportedPeerDirectory = errors.New("unsupported peer directory")
	// errManagementMeshMAC marks a management address that carries no derived mesh identity.
	errManagementMeshMAC = errors.New("management mesh identity requires an IPv4 address")
)

// PeerIdentity is one logical node's resolvable identity: its name, the extra names Docker DNS
// would also resolve (declared aliases and chassis component runtime names), its bare
// management addresses, and the address of the Pod currently realizing it.
type PeerIdentity struct {
	Name    string   `json:"name"`
	IPv4    string   `json:"ipv4,omitempty"`
	IPv6    string   `json:"ipv6,omitempty"`
	Aliases []string `json:"aliases,omitempty"`
	// Pod is the bare IPv4 address of the Pod realizing the node, empty until the Pod holds
	// one. Peers forward management traffic for this node's addresses to it.
	Pod string `json:"pod,omitempty"`
}

// PeerDirectory is the serialized directory (one shard, or the legacy whole).
type PeerDirectory struct {
	SchemaVersion int            `json:"schemaVersion"`
	Peers         []PeerIdentity `json:"peers,omitempty"`
}

// PeerDirectoryShardConfigMapName names the ConfigMap carrying one shard.
func PeerDirectoryShardConfigMapName(shard int) string {
	return PeerDirectoryConfigMapName + "-" + strconv.Itoa(shard)
}

// PeerDirectoryShardFileName names one shard's file inside the mounted directory.
func PeerDirectoryShardFileName(shard int) string {
	return "peers-" + strconv.Itoa(shard) + ".json"
}

// PeerDirectoryShard assigns a node name to its shard: a stable function of the name alone,
// so an entry never moves between shards as the namespace grows or shrinks.
func PeerDirectoryShard(name string) int {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(name))

	return int(hash.Sum32() % PeerDirectoryShardCount)
}

// RenderPeerDirectory serializes one directory deterministically (sorted by peer name) so
// repeated reconciles of unchanged content produce identical bytes.
func RenderPeerDirectory(peers []PeerIdentity) ([]byte, error) {
	sorted := slices.Clone(peers)
	slices.SortFunc(sorted, func(left, right PeerIdentity) int {
		return strings.Compare(left.Name, right.Name)
	})

	return json.Marshal(PeerDirectory{
		SchemaVersion: peerDirectorySchemaVersion,
		Peers:         sorted,
	})
}

// RenderPeerDirectoryShards splits the directory into its fixed shard set and serializes each
// shard deterministically; an empty shard still renders (a valid, peerless directory) so every
// shard object exists.
func RenderPeerDirectoryShards(peers []PeerIdentity) ([][]byte, error) {
	byShard := make([][]PeerIdentity, PeerDirectoryShardCount)

	for _, peer := range peers {
		shard := PeerDirectoryShard(peer.Name)
		byShard[shard] = append(byShard[shard], peer)
	}

	rendered := make([][]byte, PeerDirectoryShardCount)

	for shard := range byShard {
		content, err := RenderPeerDirectory(byShard[shard])
		if err != nil {
			return nil, err
		}

		rendered[shard] = content
	}

	return rendered, nil
}

// ParsePeerDirectory deserializes a directory, rejecting content from a newer schema.
func ParsePeerDirectory(content []byte) ([]PeerIdentity, error) {
	directory := PeerDirectory{}
	if err := json.Unmarshal(content, &directory); err != nil {
		return nil, fmt.Errorf("parsing peer directory: %w", err)
	}

	if directory.SchemaVersion != peerDirectorySchemaVersion {
		return nil, fmt.Errorf(
			"%w: schema %d is not the supported %d",
			errUnsupportedPeerDirectory,
			directory.SchemaVersion,
			peerDirectorySchemaVersion,
		)
	}

	return directory.Peers, nil
}

// peerDirectoryFiles lists the files a mounted directory may carry: every shard plus the
// legacy single file, so a Pod keeps resolving names across the transition to shards.
func peerDirectoryFiles(root string) []string {
	files := make([]string, 0, PeerDirectoryShardCount+1)

	for shard := range PeerDirectoryShardCount {
		files = append(files, filepath.Join(root, PeerDirectoryShardFileName(shard)))
	}

	return append(files, filepath.Join(root, PeerDirectoryConfigMapKey))
}

// ReadPeerDirectory merges every present directory file under root into one sorted peer list.
// A missing file is not an error (the directory is an optional projection: absence means no
// peers yet); an unreadable or malformed one is, though the successfully read files are still
// returned so a single bad shard never blanks the directory.
func ReadPeerDirectory(
	root string,
	readFile func(path string) ([]byte, error),
) ([]PeerIdentity, error) {
	peers := []PeerIdentity(nil)
	seen := map[string]bool{}

	var errs error

	for _, path := range peerDirectoryFiles(root) {
		content, err := readFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}

			errs = errors.Join(errs, err)

			continue
		}

		if len(content) == 0 {
			continue
		}

		parsed, err := ParsePeerDirectory(content)
		if err != nil {
			errs = errors.Join(errs, fmt.Errorf("%s: %w", filepath.Base(path), err))

			continue
		}

		for _, peer := range parsed {
			if peer.Name == "" || seen[peer.Name] {
				continue
			}

			seen[peer.Name] = true
			peers = append(peers, peer)
		}
	}

	slices.SortFunc(peers, func(left, right PeerIdentity) int {
		return strings.Compare(left.Name, right.Name)
	})

	return peers, errs
}

// The mesh tunnel-endpoint MAC prefix: locally administered, unicast, and distinct from the
// 02:c9 prefix of the deterministic gateway identity.
const (
	managementMeshMACPrefix0 = 0x06
	managementMeshMACPrefix1 = 0xc9
)

// ManagementMeshMAC derives a Pod's mesh tunnel-endpoint link-layer identity from its
// management IPv4 address. Management addresses are unique within a namespace, so the derived
// identities are too, and every peer computes them without coordination: a peer's neighbor
// entry and the peer's own tunnel endpoint agree by construction.
func ManagementMeshMAC(management netip.Addr) (net.HardwareAddr, error) {
	management = management.Unmap()
	if !management.Is4() {
		return nil, fmt.Errorf("%w: %q", errManagementMeshMAC, management)
	}

	octets := management.As4()

	return net.HardwareAddr{
		managementMeshMACPrefix0, managementMeshMACPrefix1,
		octets[0], octets[1], octets[2], octets[3],
	}, nil
}
