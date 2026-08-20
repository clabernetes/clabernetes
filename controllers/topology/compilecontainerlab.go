package topology

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	clabernetesapis "github.com/clabernetes/clabernetes/apis"
	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	claberneteserrors "github.com/clabernetes/clabernetes/errors"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
	clabtypes "github.com/srl-labs/containerlab/types"
	"gopkg.in/yaml.v3"
	k8svalidation "k8s.io/apimachinery/pkg/util/validation"
)

const (
	unknownFieldMatchCount = 3
	yamlMappingPairSize    = 2
	importedNodeLayerCount = 4
)

// compileContainerlabDefinition compiles a containerlab topology definition: every node gets
// the topology defaults and its kind expanded *into* its definition (so the emitted Node
// objects are self contained), and the links section becomes the compiled wire list.
func compileContainerlabDefinition(
	logger claberneteslogging.Instance,
	definition string,
	diagnostics *compileDiagnostics,
) (*CompiledTopology, error) {
	containerlabConfig, unknownFields, err := clabernetesutilcontainerlab.LoadContainerlabConfig(
		definition,
	)
	if err != nil {
		logger.Criticalf("failed parsing containerlab config, error: %s", err)

		return nil, err
	}

	for _, unknownField := range unknownFields {
		diagnostics.add(diagnosticFromUnknownField(unknownField))
	}

	importedTopology, err := loadImportedNodeTopology(definition)
	if err != nil {
		return nil, err
	}

	managementFieldLines, err := topLevelMappingFieldLines(definition, "mgmt")
	if err != nil {
		return nil, err
	}

	validateManagementPolicy(managementFieldLines, diagnostics)

	compiled := &CompiledTopology{
		Kind: clabernetesapis.TopologyKindContainerlab,
		Nodes: make(
			map[string]*clabernetesutilcontainerlab.NodeDefinition,
			len(containerlabConfig.Topology.Nodes),
		),
		Mgmt: containerlabConfig.Mgmt,
	}

	nodeNames := make([]string, 0, len(containerlabConfig.Topology.Nodes))
	for nodeName := range containerlabConfig.Topology.Nodes {
		nodeNames = append(nodeNames, nodeName)
	}

	sort.Strings(nodeNames)

	for _, nodeName := range nodeNames {
		compiled.Nodes[nodeName], err = flattenNodeDefinition(
			importedTopology,
			nodeName,
		)
		if err != nil {
			return nil, err
		}

		normalizeNodePorts(diagnostics, nodeName, compiled.Nodes[nodeName])
		consumeExposePortsLabel(diagnostics, nodeName, compiled.Nodes[nodeName])
		dropUnusableNodeLabels(diagnostics, nodeName, compiled.Nodes[nodeName])
	}

	validateNodeNetworkModes(compiled.Nodes, diagnostics)

	compiled.Links, err = compileContainerlabLinks(containerlabConfig, diagnostics)
	if err != nil {
		logger.Criticalf("failed compiling containerlab links, error: %s", err)

		return nil, err
	}

	for _, warning := range diagnostics.warnings() {
		logger.Warnf("topology compile: %s", formatCompilerDiagnostic(warning))
	}

	diagnosticErr := diagnostics.err()
	if diagnosticErr != nil {
		return nil, diagnosticErr
	}

	return compiled, nil
}

func validateManagementPolicy(
	fieldLines map[string]int,
	diagnostics *compileDiagnostics,
) {
	fields := []struct {
		name    string
		path    string
		message string
	}{
		{
			name:    "network",
			path:    "mgmt.network",
			message: "container runtime management network names are accepted and ignored",
		},
		{
			name:    "bridge",
			path:    "mgmt.bridge",
			message: "container runtime management bridges are accepted and ignored",
		},
		{
			name:    "mtu",
			path:    "mgmt.mtu",
			message: "Docker management-network MTU is accepted and ignored",
		},
		{
			name: "external-access",
			path: "mgmt.external-access",
			message: "Docker management-network external access is accepted and ignored; " +
				"exposure is governed by Kubernetes Services",
		},
		{
			name:    "skip-when-unused",
			path:    "mgmt.skip-when-unused",
			message: "conditional container runtime network creation is accepted and ignored",
		},
		{
			name:    "driver-opts",
			path:    "mgmt.driver-opts",
			message: "container runtime network driver options are accepted and ignored",
		},
	}
	for _, field := range fields {
		line, present := fieldLines[field.name]
		if !present {
			continue
		}

		diagnostics.add(CompilerDiagnostic{
			Code:    "ignored-management-field",
			Path:    field.path,
			Line:    line,
			Message: field.message,
			Warning: true,
		})
	}
}

