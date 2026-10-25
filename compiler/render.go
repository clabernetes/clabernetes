package compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strings"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
	clabnames "github.com/srl-labs/containerlab/names"
	clabtypes "github.com/srl-labs/containerlab/types"
	yamlv2 "gopkg.in/yaml.v2"
	"gopkg.in/yaml.v3"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
)

// RenderAll renders the Node, Link, and NodeProfile objects for the compiled topology through
// the engine once and decodes every manifest into the typed clabernetes objects. Each list is
// sorted by name. The rendered node names are the containerlab node names, sanitized only where
// Kubernetes cannot carry them -- the namespace is the topology boundary. Node-keyed policy on
// the Topology is written against the definition's names, so it is looked up by the node's
// source name.
//
// The single engine pass is deliberate: every primitive list a reconcile enforces comes from one
// render, and the render is not free (management networks, expose services, profiles).
//
// A failure means the engine produced something the c9s vocabulary cannot carry, which is a
// contract violation rather than a user error; RenderAll returns it marked by
// manifestContractError so the caller surfaces it instead of silently dropping an object -- the
// same contract decodeManifest carries.
func RenderAll(
	topology *clabernetesapisv1alpha1.Topology,
	compiled *CompiledTopology,
	configManagerGetter clabernetesconfig.ManagerGetterFunc,
) (
	nodes []*clabernetesapisv1alpha1.Node,
	links []*clabernetesapisv1alpha1.Link,
	profiles []*clabernetesapisv1alpha1.NodeProfile,
	err error,
) {
	unstructuredNodes, unstructuredLinks, unstructuredProfiles, renderErr := renderAll(
		topology,
		compiled,
		configManagerGetter,
	)
	if renderErr != nil {
		return nil, nil, nil, manifestContractError(renderErr)
	}

	nodes = make([]*clabernetesapisv1alpha1.Node, 0, len(unstructuredNodes))

	for i := range unstructuredNodes {
		node := &clabernetesapisv1alpha1.Node{}

		if decodeErr := decodeManifest(unstructuredNodes[i].Object, node); decodeErr != nil {
			return nil, nil, nil, manifestContractError(decodeErr)
		}

		nodes = append(nodes, node)
	}

	links = make([]*clabernetesapisv1alpha1.Link, 0, len(unstructuredLinks))

	for i := range unstructuredLinks {
		link := &clabernetesapisv1alpha1.Link{}

		if decodeErr := decodeManifest(unstructuredLinks[i].Object, link); decodeErr != nil {
			return nil, nil, nil, manifestContractError(decodeErr)
		}

		links = append(links, link)
	}

	profiles = make([]*clabernetesapisv1alpha1.NodeProfile, 0, len(unstructuredProfiles))

	for i := range unstructuredProfiles {
		profile := &clabernetesapisv1alpha1.NodeProfile{}

		if decodeErr := decodeManifest(unstructuredProfiles[i].Object, profile); decodeErr != nil {
			return nil, nil, nil, manifestContractError(decodeErr)
		}

		profiles = append(profiles, profile)
	}

	return nodes, links, profiles, nil
}

// manifestContractError marks an engine render failure as a contract violation: the engine
// produced something the c9s vocabulary cannot carry, which is a programming error rather than
// a user error. The error is returned rather than panicked so the reconcile path reports it
// through the same structured failure handling as compile failures.
func manifestContractError(err error) error {
	return fmt.Errorf("clabernetes compiler: %w", err)
}

// renderAll renders the compiled topology through the engine once and returns every primitive
// manifest.
func renderAll(
	topology *clabernetesapisv1alpha1.Topology,
	compiled *CompiledTopology,
	configManagerGetter clabernetesconfig.ManagerGetterFunc,
) (nodes, links, nodeProfiles []unstructured.Unstructured, err error) {
	input := compileEngineInput(topology)

	if configManagerGetter != nil {
		annotations, globalLabels := configManagerGetter().GetAllMetadata()

		input.Annotations = annotations
		input.Labels = globalLabels
	}

	engineCompiled, err := engineCompiledTopology(compiled)
	if err != nil {
		return nil, nil, nil, err
	}

	return RenderTopology(input, engineCompiled)
}

