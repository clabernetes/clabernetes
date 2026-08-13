package topology

import (
	"fmt"
	"maps"
	"regexp"
	"sort"
	"strconv"
	"strings"

	clabernetesapis "github.com/clabernetes/clabernetes/apis"
	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	claberneteserrors "github.com/clabernetes/clabernetes/errors"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
	"gopkg.in/yaml.v3"
	k8svalidation "k8s.io/apimachinery/pkg/util/validation"
)

const unknownFieldMatchCount = 3

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
		diagnostics.add(diagnosticFromUnknownField(unknownField), false)
	}

	if containerlabConfig.Mgmt != nil {
		diagnostics.add(CompilerDiagnostic{
			Code: "management-network-semantics",
			Path: "mgmt",
			Message: "topology-level management network settings are launcher-local in c9s and " +
				"do not create a shared cross-pod management network",
		}, false)
	}

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
			containerlabConfig.Topology,
			nodeName,
		)
		if err != nil {
			return nil, err
		}

		normalizeNodePorts(diagnostics, nodeName, compiled.Nodes[nodeName])
		consumeExposePortsLabel(diagnostics, nodeName, compiled.Nodes[nodeName])
		dropUnusableNodeLabels(diagnostics, nodeName, compiled.Nodes[nodeName])

		switch compiled.Nodes[nodeName].Kind {
		case "bridge", "ovs-bridge", "host":
			diagnostics.add(CompilerDiagnostic{
				Code: "unsupported-pseudo-node",
				Path: fmt.Sprintf("topology.nodes.%s.kind", nodeName),
				Message: fmt.Sprintf(
					"node %q uses native pseudo-node kind %q, which has no c9s "+
						"launcher implementation",
					nodeName,
					compiled.Nodes[nodeName].Kind,
				),
			}, true)
		}
	}

	validateNodeNetworkModes(compiled.Nodes, diagnostics)

	compiled.Links, err = compileContainerlabLinks(containerlabConfig, diagnostics)
	if err != nil {
		logger.Criticalf("failed compiling containerlab links, error: %s", err)

		return nil, err
	}

	diagnosticErr := diagnostics.err()
	if diagnosticErr != nil {
		return nil, diagnosticErr
	}

	return compiled, nil
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
			}, true)

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
			}, true)

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
				}, true)

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

// normalizeNodePorts rewrites the docker style "host:container" port entries that pasted
// containerlab topologies carry into the destination-only form Nodes accept -- the pod side port
// is an allocation clabernetes owns, so a pinned host side cannot be honored.
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
				"node %q port %q pins a host-side port, but c9s allocates launcher ports",
				nodeName,
				portDefinition,
			),
		}, false)

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
			}, true)

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

// dropUnusableNodeLabels removes containerlab node labels that cannot be carried onto the emitted
// Node's metadata. containerlab labels become docker labels, which accept far more than a
// kubernetes label does, so an unusable one has to be dropped here rather than making the Node
// rejected on create. clabernetes' own namespace and the individual keys its controllers reserve
// stay off limits too, since those labels carry meaning to reconciliation and selectors.
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
				"node %q label %q cannot become a Kubernetes label and would be omitted: %s",
				nodeName,
				key,
				reason,
			),
		}, false)

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

// flattenNodeDefinition merges the topology defaults, the node's kind, and the node's own
// definition into a single self contained node definition -- following containerlab's own
// inheritance rules: the most specific value wins for scalar fields, maps (env/sysctls) merge
// with the most specific entry winning, and binds/ports extend (defaults + kind + node).
func flattenNodeDefinition(
	topology *clabernetesutilcontainerlab.Topology,
	nodeName string,
) (*clabernetesutilcontainerlab.NodeDefinition, error) {
	flattened := &clabernetesutilcontainerlab.NodeDefinition{}

	nodeDefinition := topology.Nodes[nodeName]

	kindName := topology.Defaults.Kind
	if nodeDefinition.Kind != "" {
		kindName = nodeDefinition.Kind
	}

	// layer from least to most specific: defaults, kind, node
	layers := []*clabernetesutilcontainerlab.NodeDefinition{topology.Defaults}

	if kindDefinition, ok := topology.Kinds[kindName]; ok {
		layers = append(layers, kindDefinition)
	}

	layers = append(layers, nodeDefinition)

	for _, layer := range layers {
		if layer == nil {
			continue
		}

		err := overlayNodeDefinition(flattened, layer)
		if err != nil {
			return nil, err
		}
	}

	flattened.Kind = kindName

	return flattened, nil
}

