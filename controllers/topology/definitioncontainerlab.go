package topology

import (
	"fmt"
	"slices"
	"strings"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	claberneteserrors "github.com/srl-labs/clabernetes/errors"
	clabernetesutil "github.com/srl-labs/clabernetes/util"
	clabernetesutilcontainerlab "github.com/srl-labs/clabernetes/util/containerlab"
	"gopkg.in/yaml.v3"
)

type containerlabDefinitionProcessor struct {
	*definitionProcessor
}

const networkModeContainerPrefix = "container:"

// parseNetworkModeContainer parses a network-mode value and returns the primary node name
// if it's a container network-mode (e.g., "container:node-a" returns "node-a").
// Returns empty string if not a container network-mode.
func parseNetworkModeContainer(networkMode string) string {
	if !strings.HasPrefix(networkMode, networkModeContainerPrefix) {
		return ""
	}

	return strings.TrimPrefix(networkMode, networkModeContainerPrefix)
}

// nodeGroup represents a group of nodes that share the same network namespace.
// The primary node is the one that other nodes reference via network-mode: container:<primary>.
type nodeGroup struct {
	primary     string
	secondaries []string
}

// buildNodeGroups analyzes the topology nodes and identifies groups of nodes that share
// network namespaces via the network-mode: container:<name> directive.
// Returns a map of primary node names to their groups, and a set of secondary node names.
func buildNodeGroups(
	nodes map[string]*clabernetesutilcontainerlab.NodeDefinition,
) (groups map[string]*nodeGroup, secondaryNodes map[string]string) {
	groups = make(map[string]*nodeGroup)
	secondaryNodes = make(map[string]string) // maps secondary -> primary

	// First pass: identify all secondaries and their primaries
	for nodeName, nodeDefinition := range nodes {
		primaryName := parseNetworkModeContainer(nodeDefinition.NetworkMode)
		if primaryName == "" {
			continue
		}

		// This node is a secondary
		secondaryNodes[nodeName] = primaryName

		// Add to the primary's group
		if groups[primaryName] == nil {
			groups[primaryName] = &nodeGroup{
				primary:     primaryName,
				secondaries: []string{},
			}
		}

		groups[primaryName].secondaries = append(groups[primaryName].secondaries, nodeName)
	}

	return groups, secondaryNodes
}

func (p *containerlabDefinitionProcessor) Process() error {
	// load the containerlab topo from the CR to make sure its all good
	containerlabConfig, err := clabernetesutilcontainerlab.LoadContainerlabConfig(
		p.topology.Spec.Definition.Containerlab,
	)
	if err != nil {
		p.logger.Criticalf("failed parsing containerlab config, error: %s", err)

		return err
	}

	// we may have *different defaults per "sub-topology" so we do a cheater "deep copy" by just
	// marshalling here and unmarshalling per node in the process func :)
	defaultsYAML, err := yaml.Marshal(containerlabConfig.Topology.Defaults)
	if err != nil {
		return err
	}

	// check this here so we only have to check it once
	removeTopologyPrefix := p.getRemoveTopologyPrefix()

	// Build node groups for distributed systems (e.g., SR-SIM with network-mode: container:<name>)
	nodeGroups, secondaryNodes := buildNodeGroups(containerlabConfig.Topology.Nodes)

	for nodeName := range containerlabConfig.Topology.Nodes {
		// Skip secondary nodes - they will be processed as part of their primary's group
		if _, isSecondary := secondaryNodes[nodeName]; isSecondary {
			continue
		}

		// Get the group for this node (if it's a primary with secondaries)
		group := nodeGroups[nodeName]

		err = p.processConfigForNodeGroup(
			containerlabConfig,
			nodeName,
			group,
			secondaryNodes,
			defaultsYAML,
			removeTopologyPrefix,
		)
		if err != nil {
			return err
		}
	}

	return nil
}