// engineCompiledTopology maps the clabernetes compiled topology back onto the engine's shape so
// the engine's renderers can run against it. A failure is a vocabulary round-trip contract
// violation; renderAll's callers report it through the same contract path as manifest decoding.
func engineCompiledTopology(
	compiled *CompiledTopology,
) (*CompiledDefinition, error) {
	out := &CompiledDefinition{
		Kind: compiled.Kind,
		Nodes: make(
			map[string]*clabtypes.NodeDefinition,
			len(compiled.Nodes),
		),
		AppProtocols:    maps.Clone(compiled.AppProtocols),
		Links:           slices.Clone(compiled.Links),
		Mgmt:            compiled.Mgmt,
		NodeNameSources: maps.Clone(compiled.NodeNameSources),
	}

	for nodeName, nodeDefinition := range compiled.Nodes {
		exported, err := exportNodeDefinition(nodeDefinition)
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", nodeName, err)
		}

		out.Nodes[nodeName] = exported
	}

	return out, nil
}

// exportNodeDefinition encodes one clabernetes node definition back into containerlab vocabulary
// for the engine's renderers. The vocabulary's marshaler emits the kind-specific config under
// its wrapper key; the engine's flattened definition carries the kind keys directly, so the
// wrapper is unwrapped here.
func exportNodeDefinition(
	nodeDefinition *clabernetesutilcontainerlab.NodeDefinition,
) (*clabtypes.NodeDefinition, error) {
	raw, err := yaml.Marshal(nodeDefinition)
	if err != nil {
		return nil, fmt.Errorf("exporting node definition: %w", err)
	}

	exported := &clabtypes.NodeDefinition{}

	err = yaml.Unmarshal(raw, exported)
	if err != nil {
		return nil, fmt.Errorf("importing exported node definition: %w", err)
	}

	wrapperKey := clabernetesutilcontainerlab.KindSpecificConfigWrapperKey

	if wrapper, wrapped := exported.KindSpecificConfig[wrapperKey]; wrapped {
		entries := clabernetesutilcontainerlab.KindSpecificConfigWrapperEntries(wrapper)
		if entries != nil {
			delete(exported.KindSpecificConfig, wrapperKey)
			maps.Copy(exported.KindSpecificConfig, entries)
		}
	}

	// An explicitly cleared component inventory must survive the vocabulary round trip: the
	// vocabulary's marshaler omits an empty list, so the engine's kind map gets the explicit
	// clearing back directly.
	if nodeDefinition.Components != nil {
		if exported.KindSpecificConfig == nil {
			exported.KindSpecificConfig = map[string]any{}
		}

		if _, present := exported.KindSpecificConfig[kindSpecificComponentsKey]; !present {
			exported.KindSpecificConfig[kindSpecificComponentsKey] = []any{}
		}
	}

	return exported, nil
}

// decodeManifest decodes an engine manifest into a typed clabernetes object. A failure means
// the engine produced a manifest the c9s vocabulary cannot carry, which is a contract violation
// rather than a user error; the error is returned so the caller surfaces it instead of silently
// dropping an object.
func decodeManifest(object map[string]any, typed any) error {
	err := apimachineryruntime.DefaultUnstructuredConverter.FromUnstructured(object, typed)
	if err != nil {
		return fmt.Errorf("decoding compiled manifest: %w", err)
	}

	return nil
}