func topLevelMappingFieldLines(definition, fieldName string) (map[string]int, error) {
	document := &yaml.Node{}

	err := yaml.Unmarshal([]byte(definition), document)
	if err != nil {
		return nil, err
	}

	if len(document.Content) == 0 || document.Content[0].Kind != yaml.MappingNode {
		return map[string]int{}, nil
	}

	root := document.Content[0]
	for index := 0; index+1 < len(root.Content); index += 2 {
		key, value := root.Content[index], root.Content[index+1]
		if key.Value != fieldName || value.Kind != yaml.MappingNode {
			continue
		}

		lines := make(map[string]int, len(value.Content)/yamlMappingPairSize)
		for childIndex := 0; childIndex+1 < len(value.Content); childIndex += yamlMappingPairSize {
			childKey := value.Content[childIndex]
			lines[childKey.Value] = childKey.Line
		}

		return lines, nil
	}

	return map[string]int{}, nil
}

// validateNodeNetworkModes mirrors the Node CRD's container:<primary> contract in the compiler
// and additionally verifies references and cycles that the single-object CRD cannot see. This is
// required for strict, API-free validation and dry-run: callers must not need to create a Node
// before learning that a native host/none mode or an impossible launcher group is unsupported.
func validateNodeNetworkModes(
	nodes map[string]*clabernetesutilcontainerlab.NodeDefinition,
	diagnostics *compileDiagnostics,
) {
	nodeNames := make([]string, 0, len(nodes))
	for nodeName := range nodes {
		nodeNames = append(nodeNames, nodeName)
	}

	sort.Strings(nodeNames)

	for _, nodeName := range nodeNames {
		networkMode := nodes[nodeName].NetworkMode
		if networkMode == "" {
			continue
		}

		primary := clabernetesutilcontainerlab.ParseNetworkModeContainer(networkMode)

		path := fmt.Sprintf("topology.nodes.%s.network-mode", nodeName)
		if primary == "" || len(k8svalidation.IsDNS1123Label(primary)) != 0 {
			diagnostics.add(CompilerDiagnostic{
				Code: "unsupported-network-mode",
				Path: path,
				Message: fmt.Sprintf(
					"node %q network-mode %q is unsupported; "+
						"c9s accepts only container:<primary node name>",
					nodeName,
					networkMode,
				),
			})

			continue
		}

		if _, exists := nodes[primary]; !exists {
			diagnostics.add(CompilerDiagnostic{
				Code: "unknown-network-mode-primary",
				Path: path,
				Message: fmt.Sprintf(
					"node %q shares a launcher with nonexistent primary node %q",
					nodeName,
					primary,
				),
			})

			continue
		}

		seen := map[string]bool{nodeName: true}

		current := primary
		for current != "" {
			if seen[current] {
				diagnostics.add(CompilerDiagnostic{
					Code: "network-mode-cycle",
					Path: path,
					Message: fmt.Sprintf(
						"node %q network-mode participates in a launcher-group cycle",
						nodeName,
					),
				})

				break
			}

			seen[current] = true

			nextNode, exists := nodes[current]
			if !exists {
				break
			}

			current = clabernetesutilcontainerlab.ParseNetworkModeContainer(
				nextNode.NetworkMode,
			)
		}
	}
}

var unknownFieldPattern = regexp.MustCompile(`^line (\d+): field ([^ ]+)`)

func diagnosticFromUnknownField(message string) CompilerDiagnostic {
	diagnostic := CompilerDiagnostic{
		Code:    "unsupported-field",
		Message: message,
	}

	matches := unknownFieldPattern.FindStringSubmatch(message)
	if len(matches) != unknownFieldMatchCount {
		return diagnostic
	}

	diagnostic.Line, _ = strconv.Atoi(matches[1])
	diagnostic.Path = matches[2]

	return diagnostic
}

