package default_values_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
	k8srbacv1 "k8s.io/api/rbac/v1"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
)

func TestMain(m *testing.M) {
	clabernetestesthelper.Flags()

	os.Exit(m.Run())
}

// TestDefaultValues -- really just here to ensure that we dont accidentally break our charts; this
// will probably be *highly* irritating in times of lots of chart updates, but, once we know the
// template are in a good place we can always just re-generate the "golden" outputs.
func TestDefaultValues(t *testing.T) {
	t.Parallel()

	testName := "default_values"
	chartName := "clabernetes"

	chartsDir, err := filepath.Abs("../../..")
	if err != nil {
		t.Error(err)
	}

	clabernetestesthelper.HelmTest(
		t,
		chartName,
		testName,
		clabernetesconstants.Clabernetes,
		"",
		chartsDir,
	)
}

func TestDirectRuntimeRoleIsReadOnlyAndCannotImportImages(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("test-fixtures/golden/clusterrole.yaml")
	if err != nil {
		t.Fatal(err)
	}

	decoder := k8syaml.NewYAMLOrJSONDecoder(bytes.NewReader(raw), 4096)

	for {
		role := &k8srbacv1.ClusterRole{}
		if err = decoder.Decode(role); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			t.Fatal(err)
		}

		if role.GetName() != "clabernetes-direct-runtime-role" {
			continue
		}

		want := []k8srbacv1.PolicyRule{
			{
				APIGroups: []string{"c9s.run"}, Resources: []string{"links"},
				Verbs: []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{""}, Resources: []string{"pods", "pods/log"},
				Verbs: []string{"get"},
			},
		}
		if !reflect.DeepEqual(role.Rules, want) {
			t.Fatalf("direct runtime role grants unexpected permissions: %#v", role.Rules)
		}

		return
	}

	t.Fatal("direct runtime role is absent")
}

func TestRestrictedManagerRolesCanPublishEventsAndExecDirectContainers(t *testing.T) {
	t.Parallel()

	chartsDir, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}

	raw := clabernetestesthelper.HelmCommand(
		t,
		chartsDir,
		"template",
		"./clabernetes",
		"--namespace",
		"c9s-system",
		"--set",
		"manager.restrictedRBAC.enabled=true",
		"--set",
		"manager.restrictedRBAC.targetNamespaces[0]=lab-a",
		"--show-only",
		"templates/restricted-rbac.yaml",
	)
	decoder := k8syaml.NewYAMLOrJSONDecoder(bytes.NewReader(renderedHelmYAML(t, raw)), 4096)
	wantNamespaces := map[string]bool{"c9s-system": false, "lab-a": false}

	for {
		role := &k8srbacv1.Role{}
		if err = decoder.Decode(role); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			t.Fatal(err)
		}

		if role.Kind != "Role" || role.GetName() != "clabernetes-restricted-role" {
			continue
		}

		if _, expected := wantNamespaces[role.GetNamespace()]; !expected {
			t.Fatalf("unexpected restricted manager Role namespace %q", role.GetNamespace())
		}

		wantNamespaces[role.GetNamespace()] = true
		want := k8srbacv1.PolicyRule{
			APIGroups: []string{"", "events.k8s.io"}, Resources: []string{"events"},
			Verbs: []string{"create", "patch", "update"},
		}

		if !slices.ContainsFunc(role.Rules, func(rule k8srbacv1.PolicyRule) bool {
			return reflect.DeepEqual(rule, want)
		}) {
			t.Fatalf(
				"restricted manager Role %q cannot publish Events: %#v",
				role.GetNamespace(),
				role.Rules,
			)
		}

		wantExec := k8srbacv1.PolicyRule{
			APIGroups: []string{""},
			Resources: []string{"pods/attach", "pods/exec"},
			Verbs:     []string{"create"},
		}

		if !slices.ContainsFunc(role.Rules, func(rule k8srbacv1.PolicyRule) bool {
			return reflect.DeepEqual(rule, wantExec)
		}) {
			t.Fatalf(
				"restricted manager Role %q cannot restart direct containers: %#v",
				role.GetNamespace(),
				role.Rules,
			)
		}
	}

	for namespace, found := range wantNamespaces {
		if !found {
			t.Fatalf("restricted manager Role is absent from namespace %q", namespace)
		}
	}
}