const (
	c9sAPIVersion         = "c9s.run/v1alpha1"
	nodeKind              = "Node"
	linkKind              = "Link"
	nodeProfile           = "NodeProfile"
	labelPrefix           = "c9s.run"
	labelApp              = labelPrefix + "/app"
	labelName             = labelPrefix + "/name"
	labelOwner            = labelPrefix + "/topologyOwner"
	labelKind             = labelPrefix + "/topologyKind"
	labelNode             = labelPrefix + "/topologyNode"
	labelGroup            = labelPrefix + "/topologyGroup"
	labelOriginalNodeName = labelPrefix + "/originalNodeName"
	labelAppValue         = "clabernetes"
)

// manifestObjectKey* name the fixed manifest object fields every rendered primitive carries.
const (
	manifestAPIVersionKey = "apiVersion"
	manifestKindKey       = "kind"
	manifestMetadataKey   = "metadata"
	manifestSpecKey       = "spec"
)

// LinkHostNodeName is the reserved endpoint node name for a node-local host link.
const LinkHostNodeName = "host"

var linkNameInvalidChars = regexp.MustCompile(`[^a-z0-9-]`)

// nameMaxLen is the maximum length for a Kubernetes object name.
const nameMaxLen = 63

// nameDigestLen is the number of digest characters appended to a name that had to be truncated
// to fit nameMaxLen.
const nameDigestLen = 7

// SanitizeName maps a containerlab name onto the DNS-1035 label Kubernetes can name an object
// with: lower case, made up of a-z, 0-9 and '-', starting with a letter and at most 63 characters
// long. A large share of public labs name their routers R1/PE_1, and Kubernetes cannot carry
// those names as they are.
//
// Unlike a lossy enforcement this never rewrites a character a DNS label can carry, so a name
// Kubernetes already accepts maps onto itself and the mapping is idempotent. The result is empty
// only for a name holding nothing a Kubernetes name can be built from.
//
// The implementation is shared with the lab runtime in the names package, so a name the engine
// emits and a name the direct runtime resolves against always agree.
func SanitizeName(name string) string {
	return clabnames.SanitizeName(name)
}

// SafeConcatNameKubernetes concats all provided strings into a string joined by "-" - if the
// final string is greater than 63 characters, the string will be shortened, and a hash will be
// used at the end of the string to keep it unique, but safely within allowed lengths.
func SafeConcatNameKubernetes(name ...string) string {
	finalName := strings.Join(name, "-")

	if len(finalName) <= nameMaxLen {
		return finalName
	}

	digest := sha256.Sum256([]byte(finalName))

	return finalName[0:nameMaxLen-8] + "-" + hex.EncodeToString(digest[0:])[:nameDigestLen]
}

// sanitizeLinkNamePart makes an interface name safe for use inside a kubernetes object name.
// Any lossy normalization includes a hash of the raw value so distinct interfaces remain
// distinct.
func sanitizeLinkNamePart(part string) string {
	rawPart := part
	part = linkNameInvalidChars.ReplaceAllString(strings.ToLower(rawPart), "-")

	part = strings.Trim(part, "-")

	if part == "" {
		part = "x"
	}

	if part != rawPart {
		digest := sha256.Sum256([]byte(rawPart))
		part = fmt.Sprintf("%s-%x", part, digest[:4])
	}

	return part
}

// LinkResourceName returns the (deterministic) name of the Link object for the given wire.
func LinkResourceName(endpointA, endpointB Endpoint) string {
	return SafeConcatNameKubernetes(
		endpointA.NodeName,
		sanitizeLinkNamePart(endpointA.InterfaceName),
		endpointB.NodeName,
		sanitizeLinkNamePart(endpointB.InterfaceName),
	)
}

// ownedObjectMetadata returns the base metadata for objects the compiler emits: the topology's
// annotations and global labels, plus the compiler's own identifying labels.
func ownedObjectMetadata(input *Input, name string) map[string]any {
	labels := map[string]any{
		labelApp:   labelAppValue,
		labelName:  name,
		labelOwner: input.Name,
		labelKind:  TopologyKindContainerlab,
	}

	// Global topology labels ride along as user metadata; the labels the compiler sets itself
	// keep their compiler-assigned values.
	for key, value := range input.Labels {
		if compilerOwnedLabel(key, false) {
			continue
		}

		labels[key] = value
	}

	metadata := map[string]any{
		"name":      name,
		"labels":    labels,
		"namespace": input.Namespace,
	}

	if len(input.Annotations) != 0 {
		annotations := map[string]any{}
		for key, value := range input.Annotations {
			annotations[key] = value
		}
		metadata["annotations"] = annotations
	}

	return metadata
}