// normalizeNodePorts strips Docker-style host pinning that direct Node resources cannot
// preserve: the Pod-side port is kept, and a warning tells the author the host half was
// dropped. Host port pinning only ever described the local Docker host, so its loss cannot
// change lab behavior inside the cluster.
func normalizeNodePorts(
	diagnostics *compileDiagnostics,
	nodeName string,
	nodeDefinition *clabernetesutilcontainerlab.NodeDefinition,
) {
	for idx, portDefinition := range nodeDefinition.Ports {
		normalized := clabernetesutilcontainerlab.NormalizePortDefinition(portDefinition)
		if normalized == portDefinition {
			continue
		}

		diagnostics.add(CompilerDiagnostic{
			Code: "host-port-pinning",
			Path: fmt.Sprintf("topology.nodes.%s.ports[%d]", nodeName, idx),
			Message: fmt.Sprintf(
				"node %q port %q host pinning was dropped; the Pod-side port %q is kept",
				nodeName,
				portDefinition,
				normalized,
			),
			Warning: true,
		})

		nodeDefinition.Ports[idx] = normalized
	}
}

// consumeExposePortsLabel translates c9s' portable containerlab label directive into the same
// destination-port intent a direct Node declares in spec.ports. A label is used at the source
// boundary because adding a normal containerlab ports entry would publish that port on the local
// Docker host. The directive is consumed here -- before reserved label filtering -- and never
// becomes Kubernetes metadata.
func consumeExposePortsLabel(
	diagnostics *compileDiagnostics,
	nodeName string,
	nodeDefinition *clabernetesutilcontainerlab.NodeDefinition,
) {
	value, ok := nodeDefinition.Labels[clabernetesconstants.LabelExposePorts]
	if !ok {
		return
	}

	delete(nodeDefinition.Labels, clabernetesconstants.LabelExposePorts)

	seenDestinations := make(map[string]bool, len(nodeDefinition.Ports))

	for _, portDefinition := range nodeDefinition.Ports {
		typedPort, err := clabernetesutilcontainerlab.ProcessPortDefinition(portDefinition)
		if err != nil {
			// Existing ports are validated by the Node API/controller. Do not turn this source
			// compatibility helper into a second validator for the ordinary ports field.
			continue
		}

		seenDestinations[canonicalPortDefinition(typedPort)] = true
	}

	for idx, portDefinition := range strings.Split(value, ",") {
		portDefinition = strings.TrimSpace(portDefinition)

		typedPort, err := clabernetesutilcontainerlab.ProcessPortDefinition(portDefinition)
		if err != nil {
			diagnostics.add(CompilerDiagnostic{
				Code: "invalid-expose-ports-label",
				Path: fmt.Sprintf(
					"topology.nodes.%s.labels.%s[%d]",
					nodeName,
					clabernetesconstants.LabelExposePorts,
					idx,
				),
				Message: fmt.Sprintf(
					"node %q expose ports label entry %q is invalid: %s",
					nodeName,
					portDefinition,
					err,
				),
			})

			continue
		}

		canonical := canonicalPortDefinition(typedPort)
		if seenDestinations[canonical] {
			continue
		}

		seenDestinations[canonical] = true
		nodeDefinition.Ports = append(nodeDefinition.Ports, canonical)
	}
}

func canonicalPortDefinition(port *clabernetesutilcontainerlab.TypedPort) string {
	return fmt.Sprintf(
		"%d/%s",
		port.DestinationPort,
		strings.ToLower(port.Protocol),
	)
}

// dropUnusableNodeLabels records containerlab node labels that cannot be carried onto the emitted
// Node's metadata. The compile fails after collecting diagnostics, so the temporary flattened Node
// can be pruned without silently changing any emitted resource.
func dropUnusableNodeLabels(
	diagnostics *compileDiagnostics,
	nodeName string,
	nodeDefinition *clabernetesutilcontainerlab.NodeDefinition,
) {
	for key, value := range nodeDefinition.Labels {
		var reason string

		switch {
		case isReservedNodeLabel(key):
			reason = "the label is reserved by c9s"
		default:
			problems := append(
				k8svalidation.IsQualifiedName(key),
				k8svalidation.IsValidLabelValue(value)...,
			)
			if len(problems) == 0 {
				continue
			}

			reason = strings.Join(problems, "; ")
		}

		diagnostics.add(CompilerDiagnostic{
			Code: "unusable-node-label",
			Path: fmt.Sprintf("topology.nodes.%s.labels.%s", nodeName, key),
			Message: fmt.Sprintf(
				"node %q label %q cannot become a Kubernetes label: %s",
				nodeName,
				key,
				reason,
			),
		})

		delete(nodeDefinition.Labels, key)
	}
}

