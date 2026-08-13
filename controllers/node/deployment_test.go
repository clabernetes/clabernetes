package node_test

import (
	"slices"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetescontrollersnode "github.com/clabernetes/clabernetes/controllers/node"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
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
		clabernetesconstants.LabelTopologyKind:  "containerlab",
		"owner":                                 "roman",
	}
	node.Spec.Kind = "nokia_srlinux"
	node.Spec.Image = "ghcr.io/nokia/srlinux:latest"
	node.Spec.FilesFromConfigMap = []clabernetesapisv1alpha1.FileFromConfigMap{{
		FilePath:      "srl1/startup.cfg",
		ConfigMapName: "shared-config",
		ConfigMapPath: "startup.cfg",
	}}
	simA := &clabernetesapisv1alpha1.Node{}
	simA.Name = "sim-a"
	simA.Spec.FilesFromConfigMap = []clabernetesapisv1alpha1.FileFromConfigMap{{
		FilePath:      "sim-a/startup.cfg",
		ConfigMapName: "shared-config",
		ConfigMapPath: "startup.cfg",
	}}

	profile := testResolvedProfile(t, func(profile *clabernetescontrollersnode.ResolvedProfile) {
		profile.LauncherImage = "launcher:latest"
		profile.LauncherImagePullPolicy = "IfNotPresent"
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
			Node:         node,
			Profile:      profile,
			GroupMembers: []string{"srl1", "sim-a", "sim-b"},
			NodesByName: map[string]*clabernetesapisv1alpha1.Node{
				"srl1":  node,
				"sim-a": simA,
			},
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

	// a lab author's own labels reach the deployment *and* its pods, which is the point of
	// carrying containerlab node labels over; c9s' own namespace does not tag along, so
	// labs without such labels do not get a gratuitous pod roll on upgrade
	if deployment.Labels["owner"] != "roman" ||
		deployment.Spec.Template.ObjectMeta.Labels["owner"] != "roman" {
		t.Fatalf(
			"expected node labels on the deployment and pod template, got %v / %v",
			deployment.Labels,
			deployment.Spec.Template.ObjectMeta.Labels,
		)
	}

	if _, ok := deployment.Labels[clabernetesconstants.LabelTopologyKind]; ok {
		t.Fatalf(
			"expected c9s-owned labels not to be propagated, got %v",
			deployment.Labels,
		)
	}

	// the selector is immutable once created, so it must stay exactly the fixed set
	if _, ok := deployment.Spec.Selector.MatchLabels["owner"]; ok {
		t.Fatalf("node labels must not leak into the selector, got %v", deployment.Spec.Selector)
	}

	podAnnotations := deployment.Spec.Template.ObjectMeta.Annotations

	if podAnnotations[clabernetesconstants.AnnotationLinkAttachmentsDigest] != "attachments-digest" {
		t.Fatal("expected link attachments digest pod annotation")
	}

	if podAnnotations[clabernetesconstants.AnnotationNodeConfigDigest] != "config-digest" {
		t.Fatal("expected node config digest pod annotation")
	}

	assertRenderedContainer(t, &deployment.Spec.Template.Spec.Containers[0])

	payloadVolumeNames := map[string]bool{}

	for _, volume := range deployment.Spec.Template.Spec.Volumes {
		if volume.ConfigMap == nil {
			continue
		}

		if payloadVolumeNames[volume.Name] {
			t.Fatalf("expected unique grouped payload volume names, duplicate %q", volume.Name)
		}

		payloadVolumeNames[volume.Name] = true
	}

	if len(payloadVolumeNames) != 2 {
		t.Fatalf("expected both grouped Node payloads, got volumes %+v", payloadVolumeNames)
	}
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

	for _, env := range container.Env {
		if env.Name == "LAUNCHER_CONNECTIVITY_KIND" {
			t.Fatal("connectivity is Link-owned and must not be injected as launcher-wide env")
		}
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

func TestRenderDeploymentGenericStatusProbes(t *testing.T) {
	node := &clabernetesapisv1alpha1.Node{}
	node.Name = "generic-node"
	node.Namespace = "clabernetes"
	node.Spec.Image = "example.invalid/arbitrary-kind:latest"

	reconciler := clabernetescontrollersnode.NewDeploymentReconciler(
		&claberneteslogging.FakeInstance{},
		"clabernetes",
		"clabernetes",
		clabernetesconstants.KubernetesCRIContainerd,
		clabernetesconfig.GetFakeManager,
	)

	deployment := reconciler.Render(
		&clabernetescontrollersnode.RenderInput{
			Node: node,
			Profile: testResolvedProfile(
				t,
				func(profile *clabernetescontrollersnode.ResolvedProfile) {
					profile.StatusProbes.Enabled = true
				},
			),
			GroupMembers: []string{"generic-node"},
		},
	)

	container := deployment.Spec.Template.Spec.Containers[0]
	if container.StartupProbe == nil || container.ReadinessProbe == nil {
		t.Fatal("expected generic startup and readiness probes without TCP or SSH configuration")
	}

	wantProbeCommand := []string{"test", "-s", clabernetesconstants.NodeStatusFile}

	if !slices.Equal(container.StartupProbe.Exec.Command, wantProbeCommand) ||
		!slices.Equal(container.ReadinessProbe.Exec.Command, wantProbeCommand) {
		t.Fatalf(
			"expected quiet status-file probes %v, got startup=%v readiness=%v",
			wantProbeCommand,
			container.StartupProbe.Exec.Command,
			container.ReadinessProbe.Exec.Command,
		)
	}

	if container.StartupProbe.InitialDelaySeconds != 10 ||
		container.StartupProbe.PeriodSeconds != 10 ||
		container.StartupProbe.FailureThreshold != 90 {
		t.Fatalf("unexpected balanced startup probe timing: %+v", container.StartupProbe)
	}

	if container.ReadinessProbe.PeriodSeconds != 10 ||
		container.ReadinessProbe.FailureThreshold != 3 {
		t.Fatalf("unexpected balanced readiness probe timing: %+v", container.ReadinessProbe)
	}

	enabled := findEnv(container.Env, clabernetesconstants.LauncherStatusProbesEnabled)
	if enabled == nil || enabled.Value != clabernetesconstants.True {
		t.Fatalf("expected generic status probe enablement env, got %+v", enabled)
	}

	if findEnv(container.Env, clabernetesconstants.LauncherTCPProbePort) != nil ||
		findEnv(container.Env, clabernetesconstants.LauncherSSHProbeUsername) != nil {
		t.Fatal("generic readiness must not infer an application-specific TCP or SSH probe")
	}
}

func TestRenderDeploymentSRLinuxRuntimeReadinessWhenStatusProbesDisabled(t *testing.T) {
	node := &clabernetesapisv1alpha1.Node{}
	node.Name = "srl1"
	node.Namespace = "clabernetes"
	node.Spec.Kind = "nokia_srlinux"
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
	if container.StartupProbe == nil || container.ReadinessProbe == nil {
		t.Fatal("expected runtime startup and readiness probes for SR Linux")
	}

	enabled := findEnv(container.Env, clabernetesconstants.LauncherStatusProbesEnabled)
	if enabled == nil || enabled.Value != clabernetesconstants.True {
		t.Fatalf("expected runtime status probe enablement env, got %+v", enabled)
	}
}

func TestRenderDeploymentRoundsCustomStartupAllowanceUp(t *testing.T) {
	node := &clabernetesapisv1alpha1.Node{}
	node.Name = "generic-node"
	node.Namespace = "clabernetes"
	node.Spec.Image = "example.invalid/arbitrary-kind:latest"

	reconciler := clabernetescontrollersnode.NewDeploymentReconciler(
		&claberneteslogging.FakeInstance{},
		"clabernetes",
		"clabernetes",
		clabernetesconstants.KubernetesCRIContainerd,
		clabernetesconfig.GetFakeManager,
	)
	deployment := reconciler.Render(
		&clabernetescontrollersnode.RenderInput{
			Node: node,
			Profile: testResolvedProfile(
				t,
				func(profile *clabernetescontrollersnode.ResolvedProfile) {
					profile.StatusProbes.Enabled = true
					profile.StatusProbes.ProbeConfiguration.StartupSeconds = 21
				},
			),
			GroupMembers: []string{"generic-node"},
		},
	)

	if got := deployment.Spec.Template.Spec.Containers[0].StartupProbe.FailureThreshold; got != 3 {
		t.Fatalf("custom 21-second startup allowance threshold = %d, want 3", got)
	}
}