// DefinitionPayload renders the flattened node definition as the manifest payload the c9s Node
// vocabulary carries: the vocabulary's named fields at the top level, the `components`
// kind-specific key as its typed field, and every other kind-specific key wrapped under
// `kind-specific-config` so the API server preserves it. Clabernetes' compiler uses it to
// hydrate its own vocabulary from the engine's flattened definitions.
func DefinitionPayload(flattened *clabtypes.NodeDefinition) (map[string]any, error) {
	return definitionPayload(flattened)
}

// definitionPayload renders the flattened node definition as the manifest payload the c9s Node
// vocabulary carries: the vocabulary's named fields at the top level, the `components`
// kind-specific key as its typed field, and every other kind-specific key wrapped under
// `kind-specific-config` so the API server preserves it.
func definitionPayload(flattened *clabtypes.NodeDefinition) (map[string]any, error) {
	// The yaml.v2 marshal carries containerlab's yaml vocabulary and inlines the kind-specific
	// config keys via NodeDefinition.MarshalYAML; the yaml.v3 decode renders the same tree into
	// the JSON-native shapes the manifest needs.
	raw, err := yamlv2.Marshal(flattened)
	if err != nil {
		return nil, err
	}

	var payload map[string]any

	err = yaml.Unmarshal(raw, &payload)
	if err != nil {
		return nil, err
	}

	if payload == nil {
		payload = map[string]any{}
	}

	kindConfig := map[string]any{}

	for key := range flattened.KindSpecificConfig {
		if key == kindSpecificComponentsKey {
			// The components inventory is the one kind-specific key the c9s vocabulary carries
			// as a typed field; it stays at the top level of the payload.
			continue
		}

		// The payload already decoded the value into JSON-safe shapes.
		kindConfig[key] = payload[key]

		delete(payload, key)
	}

	if len(kindConfig) != 0 {
		payload[kindSpecificWrapperKey] = kindConfig
	}

	// containerlab carries the certificate validity as a duration; the c9s vocabulary carries
	// it as the duration string form.
	if flattened.Certificate != nil && flattened.Certificate.ValidityDuration > 0 {
		if certificate, ok := payload["certificate"].(map[string]any); ok {
			certificate["validity-duration"] = flattened.Certificate.ValidityDuration.String()
		}
	}

	// The JSON round trip normalizes every number into the float64 form Kubernetes'
	// unstructured deep-copy machinery can carry.
	normalized, err := jsonPayload(payload)
	if err != nil {
		return nil, err
	}

	normalizedMap, ok := normalized.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: definition payload is not a mapping", errInvalidData)
	}

	return normalizedMap, nil
}

// jsonPayload converts a value with JSON tags into the JSON-native shapes the emitted manifests
// carry, so the manifest is exactly what the c9s API server accepts.
func jsonPayload(value any) (any, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}

	var normalized any

	err = json.Unmarshal(raw, &normalized)
	if err != nil {
		return nil, err
	}

	return normalized, nil
}

