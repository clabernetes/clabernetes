//nolint:gocyclo,testpackage // dense fixture-driven tests exercise one boundary end to end.
package node

import (
	"slices"
	"strings"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
)

func TestRenderPlannerPodIsLockedDownAndContainsNoKindKnowledge(t *testing.T) {
	t.Parallel()

	node := &clabernetesapisv1alpha1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "node-a", Namespace: "lab-a", UID: apimachinerytypes.UID("node-uid"),
	}}

	pod, err := RenderPlannerPod(PlannerPodInput{
		Node: node, Name: "node-a-plan-abc", Image: "example/c9s@sha256:abc",
		InputConfigMapName: "node-a-plan-input-abc", InputDigest: "sha256:abc",
		PlannerRevision: "planner-v1", MaxInputBytes: 1 << 20, DeadlineSeconds: 60,
	})
	if err != nil {
		t.Fatal(err)
	}

	if pod.Spec.AutomountServiceAccountToken == nil || *pod.Spec.AutomountServiceAccountToken ||
		pod.Spec.HostNetwork || pod.Spec.HostPID || pod.Spec.HostIPC {
		t.Fatalf("planner Pod has host or API access: %#v", pod.Spec)
	}

	if got, want := len(pod.Spec.Containers), 1; got != want {
		t.Fatalf("planner containers = %d, want %d", got, want)
	}

	container := pod.Spec.Containers[0]

	security := container.SecurityContext
	if security == nil || security.Privileged != nil && *security.Privileged ||
		security.AllowPrivilegeEscalation == nil || *security.AllowPrivilegeEscalation ||
		security.ReadOnlyRootFilesystem == nil || !*security.ReadOnlyRootFilesystem ||
		security.RunAsNonRoot == nil || *security.RunAsNonRoot ||
		security.RunAsUser == nil || *security.RunAsUser != 0 ||
		security.Capabilities == nil ||
		!slices.Equal(
			security.Capabilities.Add,
			[]k8scorev1.Capability{"CHOWN", "FOWNER"},
		) ||
		len(security.Capabilities.Drop) != 1 || security.Capabilities.Drop[0] != "ALL" ||
		security.SeccompProfile == nil ||
		security.SeccompProfile.Type != k8scorev1.SeccompProfileTypeRuntimeDefault {
		t.Fatalf("planner container is not locked down: %#v", security)
	}

	for _, volume := range pod.Spec.Volumes {
		if volume.HostPath != nil || volume.PersistentVolumeClaim != nil || volume.Secret != nil {
			t.Fatalf("planner Pod mounts non-metadata state: %#v", volume)
		}
	}

	if got, want := container.Command, []string{"/clabernetes/manager"}; len(got) != len(want) ||
		got[0] != want[0] {
		t.Fatalf("planner command = %#v, want %#v", got, want)
	}
}

func TestRenderPlannerPodRejectsIncompleteBoundary(t *testing.T) {
	t.Parallel()

	if _, err := RenderPlannerPod(PlannerPodInput{}); err == nil {
		t.Fatal("RenderPlannerPod() accepted missing identity and isolation inputs")
	}
}