func getDefaultPorts() []*clabernetesutilcontainerlab.TypedPort {
	return []*clabernetesutilcontainerlab.TypedPort{
		{
			Protocol:        clabernetesconstants.TCP,
			ExposePort:      0,
			DestinationPort: clabernetesconstants.PortFTP,
		},
		{
			Protocol:        clabernetesconstants.TCP,
			ExposePort:      0,
			DestinationPort: clabernetesconstants.PortSSH,
		},
		{
			Protocol:        clabernetesconstants.TCP,
			ExposePort:      0,
			DestinationPort: clabernetesconstants.PortTelnet,
		},
		{
			Protocol:        clabernetesconstants.TCP,
			ExposePort:      0,
			DestinationPort: clabernetesconstants.PortHTTP,
		},
		{
			Protocol:        clabernetesconstants.UDP,
			ExposePort:      0,
			DestinationPort: clabernetesconstants.PortSNMP,
		},
		{
			Protocol:        clabernetesconstants.TCP,
			ExposePort:      0,
			DestinationPort: clabernetesconstants.PortHTTPS,
		},
		{
			Protocol:        clabernetesconstants.TCP,
			ExposePort:      0,
			DestinationPort: clabernetesconstants.PortNETCONF,
		},
		{
			Protocol:        clabernetesconstants.TCP,
			ExposePort:      0,
			DestinationPort: clabernetesconstants.PortQemuTelnet,
		},
		{
			Protocol:        clabernetesconstants.TCP,
			ExposePort:      0,
			DestinationPort: clabernetesconstants.PortVNC,
		},
		{
			Protocol:        clabernetesconstants.TCP,
			ExposePort:      0,
			DestinationPort: clabernetesconstants.PortGNMIArista,
		},
		{
			Protocol:        clabernetesconstants.TCP,
			ExposePort:      0,
			DestinationPort: clabernetesconstants.PortGNMI,
		},
		{
			Protocol:        clabernetesconstants.TCP,
			ExposePort:      0,
			DestinationPort: clabernetesconstants.PortGRIBI,
		},
		{
			Protocol:        clabernetesconstants.TCP,
			ExposePort:      0,
			DestinationPort: clabernetesconstants.PortP4RT,
		},
		{
			Protocol:        clabernetesconstants.TCP,
			ExposePort:      0,
			DestinationPort: clabernetesconstants.PortGNMINokia,
		},
	}
}

func getNextPort(allocatedPorts []int64) int64 {
	for possiblePort := 60_000; possiblePort < 65_000; possiblePort++ {
		var possiblePortFound bool

		if slices.Contains(allocatedPorts, int64(possiblePort)) {
			possiblePortFound = true
		}

		if !possiblePortFound {
			return int64(possiblePort)
		}
	}

	return 0
}

func typedPortsFromPortDefinitions(
	portDefinitions []string,
) []*clabernetesutilcontainerlab.TypedPort {
	typedPorts := make([]*clabernetesutilcontainerlab.TypedPort, len(portDefinitions))

	for idx, portDefinition := range portDefinitions {
		typedPort, _ := clabernetesutilcontainerlab.ProcessPortDefinition(portDefinition)

		typedPorts[idx] = typedPort
	}

	return typedPorts
}

func recordAllocatedPorts(
	typedPortDefinitions ...[]*clabernetesutilcontainerlab.TypedPort,
) (allocatedTCPExposePorts, allocatedUDPExposePorts []int64) {
	allocatedTCPExposePorts = make([]int64, 0)
	allocatedUDPExposePorts = make([]int64, 0)

	for _, typedPortDefinitionSlice := range typedPortDefinitions {
		for _, typedPort := range typedPortDefinitionSlice {
			if typedPort.ExposePort == 0 {
				continue
			}

			switch typedPort.Protocol {
			case clabernetesconstants.TCP:
				allocatedTCPExposePorts = append(allocatedTCPExposePorts, typedPort.ExposePort)
			case clabernetesconstants.UDP:
				allocatedUDPExposePorts = append(allocatedUDPExposePorts, typedPort.ExposePort)
			}
		}
	}

	return allocatedTCPExposePorts, allocatedUDPExposePorts
}

func insertMissingDefaultPorts(
	typedDefaultPorts, typedNodePorts []*clabernetesutilcontainerlab.TypedPort,
) []*clabernetesutilcontainerlab.TypedPort {
	for _, defaultTypedPort := range getDefaultPorts() {
		var alreadyDefined bool

		for _, typedPort := range typedDefaultPorts {
			if typedPort.DestinationPort != defaultTypedPort.DestinationPort {
				continue
			}

			if typedPort.Protocol != defaultTypedPort.Protocol {
				continue
			}

			alreadyDefined = true

			break
		}

		if alreadyDefined {
			continue
		}

		for _, typedPort := range typedNodePorts {
			if typedPort.DestinationPort != defaultTypedPort.DestinationPort {
				continue
			}

			if typedPort.Protocol != defaultTypedPort.Protocol {
				continue
			}

			alreadyDefined = true

			break
		}

		if alreadyDefined {
			continue
		}

		typedDefaultPorts = append(typedDefaultPorts, defaultTypedPort)
	}

	return typedDefaultPorts
}

