package default_values_test

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	k8scorev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"
)

// resourceDefaultExclusions records registry entries that are not direct c9s workloads. Keep the
// reasons user-facing: this list is also the test's documentation of intentional non-coverage.
var resourceDefaultExclusions = map[string]string{
	"host":          "host namespace connectivity is modeled by host Links, not a direct device Pod",
	"bridge":        "Linux bridges are host connectivity resources, not device application containers",
	"ovs-bridge":    "OVS bridges are host connectivity resources, not device application containers",
	"ext-container": "external containers are not created as c9s direct device Pods",
	"k8s-kind":      "nested kind clusters are not direct device application containers",
}

type resourceValues struct {
	GlobalConfig struct {
		Deployment struct {
			ResourcesByKind map[string]k8scorev1.ResourceRequirements `json:"resourcesByKind"`
		} `json:"deployment"`
	} `json:"globalConfig"`
}

func loadShippedKindResources(t *testing.T) map[string]k8scorev1.ResourceRequirements {
	t.Helper()

	raw, err := os.ReadFile("../../values.yaml")
	if err != nil {
		t.Fatal(err)
	}
	values := resourceValues{}
	if err = yaml.Unmarshal(raw, &values); err != nil {
		t.Fatal(err)
	}

	return values.GlobalConfig.Deployment.ResourcesByKind
}

func resourceCoverageErrors(
	kinds []string,
	defaults map[string]k8scorev1.ResourceRequirements,
	exclusions map[string]string,
) []string {
	problems := []string{}
	for _, kind := range kinds {
		resources, exists := defaults[kind]
		if !exists {
			if exclusions[kind] == "" {
				problems = append(
					problems,
					fmt.Sprintf("kind %q has no default or documented exclusion", kind),
				)
			}

			continue
		}

		if reason := exclusions[kind]; reason != "" {
			problems = append(problems, fmt.Sprintf("kind %q has both default and exclusion", kind))
		}
		if len(resources.Limits) != 0 {
			problems = append(problems, fmt.Sprintf("kind %q ships resource limits", kind))
		}
		if resources.Requests.Cpu().Sign() <= 0 || resources.Requests.Memory().Sign() <= 0 {
			problems = append(
				problems,
				fmt.Sprintf("kind %q lacks positive CPU and memory requests", kind),
			)
		}
	}

	return problems
}

func TestCriticalKindRequestDefaults(t *testing.T) {
	defaults := loadShippedKindResources(t)
	for kind, expected := range map[string]struct {
		cpu    string
		memory string
	}{
		"nokia_srlinux": {cpu: "2", memory: "2Gi"},
		"srl":           {cpu: "2", memory: "2Gi"},
		"nokia_srsim":   {cpu: "1", memory: "4Gi"},
	} {
		resources := defaults[kind]
		if got := resources.Requests.Cpu().String(); got != expected.cpu {
			t.Errorf("kind %q CPU request = %q, want %q", kind, got, expected.cpu)
		}
		if got := resources.Requests.Memory().String(); got != expected.memory {
			t.Errorf("kind %q memory request = %q, want %q", kind, got, expected.memory)
		}
	}
}

func TestShippedResourcesCoverSupportedRegistryKinds(t *testing.T) {
	registry := clabernetesinternaldeviceplan.NewContainerlabRegistry()
	kinds := registry.GetRegisteredNodeKindNames()
	problems := resourceCoverageErrors(
		kinds,
		loadShippedKindResources(t),
		resourceDefaultExclusions,
	)
	for _, problem := range problems {
		t.Error(problem)
	}
}

func TestResourceCoverageRejectsUnconfiguredNewKind(t *testing.T) {
	defaults := loadShippedKindResources(t)
	problems := resourceCoverageErrors(
		[]string{"future-kind-that-needs-defaults"},
		defaults,
		resourceDefaultExclusions,
	)
	if !slices.ContainsFunc(problems, func(problem string) bool {
		return strings.Contains(problem, "future-kind-that-needs-defaults")
	}) {
		t.Fatalf("new-kind coverage problems = %#v", problems)
	}
}