func isReservedNodeLabel(key string) bool {
	if strings.HasPrefix(key, clabernetesconstants.LabelPrefix+"/") {
		return true
	}

	switch key {
	case clabernetesconstants.LabelKubernetesName,
		clabernetesconstants.LabelApp,
		clabernetesconstants.LabelName,
		clabernetesconstants.LabelTopologyOwner,
		clabernetesconstants.LabelTopologyKind,
		clabernetesconstants.LabelTopologyNode:
		return true
	default:
		return false
	}
}

type importedNodeTopologyDocument struct {
	Topology *importedNodeTopology `yaml:"topology"`
}

// importedNodeTopology deliberately omits Links: the c9s Link compiler parses both native brief
// and structured endpoint syntax separately, while this projection exists only to invoke the
// imported package's node inheritance behavior.
type importedNodeTopology struct {
	Defaults *clabtypes.NodeDefinition            `yaml:"defaults,omitempty"`
	Kinds    map[string]*clabtypes.NodeDefinition `yaml:"kinds,omitempty"`
	Nodes    map[string]*clabtypes.NodeDefinition `yaml:"nodes,omitempty"`
	Groups   map[string]*clabtypes.NodeDefinition `yaml:"groups,omitempty"`
}

func loadImportedNodeTopology(definition string) (*clabtypes.Topology, error) {
	document := &importedNodeTopologyDocument{}

	err := yaml.Unmarshal([]byte(definition), document)
	if err != nil {
		return nil, err
	}

	if document.Topology == nil {
		return nil, fmt.Errorf(
			"%w: containerlab definition has no topology section",
			claberneteserrors.ErrParse,
		)
	}

	topology := &clabtypes.Topology{
		Defaults: document.Topology.Defaults,
		Kinds:    document.Topology.Kinds,
		Nodes:    document.Topology.Nodes,
		Groups:   document.Topology.Groups,
	}
	if topology.Defaults == nil {
		topology.Defaults = &clabtypes.NodeDefinition{}
	}

	return topology, nil
}

