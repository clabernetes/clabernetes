package constants

const (
	// ImagePullThroughModeAlways a constant representing the "always" image pull through mode for
	// the launcher pods.
	ImagePullThroughModeAlways = "always"

	// ImagePullThroughModeNever a constant representing the "never" image pull through mode for
	// the launcher pods.
	ImagePullThroughModeNever = "never"

	// ImagePullThroughModeAuto a constant representing the "auto" image pull through mode for
	// the launcher pods.
	ImagePullThroughModeAuto = "auto"

	// NamingModePrefixed is a constant representing the "prefixed" enum(ish) value for the naming
	// field of a Topology.
	NamingModePrefixed = "prefixed"

	// NamingModeNonPrefixed is a constant representing the "non-prefixed" enum(ish) value for the
	// naming field of a Topology.
	NamingModeNonPrefixed = "non-prefixed"

	// NamingModeGlobal is a constant representing the (default) "global" enum(ish) value for the
	// naming field of a Topology.
	NamingModeGlobal = "global"

	// NodeStatusFile is the file we write the node status to for launchers -- this is also used
	// by the deployment for startup/liveness probes.
	NodeStatusFile = "/clabernetes/.nodestatus"

	// NodeStatusHealthy is the content of hte NodeStatuesFile when/if the node in the launcher is
	// healthy.
	NodeStatusHealthy = "healthy"

	// NodeStatusReady is reported in the topology.status.nodereadiness map for nodes that have
	// their startup/readiness probes in a succeeding state.
	NodeStatusReady = "ready"

	// NodeStatusNotReady is reported in the topology.status.nodereadiness map for nodes that have
	// deployments running but do not report ready (via startup/readiness probes).
	NodeStatusNotReady = "notready"

	// NodeStatusUnknown is reported in the topology.status.nodereadiness map for nodes that have
	// no deployment available for whatever reason.
	NodeStatusUnknown = "unknown"

	// NodeStatusDeploymentDisabled is reported in the topology.status.nodereadiness map when the
	// parent topology has the "c9s.run/disableDeployments" label set.
	NodeStatusDeploymentDisabled = "deploymentDisabled"
)