func processPorts(
	topologyDefaultPorts, topologyNodePorts []string,
) (defaultPortsAsString, nodePortsAsString []string) {
	typedDefaultPorts := typedPortsFromPortDefinitions(topologyDefaultPorts)
	typedNodePorts := typedPortsFromPortDefinitions(topologyNodePorts)

	allocatedTCPExposePorts, allocatedUDPExposePorts := recordAllocatedPorts(
		typedDefaultPorts,
		typedNodePorts,
	)

	typedDefaultPorts = insertMissingDefaultPorts(typedDefaultPorts, typedNodePorts)

	defaultPortsAsString = make([]string, len(typedDefaultPorts))
	nodePortsAsString = make([]string, len(typedNodePorts))

	// now we go through and allocated "expose" ports for any ports that don't have this already
	// set -- unlike containerlab by default we need to know the expose ports ahead of time to
	// properly set up the lb bits.
	for idx, typedPort := range typedDefaultPorts {
		if typedPort.ExposePort != 0 {
			defaultPortsAsString[idx] = typedPort.AsContainerlabPortDefinition()

			continue
		}

		switch typedPort.Protocol {
		case clabernetesconstants.TCP:
			allocatedPort := getNextPort(allocatedTCPExposePorts)

			allocatedTCPExposePorts = append(allocatedTCPExposePorts, allocatedPort)

			typedPort.ExposePort = allocatedPort
		case clabernetesconstants.UDP:
			allocatedPort := getNextPort(allocatedUDPExposePorts)

			allocatedUDPExposePorts = append(allocatedUDPExposePorts, allocatedPort)

			typedPort.ExposePort = allocatedPort
		}

		defaultPortsAsString[idx] = typedPort.AsContainerlabPortDefinition()
	}

	for idx, typedPort := range typedNodePorts {
		if typedPort.ExposePort != 0 {
			nodePortsAsString[idx] = typedPort.AsContainerlabPortDefinition()

			continue
		}

		switch typedPort.Protocol {
		case clabernetesconstants.TCP:
			allocatedPort := getNextPort(allocatedTCPExposePorts)

			allocatedTCPExposePorts = append(allocatedTCPExposePorts, allocatedPort)

			typedPort.ExposePort = allocatedPort
		case clabernetesconstants.UDP:
			allocatedPort := getNextPort(allocatedUDPExposePorts)

			allocatedUDPExposePorts = append(allocatedUDPExposePorts, allocatedPort)

			typedPort.ExposePort = allocatedPort
		}

		nodePortsAsString[idx] = typedPort.AsContainerlabPortDefinition()
	}

	return defaultPortsAsString, nodePortsAsString
}

func getKindsForNode(
	clabTopo *clabernetesutilcontainerlab.Topology,
	nodeName string,
) map[string]*clabernetesutilcontainerlab.NodeDefinition {
	nodeKind := clabTopo.Defaults.Kind
	if clabTopo.Nodes[nodeName].Kind != "" {
		nodeKind = clabTopo.Nodes[nodeName].Kind
	}

	// we only want to snag our "sub topology" specific kind, otherwise we can just put nil
	// for the "kinds" part.
	if nodeKind != "" {
		nodeTopoKind, ok := clabTopo.Kinds[nodeKind]
		if ok {
			kindForNode := map[string]*clabernetesutilcontainerlab.NodeDefinition{
				nodeKind: nodeTopoKind,
			}

			if kindForNode[nodeKind].Ports == nil {
				// see util/containerlab/types; we dont want nil ports for now at least.
				kindForNode[nodeKind].Ports = []string{}
			}

			return kindForNode
		}
	}

	return nil
}