// A restricted install grants exec/log/events per namespace only; the same grants appearing in
// the ClusterRole would hand the manager cluster-wide exec and defeat restricted RBAC entirely.
func TestRestrictedClusterRoleDoesNotGrantClusterWideExecLogsOrEvents(t *testing.T) {
	t.Parallel()

	chartsDir, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}

	raw := clabernetestesthelper.HelmCommand(
		t,
		chartsDir,
		"template",
		"./clabernetes",
		"--namespace",
		"c9s-system",
		"--set",
		"manager.restrictedRBAC.enabled=true",
		"--show-only",
		"templates/clusterrole.yaml",
	)
	decoder := k8syaml.NewYAMLOrJSONDecoder(bytes.NewReader(renderedHelmYAML(t, raw)), 4096)
	seenManagerRole := false

	for {
		role := &k8srbacv1.ClusterRole{}
		if err = decoder.Decode(role); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			t.Fatal(err)
		}

		if role.Kind != "ClusterRole" || role.GetName() != "clabernetes-cluster-role" {
			continue
		}

		seenManagerRole = true

		for _, rule := range role.Rules {
			for _, resource := range rule.Resources {
				switch resource {
				case "pods/attach", "pods/exec", "pods/log", "events":
					t.Fatalf(
						"restricted ClusterRole must not grant %q cluster-wide: %#v",
						resource,
						rule,
					)
				}
			}
		}
	}

	if !seenManagerRole {
		t.Fatal("manager ClusterRole is absent")
	}
}

func TestDirectChartShipsNoNodeResidentConnectivityAgent(t *testing.T) {
	t.Parallel()

	chartsDir, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}

	rendered := clabernetestesthelper.HelmCommand(
		t,
		chartsDir,
		"template",
		"./clabernetes",
		"--namespace",
		"c9s-system",
	)

	if bytes.Contains(rendered, []byte("kind: DaemonSet")) ||
		bytes.Contains(rendered, []byte("host-endpoint")) {
		t.Fatal("direct chart renders a node-resident connectivity agent")
	}
}

func renderedHelmYAML(t *testing.T, output []byte) []byte {
	t.Helper()

	start := bytes.Index(output, []byte("---\n# Source:"))
	if start < 0 {
		t.Fatalf("Helm output contains no rendered YAML: %s", output)
	}

	return output[start:]
}

func TestOptionalRolloutAndDisabledPlannerPool(t *testing.T) {
	t.Parallel()
	chartsDir, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"null", "{}", "{\"batchSize\":0,\"maxConcurrentPerHost\":2}"} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			raw := clabernetestesthelper.HelmCommand(t, chartsDir, "template", "./clabernetes",
				"--set", "plannerPool.enabled=false", "--set-json", "globalConfig.rollout="+value)
			if bytes.Contains(raw, []byte("name: clabernetes-planner-pool")) {
				t.Fatal("disabled planner pool rendered")
			}
			if value != "null" && value != "{}" &&
				(!bytes.Contains(raw, []byte(`rolloutBatchSize: "0"`)) ||
					!bytes.Contains(raw, []byte(`rolloutMaxConcurrentPerHost: "2"`))) {
				t.Fatal("explicit rollout settings were lost")
			}
		})
	}
}

