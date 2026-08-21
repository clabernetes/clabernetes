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
				Persistence: &clabernetesapisv1alpha1.Persistence{
					Enabled: true, ClaimSize: "10Gi",
				},
			}
			spec.ImagePull = &clabernetesapisv1alpha1.LauncherProfileImagePull{
				Policy: string(k8scorev1.PullNever),
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

	if !resolved.Persistence.Enabled || resolved.Persistence.ClaimSize != "10Gi" ||
		resolved.ImagePullPolicy != string(k8scorev1.PullNever) {
		t.Fatalf("expected direct workload overrides, got %+v", resolved)
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
				PullSecrets: []string{},
			}
			spec.Scheduling = &clabernetesapisv1alpha1.Scheduling{
				NodeSelector: map[string]string{},
				Tolerations:  []k8scorev1.Toleration{},
				Affinity:     &k8scorev1.Affinity{},
			}
		},
	)
	getter := func() clabernetesconfig.Manager {
		return clabernetesconfig.NewFakeManager(
			clabernetesconfig.WithImagePullSecrets([]string{"global-registry"}),
		)
	}

	resolved, err := clabernetescontrollersnode.ResolveProfile(
		testProfileNode("srl1", nil),
		profile,
		getter,
	)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if resolved.PullSecrets == nil || resolved.NodeSelector == nil || resolved.Tolerations == nil {
		t.Fatalf("expected explicit empty collections to remain non-nil, got %+v", resolved)
	}

	if len(resolved.PullSecrets) != 0 {
		t.Fatalf("expected profile to clear global pull Secrets, got %+v", resolved.PullSecrets)
	}

	if resolved.Affinity == nil {
		t.Fatal("expected explicit empty affinity to remain non-nil")
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
