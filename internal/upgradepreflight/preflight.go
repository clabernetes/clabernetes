// Package upgradepreflight reports fields that cannot survive the direct-runtime API cut.
package upgradepreflight

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/yaml"
)

const (
	dispositionRemoved = "removed; explicit migration required"
	dispositionNoMap   = "removed; no automatic replacement"
)

var (
	// ErrIncompatible is returned when at least one stored field requires explicit migration.
	ErrIncompatible = errors.New("stored resources contain fields removed by the direct runtime")

	resourceTargets = []resourceTarget{
		{
			Kind: "Config",
			GVR: schema.GroupVersionResource{
				Group: "c9s.run", Version: "v1alpha1", Resource: "configs",
			},
		},
		{
			Kind: "LauncherProfile",
			GVR: schema.GroupVersionResource{
				Group: "c9s.run", Version: "v1alpha1", Resource: "launcherprofiles",
			},
		},
		{
			Kind: "Topology",
			GVR: schema.GroupVersionResource{
				Group: "c9s.run", Version: "v1alpha1", Resource: "topologies",
			},
		},
	}
)

type resourceTarget struct {
	Kind string
	GVR  schema.GroupVersionResource
}

type fieldRule struct {
	Lookup      []string
	Display     []string
	Source      []string
	Disposition string
	Guidance    string
}

// Diagnostic identifies one stored field that the direct-runtime API removes. It deliberately
// contains neither the stored value nor any referenced Secret data.
type Diagnostic struct {
	Kind        string `json:"kind"`
	Namespace   string `json:"namespace"`
	Name        string `json:"name"`
	Path        string `json:"path"`
	SourcePath  string `json:"sourcePath,omitempty"`
	Disposition string `json:"disposition"`
	Guidance    string `json:"guidance"`
}

// IncompatibleError reports how many diagnostics require explicit migration.
type IncompatibleError struct {
	Count int
}

// Error implements error.
func (e *IncompatibleError) Error() string {
	return fmt.Sprintf("%s: %d incompatible field(s)", ErrIncompatible, e.Count)
}

// Unwrap makes errors.Is(err, ErrIncompatible) work.
func (e *IncompatibleError) Unwrap() error {
	return ErrIncompatible
}

// Scan lists the old-schema resources without mutating them and returns every removed field.
func Scan(ctx context.Context, client dynamic.Interface) ([]Diagnostic, error) {
	diagnostics := make([]Diagnostic, 0)

	for _, target := range resourceTargets {
		list, err := client.Resource(target.GVR).List(ctx, metav1.ListOptions{})
		if apierrors.IsNotFound(err) {
			// A missing CRD cannot have stored instances to migrate.
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("listing %s resources for upgrade preflight: %w", target.Kind, err)
		}

		for i := range list.Items {
			objectDiagnostics, inspectErr := Inspect(target.Kind, &list.Items[i])
			if inspectErr != nil {
				return nil, inspectErr
			}
			diagnostics = append(diagnostics, objectDiagnostics...)
		}
	}

	sort.Slice(diagnostics, func(i, j int) bool {
		left, right := diagnostics[i], diagnostics[j]
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		if left.Namespace != right.Namespace {
			return left.Namespace < right.Namespace
		}
		if left.Name != right.Name {
			return left.Name < right.Name
		}

		if left.Path != right.Path {
			return left.Path < right.Path
		}

		return left.SourcePath < right.SourcePath
	})

	return diagnostics, nil
}

// Inspect reports removed fields on one unstructured old-schema object. Presence, rather than a
// typed zero-value check, is intentional: explicit empty, false, zero, null, and empty collections
// all require a migration decision.
func Inspect(kind string, object *unstructured.Unstructured) ([]Diagnostic, error) {
	diagnostics := make([]Diagnostic, 0)
	rules := rulesForKind(kind)
	if rules == nil {
		return nil, fmt.Errorf("unsupported upgrade preflight resource kind %q", kind)
	}

	for _, rule := range rules {
		present, err := pathPresent(object.Object, rule.Lookup)
		if err != nil {
			return nil, inspectionError(kind, object, rule.Display)
		}
		if present {
			diagnostics = append(diagnostics, newDiagnostic(kind, object, rule))
		}
	}

	if kind != "Topology" {
		return diagnostics, nil
	}

	definitionValue, definitionPresent, err := pathValue(
		object.Object,
		[]string{"spec", "definition", "containerlab"},
	)
	if err != nil {
		return nil, inspectionError(
			kind,
			object,
			[]string{"spec", "definition", "containerlab"},
		)
	}
	if !definitionPresent {
		return diagnostics, nil
	}
	definition, ok := definitionValue.(string)
	if !ok {
		return nil, inspectionError(
			kind,
			object,
			[]string{"spec", "definition", "containerlab"},
		)
	}

	decoded := map[string]any{}
	if err = yaml.Unmarshal([]byte(definition), &decoded); err != nil {
		// Do not wrap the parser error: malformed YAML diagnostics can contain source values.
		return nil, fmt.Errorf(
			"inspecting %s %s/%s path %s: embedded topology is not valid YAML",
			kind,
			object.GetNamespace(),
			object.GetName(),
			jsonPath([]string{"spec", "definition", "containerlab"}),
		)
	}

	for _, rule := range topologyManagementRules() {
		present, presentErr := pathPresent(decoded, rule.Lookup)
		if presentErr != nil {
			return nil, inspectionError(kind, object, rule.Display)
		}
		if present {
			diagnostics = append(diagnostics, newDiagnostic(kind, object, rule))
		}
	}

	return diagnostics, nil
}

