package node_test

import (
	"reflect"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetescontrollersnode "github.com/clabernetes/clabernetes/controllers/node"
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
)

func testProfileNode(name string, nodeLabels map[string]string) *clabernetesapisv1alpha1.Node {
	node := &clabernetesapisv1alpha1.Node{}
	node.Name = name
	node.Namespace = "clabernetes"
	node.Labels = nodeLabels

	return node
}

func testLauncherProfile(
	name string,
	mutate func(spec *clabernetesapisv1alpha1.LauncherProfileSpec),
) *clabernetesapisv1alpha1.LauncherProfile {
	profile := &clabernetesapisv1alpha1.LauncherProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Namespace:  "clabernetes",
			UID:        apimachinerytypes.UID(name + "-uid"),
			Generation: 7,
		},
	}

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

	if resolved.AppliedLauncherProfile != nil {
		t.Fatalf("expected no applied LauncherProfile, got %+v", resolved.AppliedLauncherProfile)
	}

	if resolved.ExposeType != "LoadBalancer" {
		t.Fatalf("expected default expose type LoadBalancer, got %q", resolved.ExposeType)
	}
}

func TestResolveExplicitLauncherProfile(t *testing.T) {
	profile := testLauncherProfile(
		"custom",
		func(spec *clabernetesapisv1alpha1.LauncherProfileSpec) {
			spec.Deployment = &clabernetesapisv1alpha1.LauncherProfileDeployment{
				LauncherLogLevel:    "debug",
				ContainerlabTimeout: new("5m"),
				PrivilegedLauncher:  new(false),
			}
			spec.Expose = &clabernetesapisv1alpha1.LauncherProfileExpose{
				DisableAutoExpose: new(true),
			}
		},
	)

	resolved, err := clabernetescontrollersnode.ResolveProfile(
		testProfileNode("srl1", map[string]string{"ignored": "label"}),
		profile,
		clabernetesconfig.GetFakeManager,
	)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if resolved.AppliedLauncherProfile == nil ||
		resolved.AppliedLauncherProfile.Name != profile.GetName() ||
		resolved.AppliedLauncherProfile.UID != profile.GetUID() ||
		resolved.AppliedLauncherProfile.Generation != profile.GetGeneration() {
		t.Fatalf(
			"expected applied profile identity from %q, got %+v",
			profile.GetName(),
			resolved.AppliedLauncherProfile,
		)
	}

	if resolved.LauncherLogLevel != "debug" || resolved.ContainerlabTimeout != "5m" {
		t.Fatalf("expected explicit deployment overrides, got %+v", resolved)
	}

	if resolved.PrivilegedLauncher {
		t.Fatal("expected explicit false to override the Config true default")
	}

	if !resolved.DisableAutoExpose {
		t.Fatal("expected explicit expose override")
	}
}

func TestResolveProfilePreservesExplicitEmptyValues(t *testing.T) {
	profile := testLauncherProfile(
		"empty-values",
		func(spec *clabernetesapisv1alpha1.LauncherProfileSpec) {
			spec.ImagePull = &clabernetesapisv1alpha1.LauncherProfileImagePull{
				InsecureRegistries: []string{},
				PullSecrets:        []string{},
				DockerDaemonConfig: new(""),
				DockerConfig:       new(""),
			}
			spec.Scheduling = &clabernetesapisv1alpha1.Scheduling{
				NodeSelector: map[string]string{},
				Tolerations:  []k8scorev1.Toleration{},
				Affinity:     &k8scorev1.Affinity{},
			}
			spec.Deployment = &clabernetesapisv1alpha1.LauncherProfileDeployment{
				ContainerlabTimeout: new(""),
				ContainerlabVersion: new(""),
				ExtraEnv:            []k8scorev1.EnvVar{},
			}
		},
	)

	resolved, err := clabernetescontrollersnode.ResolveProfile(
		testProfileNode("srl1", nil),
		profile,
		clabernetesconfig.GetFakeManager,
	)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if resolved.InsecureRegistries == nil || resolved.PullSecrets == nil ||
		resolved.NodeSelector == nil || resolved.Tolerations == nil ||
		resolved.ExtraEnv == nil {
		t.Fatalf("expected explicit empty collections to remain non-nil, got %+v", resolved)
	}

	if resolved.Affinity == nil {
		t.Fatal("expected explicit empty affinity to remain non-nil")
	}

	if resolved.DockerDaemonConfig != "" || resolved.DockerConfig != "" ||
		resolved.ContainerlabTimeout != "" || resolved.ContainerlabVersion != "" {
		t.Fatalf("expected explicit empty scalar overrides, got %+v", resolved)
	}
}

func TestResolveProfileDeepCopiesAffinity(t *testing.T) {
	affinity := &k8scorev1.Affinity{
		NodeAffinity: &k8scorev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &k8scorev1.NodeSelector{
				NodeSelectorTerms: []k8scorev1.NodeSelectorTerm{{
					MatchExpressions: []k8scorev1.NodeSelectorRequirement{{
						Key:      "topology.kubernetes.io/zone",
						Operator: k8scorev1.NodeSelectorOpIn,
						Values:   []string{"zone-a"},
					}},
				}},
			},
		},
	}
	profile := testLauncherProfile(
		"affinity",
		func(spec *clabernetesapisv1alpha1.LauncherProfileSpec) {
			spec.Scheduling = &clabernetesapisv1alpha1.Scheduling{Affinity: affinity}
		},
	)

	resolved, err := clabernetescontrollersnode.ResolveProfile(
		testProfileNode("srl1", nil),
		profile,
		clabernetesconfig.GetFakeManager,
	)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if !reflect.DeepEqual(resolved.Affinity, affinity) {
		t.Fatalf(
			"resolved affinity differs from profile: got %+v, want %+v",
			resolved.Affinity,
			affinity,
		)
	}

	affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].
		MatchExpressions[0].Values[0] = "zone-b"

	if resolved.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.
		NodeSelectorTerms[0].MatchExpressions[0].Values[0] != "zone-a" {
		t.Fatal("resolved affinity shares mutable state with the LauncherProfile")
	}
}