func TestRenderPlannerPodProjectsCertificateSecretReadOnly(t *testing.T) {
	t.Parallel()

	node := &clabernetesapisv1alpha1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "node-a", Namespace: "lab-a", UID: apimachinerytypes.UID("node-uid"),
	}}

	pod, err := RenderPlannerPod(
		PlannerPodInput{ //nolint:gosec // test fixture identifier, not a credential.
			Node: node, Name: "node-a-plan-certificate", Image: "example/c9s@sha256:abc",
			InputConfigMapName: "node-a-plan-input-certificate", InputDigest: "sha256:certificate",
			PlannerRevision: "planner-v1", MaxInputBytes: 1 << 20, DeadlineSeconds: 60,
			CertificateSecretName: "node-a-certificates",
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	worker := pod.Spec.Containers[0]
	if !slices.Contains(worker.Args, "--certificates") ||
		!slices.Contains(worker.Args, plannerCertificateRoot) {
		t.Fatalf("planner certificate arguments = %#v", worker.Args)
	}

	found := false

	for _, volume := range pod.Spec.Volumes {
		if volume.Name == plannerCertificateName && volume.Secret != nil &&
			volume.Secret.SecretName == "node-a-certificates" {
			found = true
		}
	}

	if !found {
		t.Fatalf("planner certificate volumes = %#v", pod.Spec.Volumes)
	}

	found = false

	for _, mount := range worker.VolumeMounts {
		if mount.Name == plannerCertificateName && mount.MountPath == plannerCertificateRoot &&
			mount.ReadOnly {
			found = true
		}
	}

	if !found {
		t.Fatalf("planner certificate mounts = %#v", worker.VolumeMounts)
	}
}

func TestRenderPlannerPodProjectsOnlyDeclaredPayloadObjectsReadOnly(t *testing.T) {
	t.Parallel()

	node := &clabernetesapisv1alpha1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "node-a", Namespace: "lab-a", UID: apimachinerytypes.UID("node-uid"),
	}}

	pod, err := RenderPlannerPod(PlannerPodInput{
		Node: node, Name: "node-a-plan-payload", Image: "example/c9s@sha256:abc",
		InputConfigMapName: "node-a-plan-input-payload", InputDigest: "sha256:payload",
		PlannerRevision: "planner-v1", MaxInputBytes: 1 << 20, DeadlineSeconds: 60,
		Payloads: []clabernetesinternaldeviceplan.PayloadInput{
			{
				ID: "config-input", Kind: clabernetesinternaldeviceplan.PayloadConfigMap,
				Reference: "lab-a/device-config:startup.cfg", Mode: 0o444,
			},
			{
				ID: "secret-input", Kind: clabernetesinternaldeviceplan.PayloadSecret,
				Reference: "lab-a/device-license:license.key", Mode: 0o400,
			},
			{
				ID: "url-input", Kind: clabernetesinternaldeviceplan.PayloadURL,
				Reference: "https://example.invalid/config", Mode: 0o444,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	container := pod.Spec.Containers[0]
	if !slices.Contains(container.Args, "--payloads") ||
		!slices.Contains(container.Args, plannerPayloadRootPath) {
		t.Fatalf("planner payload arguments = %#v", container.Args)
	}

	projected := map[string]bool{}

	for _, volume := range pod.Spec.Volumes {
		switch {
		case volume.ConfigMap != nil && volume.ConfigMap.Name == "device-config":
			projected["config"] = true
		case volume.Secret != nil && volume.Secret.SecretName == "device-license":
			projected["secret"] = true
		}

		if volume.ConfigMap == nil && volume.Secret == nil {
			continue
		}

		if !strings.HasPrefix(volume.Name, "planner-payload-") {
			continue
		}

		items := []k8scorev1.KeyToPath(nil)
		if volume.ConfigMap != nil {
			items = volume.ConfigMap.Items
		} else if volume.Secret != nil {
			items = volume.Secret.Items
		}

		for _, item := range items {
			if item.Mode == nil || *item.Mode != 0o444 {
				t.Fatalf("planner source projection mode = %#v", item.Mode)
			}
		}

		if !slices.ContainsFunc(container.VolumeMounts, func(mount k8scorev1.VolumeMount) bool {
			return mount.Name == volume.Name && mount.ReadOnly
		}) {
			t.Fatalf("typed payload volume is not mounted read-only: %#v", volume)
		}
	}

	if !projected["config"] || !projected["secret"] || len(projected) != 2 {
		t.Fatalf("planner typed payload projections = %#v", projected)
	}

	if len(pod.Spec.InitContainers) != 1 ||
		pod.Spec.InitContainers[0].Name != plannerURLFetcherName ||
		pod.Spec.InitContainers[0].SecurityContext == nil ||
		pod.Spec.InitContainers[0].SecurityContext.Capabilities == nil ||
		!slices.Contains(
			pod.Spec.InitContainers[0].SecurityContext.Capabilities.Add,
			k8scorev1.Capability("NET_ADMIN"),
		) {
		t.Fatalf("URL fetch-and-seal init container = %#v", pod.Spec.InitContainers)
	}
}

func TestRenderPlannerNetworkPolicyDeniesAllTraffic(t *testing.T) {
	t.Parallel()

	node := &clabernetesapisv1alpha1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "node-a", Namespace: "lab-a", UID: apimachinerytypes.UID("node-uid"),
	}}

	policy, err := RenderPlannerNetworkPolicy(PlannerPodInput{
		Node: node, Name: "node-a-plan-abc", Image: "example/c9s@sha256:abc",
		InputConfigMapName: "node-a-plan-input-abc", InputDigest: "sha256:abc",
		PlannerRevision: "planner-v1", MaxInputBytes: 1 << 20, DeadlineSeconds: 60,
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(policy.Spec.Ingress) != 0 || len(policy.Spec.Egress) != 0 ||
		len(policy.Spec.PolicyTypes) != 2 ||
		policy.Spec.PodSelector.MatchLabels[plannerLabel] != plannerDigestLabelValue("sha256:abc") {
		t.Fatalf("planner NetworkPolicy is not default-deny: %#v", policy.Spec)
	}
}

func TestRenderPlannerResourcesUseLabelSafeDigestIdentity(t *testing.T) {
	t.Parallel()

	node := &clabernetesapisv1alpha1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "node-a", Namespace: "lab-a", UID: apimachinerytypes.UID("node-uid"),
	}}
	digest := "sha256:" + strings.Repeat("a", 64)
	input := PlannerPodInput{
		Node: node, Name: "node-a-plan-abc", Image: "example/c9s@sha256:abc",
		InputConfigMapName: "node-a-plan-input-abc", InputDigest: digest,
		PlannerRevision: "planner-v1", MaxInputBytes: 1 << 20, DeadlineSeconds: 60,
	}

	pod, err := RenderPlannerPod(input)
	if err != nil {
		t.Fatal(err)
	}

	policy, err := RenderPlannerNetworkPolicy(input)
	if err != nil {
		t.Fatal(err)
	}

	labelValue := pod.GetLabels()[plannerLabel]
	if len(labelValue) != kubernetesNameLimit || strings.Contains(labelValue, ":") ||
		policy.Spec.PodSelector.MatchLabels[plannerLabel] != labelValue ||
		pod.GetLabels()[clabernetesconstants.LabelApp] != clabernetesconstants.Clabernetes ||
		policy.GetLabels()[clabernetesconstants.LabelApp] != clabernetesconstants.Clabernetes ||
		pod.GetAnnotations()[plannerInputDigest] != digest {
		t.Fatalf(
			"planner digest metadata = labels %#v annotations %#v selector %#v",
			pod.GetLabels(),
			pod.GetAnnotations(),
			policy.Spec.PodSelector.MatchLabels,
		)
	}
}

func TestRenderPlannerNetworkPolicyAllowsOnlyFetchPhaseEgressForURLInput(t *testing.T) {
	t.Parallel()

	node := &clabernetesapisv1alpha1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "node-a", Namespace: "lab-a", UID: apimachinerytypes.UID("node-uid"),
	}}

	policy, err := RenderPlannerNetworkPolicy(PlannerPodInput{
		Node: node, Name: "node-a-plan-url", Image: "example/c9s@sha256:abc",
		InputConfigMapName: "node-a-plan-input-url", InputDigest: "sha256:url",
		PlannerRevision: "planner-v1", MaxInputBytes: 1 << 20, DeadlineSeconds: 60,
		Payloads: []clabernetesinternaldeviceplan.PayloadInput{
			{Kind: clabernetesinternaldeviceplan.PayloadURL},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(policy.Spec.Ingress) != 0 || len(policy.Spec.Egress) != 2 {
		t.Fatalf("URL fetch policy = %#v", policy.Spec)
	}

	publicBlocks := 0

	for _, rule := range policy.Spec.Egress {
		for _, peer := range rule.To {
			if peer.IPBlock != nil && len(peer.IPBlock.Except) != 0 {
				publicBlocks++
			}
		}
	}

	if publicBlocks != 2 {
		t.Fatalf("URL fetch policy lacks public IPv4/IPv6 bounds: %#v", policy.Spec.Egress)
	}
}