// flattenNodeDefinition asks the imported containerlab Topology for every supported effective
// value. c9s owns the primitive shape, but it does not duplicate containerlab's inheritance rules.
func flattenNodeDefinition(
	topology *clabtypes.Topology,
	nodeName string,
) (*clabernetesutilcontainerlab.NodeDefinition, error) {
	flattened := &clabernetesutilcontainerlab.NodeDefinition{
		Kind:          topology.GetNodeKind(nodeName),
		Group:         topology.GetNodeGroup(nodeName),
		Type:          topology.GetNodeType(nodeName),
		Image:         topology.GetNodeImage(nodeName),
		License:       topology.GetNodeLicense(nodeName),
		StartupConfig: topology.GetNodeStartupConfig(nodeName),
		Entrypoint:    topology.GetNodeEntrypoint(nodeName),
		Cmd:           topology.GetNodeCmd(nodeName),
		Exec:          slices.Clone(topology.GetNodeExec(nodeName)),
		User:          topology.GetNodeUser(nodeName),
		Devices:       slices.Clone(topology.GetNodeDevices(nodeName)),
		CapAdd:        slices.Clone(topology.GetNodeCapAdd(nodeName)),
		SecurityOpts:  slices.Clone(topology.GetNodeSecurityOpts(nodeName)),
		Tmpfs:         maps.Clone(topology.GetNodeTmpfs(nodeName)),
		ShmSize:       topology.GetNodeShmSize(nodeName),
		Ports:         importedNodePorts(topology, nodeName),
		NetworkMode:   topology.GetNodeNetworkMode(nodeName),
		Env:           maps.Clone(topology.GetNodeEnv(nodeName)),
		EnvFiles:      slices.Clone(topology.GetNodeEnvFiles(nodeName)),
		Sysctls:       maps.Clone(topology.GetSysCtl(nodeName)),
		Labels:        maps.Clone(topology.GetNodeLabels(nodeName)),
	}

	layers := importedNodeLayers(topology, nodeName)
	flattened.EnforceStartupConfig = clonePointer(firstImportedPointer(
		layers,
		func(definition *clabtypes.NodeDefinition) *bool { return definition.EnforceStartupConfig },
	))
	flattened.SuppressStartupConfig = clonePointer(firstImportedPointer(
		layers,
		func(definition *clabtypes.NodeDefinition) *bool {
			return definition.SuppressStartupConfig
		},
	))
	flattened.Privileged = clonePointer(firstImportedPointer(
		layers,
		func(definition *clabtypes.NodeDefinition) *bool { return definition.Privileged },
	))

	binds, err := topology.GetNodeBinds(nodeName)
	if err != nil {
		return nil, err
	}

	flattened.Binds = slices.Clone(binds)

	if sourceNode := topology.Nodes[nodeName]; sourceNode != nil {
		// Containerlab intentionally does not inherit per-node management addresses.
		flattened.MgmtIPv4 = sourceNode.MgmtIPv4
		flattened.MgmtIPv6 = sourceNode.MgmtIPv6
	}

	if firstImportedPointer(
		layers,
		func(definition *clabtypes.NodeDefinition) *clabtypes.ConfigDispatcher {
			return definition.Config
		},
	) != nil {
		flattened.Config = &clabernetesutilcontainerlab.ConfigDispatcher{}

		transcodeErr := transcodeImportedField(
			topology.GetNodeConfigDispatcher(nodeName),
			flattened.Config,
		)
		if transcodeErr != nil {
			return nil, transcodeErr
		}
	}

	if firstImportedPointer(
		layers,
		func(definition *clabtypes.NodeDefinition) *clabtypes.DNSConfig {
			return definition.DNS
		},
	) != nil {
		flattened.DNS = &clabernetesutilcontainerlab.DNSConfig{}

		transcodeErr := transcodeImportedField(topology.GetNodeDns(nodeName), flattened.DNS)
		if transcodeErr != nil {
			return nil, transcodeErr
		}
	}

	if firstImportedPointer(
		layers,
		func(definition *clabtypes.NodeDefinition) *clabtypes.Extras {
			return definition.Extras
		},
	) != nil {
		flattened.Extras = &clabernetesutilcontainerlab.Extras{}

		transcodeErr := transcodeImportedField(
			topology.GetNodeExtras(nodeName),
			flattened.Extras,
		)
		if transcodeErr != nil {
			return nil, transcodeErr
		}
	}

	if firstImportedPointer(
		layers,
		func(definition *clabtypes.NodeDefinition) *clabtypes.CertificateConfig {
			return definition.Certificate
		},
	) != nil {
		certificate := topology.GetCertificateConfig(nodeName)

		flattened.Certificate = &clabernetesutilcontainerlab.CertificateConfig{
			Issue:   clonePointer(certificate.Issue),
			KeySize: certificate.KeySize,
			SANs:    slices.Clone(certificate.SANs),
		}
		if certificate.ValidityDuration > 0 {
			flattened.Certificate.ValidityDuration = certificate.ValidityDuration.String()
		}
	}

	components := topology.GetComponents(nodeName)
	if components != nil {
		transcodeErr := transcodeImportedField(components, &flattened.Components)
		if transcodeErr != nil {
			return nil, transcodeErr
		}
	}

	return flattened, nil
}

func importedNodeLayers(
	topology *clabtypes.Topology,
	nodeName string,
) []*clabtypes.NodeDefinition {
	layers := make([]*clabtypes.NodeDefinition, 0, importedNodeLayerCount)
	if node := topology.Nodes[nodeName]; node != nil {
		layers = append(layers, node)
	}

	if group := topology.GetGroup(topology.GetNodeGroup(nodeName)); group != nil {
		layers = append(layers, group)
	}

	if kind := topology.GetKind(topology.GetNodeKind(nodeName)); kind != nil {
		layers = append(layers, kind)
	}

	layers = append(layers, topology.GetDefaults())

	return layers
}

func firstImportedPointer[T any](
	layers []*clabtypes.NodeDefinition,
	get func(*clabtypes.NodeDefinition) *T,
) *T {
	for _, layer := range layers {
		if value := get(layer); value != nil {
			return value
		}
	}

	return nil
}

func clonePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}

	cloned := *value

	return &cloned
}

// importedNodePorts retains the package's most-specific non-empty ports rule while preserving the
// original strings so strict diagnostics can still name Docker host-side pinning exactly.
func importedNodePorts(topology *clabtypes.Topology, nodeName string) []string {
	for _, layer := range importedNodeLayers(topology, nodeName) {
		if len(layer.Ports) != 0 {
			return slices.Clone(layer.Ports)
		}
	}

	return nil
}

func transcodeImportedField(source, destination any) error {
	raw, err := yaml.Marshal(source)
	if err != nil {
		return err
	}

	return yaml.Unmarshal(raw, destination)
}