// The lab roles are what clients bind per namespace by name. The editor must operate labs only
// through c9s resources and Pod streams: no apps, no Pod writes, no Secrets, and every viewer
// rule.
func TestLabRolesGrantNoWorkloadWrites(t *testing.T) {
	t.Parallel()

	roles := goldenLabRoles(t)

	viewer, editor := roles["clabernetes-lab-viewer"], roles["clabernetes-lab-editor"]
	if viewer == nil || editor == nil || len(roles) != 2 {
		t.Fatalf("lab roles = %v, want clabernetes-lab-viewer and clabernetes-lab-editor", roles)
	}

	if viewer.AggregationRule != nil || editor.AggregationRule != nil ||
		viewer.Labels["rbac.authorization.k8s.io/aggregate-to-view"] != "" ||
		editor.Labels["rbac.authorization.k8s.io/aggregate-to-edit"] != "" {
		t.Fatal("lab roles must not aggregate into the built-in roles")
	}

	for _, rule := range viewer.Rules {
		if !slices.ContainsFunc(editor.Rules, func(candidate k8srbacv1.PolicyRule) bool {
			return reflect.DeepEqual(candidate, rule)
		}) {
			t.Fatalf("editor lacks viewer rule %#v", rule)
		}

		if slices.ContainsFunc(rule.Verbs, isWriteVerb) {
			t.Fatalf("viewer rule writes: %#v", rule)
		}
	}

	for _, rule := range editor.Rules {
		if problem := labEditorRuleProblem(rule); problem != "" {
			t.Fatalf("editor rule %#v: %s", rule, problem)
		}
	}

	for _, want := range []k8srbacv1.PolicyRule{
		{
			APIGroups: []string{"c9s.run"},
			Resources: []string{"topologies", "nodes", "links", "nodeprofiles"},
			Verbs:     []string{"create", "update", "patch", "delete"},
		},
		{
			APIGroups: []string{""}, Resources: []string{"pods/exec", "pods/portforward"},
			Verbs: []string{"get", "create"},
		},
	} {
		if !slices.ContainsFunc(editor.Rules, func(rule k8srbacv1.PolicyRule) bool {
			return reflect.DeepEqual(rule, want)
		}) {
			t.Fatalf("editor lacks %#v", want)
		}
	}
}

func TestLabRolesCanBeDisabled(t *testing.T) {
	t.Parallel()

	chartsDir, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}

	disabled := clabernetestesthelper.HelmCommand(
		t, chartsDir, "template", "./clabernetes", "--set", "labRoles.enabled=false",
	)
	if bytes.Contains(disabled, []byte("clabernetes-lab-")) {
		t.Fatal("disabled lab roles rendered")
	}
}

func goldenLabRoles(t *testing.T) map[string]*k8srbacv1.ClusterRole {
	t.Helper()

	raw, err := os.ReadFile("test-fixtures/golden/lab-roles.yaml")
	if err != nil {
		t.Fatal(err)
	}

	roles := map[string]*k8srbacv1.ClusterRole{}
	decoder := k8syaml.NewYAMLOrJSONDecoder(bytes.NewReader(raw), 4096)

	for {
		role := &k8srbacv1.ClusterRole{}
		if err = decoder.Decode(role); err != nil {
			if errors.Is(err, io.EOF) {
				return roles
			}

			t.Fatal(err)
		}

		if role.GetName() != "" {
			roles[role.GetName()] = role
		}
	}
}

func isWriteVerb(verb string) bool {
	return verb != "get" && verb != "list" && verb != "watch"
}

// labEditorRuleProblem names what a lab editor rule must never grant.
func labEditorRuleProblem(rule k8srbacv1.PolicyRule) string {
	for _, group := range rule.APIGroups {
		switch group {
		case "", "c9s.run", "events.k8s.io", "metrics.k8s.io":
		default:
			return "grants API group " + group
		}
	}

	for _, resource := range rule.Resources {
		switch resource {
		case "pods":
			if slices.ContainsFunc(rule.Verbs, isWriteVerb) {
				return "writes Pods"
			}
		case "namespaces":
			// Bound per namespace, get reads only the lab's own Namespace.
			if !slices.Equal(rule.Verbs, []string{"get"}) {
				return "grants more than get on namespaces"
			}
		case "secrets", "*", "pods/attach", "pods/ephemeralcontainers":
			return "grants " + resource
		}
	}

	return ""
}
