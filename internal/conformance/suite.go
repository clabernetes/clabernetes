// Package conformance validates externally supplied direct-runtime image scenarios without
// maintaining a c9s kind or image catalog.
package conformance

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetescontrollerstopology "github.com/clabernetes/clabernetes/controllers/topology"
	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	clabnodes "github.com/srl-labs/containerlab/nodes"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	sigsyaml "sigs.k8s.io/yaml"
)

const (
	// SchemaVersion identifies the external conformance bundle schema.
	SchemaVersion = "v1alpha1"

	defaultScenarioTimeout = 10 * time.Minute
	defaultPollInterval    = 10 * time.Second
	yamlBufferSize         = 4096
)

var (
	errInvalidSuite          = errors.New("invalid direct-runtime conformance suite")
	errManifestHasNoImages   = errors.New("manifest contains no c9s Node or Topology images")
	errInvalidImage          = errors.New("invalid Node image observation")
	errDuplicateNodeIdentity = errors.New("duplicate Node identity")
)

// Suite is an environment-owned set of image availability observations and executable scenarios.
// It is deliberately not committed as the compatibility kind inventory.
type Suite struct {
	SchemaVersion string     `json:"schemaVersion"`
	Scenarios     []Scenario `json:"scenarios"`
}

// Scenario exercises every Node image declared by one ordinary Kubernetes manifest stream.
type Scenario struct {
	ID                   string            `json:"id"`
	Availability         string            `json:"availability"`
	Manifest             string            `json:"manifest"`
	Timeout              string            `json:"timeout,omitempty"`
	PollInterval         string            `json:"pollInterval,omitempty"`
	Management           []ExecObservation `json:"management"`
	Dataplane            []ExecObservation `json:"dataplane"`
	ResolvedTimeout      time.Duration     `json:"-"`
	ResolvedPollInterval time.Duration     `json:"-"`
}

// ExecObservation is one package-opaque command executed in a scenario-owned workload. Vendor
// commands, credentials, and image-specific expectations stay in the external bundle.
type ExecObservation struct {
	Name      string   `json:"name"`
	Nodes     []string `json:"nodes"`
	Target    string   `json:"target"`
	Container string   `json:"container,omitempty"`
	Command   []string `json:"command"`
}

// ImageObservation is a kind/image pair derived from ordinary Node or Topology input.
type ImageObservation struct {
	Node  string `json:"node"`
	Kind  string `json:"kind"`
	Image string `json:"image"`
}

// ScenarioInventory is the deterministic live-registry-validated inventory for one scenario.
type ScenarioInventory struct {
	ID           string             `json:"id"`
	Availability string             `json:"availability"`
	Images       []ImageObservation `json:"images"`
}

