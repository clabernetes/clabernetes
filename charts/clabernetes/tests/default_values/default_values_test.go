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
			APIGroups: []string{""}, Resources: []string{"pods/exec"}, Verbs: []string{"create"},
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
				case "pods/exec", "pods/log", "events":
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
		"--set",
		"manager.deviceRuntimeMode=direct",
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