// renderNodes renders the Node objects for the compiled topology, sorted by name. The emitted
// node names are the containerlab node names, sanitized only where Kubernetes cannot carry them
// -- the namespace is the topology boundary. Node-keyed policy on the Topology is written against
// the definition's names, so it is looked up by the node's source name.
func renderNodes(
	input *Input,
	compiled *CompiledDefinition,
) ([]unstructured.Unstructured, error) {
	nodeNames := make([]string, 0, len(compiled.Nodes))
	for nodeName := range compiled.Nodes {
		nodeNames = append(nodeNames, nodeName)
	}

	sort.Strings(nodeNames)

	nodes := make([]unstructured.Unstructured, 0, len(compiled.Nodes))

	for _, nodeName := range nodeNames {
		nodeDefinition := compiled.Nodes[nodeName]
		sourceName := compiled.SourceNodeName(nodeName)
		profileName := nodeProfileNameForNode(input, compiled, nodeName)

		definition, err := definitionPayload(nodeDefinition)
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", nodeName, err)
		}

		spec := maps.Clone(definition)

		if profileName != "" {
			spec["profileRef"] = map[string]any{"name": profileName}
		}

		if err := applyNodePayloadProjections(input, nodeName, sourceName, spec); err != nil {
			return nil, err
		}

		if protocols := compiled.AppProtocols[nodeName]; len(protocols) != 0 {
			spec["appProtocols"] = appProtocolSpecEntries(protocols)
		}

		metadata, err := nodeMetadata(input, nodeName, nodeDefinition)
		if err != nil {
			return nil, err
		}

		nodes = append(nodes, unstructured.Unstructured{Object: map[string]any{
			manifestAPIVersionKey: c9sAPIVersion,
			manifestKindKey:       nodeKind,
			manifestMetadataKey:   metadata,
			manifestSpecKey:       spec,
		}})
	}

	return nodes, nil
}

// applyNodePayloadProjections renders the per-node file projections -- ConfigMap, Secret, and
// URL backed -- onto a Node spec.
func applyNodePayloadProjections(
	input *Input,
	nodeName string,
	sourceName string,
	spec map[string]any,
) error {
	if files := input.Deployment.FilesFromConfigMap[sourceName]; len(files) != 0 {
		raw, err := jsonPayload(files)
		if err != nil {
			return fmt.Errorf("node %q: %w", nodeName, err)
		}

		spec["filesFromConfigMap"] = raw
	}

	if files := input.Deployment.FilesFromSecret[sourceName]; len(files) != 0 {
		raw, err := jsonPayload(files)
		if err != nil {
			return fmt.Errorf("node %q: %w", nodeName, err)
		}

		spec["filesFromSecret"] = raw
	}

	if files := input.Deployment.FilesFromURL[sourceName]; len(files) != 0 {
		raw, err := jsonPayload(files)
		if err != nil {
			return fmt.Errorf("node %q: %w", nodeName, err)
		}

		spec["filesFromURL"] = raw
	}

	return nil
}

// appProtocolSpecEntries renders the application-protocol entries of one Node spec.
func appProtocolSpecEntries(protocols []AppProtocol) []map[string]any {
	entries := make([]map[string]any, 0, len(protocols))
	for _, protocol := range protocols {
		entries = append(entries, map[string]any{
			"port":        protocol.Port,
			"appProtocol": protocol.AppProtocol,
		})
	}

	return entries
}

// nodeMetadata builds one Node's object metadata: the owned labels plus the containerlab group
// and node labels.
func nodeMetadata(
	input *Input,
	nodeName string,
	nodeDefinition *clabtypes.NodeDefinition,
) (map[string]any, error) {
	metadata := ownedObjectMetadata(input, nodeName)

	labels, ok := metadata["labels"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf(
			"%w: node %q metadata carries no labels", errInvalidData, nodeName,
		)
	}

	// The labels the topology carries ride along as user metadata; the ones the compiler sets
	// itself keep their compiler-assigned values, so a c9s.run label in the clab file can never
	// shadow what the compiled object actually is.
	for key, value := range nodeDefinition.Labels {
		if compilerOwnedLabel(key, nodeDefinition.Group != "") {
			continue
		}

		labels[key] = value
	}

	// Retain this label for human selection and compatibility; profile attachment is explicit.
	labels[labelNode] = nodeName

	// The containerlab group name is organizational metadata; it rides along as a label so
	// operators can select by it, exactly as they would filter by group in containerlab.
	if nodeDefinition.Group != "" {
		labels[labelGroup] = nodeDefinition.Group
	}

	return metadata, nil
}