// LoadSuite strictly decodes and validates an external suite against the live imported registry.
func LoadSuite(path string, registry *clabnodes.NodeRegistry) (*Suite, []ScenarioInventory, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // The caller explicitly selects the suite.
	if err != nil {
		return nil, nil, fmt.Errorf("reading direct conformance suite: %w", err)
	}

	jsonRaw, err := sigsyaml.YAMLToJSONStrict(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("decoding direct conformance suite YAML: %w", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(jsonRaw))
	decoder.DisallowUnknownFields()

	suite := &Suite{}

	err = decoder.Decode(suite)
	if err != nil {
		return nil, nil, fmt.Errorf("decoding direct conformance suite: %w", err)
	}

	var trailing any

	err = decoder.Decode(&trailing)
	if !errors.Is(err, io.EOF) {
		return nil, nil, fmt.Errorf("%w: trailing suite data", errInvalidSuite)
	}

	inventories, err := suite.Validate(registry)
	if err != nil {
		return nil, nil, err
	}

	return suite, inventories, nil
}

// Validate proves that every scenario is executable and that all kind names come from the live
// imported registry. It returns inventory data rather than storing expected kind rows.
func (s *Suite) Validate(registry *clabnodes.NodeRegistry) ([]ScenarioInventory, error) {
	err := validateSuiteHeader(s)
	if err != nil {
		return nil, err
	}

	if registry == nil {
		registry = clabernetesinternaldeviceplan.NewContainerlabRegistry()
	}

	seenIDs := map[string]bool{}
	inventories := make([]ScenarioInventory, 0, len(s.Scenarios))

	for index := range s.Scenarios {
		scenario := &s.Scenarios[index]
		field := fmt.Sprintf("scenarios[%d]", index)

		inventory, validateErr := validateScenario(registry, scenario, field, seenIDs)
		if validateErr != nil {
			return nil, validateErr
		}

		inventories = append(inventories, inventory)
	}

	return inventories, nil
}

func validateSuiteHeader(suite *Suite) error {
	if suite == nil || suite.SchemaVersion != SchemaVersion {
		return fmt.Errorf(
			"%w: schemaVersion is %q, want %q",
			errInvalidSuite,
			valueOrEmpty(suite),
			SchemaVersion,
		)
	}

	if len(suite.Scenarios) == 0 {
		return fmt.Errorf("%w: scenarios are empty", errInvalidSuite)
	}

	return nil
}

func validateScenario(
	registry *clabnodes.NodeRegistry,
	scenario *Scenario,
	field string,
	seenIDs map[string]bool,
) (ScenarioInventory, error) {
	if strings.TrimSpace(scenario.ID) == "" || seenIDs[scenario.ID] {
		return ScenarioInventory{}, fmt.Errorf(
			"%w: %s has an empty or duplicate id",
			errInvalidSuite,
			field,
		)
	}

	seenIDs[scenario.ID] = true

	if scenario.Availability != "obtainable" && scenario.Availability != "restricted" {
		return ScenarioInventory{}, fmt.Errorf(
			"%w: %s availability %q is not obtainable or restricted",
			errInvalidSuite,
			field,
			scenario.Availability,
		)
	}

	if strings.TrimSpace(scenario.Manifest) == "" {
		return ScenarioInventory{}, fmt.Errorf("%w: %s manifest is empty", errInvalidSuite, field)
	}

	resolvedTimeout, err := parsePositiveDuration(
		field+".timeout",
		scenario.Timeout,
		defaultScenarioTimeout,
	)
	if err != nil {
		return ScenarioInventory{}, err
	}

	resolvedPollInterval, err := parsePositiveDuration(
		field+".pollInterval",
		scenario.PollInterval,
		defaultPollInterval,
	)
	if err != nil {
		return ScenarioInventory{}, err
	}

	if resolvedPollInterval > resolvedTimeout {
		return ScenarioInventory{}, fmt.Errorf(
			"%w: %s pollInterval exceeds timeout",
			errInvalidSuite,
			field,
		)
	}

	err = validateObservations(field+".management", scenario.Management)
	if err != nil {
		return ScenarioInventory{}, err
	}

	err = validateObservations(field+".dataplane", scenario.Dataplane)
	if err != nil {
		return ScenarioInventory{}, err
	}

	images, err := manifestImages(scenario.Manifest)
	if err != nil {
		return ScenarioInventory{}, fmt.Errorf("%w: %s: %w", errInvalidSuite, field, err)
	}

	err = validateObservationCoverage(field+".management", scenario.Management, images)
	if err != nil {
		return ScenarioInventory{}, err
	}

	err = validateObservationCoverage(field+".dataplane", scenario.Dataplane, images)
	if err != nil {
		return ScenarioInventory{}, err
	}

	err = validateRegistryImages(registry, field, images)
	if err != nil {
		return ScenarioInventory{}, err
	}

	scenario.ResolvedTimeout = resolvedTimeout
	scenario.ResolvedPollInterval = resolvedPollInterval

	return ScenarioInventory{
		ID: scenario.ID, Availability: scenario.Availability, Images: images,
	}, nil
}

func validateRegistryImages(
	registry *clabnodes.NodeRegistry,
	field string,
	images []ImageObservation,
) error {
	for _, image := range images {
		if registry.Kind(image.Kind) == nil {
			return fmt.Errorf(
				"%w: %s Node %q kind %q is absent from the live imported registry",
				errInvalidSuite,
				field,
				image.Node,
				image.Kind,
			)
		}
	}

	return nil
}

func valueOrEmpty(suite *Suite) string {
	if suite == nil {
		return ""
	}

	return suite.SchemaVersion
}

func parsePositiveDuration(field, value string, defaultValue time.Duration) (time.Duration, error) {
	if strings.TrimSpace(value) == "" {
		return defaultValue, nil
	}

	result, err := time.ParseDuration(value)
	if err != nil || result <= 0 {
		return 0, fmt.Errorf("%w: %s must be a positive duration", errInvalidSuite, field)
	}

	return result, nil
}

func validateObservations(field string, observations []ExecObservation) error {
	if len(observations) == 0 {
		return fmt.Errorf("%w: %s is empty", errInvalidSuite, field)
	}

	seen := map[string]bool{}
	for index, observation := range observations {
		if strings.TrimSpace(observation.Name) == "" || seen[observation.Name] ||
			strings.TrimSpace(observation.Target) == "" || len(observation.Nodes) == 0 ||
			len(observation.Command) == 0 {
			return fmt.Errorf(
				"%w: %s[%d] has an empty/duplicate name, node coverage, target, or command",
				errInvalidSuite,
				field,
				index,
			)
		}

		seen[observation.Name] = true

		seenNodes := map[string]bool{}
		for _, node := range observation.Nodes {
			if strings.TrimSpace(node) == "" || seenNodes[node] {
				return fmt.Errorf(
					"%w: %s[%d] has empty or duplicate Node coverage",
					errInvalidSuite,
					field,
					index,
				)
			}

			seenNodes[node] = true
		}

		if slices.Contains(observation.Command, "") {
			return fmt.Errorf(
				"%w: %s[%d] command contains an empty argument",
				errInvalidSuite,
				field,
				index,
			)
		}
	}

	return nil
}

func validateObservationCoverage(
	field string,
	observations []ExecObservation,
	images []ImageObservation,
) error {
	expected := make(map[string]bool, len(images))

	for _, image := range images {
		expected[image.Node] = true
	}

	covered := make(map[string]bool, len(expected))

	for index, observation := range observations {
		for _, node := range observation.Nodes {
			if !expected[node] {
				return fmt.Errorf(
					"%w: %s[%d] covers unknown Node %q",
					errInvalidSuite,
					field,
					index,
					node,
				)
			}

			covered[node] = true
		}
	}

	for _, image := range images {
		if !covered[image.Node] {
			return fmt.Errorf(
				"%w: %s does not cover Node %q",
				errInvalidSuite,
				field,
				image.Node,
			)
		}
	}

	return nil
}

func manifestImages(manifest string) ([]ImageObservation, error) {
	decoder := k8syaml.NewYAMLOrJSONDecoder(strings.NewReader(manifest), yamlBufferSize)
	images := []ImageObservation{}

	for {
		documentImages, done, err := decodeManifestDocument(decoder)
		if err != nil {
			return nil, err
		}

		if done {
			break
		}

		images = append(images, documentImages...)
	}

	return validateAndSortImages(images)
}

func decodeManifestDocument(
	decoder *k8syaml.YAMLOrJSONDecoder,
) ([]ImageObservation, bool, error) {
	document := map[string]any{}
	err := decoder.Decode(&document)

	if errors.Is(err, io.EOF) {
		return nil, true, nil
	}

	if err != nil {
		return nil, false, fmt.Errorf("decoding manifest document: %w", err)
	}

	if len(document) == 0 {
		return nil, false, nil
	}

	raw, err := json.Marshal(document)
	if err != nil {
		return nil, false, fmt.Errorf("encoding manifest document: %w", err)
	}

	header := metav1.TypeMeta{}

	err = json.Unmarshal(raw, &header)
	if err != nil {
		return nil, false, fmt.Errorf("decoding manifest identity: %w", err)
	}

	if header.APIVersion != "c9s.run/v1alpha1" {
		return nil, false, nil
	}

	documentImages, err := c9sDocumentImages(header.Kind, raw)
	if err != nil {
		return nil, false, err
	}

	return documentImages, false, nil
}

func c9sDocumentImages(kind string, raw []byte) ([]ImageObservation, error) {
	switch kind {
	case "Node":
		return nodeDocumentImages(raw)
	case "Topology":
		return topologyDocumentImages(raw)
	case "Link":
		return nil, decodeStrictC9sDocument(kind, raw, &clabernetesapisv1alpha1.Link{})
	case "NodeProfile":
		return nil, decodeStrictC9sDocument(
			kind,
			raw,
			&clabernetesapisv1alpha1.NodeProfile{},
		)
	case "Config":
		return nil, decodeStrictC9sDocument(kind, raw, &clabernetesapisv1alpha1.Config{})
	default:
		return nil, nil
	}
}

func decodeStrictC9sDocument(kind string, raw []byte, target any) error {
	err := decodeStrictJSON(raw, target)
	if err != nil {
		return fmt.Errorf("decoding %s: %w", kind, err)
	}

	return nil
}

func nodeDocumentImages(raw []byte) ([]ImageObservation, error) {
	node := &clabernetesapisv1alpha1.Node{}

	err := decodeStrictJSON(raw, node)
	if err != nil {
		return nil, fmt.Errorf("decoding Node: %w", err)
	}

	return []ImageObservation{imageFromNode(node.GetName(), &node.Spec.NodeDefinition)}, nil
}

func topologyDocumentImages(raw []byte) ([]ImageObservation, error) {
	topology := &clabernetesapisv1alpha1.Topology{}

	err := decodeStrictJSON(raw, topology)
	if err != nil {
		return nil, fmt.Errorf("decoding Topology: %w", err)
	}

	compiled, err := clabernetescontrollerstopology.CompileTopology(
		&claberneteslogging.FakeInstance{},
		topology,
	)
	if err != nil {
		return nil, fmt.Errorf("compiling Topology %q: %w", topology.GetName(), err)
	}

	names := make([]string, 0, len(compiled.Nodes))
	for name := range compiled.Nodes {
		names = append(names, name)
	}

	slices.Sort(names)

	images := make([]ImageObservation, 0, len(names))
	for _, name := range names {
		images = append(images, imageFromNode(name, compiled.Nodes[name]))
	}

	return images, nil
}

func decodeStrictJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()

	err := decoder.Decode(target)
	if err != nil {
		return err
	}

	var trailing any

	err = decoder.Decode(&trailing)
	if !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: trailing manifest document data", errInvalidSuite)
	}

	return nil
}

