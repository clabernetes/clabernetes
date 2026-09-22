package directpod_test

import (
	"reflect"
	"testing"

	clabernetesinternaldirectpod "github.com/clabernetes/clabernetes/internal/directpod"
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

func TestDevicePlacementSpreadsAcrossDeploymentsAndPreservesConstraints(t *testing.T) {
	t.Parallel()

	options := clabernetesinternaldirectpod.Options{
		Name: "device-a", Namespace: "lab-a", PlanConfigMapName: "device-a-plan",
		InputConfigMapName: "device-a-input", ConnectivityRevisionConfigMapName: "device-a-links",
		PreparationImage: "example/c9s@sha256:1111", ConnectivityImage: "example/c9s@sha256:1111",
		EnableContainerStopSignals: true,
		NodeSelector:               map[string]string{"device-pool": "large"},
		Affinity: &k8scorev1.Affinity{NodeAffinity: &k8scorev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &k8scorev1.NodeSelector{
				NodeSelectorTerms: []k8scorev1.NodeSelectorTerm{
					{MatchExpressions: []k8scorev1.NodeSelectorRequirement{{
						Key: "disk", Operator: k8scorev1.NodeSelectorOpIn, Values: []string{"ssd"},
					}}},
				},
			},
		}},
	}
	first, err := clabernetesinternaldirectpod.Render(renderablePlan(), options)
	if err != nil {
		t.Fatal(err)
	}
	options.Name = "device-b"
	secondPlan := renderablePlan()
	secondPlan.Nodes[0].Name = options.Name
	second, err := clabernetesinternaldirectpod.Render(secondPlan, options)
	if err != nil {
		t.Fatal(err)
	}
	constraints := first.Spec.Template.Spec.TopologySpreadConstraints
	if len(constraints) != 1 {
		t.Fatalf("expected a namespace-wide device spread preference, got %v", constraints)
	}
	spread := constraints[0]
	if spread.WhenUnsatisfiable != k8scorev1.ScheduleAnyway ||
		spread.TopologyKey != k8scorev1.LabelHostname {
		t.Fatalf("spreading must remain a soft preference across workers: %v", spread)
	}
	selector, err := metav1.LabelSelectorAsSelector(spread.LabelSelector)
	if err != nil {
		t.Fatal(err)
	}
	if !selector.Matches(labels.Set(first.Spec.Template.Labels)) ||
		!selector.Matches(labels.Set(second.Spec.Template.Labels)) ||
		selector.Matches(labels.Set{"app": "unrelated"}) {
		t.Fatalf(
			"spread preference must count different device Deployments and exclude unrelated Pods: %v",
			selector,
		)
	}
	if !reflect.DeepEqual(first.Spec.Template.Spec.NodeSelector, options.NodeSelector) ||
		!reflect.DeepEqual(first.Spec.Template.Spec.Affinity, options.Affinity) {
		t.Fatal("device placement constraints changed")
	}
}