// compilerOwnedLabel reports whether the compiler sets the label key itself, so a value coming
// from the topology cannot shadow the compiler's. The group label is only owned by a Node that
// actually carries a group; every other object never sets it.
func compilerOwnedLabel(key string, hasGroup bool) bool {
	switch key {
	case labelApp, labelName, labelOwner, labelKind, labelNode:
		return true
	case labelGroup:
		return hasGroup
	default:
		return false
	}
}

// nodeProfileNameForNode resolves the NodeProfile a Node references. A Node with distinct
// profile policy gets a dedicated profile named after it; every other Node shares the
// topology's profile.
func nodeProfileNameForNode(
	input *Input,
	compiled *CompiledDefinition,
	nodeName string,
) string {
	if hasDistinctProfilePolicy(input, compiled.SourceNodeName(nodeName)) {
		return SafeConcatNameKubernetes(input.Name, nodeName)
	}

	return input.Name
}

// hasDistinctProfilePolicy reports whether a Node needs a dedicated NodeProfile. Today the
// only per-node profile policy exposed by Topology is deployment.resources; payload maps are
// rendered directly onto Nodes and therefore do not create one-off profiles.
func hasDistinctProfilePolicy(input *Input, nodeName string) bool {
	nodeResources, hasNodeResources := input.Deployment.Resources[nodeName]
	if !hasNodeResources {
		return false
	}

	defaultResources, hasDefaultResources := input.Deployment.Resources[DefaultResourceName]
	if !hasDefaultResources {
		// The map entry is itself meaningful, including an explicitly empty resource policy.
		return true
	}

	return !apiequality.Semantic.DeepEqual(nodeResources, defaultResources)
}

// renderLinks renders the Link objects for the compiled topology, sorted by name.
func renderLinks(
	input *Input,
	compiled *CompiledDefinition,
) ([]unstructured.Unstructured, error) {
	links := make([]unstructured.Unstructured, 0, len(compiled.Links))

	ordered := slices.Clone(compiled.Links)
	sort.Slice(ordered, func(i, j int) bool {
		return LinkResourceName(ordered[i].EndpointA, ordered[i].EndpointB) <
			LinkResourceName(ordered[j].EndpointA, ordered[j].EndpointB)
	})

	for _, compiledLink := range ordered {
		mtu, err := jsonPayload(compiledLink.MTU)
		if err != nil {
			return nil, err
		}

		links = append(links, unstructured.Unstructured{Object: map[string]any{
			"apiVersion": c9sAPIVersion,
			"kind":       linkKind,
			"metadata": ownedObjectMetadata(
				input,
				LinkResourceName(compiledLink.EndpointA, compiledLink.EndpointB),
			),
			"spec": map[string]any{
				"endpointA": map[string]any{
					"nodeName":      compiledLink.EndpointA.NodeName,
					"interfaceName": compiledLink.EndpointA.InterfaceName,
				},
				"endpointB": map[string]any{
					"nodeName":      compiledLink.EndpointB.NodeName,
					"interfaceName": compiledLink.EndpointB.InterfaceName,
				},
				"mtu": mtu,
			},
		}})
	}

	return links, nil
}

