//nolint:gocognit,gocyclo,testpackage // dense fixture-driven tests exercise one boundary end to end.
package upgradepreflight

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"reflect"
	"sort"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientgofake "k8s.io/client-go/dynamic/fake"
)

const secretSentinel = "do-not-leak-this-legacy-secret-value"

func TestInspectReportsCompleteMigrationInventoryWithoutMutation(t *testing.T) {
	t.Parallel()

	expectedPaths := map[string][]string{
		"Config": {
			"$.spec.deployment.containerlabDebug",
			"$.spec.deployment.containerlabTimeout",
			"$.spec.deployment.containerlabVersion",
			"$.spec.deployment.extraEnv",
			"$.spec.deployment.launcherImage",
			"$.spec.deployment.launcherImagePullPolicy",
			"$.spec.deployment.launcherLogLevel",
			"$.spec.deployment.privilegedLauncher",
			"$.spec.deployment.resourcesByContainerlabKind",
			"$.spec.imagePull.criHostsDir",
			"$.spec.imagePull.criKindOverride",
			"$.spec.imagePull.criSockOverride",
			"$.spec.imagePull.dockerConfig",
			"$.spec.imagePull.dockerDaemonConfig",
			"$.spec.imagePull.pullThroughOverride",
		},
		"LauncherProfile": {
			"$.spec.deployment.containerlabDebug",
			"$.spec.deployment.containerlabTimeout",
			"$.spec.deployment.containerlabVersion",
			"$.spec.deployment.extraEnv",
			"$.spec.deployment.launcherImage",
			"$.spec.deployment.launcherImagePullPolicy",
			"$.spec.deployment.launcherLogLevel",
			"$.spec.deployment.privilegedLauncher",
			"$.spec.imagePull.dockerConfig",
			"$.spec.imagePull.dockerDaemonConfig",
			"$.spec.imagePull.insecureRegistries",
			"$.spec.imagePull.pullThroughOverride",
			"$.spec.mgmt['external-access']",
			"$.spec.mgmt.mtu",
			"$.spec.mgmt.network",
		},
		"Topology": {
			"$.spec.definition.containerlab#$.mgmt.bridge",
			"$.spec.definition.containerlab#$.mgmt['driver-opts']",
			"$.spec.definition.containerlab#$.mgmt['external-access']",
			"$.spec.definition.containerlab#$.mgmt.mtu",
			"$.spec.definition.containerlab#$.mgmt.network",
			"$.spec.definition.containerlab#$.mgmt['skip-when-unused']",
			"$.spec.deployment.containerlabDebug",
			"$.spec.deployment.containerlabTimeout",
			"$.spec.deployment.containerlabVersion",
			"$.spec.deployment.extraEnv",
			"$.spec.deployment.launcherImage",
			"$.spec.deployment.launcherImagePullPolicy",
			"$.spec.deployment.launcherLogLevel",
			"$.spec.deployment.privilegedLauncher",
			"$.spec.imagePull.dockerConfig",
			"$.spec.imagePull.dockerDaemonConfig",
			"$.spec.imagePull.insecureRegistries",
			"$.spec.imagePull.pullThroughOverride",
		},
	}

	for kind, wantPaths := range expectedPaths {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()

			object := legacyObject(kind, "migration-system", strings.ToLower(kind))
			for index, rule := range rulesForKind(kind) {
				value := any(secretSentinel)

				switch index % 5 {
				case 0:
					value = ""
				case 1:
					value = false
				case 2:
					value = float64(0)
				case 3:
					value = []any{}
				case 4:
					value = map[string]any{}
				}

				setPath(object.Object, rule.Lookup, value)
			}

			if kind == "Topology" {
				setPath(object.Object, []string{"spec", "definition", "containerlab"}, `
name: migration
mgmt:
  network: ""
  bridge: ""
  mtu: 0
  external-access: false
  skip-when-unused: false
  driver-opts: {}
topology:
  nodes: {}
`)
			}

			before := object.DeepCopy()

			diagnostics, err := Inspect(kind, object)
			if err != nil {
				t.Fatalf("Inspect() error = %v", err)
			}

			if !reflect.DeepEqual(object.Object, before.Object) {
				t.Fatal("preflight mutated the stored resource")
			}

			gotPaths := make([]string, 0, len(diagnostics))
			for _, diagnostic := range diagnostics {
				location := diagnostic.Path
				if diagnostic.SourcePath != "" {
					location += "#" + diagnostic.SourcePath
				}

				gotPaths = append(gotPaths, location)

				if diagnostic.Kind != kind || diagnostic.Namespace != "migration-system" ||
					!strings.EqualFold(diagnostic.Name, kind) || diagnostic.Disposition == "" ||
					diagnostic.Guidance == "" {
					t.Fatalf("incomplete diagnostic = %#v", diagnostic)
				}
			}

			sort.Strings(gotPaths)
			sort.Strings(wantPaths)

			if !reflect.DeepEqual(gotPaths, wantPaths) {
				t.Fatalf("diagnostic paths = %#v, want %#v", gotPaths, wantPaths)
			}
		})
	}
}

func TestInspectIgnoresRetainedAndReplacementFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		kind   string
		object map[string]any
	}{
		{
			kind: "Config",
			object: map[string]any{
				"spec": map[string]any{
					"imagePull": map[string]any{
						"policy": "Never", "pullSecrets": []any{},
						"registryMetadataTrust": map[string]any{},
					},
					"deployment": map[string]any{
						"resourcesDefault":     map[string]any{},
						"nodeSelectorsByImage": map[string]any{},
					},
				},
			},
		},
		{
			kind: "LauncherProfile",
			object: map[string]any{
				"spec": map[string]any{
					"imagePull": map[string]any{
						"policy": "Always", "pullSecrets": []any{},
					},
					"deployment": map[string]any{"persistence": map[string]any{}},
					"mgmt": map[string]any{
						"ipv4-subnet": "172.20.20.0/24", "ipv4-range": "172.20.20.0/25",
						"ipv4-gw": "172.20.20.1", "ipv6-subnet": "2001:db8::/64",
						"ipv6-range": "2001:db8::/80", "ipv6-gw": "2001:db8::1",
					},
				},
			},
		},
		{
			kind: "Topology",
			object: map[string]any{
				"spec": map[string]any{
					"definition": map[string]any{"containerlab": `
name: retained
mgmt:
  ipv4-subnet: 172.20.20.0/24
  ipv4-range: 172.20.20.0/25
  ipv4-gw: 172.20.20.1
topology:
  nodes: {}
`},
					"imagePull": map[string]any{
						"policy": "IfNotPresent", "pullSecrets": []any{},
					},
					"deployment": map[string]any{"persistence": map[string]any{}},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.kind, func(t *testing.T) {
			t.Parallel()

			object := legacyObject(test.kind, "default", "retained")
			maps.Copy(object.Object, test.object)

			diagnostics, err := Inspect(test.kind, object)
			if err != nil {
				t.Fatalf("Inspect() error = %v", err)
			}

			if len(diagnostics) != 0 {
				t.Fatalf("retained fields produced diagnostics = %#v", diagnostics)
			}
		})
	}
}

func TestInspectDoesNotConvertPullThroughOverrideToPullPolicy(t *testing.T) {
	t.Parallel()

	object := legacyObject("LauncherProfile", "default", "legacy-pull-through")
	setPath(
		object.Object,
		[]string{"spec", "imagePull", "pullThroughOverride"},
		"always",
	)

	diagnostics, err := Inspect("LauncherProfile", object)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}

	if len(diagnostics) != 1 ||
		diagnostics[0].Path != "$.spec.imagePull.pullThroughOverride" {
		t.Fatalf("pull-through diagnostics = %#v", diagnostics)
	}

	if _, present, lookupErr := pathValue(
		object.Object,
		[]string{"spec", "imagePull", "policy"},
	); lookupErr != nil || present {
		t.Fatalf("preflight created imagePull.policy, present=%v err=%v", present, lookupErr)
	}

	if got, _, lookupErr := pathValue(
		object.Object,
		[]string{"spec", "imagePull", "pullThroughOverride"},
	); lookupErr != nil || got != "always" {
		t.Fatalf("preflight changed pullThroughOverride, got=%#v err=%v", got, lookupErr)
	}
}

func TestInspectReportsPresentNull(t *testing.T) {
	t.Parallel()

	object := legacyObject("Config", "default", "null-field")
	setPath(object.Object, []string{"spec", "deployment", "extraEnv"}, nil)

	diagnostics, err := Inspect("Config", object)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}

	if len(diagnostics) != 1 || diagnostics[0].Path != "$.spec.deployment.extraEnv" {
		t.Fatalf("null-field diagnostics = %#v", diagnostics)
	}
}