// overlayNodeDefinition overlays the given layer onto the base node definition. Rather than
// hand-writing (and hand-maintaining) per-field merge logic over the whole containerlab
// vocabulary, the layer is marshaled and unmarshaled *onto* the base -- yaml unmarshal only
// touches fields present in the layer, which gives exactly the "most specific value wins"
// semantic for scalars and pointers. The map/extend style fields containerlab merges rather
// than replaces (env, sysctls, labels, binds, ports) are handled explicitly.
func overlayNodeDefinition(
	base,
	layer *clabernetesutilcontainerlab.NodeDefinition,
) error {
	mergedEnv := mergeMaps(base.Env, layer.Env)
	mergedSysctls := mergeMaps(base.Sysctls, layer.Sysctls)
	mergedLabels := mergeMaps(base.Labels, layer.Labels)
	mergedBinds := mergeSlices(base.Binds, layer.Binds)
	mergedPorts := mergeSlices(base.Ports, layer.Ports)

	layerYAML, err := yaml.Marshal(layer)
	if err != nil {
		return err
	}

	err = yaml.Unmarshal(layerYAML, base)
	if err != nil {
		return err
	}

	base.Env = mergedEnv
	base.Sysctls = mergedSysctls
	base.Labels = mergedLabels
	base.Binds = mergedBinds
	base.Ports = mergedPorts

	return nil
}

func mergeMaps(base, layer map[string]string) map[string]string {
	if len(base) == 0 && len(layer) == 0 {
		return nil
	}

	merged := make(map[string]string, len(base)+len(layer))

	maps.Copy(merged, base)
	maps.Copy(merged, layer)

	return merged
}

func mergeSlices(base, layer []string) []string {
	merged := make([]string, 0, len(base)+len(layer))
	seen := make(map[string]bool, len(base)+len(layer))

	for _, entry := range append(append([]string{}, base...), layer...) {
		if seen[entry] {
			continue
		}

		seen[entry] = true

		merged = append(merged, entry)
	}

	return merged
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
			}, false)
		}

		if len(link.Vars) != 0 {
			diagnostics.add(CompilerDiagnostic{
				Code:    "unsupported-link-vars",
				Path:    linkPath + ".vars",
				Message: "link vars are not preserved by the c9s Link API",
			}, false)
		}

		switch link.Type {
		case "", "brief", "veth":
		default:
			diagnostics.add(unsupportedContainerlabLinkTypeDiagnostic(link.Type, linkPath), true)

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
				}, true)

				invalidEndpoint = true

				continue
			}

			if _, exists := containerlabConfig.Topology.Nodes[nodeName]; !exists {
				diagnostics.add(CompilerDiagnostic{
					Code:    "unknown-link-endpoint",
					Path:    fmt.Sprintf("%s.endpoints[%d]", linkPath, endpointIndex),
					Message: fmt.Sprintf("link endpoint references nonexistent node %q", nodeName),
				}, true)

				invalidEndpoint = true
			}
		}

		if endpointAParts[0] == clabernetesapisv1alpha1.LinkHostNodeName &&
			endpointBParts[0] == clabernetesapisv1alpha1.LinkHostNodeName {
			diagnostics.add(CompilerDiagnostic{
				Code:    "invalid-host-link",
				Path:    linkPath + ".endpoints",
				Message: "a c9s host link must have exactly one Node endpoint",
			}, true)

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

	if linkType == "veth" || linkType == "host" {
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