// compileContainerlabLinks converts the containerlab links section into compiled wires.
func compileContainerlabLinks( //nolint:gocyclo
	containerlabConfig *clabernetesutilcontainerlab.Config,
	diagnostics *compileDiagnostics,
) ([]CompiledLink, error) {
	links := make([]CompiledLink, 0, len(containerlabConfig.Topology.Links))

	for linkIndex, link := range containerlabConfig.Topology.Links {
		linkPath := fmt.Sprintf("topology.links[%d]", linkIndex)
		if len(link.Labels) != 0 {
			diagnostics.add(CompilerDiagnostic{
				Code:    "unsupported-link-labels",
				Path:    linkPath + ".labels",
				Message: "link labels are not preserved by the c9s Link API",
			})
		}

		if len(link.Vars) != 0 {
			diagnostics.add(CompilerDiagnostic{
				Code:    "unsupported-link-vars",
				Path:    linkPath + ".vars",
				Message: "link vars are not preserved by the c9s Link API",
			})
		}

		switch link.Type {
		case "", "brief", "veth":
		default:
			diagnostics.add(unsupportedContainerlabLinkTypeDiagnostic(link.Type, linkPath))

			continue
		}

		if len(link.Endpoints) != clabernetesapisv1alpha1.LinkEndpointElementCount {
			return nil, fmt.Errorf(
				"%w: endpoint '%q' has wrong syntax, unexpected number of items",
				claberneteserrors.ErrParse,
				link.Endpoints,
			)
		}

		endpointAParts := strings.Split(link.Endpoints[0], ":")
		endpointBParts := strings.Split(link.Endpoints[1], ":")

		if len(endpointAParts) != clabernetesapisv1alpha1.LinkEndpointElementCount ||
			len(endpointBParts) != clabernetesapisv1alpha1.LinkEndpointElementCount {
			return nil, fmt.Errorf(
				"%w: endpoint '%q' has wrong syntax, bad node:interface config",
				claberneteserrors.ErrParse,
				link.Endpoints,
			)
		}

		invalidEndpoint := false

		for endpointIndex, endpointParts := range [][]string{endpointAParts, endpointBParts} {
			nodeName := endpointParts[0]

			if nodeName == clabernetesapisv1alpha1.LinkHostNodeName {
				continue
			}

			if nodeName == "mgmt-net" || nodeName == "macvlan" {
				diagnostics.add(CompilerDiagnostic{
					Code: "unsupported-special-endpoint",
					Path: fmt.Sprintf("%s.endpoints[%d]", linkPath, endpointIndex),
					Message: fmt.Sprintf(
						"special endpoint %q requires host networking that c9s does not provide",
						nodeName,
					),
				})

				invalidEndpoint = true

				continue
			}

			if _, exists := containerlabConfig.Topology.Nodes[nodeName]; !exists {
				diagnostics.add(CompilerDiagnostic{
					Code:    "unknown-link-endpoint",
					Path:    fmt.Sprintf("%s.endpoints[%d]", linkPath, endpointIndex),
					Message: fmt.Sprintf("link endpoint references nonexistent node %q", nodeName),
				})

				invalidEndpoint = true
			}
		}

		if endpointAParts[0] == clabernetesapisv1alpha1.LinkHostNodeName &&
			endpointBParts[0] == clabernetesapisv1alpha1.LinkHostNodeName {
			diagnostics.add(CompilerDiagnostic{
				Code:    "invalid-host-link",
				Path:    linkPath + ".endpoints",
				Message: "a c9s host link must have exactly one Node endpoint",
			})

			invalidEndpoint = true
		}

		if invalidEndpoint {
			continue
		}

		links = append(links, CompiledLink{
			EndpointA: clabernetesapisv1alpha1.LinkEndpointSpec{
				NodeName:      endpointAParts[0],
				InterfaceName: endpointAParts[1],
			},
			EndpointB: clabernetesapisv1alpha1.LinkEndpointSpec{
				NodeName:      endpointBParts[0],
				InterfaceName: endpointBParts[1],
			},
			MTU: link.MTU,
		})
	}

	return links, nil
}

func unsupportedContainerlabLinkTypeDiagnostic(linkType, linkPath string) CompilerDiagnostic {
	message := fmt.Sprintf(
		"native link type %q has no c9s topology-link equivalent",
		linkType,
	)

	if linkType == "host" {
		message = fmt.Sprintf(
			"explicit native link type %q requires structured endpoints that the "+
				"c9s topology compiler does not support; use brief endpoints",
			linkType,
		)
	}

	return CompilerDiagnostic{
		Code:    "unsupported-link-type",
		Path:    linkPath + ".type",
		Message: message,
	}
}
