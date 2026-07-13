package node_test

import (
	"testing"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/srl-labs/clabernetes/config"
	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	clabernetescontrollersnode "github.com/srl-labs/clabernetes/controllers/node"
	claberneteslogging "github.com/srl-labs/clabernetes/logging"
	k8scorev1 "k8s.io/api/core/v1"
)

func findEnv(envs []k8scorev1.EnvVar, name string) *k8scorev1.EnvVar {
	for idx := range envs {
		if envs[idx].Name == name {
			return &envs[idx]
		}
	}

	return nil
}

func TestRenderDeployment(t *testing.T) {
	node := &clabernetesapisv1alpha1.Node{}
	node.Name = "srl1"
	node.Namespace = "clabernetes"
	node.Labels = map[string]string{
		clabernetesconstants.LabelTopologyOwner: "my-lab",
	}
	node.Spec.Kind = "nokia_srlinux"
	node.Spec.Image = "ghcr.io/nokia/srlinux:latest"

	profile := testResolvedProfile(t, func(profile *clabernetescontrollersnode.ResolvedProfile) {
		profile.LauncherImage = "launcher:latest"
		profile.LauncherImagePullPolicy = "IfNotPresent"
		profile.Connectivity = "vxlan"
	})

	reconciler := clabernetescontrollersnode.NewDeploymentReconciler(
		&claberneteslogging.FakeInstance{},
		"clabernetes",
		"clabernetes",
		clabernetesconstants.KubernetesCRIContainerd,
		clabernetesconfig.GetFakeManager,
	)

	deployment := reconciler.Render(
		&clabernetescontrollersnode.RenderInput{
			Node:                  node,
			Profile:               profile,
			GroupMembers:          []string{"srl1", "sim-a", "sim-b"},
			LinkAttachmentsDigest: "attachments-digest",
			NodeConfigDigest:      "config-digest",
		},
	)

	if deployment.Name != "srl1" || deployment.Spec.Template.Spec.Hostname != "srl1" {
		t.Fatalf(
			"expected deployment/hostname named after the node, got %q/%q",
			deployment.Name,
			deployment.Spec.Template.Spec.Hostname,
		)
	}

	if deployment.Labels[clabernetesconstants.LabelTopologyOwner] != "my-lab" {
		t.Fatal("expected topology owner label to be propagated from the node")
	}

	podAnnotations := deployment.Spec.Template.ObjectMeta.Annotations

	if podAnnotations[clabernetesconstants.AnnotationLinkAttachmentsDigest] != "attachments-digest" {
		t.Fatal("expected link attachments digest pod annotation")
	}

	if podAnnotations[clabernetesconstants.AnnotationNodeConfigDigest] != "config-digest" {
		t.Fatal("expected node config digest pod annotation")
	}

	assertRenderedContainer(t, &deployment.Spec.Template.Spec.Containers[0])
}

func assertRenderedContainer(t *testing.T, container *k8scorev1.Container) {
	t.Helper()

	if container.Image != "launcher:latest" {
		t.Fatalf("expected launcher image from profile, got %q", container.Image)
	}

	nodeNameEnv := findEnv(container.Env, clabernetesconstants.LauncherNodeNameEnv)
	if nodeNameEnv == nil || nodeNameEnv.Value != "srl1" {
		t.Fatalf("expected launcher node name env, got %+v", nodeNameEnv)
	}

	groupMembersEnv := findEnv(container.Env, clabernetesconstants.LauncherGroupMembersEnv)
	if groupMembersEnv == nil || groupMembersEnv.Value != "sim-a,sim-b" {
		t.Fatalf("expected group members env 'sim-a,sim-b', got %+v", groupMembersEnv)
	}

	nodeImageEnv := findEnv(container.Env, clabernetesconstants.LauncherNodeImageEnv)
	if nodeImageEnv == nil || nodeImageEnv.Value != "ghcr.io/nokia/srlinux:latest" {
		t.Fatalf("expected node image env, got %+v", nodeImageEnv)
	}

	var podinfoMounted bool

	for _, mount := range container.VolumeMounts {
		if mount.Name == "podinfo" && mount.MountPath == "/clabernetes/podinfo" {
			podinfoMounted = true
		}
	}

	if !podinfoMounted {
		t.Fatal("expected the downward api podinfo volume to be mounted")
	}
}

func TestRenderDeploymentStandaloneHasNoGroupEnv(t *testing.T) {
	node := &clabernetesapisv1alpha1.Node{}
	node.Name = "srl1"
	node.Namespace = "clabernetes"
	node.Spec.Image = "ghcr.io/nokia/srlinux:latest"

	reconciler := clabernetescontrollersnode.NewDeploymentReconciler(
		&claberneteslogging.FakeInstance{},
		"clabernetes",
		"clabernetes",
		clabernetesconstants.KubernetesCRIContainerd,
		clabernetesconfig.GetFakeManager,
	)

	deployment := reconciler.Render(
		&clabernetescontrollersnode.RenderInput{
			Node:         node,
			Profile:      testResolvedProfile(t, nil),
			GroupMembers: []string{"srl1"},
		},
	)

	container := deployment.Spec.Template.Spec.Containers[0]

	if findEnv(container.Env, clabernetesconstants.LauncherGroupMembersEnv) != nil {
		t.Fatal("expected no group members env for a standalone node")
	}
}