// Run scans a cluster and writes stable JSON-lines diagnostics. Any diagnostic makes the command
// fail; a clean run emits one short success message.
func Run(ctx context.Context, client dynamic.Interface, output io.Writer) error {
	diagnostics, err := Scan(ctx, client)
	if err != nil {
		return err
	}
	if len(diagnostics) == 0 {
		_, err = fmt.Fprintln(output, "upgrade preflight passed: no removed fields found")

		return err
	}

	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	for _, diagnostic := range diagnostics {
		if err = encoder.Encode(diagnostic); err != nil {
			return fmt.Errorf("writing upgrade preflight diagnostic: %w", err)
		}
	}

	return &IncompatibleError{Count: len(diagnostics)}
}

func rulesForKind(kind string) []fieldRule {
	switch kind {
	case "Config":
		rules := configImagePullRules()
		rules = append(rules, deploymentRules([]string{"spec", "deployment"})...)
		rules = append(rules, fieldRule{
			Lookup:      []string{"spec", "deployment", "resourcesByContainerlabKind"},
			Display:     []string{"spec", "deployment", "resourcesByContainerlabKind"},
			Disposition: dispositionRemoved,
			Guidance: "Use deployment.resourcesDefault or explicit LauncherProfile resources; " +
				"the imported plan owns kind requirements.",
		})

		return rules
	case "LauncherProfile":
		rules := profileImagePullRules([]string{"spec", "imagePull"})
		rules = append(rules, deploymentRules([]string{"spec", "deployment"})...)
		rules = append(rules, managementRules([]string{"spec", "mgmt"})...)

		return rules
	case "Topology":
		rules := profileImagePullRules([]string{"spec", "imagePull"})
		rules = append(rules, deploymentRules([]string{"spec", "deployment"})...)

		return rules
	default:
		return nil
	}
}

func configImagePullRules() []fieldRule {
	prefix := []string{"spec", "imagePull"}
	rules := sharedImagePullRules(prefix)
	for _, field := range []string{"criSockOverride", "criKindOverride", "criHostsDir"} {
		rules = append(rules, fieldRule{
			Lookup:      appendPath(prefix, field),
			Display:     appendPath(prefix, field),
			Disposition: dispositionNoMap,
			Guidance: "Configure registry and CRI behavior on eligible cluster nodes; direct device " +
				"Pods receive no runtime socket or hosts-directory mount.",
		})
	}

	return rules
}

func profileImagePullRules(prefix []string) []fieldRule {
	rules := sharedImagePullRules(prefix)
	rules = append(rules, fieldRule{
		Lookup:      appendPath(prefix, "insecureRegistries"),
		Display:     appendPath(prefix, "insecureRegistries"),
		Disposition: dispositionRemoved,
		Guidance: "Configure registry transport on eligible cluster nodes and, only when needed, " +
			"configure controller metadata trust in Config.",
	})

	return rules
}

func sharedImagePullRules(prefix []string) []fieldRule {
	return []fieldRule{
		{
			Lookup:      appendPath(prefix, "pullThroughOverride"),
			Display:     appendPath(prefix, "pullThroughOverride"),
			Disposition: dispositionNoMap,
			Guidance: "Configure mirror or pre-pull behavior on the cluster node runtime; this field " +
				"is not converted to imagePull.policy.",
		},
		{
			Lookup:      appendPath(prefix, "dockerDaemonConfig"),
			Display:     appendPath(prefix, "dockerDaemonConfig"),
			Disposition: dispositionRemoved,
			Guidance: "Use imagePull.pullSecrets for registry credentials and configure daemon " +
				"transport or mirrors on cluster nodes.",
		},
		{
			Lookup:      appendPath(prefix, "dockerConfig"),
			Display:     appendPath(prefix, "dockerConfig"),
			Disposition: dispositionRemoved,
			Guidance: "Use imagePull.pullSecrets for registry credentials and configure daemon " +
				"transport or mirrors on cluster nodes.",
		},
	}
}

