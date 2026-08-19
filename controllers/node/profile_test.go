package node_test

import (
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetescontrollersnode "github.com/clabernetes/clabernetes/controllers/node"
	clabernetesutil "github.com/clabernetes/clabernetes/util"
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
				DisableAutoExpose: clabernetesutil.ToPointer(true),
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
}
