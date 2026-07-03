package constants

const (
	// LabelKubernetesName is the key for the standard kubernetes app.kubernetes.io/name label --
	// some tools use this label so we want to put it on all the deployments we spawn.
	LabelKubernetesName = "app.kubernetes.io/name"

	// LabelApp is the label key for the simple app name.
	LabelApp = "clabernetes/app"

	// LabelName is the label key for the name of the project/application.
	LabelName = "clabernetes/name"

	// LabelComponent is the label key for the component label, it should define the component/tier
	// in the app, i.e. "manager".
	LabelComponent = "clabernetes/component"

	// LabelTopologyOwner is the label indicating the topology that owns the given resource.
	LabelTopologyOwner = "clabernetes/topologyOwner"

	// LabelTopologyNode is the label indicating the node the deployment represents in a topology.
	LabelTopologyNode = "clabernetes/topologyNode"

	// LabelTopologyKind is the label indicating the resource *kind* the object is associated with.
	// For example, a "containerlab" kind, or a "kne" kind.
	LabelTopologyKind = "clabernetes/topologyKind"

	// LabelTopologyServiceType is a label that identifies what flavor of service a given service
	// is -- that is, it is either a "connectivity" service, or an "expose" service; note that
	// this is strictly a clabernetes concept, obviously not a kubernetes one!
	LabelTopologyServiceType = "clabernetes/topologyServiceType"

	// LabelLinkEndpointA is the label indicating the launcher node that terminates the "a" side
	// of a link -- this is set on Link objects so launchers can watch "their" links with a simple
	// label selector.
	LabelLinkEndpointA = "clabernetes/linkEndpointA"

	// LabelLinkEndpointB is the label indicating the launcher node that terminates the "b" side
	// of a link -- this is set on Link objects so launchers can watch "their" links with a simple
	// label selector.
	LabelLinkEndpointB = "clabernetes/linkEndpointB"
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
	LabelClickerNodeConfigured = "clabernetes/clickerNodeConfigured"
	// LabelClickerNodeTarget is the target node for the clicker job.
	LabelClickerNodeTarget = "clabernetes/clickerNodeTarget"
)

const (
	// LabelIgnoreReconcile indicates that controller should ignore reconciling a given topology.
	// Note that this basically ignored during deletion since our controller doest do anything in
	// the delete case (owner reference handles clean up).
	LabelIgnoreReconcile = "clabernetes/ignoreReconcile"

	// LabelDisableDeployments indicates that controller should reconcile normally but not create
	// update or delete any deployments.
	LabelDisableDeployments = "clabernetes/disableDeployments"
)

const (
	// LabelPullerImageHash is a label that holds the (shortened) hash of the image tag that the
	// puller is trying to pull onto a node.
	LabelPullerImageHash = "clabernetes/pullerImageHash"

	// LabelPullerNodeTarget is a label that holds the node name that is being targeted by the
	// puller pod.
	LabelPullerNodeTarget = "clabernetes/pullerNodeTarget"
)