func validateAndSortImages(images []ImageObservation) ([]ImageObservation, error) {
	if len(images) == 0 {
		return nil, errManifestHasNoImages
	}

	for _, image := range images {
		if image.Node == "" || image.Kind == "" || image.Image == "" {
			return nil, fmt.Errorf(
				"%w: Node %q must declare non-empty kind and image",
				errInvalidImage,
				image.Node,
			)
		}
	}

	slices.SortFunc(images, compareImageObservations)

	for index := 1; index < len(images); index++ {
		if images[index].Node == images[index-1].Node {
			return nil, fmt.Errorf(
				"%w %q in one scenario manifest",
				errDuplicateNodeIdentity,
				images[index].Node,
			)
		}
	}

	return images, nil
}

func compareImageObservations(left, right ImageObservation) int {
	if result := strings.Compare(left.Node, right.Node); result != 0 {
		return result
	}

	if result := strings.Compare(left.Kind, right.Kind); result != 0 {
		return result
	}

	return strings.Compare(left.Image, right.Image)
}

func imageFromNode(
	name string,
	definition *clabernetesapisv1alpha1.NodeDefinition,
) ImageObservation {
	if definition == nil {
		return ImageObservation{Node: name}
	}

	return ImageObservation{Node: name, Kind: definition.Kind, Image: definition.Image}
}
