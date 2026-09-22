package constants

// AnnotationStartupHold is controller-owned admission state on Topology-generated Nodes.
// Its value is the owning Topology UID; it is removed durably when a batch is admitted.
const AnnotationStartupHold = "c9s.run/startup-hold"

// AnnotationStartupAdmitted persists installation-wide startup admission for a Node UID.
const AnnotationStartupAdmitted = "c9s.run/startup-admitted"