func getDestinationLinkEndpoint(
	targetNode string,
	interestingEndpoint clabernetesapisv1alpha1.LinkEndpoint,
	uninterestingEndpoint clabernetesapisv1alpha1.LinkEndpoint,
) string {
	if targetNode == clabernetesconstants.HostKeyword {
		// It is a containerlab host entry, so the original provided interface is preserved
		return fmt.Sprintf(
			"%s:%s",
			clabernetesconstants.HostKeyword,
			uninterestingEndpoint.InterfaceName,
		)
	}

	return fmt.Sprintf(
		"%s:%s-%s",
		clabernetesconstants.HostKeyword,
		interestingEndpoint.NodeName,
		interestingEndpoint.InterfaceName,
	)
}

// nodeGroupContext holds the context needed for processing a node group.
type nodeGroupContext struct {
	containerlabConfig *clabernetesutilcontainerlab.Config
	primaryNodeName    string
	group              *nodeGroup
	groupNodeNames     []string
	groupNodesSet      clabernetesutil.StringSet
	deepCopiedDefaults *clabernetesutilcontainerlab.NodeDefinition
	disableExpose      bool
	disableAutoExpose  bool
}

// buildGroupNodesList returns the list and set of nodes in the group.
func buildGroupNodesList(
	primaryNodeName string,
	group *nodeGroup,
) (groupNodeNames []string, groupNodesSet clabernetesutil.StringSet) {
	groupNodeNames = []string{primaryNodeName}
	if group != nil {
		groupNodeNames = append(groupNodeNames, group.secondaries...)
	}

	groupNodesSet = clabernetesutil.NewStringSetWithValues(groupNodeNames...)

	return groupNodeNames, groupNodesSet
}

// buildNodesMapForGroup builds the nodes map for the sub-topology.
func buildNodesMapForGroup(
	ctx *nodeGroupContext,
) map[string]*clabernetesutilcontainerlab.NodeDefinition {
	nodesMap := make(map[string]*clabernetesutilcontainerlab.NodeDefinition)

	for _, nodeName := range ctx.groupNodeNames {
		nodeDefinition := ctx.containerlabConfig.Topology.Nodes[nodeName]

		isSecondaryNode := parseNetworkModeContainer(nodeDefinition.NetworkMode) != ""

		switch {
		case isSecondaryNode:
			nodeDefinition.Ports = []string{}
		case !ctx.disableExpose && !ctx.disableAutoExpose:
			defaultPorts, nodePorts := processPorts(
				ctx.containerlabConfig.Topology.Defaults.Ports,
				nodeDefinition.Ports,
			)

			ctx.deepCopiedDefaults.Ports = defaultPorts
			nodeDefinition.Ports = nodePorts
		default:
			nodeDefinition.Ports = []string{}
		}

		nodesMap[nodeName] = nodeDefinition
	}

	return nodesMap
}

// collectKindsForGroup collects all kinds used by nodes in the group.
func collectKindsForGroup(
	topology *clabernetesutilcontainerlab.Topology,
	groupNodeNames []string,
) map[string]*clabernetesutilcontainerlab.NodeDefinition {
	kindsMap := make(map[string]*clabernetesutilcontainerlab.NodeDefinition)

	for _, nodeName := range groupNodeNames {
		nodeKinds := getKindsForNode(topology, nodeName)
		for kindName, kindDef := range nodeKinds {
			if _, exists := kindsMap[kindName]; !exists {
				kindsMap[kindName] = kindDef
			}
		}
	}

	if len(kindsMap) == 0 {
		return nil
	}

	return kindsMap
}

func collectGroupsForGroup(
	topology *clabernetesutilcontainerlab.Topology,
	groupNodeNames []string,
) map[string]*clabernetesutilcontainerlab.NodeDefinition {
	if topology.Groups == nil {
		return nil
	}

	groupsMap := make(map[string]*clabernetesutilcontainerlab.NodeDefinition)

	for _, nodeName := range groupNodeNames {
		nodeDefinition, ok := topology.Nodes[nodeName]
		if !ok || nodeDefinition.Group == "" {
			continue
		}

		groupName := nodeDefinition.Group

		if _, alreadyCollected := groupsMap[groupName]; alreadyCollected {
			continue
		}

		groupDefinition, groupOk := topology.Groups[groupName]
		if groupOk {
			groupsMap[groupName] = groupDefinition
		}
	}

	if len(groupsMap) == 0 {
		return nil
	}

	return groupsMap
}