// renderNodeProfiles renders one shared topology NodeProfile plus a complete dedicated
// profile for each compiled Node with distinct profile policy.
func renderNodeProfiles(
	input *Input,
	compiled *CompiledDefinition,
) ([]unstructured.Unstructured, error) {
	perNodeNames := make([]string, 0)
	sharedProfileNeeded := false

	for nodeName := range compiled.Nodes {
		if !hasDistinctProfilePolicy(input, compiled.SourceNodeName(nodeName)) {
			sharedProfileNeeded = true

			continue
		}

		perNodeNames = append(perNodeNames, nodeName)
	}

	sort.Strings(perNodeNames)

	profiles := make([]unstructured.Unstructured, 0, len(perNodeNames)+1)

	if sharedProfileNeeded {
		profile, err := renderTopologyNodeProfile(input, compiled, input.Name)
		if err != nil {
			return nil, err
		}

		profiles = append(profiles, profile)
	}

	for _, nodeName := range perNodeNames {
		profile, err := renderTopologyNodeProfile(
			input,
			compiled,
			SafeConcatNameKubernetes(input.Name, nodeName),
		)
		if err != nil {
			return nil, err
		}

		if nodeResources, ok := input.Deployment.
			Resources[compiled.SourceNodeName(nodeName)]; ok {
			raw, err := jsonPayload(nodeResources)
			if err != nil {
				return nil, fmt.Errorf("node profile %q: %w", profile.GetName(), err)
			}

			spec, ok := profile.Object["spec"].(map[string]any)
			if !ok {
				return nil, fmt.Errorf(
					"%w: profile %q carries no spec", errInvalidData, profile.GetName(),
				)
			}

			spec["resources"] = raw
		}

		profiles = append(profiles, profile)
	}

	return profiles, nil
}

// renderTopologyNodeProfile compiles topology-wide policy into a NodeProfile. Only values
// represented by the Topology API are copied; omitted values continue to inherit Config
// defaults.
func renderTopologyNodeProfile(
	input *Input,
	compiled *CompiledDefinition,
	profileName string,
) (unstructured.Unstructured, error) {
	spec := map[string]any{}

	spec["expose"] = exposeSpec(input.Expose)

	if input.ImagePull.Policy != "" || len(input.ImagePull.PullSecrets) != 0 {
		imagePull, err := imagePullSpec(input.ImagePull)
		if err != nil {
			return unstructured.Unstructured{}, err
		}

		spec["imagePull"] = imagePull
	}

	if err := applyProfileDeploymentSpec(input, spec); err != nil {
		return unstructured.Unstructured{}, err
	}

	// Probe policy is matched against Node object names, so the node names the author wrote have
	// to follow the rename the compiler made.
	spec["statusProbes"] = compiledStatusProbes(input, compiled)

	applyProfileManagementSpec(input, compiled, spec)

	return unstructured.Unstructured{Object: map[string]any{
		manifestAPIVersionKey: c9sAPIVersion,
		manifestKindKey:       nodeProfile,
		manifestMetadataKey:   ownedObjectMetadata(input, profileName),
		manifestSpecKey:       spec,
	}}, nil
}

// exposeSpec renders the expose policy section of a NodeProfile spec.
func exposeSpec(expose Expose) map[string]any {
	out := map[string]any{
		"disableAutoExpose":      expose.DisableAutoExpose,
		"useNodeMgmtIpv4Address": expose.UseNodeMgmtIpv4Address,
		"useNodeMgmtIpv6Address": expose.UseNodeMgmtIpv6Address,
	}
	if expose.ExposeType != "" {
		out["exposeType"] = expose.ExposeType
	}

	return out
}

// imagePullSpec renders the image-pull section of a NodeProfile spec.
func imagePullSpec(imagePull ImagePull) (map[string]any, error) {
	out := map[string]any{}

	if imagePull.Policy != "" {
		out["policy"] = imagePull.Policy
	}

	if len(imagePull.PullSecrets) != 0 {
		raw, err := jsonPayload(imagePull.PullSecrets)
		if err != nil {
			return nil, err
		}

		out["pullSecrets"] = raw
	}

	return out, nil
}

