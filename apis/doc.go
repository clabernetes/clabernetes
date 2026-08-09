package apis

const (
	// Group is the main (only!) group for clabernetes CRDs.
	Group = "c9s.run"

	// Topology is the Kind of the Topology custom resource.
	Topology = "topology"

	// TopologyKindContainerlab is the "containerlab" kind of topology.
	TopologyKindContainerlab = "containerlab"

	// TopologyKindKne is the "kne" kind of topology.
	TopologyKindKne = "kne"

	// ImageRequest is the Kind of the ImageRequest custom resource.
	ImageRequest = "imageRequest"

	// Node is the Kind of the Node custom resource.
	Node = "node"

	// Link is the Kind of the Link custom resource.
	Link = "link"

	// LauncherProfile is the Kind of the LauncherProfile custom resource.
	LauncherProfile = "launcherProfile"
)