func deploymentRules(prefix []string) []fieldRule {
	rules := make([]fieldRule, 0, 8)
	for _, field := range []string{
		"privilegedLauncher",
		"launcherImage",
		"launcherImagePullPolicy",
		"launcherLogLevel",
		"extraEnv",
	} {
		guidance := "Manage c9s controller and helper policy through the release; express device " +
			"requirements only through supported Node input and the imported plan."
		if field == "privilegedLauncher" {
			guidance = "Device privilege comes exclusively from the imported plan; launcher privilege " +
				"is not copied to an application container."
		}
		rules = append(rules, fieldRule{
			Lookup:      appendPath(prefix, field),
			Display:     appendPath(prefix, field),
			Disposition: dispositionNoMap,
			Guidance:    guidance,
		})
	}
	for _, field := range []string{
		"containerlabDebug",
		"containerlabTimeout",
		"containerlabVersion",
	} {
		rules = append(rules, fieldRule{
			Lookup:      appendPath(prefix, field),
			Display:     appendPath(prefix, field),
			Disposition: dispositionNoMap,
			Guidance: "Upgrade the pinned c9s containerlab module through a c9s release and use " +
				"controller diagnostics; there is no per-workload launcher setting.",
		})
	}

	return rules
}

func managementRules(prefix []string) []fieldRule {
	return []fieldRule{
		{
			Lookup:      appendPath(prefix, "network"),
			Display:     appendPath(prefix, "network"),
			Disposition: dispositionNoMap,
			Guidance:    "Use portable direct management allocation; Pods have no Docker network name.",
		},
		{
			Lookup:      appendPath(prefix, "mtu"),
			Display:     appendPath(prefix, "mtu"),
			Disposition: dispositionNoMap,
			Guidance:    "Use planned management semantics and Link MTU where applicable.",
		},
		{
			Lookup:      appendPath(prefix, "external-access"),
			Display:     appendPath(prefix, "external-access"),
			Disposition: dispositionNoMap,
			Guidance:    "Use Kubernetes Services and explicit exposure policy.",
		},
	}
}

func topologyManagementRules() []fieldRule {
	prefix := []string{"mgmt"}
	display := []string{"spec", "definition", "containerlab"}
	fields := []struct {
		name     string
		guidance string
	}{
		{name: "network", guidance: "Use portable direct management allocation; Pods have no Docker network name."},
		{name: "bridge", guidance: "A Docker management bridge has no direct-Pod replacement."},
		{name: "mtu", guidance: "Use planned management semantics and Link MTU where applicable."},
		{name: "external-access", guidance: "Use Kubernetes Services and explicit exposure policy."},
		{name: "skip-when-unused", guidance: "Conditional Docker network creation has no direct-Pod replacement."},
		{name: "driver-opts", guidance: "Configure required networking through portable cluster and Link policy."},
	}
	rules := make([]fieldRule, 0, len(fields))
	for _, field := range fields {
		rules = append(rules, fieldRule{
			Lookup:      appendPath(prefix, field.name),
			Display:     display,
			Source:      appendPath(prefix, field.name),
			Disposition: dispositionNoMap,
			Guidance:    field.guidance,
		})
	}

	return rules
}

func newDiagnostic(
	kind string,
	object *unstructured.Unstructured,
	rule fieldRule,
) Diagnostic {
	return Diagnostic{
		Kind:        kind,
		Namespace:   object.GetNamespace(),
		Name:        object.GetName(),
		Path:        jsonPath(rule.Display),
		SourcePath:  optionalJSONPath(rule.Source),
		Disposition: rule.Disposition,
		Guidance:    rule.Guidance,
	}
}

func optionalJSONPath(segments []string) string {
	if len(segments) == 0 {
		return ""
	}

	return jsonPath(segments)
}

func pathPresent(object map[string]any, path []string) (bool, error) {
	_, present, err := pathValue(object, path)

	return present, err
}

func pathValue(object map[string]any, path []string) (any, bool, error) {
	current := object
	for i, segment := range path {
		value, exists := current[segment]
		if !exists {
			return nil, false, nil
		}
		if i == len(path)-1 {
			return value, true, nil
		}
		next, ok := value.(map[string]any)
		if !ok {
			return nil, false, fmt.Errorf("parent is not an object")
		}
		current = next
	}

	return nil, false, nil
}

func inspectionError(
	kind string,
	object *unstructured.Unstructured,
	path []string,
) error {
	return fmt.Errorf(
		"inspecting %s %s/%s path %s: parent is not an object",
		kind,
		object.GetNamespace(),
		object.GetName(),
		jsonPath(path),
	)
}

func appendPath(prefix []string, field string) []string {
	path := make([]string, len(prefix), len(prefix)+1)
	copy(path, prefix)

	return append(path, field)
}

func jsonPath(segments []string) string {
	path := "$"
	for _, segment := range segments {
		if isIdentifier(segment) {
			path += "." + segment
		} else {
			path += "['" + segment + "']"
		}
	}

	return path
}

func isIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for i, char := range []byte(value) {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || char == '_' ||
			(i > 0 && char >= '0' && char <= '9') {
			continue
		}

		return false
	}

	return true
}