// moveDefaultsPortsToPrimary moves default ports to the primary node when there are secondaries.
func moveDefaultsPortsToPrimary(
	nodesMap map[string]*clabernetesutilcontainerlab.NodeDefinition,
	primaryNodeName string,
	group *nodeGroup,
	defaults *clabernetesutilcontainerlab.NodeDefinition,
) {
	if group == nil || len(group.secondaries) == 0 {
		return
	}

	primaryNode := nodesMap[primaryNodeName]
	allPorts := make([]string, 0, len(defaults.Ports)+len(primaryNode.Ports))
	allPorts = append(allPorts, defaults.Ports...)
	allPorts = append(allPorts, primaryNode.Ports...)
	primaryNode.Ports = allPorts

	defaults.Ports = []string{}
}

// linkEndpoints holds parsed link endpoint information.
type linkEndpoints struct {
	endpointA clabernetesapisv1alpha1.LinkEndpoint
	endpointB clabernetesapisv1alpha1.LinkEndpoint
}

// parseLinkEndpoints parses and validates link endpoints.
func parseLinkEndpoints(link *clabernetesutilcontainerlab.LinkDefinition) (*linkEndpoints, error) {
	if len(link.Endpoints) != clabernetesapisv1alpha1.LinkEndpointElementCount {
		return nil, fmt.Errorf(
			"%w: endpoint '%q' has wrong syntax, unexpected number of items",
			claberneteserrors.ErrParse, link.Endpoints,
		)
	}

	endpointAParts := strings.Split(link.Endpoints[0], ":")
	endpointBParts := strings.Split(link.Endpoints[1], ":")

	if len(endpointAParts) != clabernetesapisv1alpha1.LinkEndpointElementCount ||
		len(endpointBParts) != clabernetesapisv1alpha1.LinkEndpointElementCount {
		return nil, fmt.Errorf(
			"%w: endpoint '%q' has wrong syntax, bad endpoint:interface config",
			claberneteserrors.ErrParse, link.Endpoints,
		)
	}

	return &linkEndpoints{
		endpointA: clabernetesapisv1alpha1.LinkEndpoint{
			NodeName:      endpointAParts[0],
			InterfaceName: endpointAParts[1],
		},
		endpointB: clabernetesapisv1alpha1.LinkEndpoint{
			NodeName:      endpointBParts[0],
			InterfaceName: endpointBParts[1],
		},
	}, nil
}

// processConfigForNodeGroup processes a group of nodes that share a network namespace.
// For standalone nodes (group is nil), it processes just that single node.
// For grouped nodes (distributed systems like SR-SIM), it creates a single sub-topology
// containing all nodes in the group so they can be deployed in the same pod and share
// the network namespace.
// The secondaryNodes map is used to resolve tunnel destinations - if a remote node is a
// secondary, the tunnel should point to its primary's service instead.
func (p *containerlabDefinitionProcessor) processConfigForNodeGroup(
	containerlabConfig *clabernetesutilcontainerlab.Config,
	primaryNodeName string,
	group *nodeGroup,
	secondaryNodes map[string]string,
	defaultsYAML []byte,
	removeTopologyPrefix bool,
) error {
	deepCopiedDefaults := &clabernetesutilcontainerlab.NodeDefinition{}

	err := yaml.Unmarshal(defaultsYAML, deepCopiedDefaults)
	if err != nil {
		return err
	}

	groupNodeNames, groupNodesSet := buildGroupNodesList(primaryNodeName, group)

	ctx := &nodeGroupContext{
		containerlabConfig: containerlabConfig,
		primaryNodeName:    primaryNodeName,
		group:              group,
		groupNodeNames:     groupNodeNames,
		groupNodesSet:      groupNodesSet,
		deepCopiedDefaults: deepCopiedDefaults,
		disableExpose:      p.topology.Spec.Expose.DisableExpose,
		disableAutoExpose:  p.topology.Spec.Expose.DisableAutoExpose,
	}

	nodesMap := buildNodesMapForGroup(ctx)
	resolvedKinds := collectKindsForGroup(containerlabConfig.Topology, groupNodeNames)
	resolvedGroups := collectGroupsForGroup(containerlabConfig.Topology, groupNodeNames)

	moveDefaultsPortsToPrimary(nodesMap, primaryNodeName, group, deepCopiedDefaults)

	p.reconcileData.ResolvedConfigs[primaryNodeName] = &clabernetesutilcontainerlab.Config{
		Name: fmt.Sprintf("clabernetes-%s", primaryNodeName),
		Mgmt: containerlabConfig.Mgmt,
		Topology: &clabernetesutilcontainerlab.Topology{
			Defaults: deepCopiedDefaults,
			Kinds:    resolvedKinds,
			Groups:   resolvedGroups,
			Nodes:    nodesMap,
			Links:    nil,
		},
		Prefix: clabernetesutil.ToPointer(""),
	}

	return p.processLinksForNodeGroup(
		containerlabConfig,
		primaryNodeName,
		groupNodesSet,
		secondaryNodes,
		removeTopologyPrefix,
	)
}