// applyProfileDeploymentSpec renders the per-topology deployment policy sections -- the shared
// default resources, scheduling, and persistence -- onto a NodeProfile spec.
func applyProfileDeploymentSpec(
	input *Input,
	spec map[string]any,
) error {
	if defaultResources, ok := input.Deployment.Resources[DefaultResourceName]; ok {
		raw, err := jsonPayload(defaultResources)
		if err != nil {
			return err
		}

		spec["resources"] = raw
	}

	if scheduling := input.Deployment.Scheduling; scheduling != nil &&
		(scheduling.NodeSelector != nil || scheduling.Tolerations != nil ||
			scheduling.Affinity != nil) {
		raw, err := jsonPayload(scheduling)
		if err != nil {
			return err
		}

		spec["scheduling"] = raw
	}

	if input.Deployment.NetworkIsolation != "" {
		deploymentSpec, ok := spec["deployment"].(map[string]any)
		if !ok {
			deploymentSpec = map[string]any{}
			spec["deployment"] = deploymentSpec
		}

		deploymentSpec["networkIsolation"] = input.Deployment.NetworkIsolation
	}

	if persistence := input.Deployment.Persistence; persistence != nil && persistence.Enabled {
		raw, err := jsonPayload(persistence)
		if err != nil {
			return err
		}

		deploymentSpec, ok := spec["deployment"].(map[string]any)
		if !ok {
			deploymentSpec = map[string]any{}
			spec["deployment"] = deploymentSpec
		}

		deploymentSpec["persistence"] = raw
	}

	return nil
}

// applyProfileManagementSpec renders the management policy section of a NodeProfile spec: the
// topology's management policy, or an explicitly disabled management overlay when the topology
// disables management outright.
func applyProfileManagementSpec(
	input *Input,
	compiled *CompiledDefinition,
	spec map[string]any,
) {
	if input.DisableManagement {
		spec["mgmt"] = map[string]any{"disabled": true}

		return
	}

	if compiled.Mgmt == nil {
		return
	}

	spec["mgmt"] = map[string]any{
		"ipv4-subnet": compiled.Mgmt.IPv4Subnet,
		"ipv4-gw":     compiled.Mgmt.IPv4Gw,
		"ipv4-range":  compiled.Mgmt.IPv4Range,
		"ipv6-subnet": compiled.Mgmt.IPv6Subnet,
		"ipv6-gw":     compiled.Mgmt.IPv6Gw,
		"ipv6-range":  compiled.Mgmt.IPv6Range,
	}
}

// compiledStatusProbes copies the topology's probe policy with every node name it holds mapped
// onto the compiled node name the Node objects carry.
func compiledStatusProbes(
	input *Input,
	compiled *CompiledDefinition,
) map[string]any {
	statusProbes := map[string]any{"enabled": input.StatusProbes.Enabled}

	if input.StatusProbes.ProbeConfiguration != (ProbeConfiguration{}) {
		raw, err := jsonPayload(input.StatusProbes.ProbeConfiguration)
		if err == nil {
			statusProbes["probeConfiguration"] = raw
		}
	}

	if len(input.StatusProbes.NodeProbeConfigurations) != 0 {
		configurations := map[string]any{}

		for nodeName, probeConfiguration := range input.StatusProbes.NodeProbeConfigurations {
			if compiledName, renamed := renameSourceToCompiled(compiled, nodeName); renamed {
				nodeName = compiledName
			}

			raw, err := jsonPayload(probeConfiguration)
			if err != nil {
				continue
			}

			configurations[nodeName] = raw
		}

		statusProbes["nodeProbeConfigurations"] = configurations
	}

	if len(input.StatusProbes.ExcludedNodes) != 0 {
		excluded := make([]any, 0, len(input.StatusProbes.ExcludedNodes))

		for _, nodeName := range input.StatusProbes.ExcludedNodes {
			if compiledName, renamed := renameSourceToCompiled(compiled, nodeName); renamed {
				nodeName = compiledName
			}

			excluded = append(excluded, nodeName)
		}

		statusProbes["excludedNodes"] = excluded
	}

	return statusProbes
}

func renameSourceToCompiled(compiled *CompiledDefinition, sourceName string) (string, bool) {
	for compiledName, candidate := range compiled.NodeNameSources {
		if candidate == sourceName {
			return compiledName, true
		}
	}

	return sourceName, false
}
