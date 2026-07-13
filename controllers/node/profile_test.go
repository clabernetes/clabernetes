package node_test

import (
	"reflect"
	"testing"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/srl-labs/clabernetes/config"
	clabernetescontrollersnode "github.com/srl-labs/clabernetes/controllers/node"
	clabernetesutil "github.com/srl-labs/clabernetes/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func testProfileNode(name string, nodeLabels map[string]string) *clabernetesapisv1alpha1.Node {
	node := &clabernetesapisv1alpha1.Node{}
	node.Name = name
	node.Namespace = "clabernetes"
	node.Labels = nodeLabels

	return node
}

func testProfile(
	name string,
	priority int,
	matchLabels map[string]string,
	mutate func(spec *clabernetesapisv1alpha1.NodeProfileSpec),
) clabernetesapisv1alpha1.NodeProfile {
	profile := clabernetesapisv1alpha1.NodeProfile{}
	profile.Name = name
	profile.Namespace = "clabernetes"
	profile.Spec.Priority = priority
	profile.Spec.NodeSelector = metav1.LabelSelector{MatchLabels: matchLabels}

	if mutate != nil {
		mutate(&profile.Spec)
	}

	return profile
}

func TestResolveProfileDefaults(t *testing.T) {
	resolved, err := clabernetescontrollersnode.ResolveProfile(
		testProfileNode("srl1", nil),
		nil,
		clabernetesconfig.GetFakeManager,
	)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if len(resolved.AppliedProfiles) != 0 {
		t.Fatalf("expected no applied profiles, got %v", resolved.AppliedProfiles)
	}

	if resolved.ExposeType != "LoadBalancer" {
		t.Fatalf("expected default expose type LoadBalancer, got %q", resolved.ExposeType)
	}

	if resolved.Connectivity != "vxlan" {
		t.Fatalf("expected default connectivity vxlan, got %q", resolved.Connectivity)
	}
}

func TestResolveProfileSelectorsAndPrecedence(t *testing.T) {
	profiles := []clabernetesapisv1alpha1.NodeProfile{
		testProfile(
			"low-priority",
			1,
			map[string]string{"team": "netops"},
			func(spec *clabernetesapisv1alpha1.NodeProfileSpec) {
				spec.Deployment = &clabernetesapisv1alpha1.NodeProfileDeployment{
					LauncherLogLevel:    "debug",
					ContainerlabTimeout: "5m",
				}
				spec.Connectivity = "slurpeeth"
			},
		),
		testProfile(
			"high-priority",
			10,
			map[string]string{"team": "netops"},
			func(spec *clabernetesapisv1alpha1.NodeProfileSpec) {
				spec.Deployment = &clabernetesapisv1alpha1.NodeProfileDeployment{
					LauncherLogLevel: "info",
				}
			},
		),
		testProfile(
			"unrelated",
			100,
			map[string]string{"team": "someone-else"},
			func(spec *clabernetesapisv1alpha1.NodeProfileSpec) {
				spec.Deployment = &clabernetesapisv1alpha1.NodeProfileDeployment{
					LauncherLogLevel: "critical",
				}
			},
		),
	}

	resolved, err := clabernetescontrollersnode.ResolveProfile(
		testProfileNode("srl1", map[string]string{"team": "netops"}),
		profiles,
		clabernetesconfig.GetFakeManager,
	)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	expectedApplied := []string{"low-priority", "high-priority"}
	if !reflect.DeepEqual(resolved.AppliedProfiles, expectedApplied) {
		t.Fatalf("expected applied profiles %v, got %v", expectedApplied, resolved.AppliedProfiles)
	}

	// higher priority wins per field...
	if resolved.LauncherLogLevel != "info" {
		t.Fatalf("expected launcher log level 'info', got %q", resolved.LauncherLogLevel)
	}

	// ...but fields the higher priority profile does not set survive from the lower one
	if resolved.ContainerlabTimeout != "5m" {
		t.Fatalf("expected containerlab timeout '5m', got %q", resolved.ContainerlabTimeout)
	}

	if resolved.Connectivity != "slurpeeth" {
		t.Fatalf("expected connectivity 'slurpeeth', got %q", resolved.Connectivity)
	}
}

func TestResolveProfileEmptySelectorSelectsAll(t *testing.T) {
	profiles := []clabernetesapisv1alpha1.NodeProfile{
		testProfile(
			"everyone",
			0,
			nil,
			func(spec *clabernetesapisv1alpha1.NodeProfileSpec) {
				spec.Expose = &clabernetesapisv1alpha1.NodeProfileExpose{
					DisableAutoExpose: clabernetesutil.ToPointer(true),
				}
			},
		),
	}

	resolved, err := clabernetescontrollersnode.ResolveProfile(
		testProfileNode("srl1", nil),
		profiles,
		clabernetesconfig.GetFakeManager,
	)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if !resolved.DisableAutoExpose {
		t.Fatal("expected empty selector profile to apply to unlabeled node")
	}
}
