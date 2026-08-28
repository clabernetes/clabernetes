package directruntime

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// The peer directory is the namespace-wide map of logical node names onto management
// addresses. It rides one namespace-scoped ConfigMap mounted into every device Pod, so lab
// membership changes reach running Pods through the kubelet's ConfigMap sync — no Deployment
// change, no Pod restart. The launch boundary realizes it into /etc/hosts before the device
// process starts, and the connectivity sidecar re-asserts it while the Pod runs.
const (
	// PeerDirectoryConfigMapName is the namespace-scoped ConfigMap carrying the directory.
	PeerDirectoryConfigMapName = "c9s-peer-directory"
	// PeerDirectoryConfigMapKey is the single file key inside that ConfigMap.
	PeerDirectoryConfigMapKey = "peers.json"
	// ApplicationPeerDirectoryRoot is where application containers mount the directory.
	ApplicationPeerDirectoryRoot = "/var/lib/clabernetes/peer-directory"
	// ConnectivityPeerDirectoryRoot is where the connectivity sidecar mounts the directory.
	ConnectivityPeerDirectoryRoot = "/var/run/clabernetes/peer-directory"
	// peerDirectorySchemaVersion gates forward-incompatible directory changes.
	peerDirectorySchemaVersion = 1
)

// errUnsupportedPeerDirectory marks directory content from an incompatible schema.
var errUnsupportedPeerDirectory = errors.New("unsupported peer directory")

// PeerIdentity is one logical node's resolvable identity: its name, the extra names Docker DNS
// would also resolve (declared aliases and chassis component runtime names), and its bare
// management addresses.
type PeerIdentity struct {
	Name    string   `json:"name"`
	IPv4    string   `json:"ipv4,omitempty"`
	IPv6    string   `json:"ipv6,omitempty"`
	Aliases []string `json:"aliases,omitempty"`
}

// PeerDirectory is the serialized namespace directory.
type PeerDirectory struct {
	SchemaVersion int            `json:"schemaVersion"`
	Peers         []PeerIdentity `json:"peers,omitempty"`
}

// RenderPeerDirectory serializes the directory deterministically (sorted by peer name) so
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