func TestRunSortsValueFreeDiagnosticsAndReturnsIncompatible(t *testing.T) {
	t.Parallel()

	config := legacyObject("Config", "z-system", "z-config")
	setPath(
		config.Object,
		[]string{"spec", "imagePull", "pullThroughOverride"},
		secretSentinel,
	)

	profile := legacyObject("LauncherProfile", "a-system", "a-profile")
	setPath(
		profile.Object,
		[]string{"spec", "deployment", "privilegedLauncher"},
		false,
	)

	topology := legacyObject("Topology", "b-system", "b-topology")
	setPath(
		topology.Object,
		[]string{"spec", "definition", "containerlab"},
		"name: test\nmgmt:\n  mtu: 0\ntopology:\n  nodes: {}\n",
	)

	client := clientgofake.NewSimpleDynamicClientWithCustomListKinds(
		apimachineryruntime.NewScheme(),
		map[schema.GroupVersionResource]string{
			resourceTargets[0].GVR: "ConfigList",
			resourceTargets[1].GVR: "LauncherProfileList",
			resourceTargets[2].GVR: "TopologyList",
		},
		config,
		profile,
		topology,
	)
	output := &bytes.Buffer{}

	err := Run(context.Background(), client, output)
	if !errors.Is(err, ErrIncompatible) {
		t.Fatalf("Run() error = %v, want ErrIncompatible", err)
	}

	var incompatible *IncompatibleError
	if !errors.As(err, &incompatible) || incompatible.Count != 3 {
		t.Fatalf("Run() error = %#v, want count 3", err)
	}

	if strings.Contains(output.String(), secretSentinel) {
		t.Fatalf("diagnostics leaked stored value: %s", output.String())
	}

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 3 || !strings.Contains(lines[0], `"kind":"Config"`) ||
		!strings.Contains(lines[1], `"kind":"LauncherProfile"`) ||
		!strings.Contains(lines[2], `"kind":"Topology"`) {
		t.Fatalf("diagnostics are not sorted JSON lines: %q", lines)
	}

	actions := client.Actions()
	if len(actions) != len(resourceTargets) {
		t.Fatalf("client actions = %#v", actions)
	}

	for _, action := range actions {
		if action.GetVerb() != "list" {
			t.Fatalf("preflight performed mutating action %#v", action)
		}
	}
}

func TestRunSucceedsWithoutRemovedFields(t *testing.T) {
	t.Parallel()

	client := clientgofake.NewSimpleDynamicClientWithCustomListKinds(
		apimachineryruntime.NewScheme(),
		map[schema.GroupVersionResource]string{
			resourceTargets[0].GVR: "ConfigList",
			resourceTargets[1].GVR: "LauncherProfileList",
			resourceTargets[2].GVR: "TopologyList",
		},
		legacyObject("Config", "default", "clabernetes"),
	)

	output := &bytes.Buffer{}
	if err := Run(context.Background(), client, output); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if got := output.String(); got != "upgrade preflight passed: no removed fields found\n" {
		t.Fatalf("Run() output = %q", got)
	}
}

func TestInspectMalformedTopologyDoesNotLeakDefinition(t *testing.T) {
	t.Parallel()

	object := legacyObject("Topology", "default", "malformed")
	setPath(
		object.Object,
		[]string{"spec", "definition", "containerlab"},
		"name: ["+secretSentinel,
	)

	_, err := Inspect("Topology", object)
	if err == nil {
		t.Fatal("Inspect() accepted malformed embedded topology")
	}

	if strings.Contains(err.Error(), secretSentinel) {
		t.Fatalf("Inspect() error leaked topology source: %v", err)
	}
}

func legacyObject(kind, namespace, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "c9s.run/v1alpha1",
		"kind":       kind,
		"metadata": map[string]any{
			"namespace": namespace,
			"name":      name,
		},
		"spec": map[string]any{},
	}}
}

func setPath(object map[string]any, path []string, value any) {
	current := object
	for _, segment := range path[:len(path)-1] {
		next, ok := current[segment].(map[string]any)
		if !ok {
			next = map[string]any{}
			current[segment] = next
		}

		current = next
	}

	current[path[len(path)-1]] = value
}
