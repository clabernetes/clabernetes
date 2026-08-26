package deviceplan

// ArtifactNodeDirectory returns the opaque directory assigned to one logical Node inside the
// shared artifact root. Logical identities are intentionally not interpreted as filesystem paths.
func ArtifactNodeDirectory(nodeID string) string {
	return "node-" + shortDigest(nodeID)
}
