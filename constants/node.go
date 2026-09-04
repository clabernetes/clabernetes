package constants

const (
	// NodeStatusReady is reported in Node.status.readiness for nodes whose active runtime is
	// observed ready.
	NodeStatusReady = "ready"

	// NodeStatusNotReady is reported in Node.status.readiness for nodes whose workload exists but
	// does not report ready.
	NodeStatusNotReady = "notready"

	// NodeStatusUnknown is reported in Node.status.readiness for nodes whose workload readiness
	// cannot be observed.
	NodeStatusUnknown = "unknown"
)