// processLinksForNodeGroup processes all links for nodes in the group.
func (p *containerlabDefinitionProcessor) processLinksForNodeGroup(
	containerlabConfig *clabernetesutilcontainerlab.Config,
	primaryNodeName string,
	groupNodesSet clabernetesutil.StringSet,
	secondaryNodes map[string]string,
	removeTopologyPrefix bool,
) error {
	for _, link := range containerlabConfig.Topology.Links {
		endpoints, err := parseLinkEndpoints(link)
		if err != nil {
			p.logger.Critical(err.Error())

			return err
		}

		err = p.processLinkForGroup(
			link,
			endpoints,
			primaryNodeName,
			groupNodesSet,
			secondaryNodes,
			removeTopologyPrefix,
		)
		if err != nil {
			return err
		}
	}

	return nil
}

// processLinkForGroup processes a single link for a node group.
func (p *containerlabDefinitionProcessor) processLinkForGroup(
	link *clabernetesutilcontainerlab.LinkDefinition,
	endpoints *linkEndpoints,
	primaryNodeName string,
	groupNodesSet clabernetesutil.StringSet,
	secondaryNodes map[string]string,
	removeTopologyPrefix bool,
) error {
	endpointAInGroup := groupNodesSet.Contains(endpoints.endpointA.NodeName)
	endpointBInGroup := groupNodesSet.Contains(endpoints.endpointB.NodeName)

	if !endpointAInGroup && !endpointBInGroup {
		return nil
	}

	if endpointAInGroup && endpointBInGroup {
		p.reconcileData.ResolvedConfigs[primaryNodeName].Topology.Links = append(
			p.reconcileData.ResolvedConfigs[primaryNodeName].Topology.Links,
			link,
		)

		return nil
	}

	interestingEndpoint, uninterestingEndpoint := endpoints.endpointA, endpoints.endpointB
	if !endpointAInGroup {
		interestingEndpoint, uninterestingEndpoint = endpoints.endpointB, endpoints.endpointA
	}

	p.reconcileData.ResolvedConfigs[primaryNodeName].Topology.Links = append(
		p.reconcileData.ResolvedConfigs[primaryNodeName].Topology.Links,
		&clabernetesutilcontainerlab.LinkDefinition{
			LinkConfig: clabernetesutilcontainerlab.LinkConfig{
				Endpoints: []string{
					fmt.Sprintf("%s:%s",
						interestingEndpoint.NodeName,
						interestingEndpoint.InterfaceName,
					),
					getDestinationLinkEndpoint(
						uninterestingEndpoint.NodeName,
						interestingEndpoint,
						uninterestingEndpoint,
					),
				},
			},
		},
	)

	if uninterestingEndpoint.NodeName == clabernetesconstants.HostKeyword {
		return nil
	}

	destinationNodeName := uninterestingEndpoint.NodeName
	if remotePrimary, isSecondary := secondaryNodes[uninterestingEndpoint.NodeName]; isSecondary {
		destinationNodeName = remotePrimary
	}

	p.reconcileData.ResolvedTunnels[primaryNodeName] = append(
		p.reconcileData.ResolvedTunnels[primaryNodeName],
		&clabernetesapisv1alpha1.PointToPointTunnel{
			LocalNode:  interestingEndpoint.NodeName,
			RemoteNode: uninterestingEndpoint.NodeName,
			Destination: resolveConnectivityDestination(
				p.topology.Name,
				destinationNodeName,
				p.topology.Namespace,
				removeTopologyPrefix,
				p.configManagerGetter,
			),
			LocalInterface:  interestingEndpoint.InterfaceName,
			RemoteInterface: uninterestingEndpoint.InterfaceName,
		},
	)

	return nil
}
