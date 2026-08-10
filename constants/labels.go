package constants

const (
	// LabelPrefix is the namespace for labels owned by c9s.
	LabelPrefix = "c9s.run"

	// LabelKubernetesName is the key for the standard kubernetes app.kubernetes.io/name label --
	// some tools use this label so we want to put it on all the deployments we spawn.
	LabelKubernetesName = "app.kubernetes.io/name"

	// LabelApp is the label key for the simple app name.
	LabelApp = LabelPrefix + "/app"

	// LabelName is the label key for the name of the project/application.
	LabelName = LabelPrefix + "/name"

	// LabelComponent is the label key for the component label, it should define the component/tier
	// in the app, i.e. "manager".
	LabelComponent = LabelPrefix + "/component"

	// LabelTopologyOwner is the label indicating the topology that owns the given resource.
	LabelTopologyOwner = LabelPrefix + "/topologyOwner"

	// LabelTopologyNode is the label indicating the node the deployment represents in a topology.
	LabelTopologyNode = LabelPrefix + "/topologyNode"

	// LabelTopologyKind is the label indicating the resource *kind* the object is associated with.
	// For example, a "containerlab" kind.
	LabelTopologyKind = LabelPrefix + "/topologyKind"

	// LabelTopologyServiceType is a label that identifies what flavor of service a given service
	// is -- that is, it is either a "connectivity" service, or an "expose" service; note that
	// this is strictly a clabernetes concept, obviously not a kubernetes one!
	LabelTopologyServiceType = LabelPrefix + "/topologyServiceType"
)

const (
	// AnnotationLinkAttachmentsDigest is the pod (template) annotation holding the digest of the
	// set of link attachments (local interface + materialization mode) of the launcher's node
	// group -- attachment set changes roll the pod (containerlab wiring is boot time), while
	// remote-end-only changes ("rewires") keep the digest stable and are handled live by the
	// launcher. The launcher reads the annotation via the downward api and compares it against
	// the digest of the links it fetched to know its view is complete.
	AnnotationLinkAttachmentsDigest = "clabernetes/linkAttachmentsDigest"

	// AnnotationNodeConfigDigest is the pod (template) annotation holding the digest of the
	// launcher-relevant node configuration (the node definitions of the launcher's group, the
	// expose port allocations, and the management network settings) -- so config changes that
	// are not otherwise visible in the deployment spec still roll the pod.
	AnnotationNodeConfigDigest = "clabernetes/nodeConfigDigest"
)

const (
	// TopologyServiceTypeFabric is one of the allowed values for the LabelTopologyServiceType label
	// type -- this indicates that this service is of the type that facilitates the connectivity
	// between containerlab devices in the cluster.
	TopologyServiceTypeFabric = "fabric"
	// TopologyServiceTypeExpose is one of the allowed values for the LabelTopologyServiceType label
	// type -- this indicates that this service is of the type that is used for exposing ports on
	// a containerlab node via a LoadBalancer service.
	TopologyServiceTypeExpose = "expose"
)

const (
	// LabelClickerNodeConfigured is a label that is set on nodes that have been tickled via the
	// clabernetes clicker tool -- the value is the unix timestamp that the node was tickled.
	LabelClickerNodeConfigured = LabelPrefix + "/clickerNodeConfigured"
	// LabelClickerNodeTarget is the target node for the clicker job.
	LabelClickerNodeTarget = LabelPrefix + "/clickerNodeTarget"
)

const (
	// LabelIgnoreReconcile indicates that controller should ignore reconciling a given topology.
	// Note that this basically ignored during deletion since our controller doest do anything in
	// the delete case (owner reference handles clean up).
	LabelIgnoreReconcile = LabelPrefix + "/ignoreReconcile"

	// LabelDisableDeployments indicates that controller should reconcile normally but not create
	// update or delete any deployments.
	LabelDisableDeployments = LabelPrefix + "/disableDeployments"
)

const (
	// LabelPullerImageHash is a label that holds the (shortened) hash of the image tag that the
	// puller is trying to pull onto a node.
	LabelPullerImageHash = LabelPrefix + "/pullerImageHash"

	// LabelPullerNodeTarget is a label that holds the node name that is being targeted by the
	// puller pod.
	LabelPullerNodeTarget = LabelPrefix + "/pullerNodeTarget"
)
