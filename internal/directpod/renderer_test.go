//nolint:gocognit,gocyclo // dense fixture-driven tests exercise one boundary end to end.
package directpod_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	clabernetesinternaldirectpod "github.com/clabernetes/clabernetes/internal/directpod"
	clabernetesinternaldirectruntime "github.com/clabernetes/clabernetes/internal/directruntime"
	k8scorev1 "k8s.io/api/core/v1"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestValidatePlanRejectsUnportableHostDeviceAndSecurityInputs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		mutate    func(*clabernetesinternaldeviceplan.Plan)
		wantField string
	}{
		{
			name: "unprivileged host device",
			mutate: func(plan *clabernetesinternaldeviceplan.Plan) {
				plan.Containers[0].Security.Privileged = false
			},
			wantField: "containers[1].security.devices[0]",
		},
		{
			name: "host path outside dev",
			mutate: func(plan *clabernetesinternaldeviceplan.Plan) {
				plan.Containers[0].Security.Devices[0].HostPath = "/sys/devices/kvm"
			},
			wantField: "containers[1].security.devices[0]",
		},
		{
			name: "noncanonical target path",
			mutate: func(plan *clabernetesinternaldeviceplan.Plan) {
				plan.Containers[0].Security.Devices[0].ContainerPath = "/dev/../etc/kvm"
			},
			wantField: "containers[1].security.devices[0]",
		},
		{
			name: "ambiguous permissions",
			mutate: func(plan *clabernetesinternaldeviceplan.Plan) {
				plan.Containers[0].Security.Devices[0].Permissions = "rr"
			},
			wantField: "containers[1].security.devices[0]",
		},
		{
			name: "escaping seccomp profile",
			mutate: func(plan *clabernetesinternaldeviceplan.Plan) {
				plan.Containers[0].Security.SeccompProfile = "localhost/../escape.json"
			},
			wantField: "containers[1].security.seccompProfile",
		},
		{
			name: "unsupported AppArmor profile",
			mutate: func(plan *clabernetesinternaldeviceplan.Plan) {
				plan.Containers[0].Security.AppArmorProfile = "docker/default"
			},
			wantField: "containers[1].security.appArmorProfile",
		},
		{
			name: "device volume outside dev",
			mutate: func(plan *clabernetesinternaldeviceplan.Plan) {
				plan.Volumes = append(plan.Volumes, clabernetesinternaldeviceplan.VolumePlan{
					ID: "node-a/unsafe-device", NodeID: "node-a",
					Kind: clabernetesinternaldeviceplan.VolumeDevice, Reference: "/dev/../etc/shadow",
				})
			},
			wantField: "volumes[2].reference",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			plan := renderablePlan()
			test.mutate(&plan)
			err := clabernetesinternaldirectpod.ValidatePlan(plan)

			var planningErr *clabernetesinternaldeviceplan.Error
			if !errors.As(err, &planningErr) ||
				planningErr.Code != clabernetesinternaldeviceplan.ErrorUnsupported ||
				planningErr.NodeID != "node-a" || planningErr.Field != test.wantField ||
				planningErr.Behavior != "kubernetes-workload-preflight" {
				t.Fatalf("ValidatePlan() error = %#v", err)
			}
		})
	}
}

func TestRenderAppliesProfilePullDefaultWithoutOverwritingExplicitNodePolicy(t *testing.T) {
	t.Parallel()

	plan := renderablePlan()
	plan.Containers[0].ImagePullPolicy = string(k8scorev1.PullNever)
	plan.Containers[0].ImagePullPolicyExplicit = true

	deployment, err := clabernetesinternaldirectpod.Render(
		plan,
		clabernetesinternaldirectpod.Options{
			Name: "device-a", Namespace: "lab-a", PlanConfigMapName: "device-a-plan-abc",
			InputConfigMapName:                "device-a-plan-input-abc",
			ConnectivityRevisionConfigMapName: "device-a-connectivity",
			PreparationImage:                  "example/c9s@sha256:1111",
			ConnectivityImage:                 "example/c9s@sha256:1111",
			ApplicationImagePullPolicy:        string(k8scorev1.PullAlways),
			EnableContainerStopSignals:        true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	root := containerByImage(
		t,
		deployment.Spec.Template.Spec.Containers,
		"example/device@sha256:"+strings.Repeat("a", 64),
	)

	component := containerByImage(
		t,
		deployment.Spec.Template.Spec.Containers,
		"example/component@sha256:"+strings.Repeat("b", 64),
	)
	if root.ImagePullPolicy != k8scorev1.PullNever ||
		component.ImagePullPolicy != k8scorev1.PullAlways {
		t.Fatalf(
			"application image pull policies = root %q component %q",
			root.ImagePullPolicy,
			component.ImagePullPolicy,
		)
	}
}

func TestRenderGivesEveryApplicationContainerThePodAddress(t *testing.T) {
	t.Parallel()

	deployment, err := clabernetesinternaldirectpod.Render(
		renderablePlan(),
		clabernetesinternaldirectpod.Options{
			Name: "device-a", Namespace: "lab-a", PlanConfigMapName: "device-a-plan-abc",
			InputConfigMapName:                "device-a-plan-input-abc",
			ConnectivityRevisionConfigMapName: "device-a-connectivity",
			PreparationImage:                  "example/c9s@sha256:1111",
			ConnectivityImage:                 "example/c9s@sha256:1111",
			EnableContainerStopSignals:        true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	for _, image := range []string{
		"example/device@sha256:" + strings.Repeat("a", 64),
		"example/component@sha256:" + strings.Repeat("b", 64),
	} {
		container := containerByImage(t, deployment.Spec.Template.Spec.Containers, image)
		if !hasDownwardEnvironment(*container, "C9S_POD_ADDRESS", "status.podIP") {
			t.Fatalf("application container %q has no Pod address identity", container.Name)
		}
	}
}

func TestRenderCreatesDirectApplicationContainersFromGenericPlan(t *testing.T) {
	t.Parallel()

	plan := renderablePlan()

	deployment, err := clabernetesinternaldirectpod.Render(
		plan,
		clabernetesinternaldirectpod.Options{
			Name: "device-a", Namespace: "lab-a", PlanConfigMapName: "device-a-plan-abc",
			InputConfigMapName:                "device-a-plan-input-abc",
			ConnectivityRevisionConfigMapName: "device-a-connectivity",
			PreparationImage:                  "example/c9s@sha256:1111", ConnectivityImage: "example/c9s@sha256:1111",
			EnableContainerStopSignals: true,
			ImagePullSecrets:           []k8scorev1.LocalObjectReference{{Name: "device-registry"}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	pod := deployment.Spec.Template.Spec

	references, err := clabernetesinternaldirectpod.DeploymentPlanReferences(deployment)
	if err != nil {
		t.Fatal(err)
	}

	if references.PlanConfigMapName != "device-a-plan-abc" ||
		references.InputConfigMapName != "device-a-plan-input-abc" ||
		references.ConnectivityRevisionConfigMapName != "device-a-connectivity" ||
		references.PlanDigest == "" {
		t.Fatalf("cold plan references = %#v", references)
	}

	if deployment.Spec.Strategy.Type != "Recreate" || len(pod.Containers) != 2 ||
		len(pod.InitContainers) != 2 {
		t.Fatalf("direct Deployment shape = %#v", deployment.Spec)
	}

	if deployment.Spec.RevisionHistoryLimit == nil ||
		*deployment.Spec.RevisionHistoryLimit != 0 || pod.Hostname != "device-a" {
		t.Fatalf(
			"direct Recreate lifecycle policy = revision history %#v, hostname %q",
			deployment.Spec.RevisionHistoryLimit,
			pod.Hostname,
		)
	}

	if pod.AutomountServiceAccountToken == nil || *pod.AutomountServiceAccountToken {
		t.Fatal("direct device Pod unexpectedly mounts a service-account token")
	}

	if !slices.Equal(
		pod.ImagePullSecrets,
		[]k8scorev1.LocalObjectReference{{Name: "device-registry"}},
	) {
		t.Fatalf("direct kubelet image pull Secrets = %#v", pod.ImagePullSecrets)
	}

	if got, want := deployment.Spec.Template.Annotations[clabernetesinternaldirectpod.KubectlDefaultContainerAnnotation], clabernetesinternaldirectpod.ApplicationContainerName("node-a/root"); got != want {
		t.Fatalf("kubectl default application container = %q, want %q", got, want)
	}

	if pod.InitContainers[0].Name != "prepare-device-plan" ||
		pod.InitContainers[1].Name != "device-connectivity" ||
		pod.InitContainers[1].RestartPolicy == nil ||
		*pod.InitContainers[1].RestartPolicy != k8scorev1.ContainerRestartPolicyAlways {
		t.Fatalf("ordered native helpers = %#v", pod.InitContainers)
	}

	if slices.Contains(pod.InitContainers[0].Args, "--state") ||
		!slices.Contains(pod.InitContainers[1].Args, "--state") {
		t.Fatalf(
			"helper state arguments = prepare %#v connectivity %#v",
			pod.InitContainers[0].Args,
			pod.InitContainers[1].Args,
		)
	}

	connectivity := pod.InitContainers[1]
	if !slices.Contains(connectivity.Args, "--podUID") ||
		!slices.Contains(connectivity.Args, "$(C9S_POD_UID)") ||
		!slices.Contains(connectivity.Args, "--podNamespace") ||
		!slices.Contains(connectivity.Args, "$(C9S_POD_NAMESPACE)") ||
		!slices.Contains(connectivity.Args, "--podName") ||
		!slices.Contains(connectivity.Args, "$(C9S_POD_NAME)") ||
		!slices.Contains(connectivity.Args, "--podAddress") ||
		!slices.Contains(connectivity.Args, "$(C9S_POD_ADDRESS)") ||
		!slices.Contains(connectivity.Args, "--connectivityRevision") ||
		!hasReadOnlyMount(connectivity, "/var/run/clabernetes/connectivity-revision") ||
		!hasDownwardEnvironment(connectivity, "C9S_POD_NAMESPACE", "metadata.namespace") ||
		!hasDownwardEnvironment(connectivity, "C9S_POD_NAME", "metadata.name") ||
		!hasDownwardEnvironment(connectivity, "C9S_POD_UID", "metadata.uid") ||
		!hasDownwardEnvironment(connectivity, "C9S_POD_ADDRESS", "status.podIP") {
		t.Fatalf("connectivity Pod UID ownership input = %#v", connectivity)
	}

	if connectivity.StartupProbe == nil || connectivity.ReadinessProbe == nil ||
		connectivity.ReadinessProbe.Exec == nil ||
		!slices.Contains(connectivity.ReadinessProbe.Exec.Command, "--connectivityRevision") {
		t.Fatalf("connectivity revision readiness probes = %#v", connectivity)
	}

	// The daemonless Pod grants no daemon socket to any container: no hostPath volume exists
	// unless the plan requires the worker namespace handle for host Links.
	for _, volume := range pod.Volumes {
		if volume.HostPath != nil &&
			strings.Contains(volume.HostPath.Path, "host-endpoint") {
			t.Fatalf("direct Pod mounts a daemon socket volume: %#v", volume)
		}
	}

	for _, container := range append(
		append([]k8scorev1.Container{}, pod.Containers...),
		pod.InitContainers[0],
	) {
		if hasReadOnlyMount(container, "/var/run/clabernetes/connectivity-revision") {
			t.Fatalf("non-connectivity container %q received mutable revision", container.Name)
		}
	}

	if !hasWritableEmptyDirMount(pod, pod.InitContainers[0], "/tmp") {
		t.Fatalf(
			"preparation helper has no writable scratch mount: %#v",
			pod.InitContainers[0].VolumeMounts,
		)
	}

	preparationSecurity := pod.InitContainers[0].SecurityContext
	if preparationSecurity == nil || preparationSecurity.Privileged != nil &&
		*preparationSecurity.Privileged || preparationSecurity.RunAsUser == nil ||
		*preparationSecurity.RunAsUser != 0 || preparationSecurity.Capabilities == nil ||
		!slices.Equal(
			preparationSecurity.Capabilities.Add,
			[]k8scorev1.Capability{"CHOWN", "FOWNER"},
		) || !slices.Equal(
		preparationSecurity.Capabilities.Drop,
		[]k8scorev1.Capability{"ALL"},
	) {
		t.Fatalf("preparation filesystem capability boundary = %#v", preparationSecurity)
	}

	if !hasReadOnlyMount(pod.InitContainers[0], "/dev/kvm") {
		t.Fatalf(
			"target-worker condition helper has no read-only host-device observation: %#v",
			pod.InitContainers[0].VolumeMounts,
		)
	}

	root := containerByImage(
		t,
		pod.Containers,
		"example/device@sha256:"+strings.Repeat("a", 64),
	)

	component := containerByImage(
		t,
		pod.Containers,
		"example/component@sha256:"+strings.Repeat("b", 64),
	)
	if root == nil || component == nil {
		t.Fatalf("device application containers = %#v", pod.Containers)
	}
	// Every application container starts through the launch boundary, which resolves the OCI
	// entrypoint/command from the plan and restores conventional process limits before exec.
	if len(root.Command) == 0 ||
		root.Command[0] != "/var/lib/clabernetes/lifecycle-bin/manager" ||
		!slices.Contains(root.Command, "launch") || root.Args != nil {
		t.Fatalf("launch boundary mapping = command %#v args %#v", root.Command, root.Args)
	}

	if root.SecurityContext == nil || root.SecurityContext.Privileged == nil ||
		!*root.SecurityContext.Privileged || root.SecurityContext.RunAsUser == nil ||
		*root.SecurityContext.RunAsUser != 1000 || root.Lifecycle == nil ||
		root.Lifecycle.StopSignal == nil || *root.Lifecycle.StopSignal != "SIGTERM" {
		t.Fatalf("security and lifecycle mapping = %#v", root)
	}

	if root.ReadinessProbe == nil || root.ReadinessProbe.Exec == nil ||
		len(root.ReadinessProbe.Exec.Command) == 0 {
		t.Fatalf("healthcheck mapping = %#v", root.ReadinessProbe)
	}

	if pod.DNSPolicy != k8scorev1.DNSNone || pod.DNSConfig == nil ||
		len(pod.DNSConfig.Nameservers) != 1 || pod.DNSConfig.Nameservers[0] != "192.0.2.53" {
		t.Fatalf("Pod DNS mapping = policy %q config %#v", pod.DNSPolicy, pod.DNSConfig)
	}

	if pod.SecurityContext != nil && len(pod.SecurityContext.Sysctls) != 0 {
		t.Fatalf("package sysctls must not require kubelet admission: %#v", pod.SecurityContext)
	}

	if len(root.VolumeMounts) < 2 || len(pod.Volumes) < 4 {
		t.Fatalf("storage/device mapping = mounts %#v volumes %#v", root.VolumeMounts, pod.Volumes)
	}

	raw, err := json.Marshal(deployment)
	if err != nil {
		t.Fatal(err)
	}

	for _, forbidden := range []string{
		"dockerd", "docker.sock", "containerlab", "/var/lib/docker", "/clabernetes/.nodestatus",
	} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("direct workload contains nested-runtime artifact %q: %s", forbidden, raw)
		}
	}
}

func TestRenderOwnsLinkLifecycleRolloutAnnotations(t *testing.T) {
	t.Parallel()

	digest := "sha256:" + strings.Repeat("a", 64)
	options := clabernetesinternaldirectpod.Options{
		Name: "device-a", Namespace: "lab-a", PlanConfigMapName: "device-a-plan-abc",
		InputConfigMapName:                "device-a-plan-input-abc",
		ConnectivityRevisionConfigMapName: "device-a-connectivity",
		PreparationImage:                  "example/c9s:1",
		ConnectivityImage:                 "example/c9s:1",
		EnableContainerStopSignals:        true,
		Annotations: map[string]string{
			clabernetesinternaldirectpod.LinkLifecycleModeAnnotation:       "spoofed",
			clabernetesinternaldirectpod.LinkLifecyclePlanDigestAnnotation: digest,
		},
	}

	withoutLifecycle, err := clabernetesinternaldirectpod.Render(renderablePlan(), options)
	if err != nil {
		t.Fatal(err)
	}

	if withoutLifecycle.Spec.Template.Annotations[clabernetesinternaldirectpod.LinkLifecycleModeAnnotation] != "" ||
		withoutLifecycle.Spec.Template.Annotations[clabernetesinternaldirectpod.LinkLifecyclePlanDigestAnnotation] != "" {
		t.Fatalf(
			"user-supplied lifecycle annotations were retained: %#v",
			withoutLifecycle.Spec.Template.Annotations,
		)
	}

	options.LinkLifecycleMode = clabernetesinternaldeviceplan.LinkApplyRecreate
	options.LinkLifecyclePlanDigest = digest

	withLifecycle, err := clabernetesinternaldirectpod.Render(renderablePlan(), options)
	if err != nil {
		t.Fatal(err)
	}

	if withLifecycle.Spec.Template.Annotations[clabernetesinternaldirectpod.LinkLifecycleModeAnnotation] !=
		string(
			clabernetesinternaldeviceplan.LinkApplyRecreate,
		) ||
		withLifecycle.Spec.Template.Annotations[clabernetesinternaldirectpod.LinkLifecyclePlanDigestAnnotation] != digest {
		t.Fatalf("owned lifecycle annotations = %#v", withLifecycle.Spec.Template.Annotations)
	}

	for _, mode := range []clabernetesinternaldeviceplan.LinkApplyMode{
		clabernetesinternaldeviceplan.LinkApplyLive,
		clabernetesinternaldeviceplan.LinkApplyRestart,
	} {
		options.LinkLifecycleMode = mode
		if _, err = clabernetesinternaldirectpod.Render(renderablePlan(), options); err == nil {
			t.Fatalf("renderer accepted a %s lifecycle action that would roll the Pod", mode)
		}
	}
}

func TestApplicationRestartCommandUsesPlanScopedShellIndependentBoundary(t *testing.T) {
	t.Parallel()

	container := renderablePlan().Containers[0]
	container.StopSignal = "SIGUSR1"
	digest := "sha256:" + strings.Repeat("b", 64)

	command, err := clabernetesinternaldirectpod.ApplicationRestartCommand(digest, container)
	if err != nil {
		t.Fatal(err)
	}

	for _, value := range []string{
		"/var/lib/clabernetes/lifecycle-bin/manager",
		"restart",
		"--request",
		digest,
		"--signal",
		"SIGUSR1",
	} {
		if !slices.Contains(command, value) {
			t.Fatalf("application restart command lacks %q: %#v", value, command)
		}
	}

	if slices.Contains(command, "sh") || slices.Contains(command, "kill") {
		t.Fatalf("application restart command depends on device-image tools: %#v", command)
	}
}

func TestRenderPreservesGenericWorkloadPolicyMetadataAndOwnership(t *testing.T) {
	t.Parallel()

	owner := metav1.OwnerReference{
		APIVersion: "c9s.run/v1alpha1", Kind: "Node", Name: "device-a", UID: "node-a-uid",
	}
	tolerations := []k8scorev1.Toleration{{
		Key: "network-device", Operator: k8scorev1.TolerationOpExists,
	}}
	affinity := &k8scorev1.Affinity{NodeAffinity: &k8scorev1.NodeAffinity{
		RequiredDuringSchedulingIgnoredDuringExecution: &k8scorev1.NodeSelector{},
	}}

	deployment, err := clabernetesinternaldirectpod.Render(
		renderablePlan(),
		clabernetesinternaldirectpod.Options{
			Name: "device-a", Namespace: "lab-a", PlanConfigMapName: "device-a-plan-abc",
			InputConfigMapName: "device-a-plan-input-abc",
			PreparationImage:   "example/c9s:1", ConnectivityImage: "example/c9s:1",
			EnableContainerStopSignals: true,
			Labels:                     map[string]string{"team": "routing"},
			Annotations:                map[string]string{"example.io/policy": "strict"},
			OwnerReferences:            []metav1.OwnerReference{owner},
			NodeSelector:               map[string]string{"device-pool": "vm"},
			Tolerations:                tolerations,
			Affinity:                   affinity,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	pod := deployment.Spec.Template
	if deployment.Labels["team"] != "routing" || pod.Labels["team"] != "routing" ||
		deployment.Annotations["example.io/policy"] != "strict" ||
		pod.Annotations["example.io/policy"] != "strict" {
		t.Fatalf(
			"direct workload metadata = deployment %#v, pod %#v",
			deployment.ObjectMeta,
			pod.ObjectMeta,
		)
	}

	if !reflect.DeepEqual(deployment.OwnerReferences, []metav1.OwnerReference{owner}) {
		t.Fatalf("direct workload owner references = %#v", deployment.OwnerReferences)
	}

	if len(deployment.Spec.Selector.MatchLabels) != 1 ||
		deployment.Spec.Selector.MatchLabels["team"] != "" {
		t.Fatalf("user metadata leaked into immutable Pod selector: %#v", deployment.Spec.Selector)
	}

	if !reflect.DeepEqual(pod.Spec.NodeSelector, map[string]string{"device-pool": "vm"}) ||
		!reflect.DeepEqual(pod.Spec.Tolerations, tolerations) ||
		!reflect.DeepEqual(pod.Spec.Affinity, affinity) {
		t.Fatalf("direct scheduling policy = %#v", pod.Spec)
	}
}

func TestRenderScopesHostNamespaceToImportedEndpointHelper(t *testing.T) {
	t.Parallel()

	plan := renderablePlan()
	plan.Actions = append(plan.Actions, clabernetesinternaldeviceplan.Action{
		ID:    "imported-deploy-endpoints/node-a",
		Phase: clabernetesinternaldeviceplan.PhaseInterfaceFixup,
		Target: clabernetesinternaldeviceplan.ActionTarget{
			NodeID: "node-a", ContainerID: "node-a/root", NamespaceOwnerID: "node-a/root",
		},
		Kind:                    clabernetesinternaldeviceplan.ActionImportedDeployEndpoints,
		ImportedDeployEndpoints: &clabernetesinternaldeviceplan.ImportedDeployEndpointsAction{},
	})

	deployment, err := clabernetesinternaldirectpod.Render(
		plan,
		clabernetesinternaldirectpod.Options{
			Name: "device-a", Namespace: "lab-a", PlanConfigMapName: "device-a-plan-abc",
			InputConfigMapName: "device-a-plan-input-abc",
			PreparationImage:   "example/c9s:1", ConnectivityImage: "example/c9s:1",
			EnableContainerStopSignals: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	pod := deployment.Spec.Template.Spec
	hostVolumeName := ""

	for _, volume := range pod.Volumes {
		if volume.HostPath != nil && volume.HostPath.Path == "/proc/1/ns" &&
			volume.HostPath.Type != nil && *volume.HostPath.Type == k8scorev1.HostPathDirectory {
			hostVolumeName = volume.Name
		}
	}

	if hostVolumeName == "" {
		t.Fatalf("host network namespace volume = %#v", pod.Volumes)
	}

	connectivity := pod.InitContainers[1]
	if !slices.Contains(connectivity.Args, "--hostNetworkNamespace") ||
		!slices.Contains(connectivity.Args, "--artifacts") ||
		!slices.Contains(connectivity.Args, "--revision") ||
		!hasReadOnlyMount(
			connectivity,
			"/var/run/clabernetes/host-network-namespaces",
		) {
		t.Fatalf("imported endpoint connectivity helper = %#v", connectivity)
	}

	if connectivity.SecurityContext == nil || connectivity.SecurityContext.Privileged == nil ||
		!*connectivity.SecurityContext.Privileged || connectivity.SecurityContext.RunAsUser == nil ||
		*connectivity.SecurityContext.RunAsUser != 0 {
		t.Fatalf("endpoint helper host-namespace security = %#v", connectivity.SecurityContext)
	}

	for _, container := range append(
		[]k8scorev1.Container{pod.InitContainers[0]},
		pod.Containers...,
	) {
		for _, mount := range container.VolumeMounts {
			if mount.Name == hostVolumeName {
				t.Fatalf("host namespace leaked into container %q", container.Name)
			}
		}
	}
}

func TestRenderProjectsOpaqueEntropyIntoImportedHookBoundaries(t *testing.T) {
	t.Parallel()

	plan := renderablePlan()
	plan.Actions = append(
		plan.Actions,
		clabernetesinternaldeviceplan.Action{
			ID: "imported-endpoints/node-a", Phase: clabernetesinternaldeviceplan.PhaseInterfaceFixup,
			Target: clabernetesinternaldeviceplan.ActionTarget{
				NodeID: "node-a", ContainerID: "node-a/root", NamespaceOwnerID: "node-a/root",
			},
			Kind:                    clabernetesinternaldeviceplan.ActionImportedDeployEndpoints,
			ImportedDeployEndpoints: &clabernetesinternaldeviceplan.ImportedDeployEndpointsAction{},
		},
		clabernetesinternaldeviceplan.Action{
			ID: "imported-readiness/node-a", Phase: clabernetesinternaldeviceplan.PhaseReadiness,
			Target: clabernetesinternaldeviceplan.ActionTarget{
				NodeID: "node-a", ContainerID: "node-a/root", NamespaceOwnerID: "node-a/root",
			},
			Kind:              clabernetesinternaldeviceplan.ActionImportedReadiness,
			ImportedReadiness: &clabernetesinternaldeviceplan.ImportedReadinessAction{},
		},
	)

	deployment, err := clabernetesinternaldirectpod.Render(
		plan,
		clabernetesinternaldirectpod.Options{
			Name: "device-a", Namespace: "lab-a", PlanConfigMapName: "device-a-plan-abc",
			InputConfigMapName: "device-a-plan-input-abc",
			PreparationImage:   "example/c9s:1", ConnectivityImage: "example/c9s:1",
			EntropySecretName: "device-a-entropy", EnableContainerStopSignals: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	pod := deployment.Spec.Template.Spec
	preparation, connectivity := pod.InitContainers[0], pod.InitContainers[1]
	root := containerByImage(
		t,
		pod.Containers,
		"example/device@sha256:"+strings.Repeat("a", 64),
	)

	component := containerByImage(
		t,
		pod.Containers,
		"example/component@sha256:"+strings.Repeat("b", 64),
	)
	if !slices.Contains(preparation.Args, "--entropy") ||
		!hasReadOnlyMount(preparation, "/var/run/clabernetes/entropy") ||
		!slices.Contains(connectivity.Args, "--entropy") ||
		!hasReadOnlyMount(connectivity, "/var/run/clabernetes/entropy") || root == nil ||
		root.ReadinessProbe == nil || root.ReadinessProbe.Exec == nil ||
		!slices.Contains(root.ReadinessProbe.Exec.Command, "--entropy") ||
		!hasReadOnlyMount(*root, "/var/lib/clabernetes/lifecycle-entropy") || component == nil ||
		hasMount(*component, "/var/lib/clabernetes/lifecycle-entropy") {
		t.Fatalf(
			"entropy boundaries = preparation %#v connectivity %#v root %#v component %#v",
			preparation,
			connectivity,
			root,
			component,
		)
	}

	foundProjection := false

	for _, volume := range pod.Volumes {
		if volume.Secret == nil || volume.Secret.SecretName != "device-a-entropy" {
			continue
		}

		if len(volume.Secret.Items) != 1 ||
			volume.Secret.Items[0].Key != clabernetesinternaldeviceplan.EntropySeedKey ||
			volume.Secret.Items[0].Path != clabernetesinternaldeviceplan.EntropySeedKey {
			t.Fatalf("entropy Secret projection = %#v", volume.Secret)
		}

		foundProjection = true
	}

	if !foundProjection {
		t.Fatalf("entropy Secret volume = %#v", pod.Volumes)
	}
}

func TestRenderProjectsOnlyTargetCertificatesIntoImportedEndpointHelper(t *testing.T) {
	t.Parallel()

	plan := renderablePlan()
	plan.Nodes = append(plan.Nodes, clabernetesinternaldeviceplan.NodePlan{
		ID: "node-b", Name: "device-b", Kind: "future-registry-kind",
		ContainerIDs:          []string{"node-b/root"},
		ReadinessContainerIDs: []string{"node-b/root"},
	})
	plan.Containers = append(plan.Containers, clabernetesinternaldeviceplan.ContainerPlan{
		ID: "node-b/root", NodeID: "node-b", NamespaceOwnerID: "node-a/root",
		Image: "example/device-b:1", ImageDigest: "sha256:" + strings.Repeat("c", 64),
		Required: true,
	})
	plan.Actions = append(plan.Actions, clabernetesinternaldeviceplan.Action{
		ID:    "imported-deploy-endpoints/node-a",
		Phase: clabernetesinternaldeviceplan.PhaseInterfaceFixup,
		Target: clabernetesinternaldeviceplan.ActionTarget{
			NodeID: "node-a", ContainerID: "node-a/root", NamespaceOwnerID: "node-a/root",
		},
		Kind:                    clabernetesinternaldeviceplan.ActionImportedDeployEndpoints,
		ImportedDeployEndpoints: &clabernetesinternaldeviceplan.ImportedDeployEndpointsAction{},
	})
	targetCertificate := clabernetesinternaldeviceplan.CertificateInput{
		NodeID: "node-a", StorageName: "target-package-storage",
		CertificateDigest:   "sha256:" + strings.Repeat("1", 64),
		PrivateKeyDigest:    "sha256:" + strings.Repeat("2", 64),
		CACertificateDigest: "sha256:" + strings.Repeat("3", 64),
		CAPrivateKeyDigest:  "sha256:" + strings.Repeat("4", 64),
	}
	otherCertificate := clabernetesinternaldeviceplan.CertificateInput{
		NodeID: "node-b", StorageName: "other-package-storage",
		CertificateDigest:   "sha256:" + strings.Repeat("5", 64),
		PrivateKeyDigest:    "sha256:" + strings.Repeat("6", 64),
		CACertificateDigest: "sha256:" + strings.Repeat("3", 64),
		CAPrivateKeyDigest:  "sha256:" + strings.Repeat("4", 64),
	}

	deployment, err := clabernetesinternaldirectpod.Render(
		plan,
		clabernetesinternaldirectpod.Options{ //nolint:gosec // test fixture identifier, not a credential.
			Name:                  "device-a",
			Namespace:             "lab-a",
			PlanConfigMapName:     "device-a-plan-abc",
			InputConfigMapName:    "device-a-plan-input-abc",
			PreparationImage:      "example/c9s:1",
			ConnectivityImage:     "example/c9s:1",
			CertificateSecretName: "device-a-certificates",
			CertificateInputs: []clabernetesinternaldeviceplan.CertificateInput{
				targetCertificate,
				otherCertificate,
			},
			EnableContainerStopSignals: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	pod := deployment.Spec.Template.Spec
	connectivity := pod.InitContainers[1]
	endpointVolumeName := ""

	for _, mount := range connectivity.VolumeMounts {
		if mount.MountPath == "/var/run/clabernetes/certificates" {
			if !mount.ReadOnly {
				t.Fatal("endpoint certificate material is mounted writable")
			}

			endpointVolumeName = mount.Name
		}
	}

	if endpointVolumeName == "" || !slices.Contains(connectivity.Args, "--certificates") {
		t.Fatalf("endpoint certificate boundary = %#v", connectivity)
	}

	targetCertificateKey, targetPrivateKey := clabernetesinternaldeviceplan.CertificateMaterialKeys(
		targetCertificate.NodeID,
		targetCertificate.StorageName,
	)
	otherCertificateKey, otherPrivateKey := clabernetesinternaldeviceplan.CertificateMaterialKeys(
		otherCertificate.NodeID,
		otherCertificate.StorageName,
	)
	wanted := []string{
		clabernetesinternaldeviceplan.CertificateCACertKey,
		targetCertificateKey,
		targetPrivateKey,
	}
	slices.Sort(wanted)

	found := false

	for _, volume := range pod.Volumes {
		if volume.Name != endpointVolumeName || volume.Secret == nil {
			continue
		}

		keys := make([]string, 0, len(volume.Secret.Items))
		for _, item := range volume.Secret.Items {
			keys = append(keys, item.Key)
		}

		slices.Sort(keys)

		if !slices.Equal(keys, wanted) {
			t.Fatalf("endpoint certificate projection keys = %#v, want %#v", keys, wanted)
		}

		if slices.Contains(keys, clabernetesinternaldeviceplan.CertificateCAKeyKey) ||
			slices.Contains(keys, otherCertificateKey) || slices.Contains(keys, otherPrivateKey) {
			t.Fatalf("unneeded certificate material leaked into endpoint helper: %#v", keys)
		}

		found = true
	}

	if !found {
		t.Fatalf("endpoint certificate projection = %#v", pod.Volumes)
	}

	for _, container := range append(
		[]k8scorev1.Container{pod.InitContainers[0]},
		pod.Containers...,
	) {
		for _, mount := range container.VolumeMounts {
			if mount.Name == endpointVolumeName {
				t.Fatalf("endpoint certificate projection leaked into container %q", container.Name)
			}
		}
	}
}

func TestRenderKubectlDefaultContainerUsesGroupedWorkloadPrimary(t *testing.T) {
	t.Parallel()

	plan := renderablePlan()
	plan.Nodes = append(plan.Nodes, clabernetesinternaldeviceplan.NodePlan{
		ID: "a-secondary", Name: "device-b", Kind: "another-package-kind",
		ContainerIDs:          []string{"a-secondary/root"},
		ReadinessContainerIDs: []string{"a-secondary/root"},
	})
	plan.Containers = append(plan.Containers, clabernetesinternaldeviceplan.ContainerPlan{
		ID: "a-secondary/root", NodeID: "a-secondary", NamespaceOwnerID: "node-a/root",
		Image: "example/device-b:1", ImageDigest: "sha256:" + strings.Repeat("c", 64),
		Required: true,
	})

	deployment, err := clabernetesinternaldirectpod.Render(
		plan,
		clabernetesinternaldirectpod.Options{
			Name: "device-a", Namespace: "lab-a", PlanConfigMapName: "device-a-plan-abc",
			InputConfigMapName: "device-a-plan-input-abc",
			PreparationImage:   "example/c9s:1", ConnectivityImage: "example/c9s:1",
			EnableContainerStopSignals: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if got, want := deployment.Spec.Template.Annotations[clabernetesinternaldirectpod.KubectlDefaultContainerAnnotation], clabernetesinternaldirectpod.ApplicationContainerName("node-a/root"); got != want {
		t.Fatalf("grouped kubectl default application container = %q, want %q", got, want)
	}
}

func TestRenderMakesImportedSaveBoundaryAvailableInPrimaryApplicationContainer(t *testing.T) {
	t.Parallel()

	plan := renderablePlan()
	plan.Actions = []clabernetesinternaldeviceplan.Action{{
		ID: "imported-save/node-a", Phase: clabernetesinternaldeviceplan.PhaseSave,
		Target: clabernetesinternaldeviceplan.ActionTarget{
			NodeID: "node-a", ContainerID: "node-a/root", NamespaceOwnerID: "node-a/root",
		},
		Kind: clabernetesinternaldeviceplan.ActionSave,
		Save: &clabernetesinternaldeviceplan.SaveAction{
			Method: clabernetesinternaldeviceplan.SaveMethodImported,
		},
	}}

	deployment, err := clabernetesinternaldirectpod.Render(
		plan,
		clabernetesinternaldirectpod.Options{
			Name: "device-a", Namespace: "lab-a", PlanConfigMapName: "device-a-plan-abc",
			InputConfigMapName: "device-a-plan-input-abc",
			PreparationImage:   "example/c9s:1", ConnectivityImage: "example/c9s:1",
			EnableContainerStopSignals: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	root := containerByImage(
		t,
		deployment.Spec.Template.Spec.Containers,
		"example/device@sha256:"+strings.Repeat("a", 64),
	)
	if root == nil || !hasReadOnlyMount(*root, "/var/lib/clabernetes/lifecycle-bin") ||
		!hasWritableMount(
			*root,
			"/var/lib/clabernetes/lifecycle-artifacts/"+
				clabernetesinternaldeviceplan.ArtifactNodeDirectory("node-a"),
		) {
		t.Fatalf("imported save lifecycle mounts = %#v", root)
	}
}

func TestApplicationSaveCommandTargetsOnlyPlanDeclaredContainerWithoutKindKnowledge(t *testing.T) {
	t.Parallel()

	plan := renderablePlan()
	plan.Nodes[0].Kind = "future-package-kind"
	plan.Actions = []clabernetesinternaldeviceplan.Action{{
		ID: "imported-save/node-a", Phase: clabernetesinternaldeviceplan.PhaseSave,
		Target: clabernetesinternaldeviceplan.ActionTarget{
			NodeID: "node-a", ContainerID: "node-a/root", NamespaceOwnerID: "node-a/root",
		},
		Kind: clabernetesinternaldeviceplan.ActionSave,
		Save: &clabernetesinternaldeviceplan.SaveAction{
			Method: clabernetesinternaldeviceplan.SaveMethodImported,
		},
	}}

	command, err := clabernetesinternaldirectpod.ApplicationSaveCommand(plan, "node-a/root")
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Contains(command, string(clabernetesinternaldeviceplan.PhaseSave)) ||
		!slices.Contains(command, "node-a/root") ||
		slices.Contains(command, "future-package-kind") ||
		command[0] != "/var/lib/clabernetes/lifecycle-bin/manager" {
		t.Fatalf("application save command = %#v", command)
	}

	if _, err = clabernetesinternaldirectpod.ApplicationSaveCommand(plan, "node-a/component"); err == nil ||
		!strings.Contains(err.Error(), "exactly one imported save action") {
		t.Fatalf("component save error = %v", err)
	}
}

func TestPacketCaptureCommandAuthorizesOnlyPlanOwnedInterface(t *testing.T) {
	t.Parallel()

	plan := renderablePlan()
	plan.Nodes[0].Kind = "future-package-kind"
	plan.Interfaces = []clabernetesinternaldeviceplan.InterfacePlan{
		{
			ID: "link/a", NodeID: "node-a", NamespaceOwnerID: "node-a/root",
			Name: "package-a", LinkID: "link", PeerNodeID: "node-a",
			PeerInterface: "package-b", Connectivity: "loopback", MTU: 1500,
			LinkApplyMode: clabernetesinternaldeviceplan.LinkApplyLive, RequiredAtStart: true,
		},
		{
			ID: "link/b", NodeID: "node-a", NamespaceOwnerID: "node-a/root",
			Name: "package-b", LinkID: "link", PeerNodeID: "node-a",
			PeerInterface: "package-a", Connectivity: "loopback", MTU: 1500,
			LinkApplyMode: clabernetesinternaldeviceplan.LinkApplyLive, RequiredAtStart: true,
		},
	}

	command, err := clabernetesinternaldirectpod.PacketCaptureCommand(
		plan,
		clabernetesinternaldirectruntime.PacketCaptureOptions{
			NodeID: "node-a", InterfaceName: "package-a", PacketLimit: 10,
			Duration: 5 * time.Second,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	for _, expected := range []string{
		"packet-capture",
		"/var/run/clabernetes/plan/plan.json",
		"/var/run/clabernetes/input/input.json",
		"/var/run/clabernetes/connectivity-revision/revision.json",
		"node-a", "package-a", "10", "5s",
	} {
		if !slices.Contains(command, expected) {
			t.Fatalf("packet capture command lacks %q: %#v", expected, command)
		}
	}

	if slices.Contains(command, "future-package-kind") || command[0] != "/clabernetes/manager" {
		t.Fatalf("packet capture command contains kind knowledge: %#v", command)
	}

	if _, err = clabernetesinternaldirectpod.PacketCaptureCommand(
		plan,
		clabernetesinternaldirectruntime.PacketCaptureOptions{
			NodeID: "node-a", InterfaceName: "eth0", PacketLimit: 1,
		},
	); err == nil || !strings.Contains(err.Error(), "not uniquely planned") {
		t.Fatalf("unplanned interface capture error = %v", err)
	}
}

func TestRenderProjectsCertificateMaterialOnlyIntoPreparationHelper(t *testing.T) {
	t.Parallel()

	deployment, err := clabernetesinternaldirectpod.Render(
		renderablePlan(),
		clabernetesinternaldirectpod.Options{ //nolint:gosec // test fixture identifier, not a credential.
			Name:                       "device-a",
			Namespace:                  "lab-a",
			PlanConfigMapName:          "device-a-plan-abc",
			InputConfigMapName:         "device-a-plan-input-abc",
			PreparationImage:           "example/c9s:1",
			ConnectivityImage:          "example/c9s:1",
			CertificateSecretName:      "device-a-certificates",
			EnableContainerStopSignals: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	pod := deployment.Spec.Template.Spec
	foundVolume := false

	for _, volume := range pod.Volumes {
		if volume.Secret != nil && volume.Secret.SecretName == "device-a-certificates" {
			foundVolume = true
		}
	}

	if !foundVolume {
		t.Fatalf("certificate Secret volume = %#v", pod.Volumes)
	}

	for index, container := range append(pod.InitContainers, pod.Containers...) {
		foundMount := false

		for _, mount := range container.VolumeMounts {
			if mount.MountPath == "/var/run/clabernetes/certificates" {
				foundMount = true

				if !mount.ReadOnly {
					t.Fatal("certificate material is mounted writable")
				}
			}
		}

		if (index == 0) != foundMount {
			t.Fatalf("certificate mount leaked beyond preparation helper: %#v", container)
		}
	}

	if !slices.Contains(pod.InitContainers[0].Args, "--certificates") {
		t.Fatalf("preparation arguments = %#v", pod.InitContainers[0].Args)
	}
}

func TestRenderProjectsOnlyNodeCertificateMaterialIntoImportedLifecycleTarget(t *testing.T) {
	t.Parallel()

	plan := renderablePlan()
	plan.Actions = append(plan.Actions, clabernetesinternaldeviceplan.Action{
		ID: "imported-post-deploy/node-a", Phase: clabernetesinternaldeviceplan.PhasePostStart,
		Target: clabernetesinternaldeviceplan.ActionTarget{
			NodeID: "node-a", ContainerID: "node-a/root", NamespaceOwnerID: "node-a/root",
		},
		Kind:               clabernetesinternaldeviceplan.ActionImportedPostDeploy,
		ImportedPostDeploy: &clabernetesinternaldeviceplan.ImportedPostDeployAction{},
	})
	certificate := clabernetesinternaldeviceplan.CertificateInput{
		NodeID: "node-a", StorageName: "package-node-name",
		CertificateDigest:   "sha256:" + strings.Repeat("1", 64),
		PrivateKeyDigest:    "sha256:" + strings.Repeat("2", 64),
		CACertificateDigest: "sha256:" + strings.Repeat("3", 64),
		CAPrivateKeyDigest:  "sha256:" + strings.Repeat("4", 64),
	}

	deployment, err := clabernetesinternaldirectpod.Render(
		plan,
		clabernetesinternaldirectpod.Options{ //nolint:gosec // test fixture identifier, not a credential.
			Name:                       "device-a",
			Namespace:                  "lab-a",
			PlanConfigMapName:          "device-a-plan-abc",
			InputConfigMapName:         "device-a-plan-input-abc",
			PreparationImage:           "example/c9s:1",
			ConnectivityImage:          "example/c9s:1",
			ServiceAccountName:         "direct-runtime",
			EnableApplicationLogBroker: true,
			CertificateSecretName:      "device-a-certificates",
			CertificateInputs: []clabernetesinternaldeviceplan.CertificateInput{
				certificate,
			},
			EnableContainerStopSignals: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	pod := deployment.Spec.Template.Spec
	target := containerByImage(
		t,
		pod.Containers,
		"example/device@sha256:"+strings.Repeat("a", 64),
	)

	component := containerByImage(
		t,
		pod.Containers,
		"example/component@sha256:"+strings.Repeat("b", 64),
	)
	if target == nil || target.Lifecycle == nil || target.Lifecycle.PostStart == nil ||
		target.Lifecycle.PostStart.Exec == nil ||
		!slices.Contains(target.Lifecycle.PostStart.Exec.Command, "--certificates") ||
		!hasReadOnlyMount(*target, "/var/lib/clabernetes/lifecycle-certificates") ||
		!hasReadOnlyMount(*target, "/var/lib/clabernetes/lifecycle-input") ||
		!hasWritableMount(*target, "/var/lib/clabernetes/lifecycle-scratch") ||
		!hasWritableMount(
			*target,
			"/var/lib/clabernetes/lifecycle-artifacts/"+
				clabernetesinternaldeviceplan.ArtifactNodeDirectory("node-a"),
		) {
		t.Fatalf("imported lifecycle certificate target = %#v", target)
	}

	if component == nil || hasMount(*component, "/var/lib/clabernetes/lifecycle-certificates") {
		t.Fatalf("component received another container's certificate material: %#v", component)
	}

	if pod.ServiceAccountName != "direct-runtime" ||
		!hasReadOnlyMount(*target, "/var/lib/clabernetes/runtime-api") ||
		hasMount(*component, "/var/lib/clabernetes/runtime-api") {
		t.Fatalf(
			"plan-scoped application runtime API = target %#v component %#v",
			target,
			component,
		)
	}

	for _, container := range append(pod.Containers, pod.InitContainers[0]) {
		if hasMount(container, "/var/run/secrets/kubernetes.io/serviceaccount") {
			t.Fatalf(
				"non-connectivity container %q received Kubernetes credentials",
				container.Name,
			)
		}
	}

	connectivity := pod.InitContainers[1]
	if !hasReadOnlyMount(connectivity, "/var/run/secrets/kubernetes.io/serviceaccount") ||
		!hasWritableMount(connectivity, "/var/lib/clabernetes/runtime-api") ||
		!slices.Contains(connectivity.Args, "--applicationRuntimeSocket") {
		t.Fatalf("connectivity log broker boundary = %#v", connectivity)
	}

	foundProjectedCredential := false

	for _, volume := range pod.Volumes {
		if volume.Name != "device-runtime-credentials" {
			continue
		}

		foundProjectedCredential = volume.Projected != nil &&
			len(volume.Projected.Sources) == 2 &&
			volume.Projected.Sources[0].ServiceAccountToken != nil
	}

	if !foundProjectedCredential {
		t.Fatalf("sidecar-only projected credential volume = %#v", pod.Volumes)
	}

	certificateKey, privateKeyKey := clabernetesinternaldeviceplan.CertificateMaterialKeys(
		certificate.NodeID,
		certificate.StorageName,
	)
	wanted := []string{
		clabernetesinternaldeviceplan.CertificateCACertKey,
		certificateKey,
		privateKeyKey,
	}
	slices.Sort(wanted)

	found := false

	for _, volume := range pod.Volumes {
		if volume.Secret == nil || volume.Secret.SecretName != "device-a-certificates" ||
			len(volume.Secret.Items) == 0 {
			continue
		}

		keys := make([]string, 0, len(volume.Secret.Items))
		for _, item := range volume.Secret.Items {
			keys = append(keys, item.Key)
		}

		slices.Sort(keys)

		if slices.Equal(keys, wanted) {
			found = true
		}

		if slices.Contains(keys, clabernetesinternaldeviceplan.CertificateCAKeyKey) {
			t.Fatalf("CA signing key leaked into application lifecycle volume: %#v", volume)
		}
	}

	if !found {
		t.Fatalf("node-scoped lifecycle certificate volume = %#v", pod.Volumes)
	}
}

func TestRenderMapsGenericPostStartActionsIntoTargetApplicationContainer(t *testing.T) {
	t.Parallel()

	plan := renderablePlan()
	plan.Files = []clabernetesinternaldeviceplan.FilePlan{{
		ID: "generated/config", NodeID: "node-a",
		SourceKind:      clabernetesinternaldeviceplan.FileSourceGenerator,
		SourceReference: "imported-hook", ArtifactPath: "generated/config",
		Digest: "sha256:" + strings.Repeat("c", 64), Mode: 0o600,
	}}
	plan.Actions = []clabernetesinternaldeviceplan.Action{
		{
			ID: "prepare", Phase: clabernetesinternaldeviceplan.PhasePrepare,
			Target: clabernetesinternaldeviceplan.ActionTarget{NodeID: "node-a"},
			Kind:   clabernetesinternaldeviceplan.ActionFile,
			File:   &clabernetesinternaldeviceplan.FileAction{FileID: "generated/config"},
		},
		{
			ID: "copy", Phase: clabernetesinternaldeviceplan.PhasePostStart, Order: 1,
			Target: clabernetesinternaldeviceplan.ActionTarget{
				NodeID: "node-a", ContainerID: "node-a/root",
			},
			Kind: clabernetesinternaldeviceplan.ActionFile,
			File: &clabernetesinternaldeviceplan.FileAction{
				FileID: "generated/config", Destination: "/etc/device/generated.conf",
			},
		},
		{
			ID: "exec", Phase: clabernetesinternaldeviceplan.PhasePostStart, Order: 2,
			Target: clabernetesinternaldeviceplan.ActionTarget{
				NodeID: "node-a", ContainerID: "node-a/root",
			},
			Kind: clabernetesinternaldeviceplan.ActionExec,
			Exec: &clabernetesinternaldeviceplan.ExecAction{
				Command: []string{"/usr/bin/apply-config"}, Wait: true,
			},
		},
	}

	deployment, err := clabernetesinternaldirectpod.Render(
		plan,
		clabernetesinternaldirectpod.Options{
			Name: "device-a", Namespace: "lab-a", PlanConfigMapName: "device-a-plan-abc",
			InputConfigMapName: "device-a-plan-input-abc",
			PreparationImage:   "example/c9s:1", ConnectivityImage: "example/c9s:1",
			EnableContainerStopSignals: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	pod := deployment.Spec.Template.Spec
	root := containerByImage(
		t,
		pod.Containers,
		"example/device@sha256:"+strings.Repeat("a", 64),
	)

	component := containerByImage(
		t,
		pod.Containers,
		"example/component@sha256:"+strings.Repeat("b", 64),
	)
	if root == nil || root.Lifecycle == nil || root.Lifecycle.PostStart == nil ||
		root.Lifecycle.PostStart.Exec == nil ||
		!slices.Contains(root.Lifecycle.PostStart.Exec.Command, "node-a/root") ||
		root.Lifecycle.StopSignal == nil {
		t.Fatalf("target lifecycle = %#v", root)
	}

	if component == nil || component.Lifecycle != nil {
		t.Fatalf("non-target lifecycle = %#v", component)
	}

	for _, mountPath := range []string{
		"/var/lib/clabernetes/lifecycle-bin",
		"/var/lib/clabernetes/lifecycle-plan",
	} {
		if !hasReadOnlyMount(*root, mountPath) {
			t.Fatalf("target lacks read-only lifecycle mount %q: %#v", mountPath, root.VolumeMounts)
		}

		if !hasReadOnlyMount(*component, mountPath) {
			t.Fatalf("restart-capable component lacks lifecycle mount %q", mountPath)
		}
	}

	artifactMount := "/var/lib/clabernetes/lifecycle-artifacts/" +
		clabernetesinternaldeviceplan.ArtifactNodeDirectory("node-a")
	if !hasReadOnlyMount(*root, artifactMount) || hasMount(*component, artifactMount) {
		t.Fatalf(
			"post-start artifact mounts = root %#v component %#v",
			root.VolumeMounts,
			component.VolumeMounts,
		)
	}

	for _, container := range []*k8scorev1.Container{root, component} {
		if !hasWritableMount(*container, "/var/lib/clabernetes/lifecycle-scratch") {
			t.Fatalf("restart-capable container lacks lifecycle state: %#v", container.VolumeMounts)
		}
	}

	preparation := pod.InitContainers[0]
	if !slices.Contains(preparation.Args, "--lifecycleBinary") ||
		!hasWritableMount(preparation, "/var/lib/clabernetes/lifecycle-bin") {
		t.Fatalf("preparation lifecycle installation = %#v", preparation)
	}

	foundLifecycleVolume := false

	for _, volume := range pod.Volumes {
		if volume.Name == "device-lifecycle-manager" && volume.EmptyDir != nil {
			foundLifecycleVolume = true
		}
	}

	if !foundLifecycleVolume {
		t.Fatalf("lifecycle binary volume = %#v", pod.Volumes)
	}
}

func TestRenderRejectsPostStartActionAcrossLogicalNodes(t *testing.T) {
	t.Parallel()

	plan := renderablePlan()
	plan.Nodes = append(plan.Nodes, clabernetesinternaldeviceplan.NodePlan{
		ID: "node-b", Name: "device-b", Kind: "registry-driven",
		ContainerIDs: []string{"node-b/root"}, ReadinessContainerIDs: []string{"node-b/root"},
	})
	plan.Containers = append(plan.Containers, clabernetesinternaldeviceplan.ContainerPlan{
		ID: "node-b/root", NodeID: "node-b", NamespaceOwnerID: "node-b/root",
		Image: "example/other:1", ImageDigest: "sha256:" + strings.Repeat("d", 64), Required: true,
	})
	plan.Actions = []clabernetesinternaldeviceplan.Action{{
		ID: "cross-node", Phase: clabernetesinternaldeviceplan.PhasePostStart,
		Target: clabernetesinternaldeviceplan.ActionTarget{
			NodeID: "node-b", ContainerID: "node-a/root",
		},
		Kind: clabernetesinternaldeviceplan.ActionExec,
		Exec: &clabernetesinternaldeviceplan.ExecAction{Command: []string{"true"}, Wait: true},
	}}

	_, err := clabernetesinternaldirectpod.Render(plan, clabernetesinternaldirectpod.Options{
		Name: "device-a", Namespace: "lab-a", PlanConfigMapName: "device-a-plan-abc",
		InputConfigMapName: "device-a-plan-input-abc",
		PreparationImage:   "example/c9s:1", ConnectivityImage: "example/c9s:1",
		EnableContainerStopSignals: true,
	})
	if err == nil || !strings.Contains(err.Error(), "crosses logical Node ownership") {
		t.Fatalf("Render() error = %v", err)
	}
}

func TestRenderRunsGenericTmpfsMountBeforeImageEntrypoint(t *testing.T) {
	t.Parallel()

	plan := renderablePlan()
	plan.Containers[0].ImageEntrypoint = []string{"/image/default-entrypoint"}
	plan.Containers[0].ImageCommand = []string{"default-command"}
	plan.Volumes = append(plan.Volumes, clabernetesinternaldeviceplan.VolumePlan{
		ID: "node-a/tmpfs", NodeID: "node-a", Kind: clabernetesinternaldeviceplan.VolumeEmptyDir,
		Medium: "Memory", Size: "8000000",
	})
	plan.Mounts = append(plan.Mounts, clabernetesinternaldeviceplan.MountPlan{
		ID: "mount/tmpfs", ContainerID: "node-a/root", VolumeID: "node-a/tmpfs",
		Destination: "/run/package",
	})
	plan.Containers[0].MountIDs = append(plan.Containers[0].MountIDs, "mount/tmpfs")
	plan.Actions = []clabernetesinternaldeviceplan.Action{{
		ID: "mount-tmpfs", Phase: clabernetesinternaldeviceplan.PhasePreStart,
		Target: clabernetesinternaldeviceplan.ActionTarget{
			NodeID: "node-a", ContainerID: "node-a/root",
		},
		Kind: clabernetesinternaldeviceplan.ActionMount,
		Mount: &clabernetesinternaldeviceplan.MountAction{
			MountID: "mount/tmpfs", Filesystem: "tmpfs", Source: "tmpfs",
			Options: []string{"rw", "nosuid", "nodev", "noexec"},
		},
	}}

	deployment, err := clabernetesinternaldirectpod.Render(
		plan,
		clabernetesinternaldirectpod.Options{
			Name: "device-a", Namespace: "lab-a", PlanConfigMapName: "device-a-plan-abc",
			InputConfigMapName: "device-a-plan-input-abc",
			PreparationImage:   "example/c9s:1", ConnectivityImage: "example/c9s:1",
			EnableContainerStopSignals: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	root := containerByImage(
		t,
		deployment.Spec.Template.Spec.Containers,
		"example/device@sha256:"+strings.Repeat("a", 64),
	)
	if root == nil || !slices.Contains(root.Command, "launch") ||
		!slices.Contains(root.Command, "node-a/root") || len(root.Args) != 0 ||
		!hasReadOnlyMount(*root, "/var/lib/clabernetes/lifecycle-plan") {
		t.Fatalf("synchronous application launch = %#v", root)
	}
}

func TestRenderRejectsTmpfsMountWithoutSysAdmin(t *testing.T) {
	t.Parallel()

	plan := renderablePlan()
	plan.Containers[1].Security = clabernetesinternaldeviceplan.SecurityPlan{}
	plan.Actions = []clabernetesinternaldeviceplan.Action{{
		ID: "mount-tmpfs", Phase: clabernetesinternaldeviceplan.PhasePreStart,
		Target: clabernetesinternaldeviceplan.ActionTarget{
			NodeID: "node-a", ContainerID: "node-a/component",
		},
		Kind: clabernetesinternaldeviceplan.ActionMount,
		Mount: &clabernetesinternaldeviceplan.MountAction{
			MountID: "mount/run", Filesystem: "tmpfs", Source: "tmpfs",
		},
	}}

	_, err := clabernetesinternaldirectpod.Render(plan, clabernetesinternaldirectpod.Options{
		Name: "device-a", Namespace: "lab-a", PlanConfigMapName: "device-a-plan-abc",
		InputConfigMapName: "device-a-plan-input-abc",
		PreparationImage:   "example/c9s:1", ConnectivityImage: "example/c9s:1",
		EnableContainerStopSignals: true,
	})
	if err == nil || !strings.Contains(err.Error(), "SYS_ADMIN") {
		t.Fatalf("Render() error = %v", err)
	}
}

func TestRenderUsesGenericLaunchHelperForStartupDelay(t *testing.T) {
	t.Parallel()

	plan := renderablePlan()
	plan.Containers[1].StartupDelay = 9
	plan.Containers[1].ImageEntrypoint = []string{"/image/component"}

	deployment, err := clabernetesinternaldirectpod.Render(
		plan,
		clabernetesinternaldirectpod.Options{
			Name: "device-a", Namespace: "lab-a", PlanConfigMapName: "device-a-plan-abc",
			InputConfigMapName: "device-a-plan-input-abc",
			PreparationImage:   "example/c9s:1", ConnectivityImage: "example/c9s:1",
			EnableContainerStopSignals: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	component := containerByImage(
		t,
		deployment.Spec.Template.Spec.Containers,
		"example/component@sha256:"+strings.Repeat("b", 64),
	)
	if component == nil || !slices.Contains(component.Command, "launch") ||
		!slices.Contains(component.Command, "node-a/component") ||
		!hasReadOnlyMount(*component, "/var/lib/clabernetes/lifecycle-plan") {
		t.Fatalf("startup-delay application wrapper = %#v", component)
	}
}

func TestRenderHealthcheckPreservesEarlySuccessAndStartupAllowance(t *testing.T) {
	t.Parallel()

	plan := renderablePlan()
	plan.Containers[0].Healthcheck = &clabernetesinternaldeviceplan.Healthcheck{
		Test:        []string{"CMD", "/usr/bin/ready"},
		Interval:    int64(5500 * time.Millisecond),
		StartPeriod: int64(13 * time.Second),
		Retries:     4,
	}

	deployment, err := clabernetesinternaldirectpod.Render(
		plan,
		clabernetesinternaldirectpod.Options{
			Name: "device-a", Namespace: "lab-a", PlanConfigMapName: "device-a-plan-abc",
			InputConfigMapName: "device-a-plan-input-abc",
			PreparationImage:   "example/c9s:1", ConnectivityImage: "example/c9s:1",
			EnableContainerStopSignals: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	container := containerByImage(
		t,
		deployment.Spec.Template.Spec.Containers,
		"example/device@sha256:"+strings.Repeat("a", 64),
	)
	if container == nil || container.StartupProbe == nil || container.ReadinessProbe == nil {
		t.Fatalf("healthcheck probes = %#v", container)
	}

	startup, readiness := container.StartupProbe, container.ReadinessProbe
	if startup.PeriodSeconds != 6 || startup.TimeoutSeconds != 30 ||
		startup.FailureThreshold != 7 || startup.InitialDelaySeconds != 0 {
		t.Fatalf("startup probe timing = %#v", startup)
	}

	if readiness.PeriodSeconds != 6 || readiness.TimeoutSeconds != 30 ||
		readiness.FailureThreshold != 4 || readiness.InitialDelaySeconds != 0 {
		t.Fatalf("readiness probe timing = %#v", readiness)
	}

	if !slices.Equal(startup.Exec.Command, readiness.Exec.Command) ||
		!slices.Equal(readiness.Exec.Command, []string{"/usr/bin/ready"}) {
		t.Fatalf("healthcheck probe commands = startup %#v readiness %#v", startup, readiness)
	}
}

func TestRenderImportedReadinessComposesOCIProbeWithoutLosingTiming(t *testing.T) {
	t.Parallel()

	plan := renderablePlan()
	plan.Actions = append(plan.Actions, clabernetesinternaldeviceplan.Action{
		ID: "imported-readiness/node-a", Phase: clabernetesinternaldeviceplan.PhaseReadiness,
		Target: clabernetesinternaldeviceplan.ActionTarget{
			NodeID: "node-a", ContainerID: "node-a/root", NamespaceOwnerID: "node-a/root",
		},
		Kind:              clabernetesinternaldeviceplan.ActionImportedReadiness,
		ImportedReadiness: &clabernetesinternaldeviceplan.ImportedReadinessAction{},
	})

	deployment, err := clabernetesinternaldirectpod.Render(
		plan,
		clabernetesinternaldirectpod.Options{
			Name: "device-a", Namespace: "lab-a", PlanConfigMapName: "device-a-plan-abc",
			InputConfigMapName: "device-a-plan-input-abc",
			PreparationImage:   "example/c9s:1", ConnectivityImage: "example/c9s:1",
			EnableContainerStopSignals: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	container := containerByImage(
		t,
		deployment.Spec.Template.Spec.Containers,
		"example/device@sha256:"+strings.Repeat("a", 64),
	)
	if container == nil || container.StartupProbe == nil || container.ReadinessProbe == nil ||
		container.StartupProbe.Exec == nil || container.ReadinessProbe.Exec == nil {
		t.Fatalf("composed readiness probes = %#v", container)
	}

	if container.ReadinessProbe.PeriodSeconds != 5 ||
		container.ReadinessProbe.TimeoutSeconds != 2 ||
		container.ReadinessProbe.FailureThreshold != 3 {
		t.Fatalf("OCI readiness timing changed = %#v", container.ReadinessProbe)
	}

	if container.StartupProbe.PeriodSeconds != 5 ||
		container.StartupProbe.TimeoutSeconds != 2 ||
		container.StartupProbe.FailureThreshold != 180 ||
		container.StartupProbe.InitialDelaySeconds != 10 {
		t.Fatalf("generic startup allowance = %#v", container.StartupProbe)
	}

	if !slices.Contains(container.ReadinessProbe.Exec.Command, "readiness") ||
		!slices.Contains(container.ReadinessProbe.Exec.Command, "renderer-test") ||
		!hasReadOnlyMount(*container, "/var/lib/clabernetes/lifecycle-input") ||
		!hasWritableEmptyDirMount(
			deployment.Spec.Template.Spec,
			*container,
			"/var/lib/clabernetes/lifecycle-scratch",
		) {
		t.Fatalf("imported readiness execution boundary = %#v", container)
	}

	component := containerByImage(
		t,
		deployment.Spec.Template.Spec.Containers,
		"example/component@sha256:"+strings.Repeat("b", 64),
	)
	if component == nil || hasMount(*component, "/var/lib/clabernetes/lifecycle-input") {
		t.Fatalf("readiness input leaked to non-target component = %#v", component)
	}
}

func TestRenderExplicitProbePolicyUsesRoundedStartupAndSecretProjection(t *testing.T) {
	t.Parallel()

	plan := renderablePlan()
	plan.Actions = append(plan.Actions, clabernetesinternaldeviceplan.Action{
		ID: "imported-readiness/node-a", Phase: clabernetesinternaldeviceplan.PhaseReadiness,
		Target: clabernetesinternaldeviceplan.ActionTarget{
			NodeID: "node-a", ContainerID: "node-a/root", NamespaceOwnerID: "node-a/root",
		},
		Kind:              clabernetesinternaldeviceplan.ActionImportedReadiness,
		ImportedReadiness: &clabernetesinternaldeviceplan.ImportedReadinessAction{},
	})

	deployment, err := clabernetesinternaldirectpod.Render(
		plan,
		clabernetesinternaldirectpod.Options{
			Name: "device-a", Namespace: "lab-a", PlanConfigMapName: "device-a-plan-abc",
			InputConfigMapName: "device-a-plan-input-abc",
			PreparationImage:   "example/c9s:1", ConnectivityImage: "example/c9s:1",
			EnableContainerStopSignals: true,
			ProbeSecretName:            "device-a-probes-abc",
			ProbePolicies: map[string]clabernetesinternaldirectpod.ProbePolicy{
				"node-a": { //nolint:gosec // test fixture identifier, not a credential.
					StartupSeconds: 21, TCPPort: 830, SSHUsername: "operator", SSHPort: 22,
					SSHPasswordKey: "ssh-node-a",
				},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	container := containerByImage(
		t,
		deployment.Spec.Template.Spec.Containers,
		"example/device@sha256:"+strings.Repeat("a", 64),
	)
	if container == nil || container.StartupProbe == nil ||
		container.StartupProbe.FailureThreshold != 5 {
		t.Fatalf("rounded explicit startup allowance = %#v", container)
	}

	command := container.ReadinessProbe.Exec.Command
	for _, expected := range []string{
		"--tcpPort", "830", "--sshUsername", "operator", "--sshPort", "22",
		"--sshPasswordFile", "/var/lib/clabernetes/probe-secret/password",
	} {
		if !slices.Contains(command, expected) {
			t.Fatalf("application probe command = %#v", command)
		}
	}

	foundPasswordMount := false

	for _, mount := range container.VolumeMounts {
		if mount.MountPath == "/var/lib/clabernetes/probe-secret/password" {
			foundPasswordMount = mount.ReadOnly && mount.SubPath == "ssh-node-a"
		}
	}

	if !foundPasswordMount {
		t.Fatalf("SSH password projection = %#v", container.VolumeMounts)
	}

	foundSecret := false

	for _, volume := range deployment.Spec.Template.Spec.Volumes {
		if volume.Secret != nil && volume.Secret.SecretName == "device-a-probes-abc" {
			foundSecret = true
		}
	}

	if !foundSecret {
		t.Fatalf("probe Secret volume = %#v", deployment.Spec.Template.Spec.Volumes)
	}
}

func TestRenderRejectsHealthcheckOutsideKubernetesProbeLimits(t *testing.T) {
	t.Parallel()

	plan := renderablePlan()
	plan.Containers[0].Healthcheck = &clabernetesinternaldeviceplan.Healthcheck{
		Test: []string{"CMD", "true"}, Interval: -1,
	}

	_, err := clabernetesinternaldirectpod.Render(plan, clabernetesinternaldirectpod.Options{
		Name: "device-a", Namespace: "lab-a", PlanConfigMapName: "device-a-plan-abc",
		InputConfigMapName: "device-a-plan-input-abc",
		PreparationImage:   "example/c9s:1", ConnectivityImage: "example/c9s:1",
		EnableContainerStopSignals: true,
	})
	if err == nil || !strings.Contains(err.Error(), "healthcheck interval") {
		t.Fatalf("Render() healthcheck error = %v", err)
	}
}

func hasWritableEmptyDirMount(
	pod k8scorev1.PodSpec,
	container k8scorev1.Container,
	destination string,
) bool {
	volumes := map[string]bool{}
	for _, volume := range pod.Volumes {
		volumes[volume.Name] = volume.EmptyDir != nil
	}

	for _, mount := range container.VolumeMounts {
		if mount.MountPath == destination && !mount.ReadOnly && volumes[mount.Name] {
			return true
		}
	}

	return false
}

func hasMount(container k8scorev1.Container, destination string) bool {
	for _, mount := range container.VolumeMounts {
		if mount.MountPath == destination {
			return true
		}
	}

	return false
}

func hasReadOnlyMount(container k8scorev1.Container, destination string) bool {
	for _, mount := range container.VolumeMounts {
		if mount.MountPath == destination {
			return mount.ReadOnly
		}
	}

	return false
}

func hasWritableMount(container k8scorev1.Container, destination string) bool {
	for _, mount := range container.VolumeMounts {
		if mount.MountPath == destination {
			return !mount.ReadOnly
		}
	}

	return false
}

func TestRenderRejectsUnrepresentableGenericFieldsWithoutKindDispatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*clabernetesinternaldeviceplan.ContainerPlan)
	}{
		{name: "CPU pinning", mutate: func(container *clabernetesinternaldeviceplan.ContainerPlan) {
			container.Resources.CPUSet = "0-1"
		}},
		{name: "named user", mutate: func(container *clabernetesinternaldeviceplan.ContainerPlan) {
			container.User = "operator"
		}},
		{
			name: "per-container restart",
			mutate: func(container *clabernetesinternaldeviceplan.ContainerPlan) {
				container.RestartPolicy = "on-failure"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			plan := renderablePlan()
			test.mutate(&plan.Containers[0])

			_, err := clabernetesinternaldirectpod.Render(
				plan,
				clabernetesinternaldirectpod.Options{
					Name: "device-a", Namespace: "lab-a", PlanConfigMapName: "device-a-plan-abc",
					InputConfigMapName: "device-a-plan-input-abc",
					PreparationImage:   "example/c9s:1", ConnectivityImage: "example/c9s:1",
					EnableContainerStopSignals: true,
				},
			)
			if err == nil {
				t.Fatalf("Render() accepted unrepresentable %s", test.name)
			}

			if strings.Contains(strings.ToLower(err.Error()), "kind") {
				t.Fatalf("generic rejection is kind-dependent: %v", err)
			}
		})
	}
}

func TestRenderRejectsPayloadActionUntilTypedSourceIsMounted(t *testing.T) {
	t.Parallel()

	plan := renderablePlan()
	plan.Files = []clabernetesinternaldeviceplan.FilePlan{{
		ID: "payload-a", NodeID: "node-a", SourceKind: clabernetesinternaldeviceplan.FileSourcePayload,
		SourceReference: "payload-input-a", ArtifactPath: "payloads/a",
		Destination: "/etc/device/a", Mode: 0o600,
	}}
	plan.Actions = []clabernetesinternaldeviceplan.Action{{
		ID: "prepare/payload-a", Phase: clabernetesinternaldeviceplan.PhasePrepare,
		Target: clabernetesinternaldeviceplan.ActionTarget{NodeID: "node-a"},
		Kind:   clabernetesinternaldeviceplan.ActionFile,
		File:   &clabernetesinternaldeviceplan.FileAction{FileID: "payload-a"},
	}}

	_, err := clabernetesinternaldirectpod.Render(plan, clabernetesinternaldirectpod.Options{
		Name: "device-a", Namespace: "lab-a", PlanConfigMapName: "device-a-plan-abc",
		InputConfigMapName: "device-a-plan-input-abc",
		PreparationImage:   "example/c9s:1", ConnectivityImage: "example/c9s:1",
	})
	if err == nil {
		t.Fatal("Render() accepted a payload action with no typed source mount")
	}
}

func TestRenderMountsTypedPayloadSourceOnlyIntoPreparationHelper(t *testing.T) {
	t.Parallel()

	plan := renderablePlan()
	digest := clabernetesinternaldeviceplan.Digest([]byte("payload bytes"))
	plan.Files = []clabernetesinternaldeviceplan.FilePlan{{
		ID: "payload-a", NodeID: "node-a", SourceKind: clabernetesinternaldeviceplan.FileSourcePayload,
		SourceReference: "payload-input-a", Digest: digest, ArtifactPath: "payloads/a",
		Destination: "/etc/device/a", Mode: 0o444,
	}}
	plan.Actions = []clabernetesinternaldeviceplan.Action{{
		ID: "prepare/payload-a", Phase: clabernetesinternaldeviceplan.PhasePrepare,
		Target: clabernetesinternaldeviceplan.ActionTarget{NodeID: "node-a"},
		Kind:   clabernetesinternaldeviceplan.ActionFile,
		File:   &clabernetesinternaldeviceplan.FileAction{FileID: "payload-a"},
	}}

	deployment, err := clabernetesinternaldirectpod.Render(
		plan,
		clabernetesinternaldirectpod.Options{
			Name: "device-a", Namespace: "lab-a", PlanConfigMapName: "device-a-plan-abc",
			InputConfigMapName: "device-a-plan-input-abc",
			PreparationImage:   "example/c9s:1", ConnectivityImage: "example/c9s:1",
			EnableContainerStopSignals: true,
			Payloads: []clabernetesinternaldeviceplan.PayloadInput{{
				ID: "payload-input-a", NodeID: "node-a",
				Kind: clabernetesinternaldeviceplan.PayloadConfigMap, Reference: "lab-a/device-config:startup",
				Digest: digest, Destination: "/etc/device/a", Mode: 0o444,
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	pod := deployment.Spec.Template.Spec
	foundSource := false

	for _, volume := range pod.Volumes {
		if volume.ConfigMap != nil && volume.ConfigMap.Name == "device-config" &&
			len(volume.ConfigMap.Items) == 1 && volume.ConfigMap.Items[0].Key == "startup" {
			foundSource = true

			for _, container := range pod.Containers {
				for _, mount := range container.VolumeMounts {
					if mount.Name == volume.Name {
						t.Fatalf(
							"raw payload source is mounted into application container %q",
							container.Name,
						)
					}
				}
			}
		}
	}

	if !foundSource || !slices.Contains(pod.InitContainers[0].Args, "--payloads") {
		t.Fatalf("typed preparation payload source was not rendered: %#v", pod)
	}
}

func TestRenderProjectsSecretPayloadOnlyIntoPreparationHelper(t *testing.T) {
	t.Parallel()

	plan := renderablePlan()
	digest := clabernetesinternaldeviceplan.Digest([]byte("sensitive payload bytes"))
	plan.Files = []clabernetesinternaldeviceplan.FilePlan{{
		ID: "payload-secret", NodeID: "node-a",
		SourceKind:      clabernetesinternaldeviceplan.FileSourcePayload,
		SourceReference: "payload-input-secret", Digest: digest,
		ArtifactPath: "payloads/license", Destination: "/etc/device/license.key", Mode: 0o444,
	}}
	plan.Actions = []clabernetesinternaldeviceplan.Action{{
		ID: "prepare/payload-secret", Phase: clabernetesinternaldeviceplan.PhasePrepare,
		Target: clabernetesinternaldeviceplan.ActionTarget{NodeID: "node-a"},
		Kind:   clabernetesinternaldeviceplan.ActionFile,
		File:   &clabernetesinternaldeviceplan.FileAction{FileID: "payload-secret"},
	}}

	deployment, err := clabernetesinternaldirectpod.Render(
		plan,
		clabernetesinternaldirectpod.Options{
			Name: "device-a", Namespace: "lab-a", PlanConfigMapName: "device-a-plan-abc",
			InputConfigMapName: "device-a-plan-input-abc",
			PreparationImage:   "example/c9s:1", ConnectivityImage: "example/c9s:1",
			EnableContainerStopSignals: true,
			Payloads: []clabernetesinternaldeviceplan.PayloadInput{{
				ID: "payload-input-secret", NodeID: "node-a",
				Kind: clabernetesinternaldeviceplan.PayloadSecret, Reference: "lab-a/device-license:license.key",
				Digest: digest, Destination: "/etc/device/license.key", Mode: 0o444, Sensitive: true,
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	pod := deployment.Spec.Template.Spec
	for _, volume := range pod.Volumes {
		if volume.Secret == nil || volume.Secret.SecretName != "device-license" {
			continue
		}

		if len(volume.Secret.Items) != 1 || volume.Secret.Items[0].Key != "license.key" ||
			volume.Secret.Items[0].Path != "source" {
			t.Fatalf("Secret projection = %#v", volume.Secret)
		}

		for _, container := range pod.Containers {
			for _, mount := range container.VolumeMounts {
				if mount.Name == volume.Name {
					t.Fatalf(
						"Secret source is mounted into application container %q",
						container.Name,
					)
				}
			}
		}

		if !slices.ContainsFunc(
			pod.InitContainers[0].VolumeMounts,
			func(mount k8scorev1.VolumeMount) bool {
				return mount.Name == volume.Name && mount.ReadOnly
			},
		) {
			t.Fatalf("Secret source is not mounted read-only into preparation helper: %#v", pod)
		}

		return
	}

	t.Fatalf("Secret payload projection is absent: %#v", pod.Volumes)
}

func TestRenderAppliesProfileResourcesOnlyToLogicalNodePrimaries(t *testing.T) {
	t.Parallel()

	plan := renderablePlan()
	plan.Nodes = append(plan.Nodes, clabernetesinternaldeviceplan.NodePlan{
		ID: "node-b", Name: "device-b", Kind: "another-package-kind",
		ContainerIDs: []string{"node-b/root"}, ReadinessContainerIDs: []string{"node-b/root"},
	})
	plan.Containers = append(plan.Containers, clabernetesinternaldeviceplan.ContainerPlan{
		ID: "node-b/root", NodeID: "node-b", NamespaceOwnerID: "node-a/root",
		Image: "example/device-b:1", ImageDigest: "sha256:" + strings.Repeat("c", 64),
		Required: true,
	})

	deployment, err := clabernetesinternaldirectpod.Render(
		plan,
		clabernetesinternaldirectpod.Options{
			Name: "device-a", Namespace: "lab-a", PlanConfigMapName: "device-a-plan-abc",
			InputConfigMapName: "device-a-plan-input-abc",
			PreparationImage:   "example/c9s:1", ConnectivityImage: "example/c9s:1",
			EnableContainerStopSignals: true,
			PrimaryContainerResources: &k8scorev1.ResourceRequirements{
				Requests: k8scorev1.ResourceList{
					k8scorev1.ResourceCPU: apiresource.MustParse("250m"),
				},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	root := containerByImage(
		t,
		deployment.Spec.Template.Spec.Containers,
		"example/device@sha256:"+strings.Repeat("a", 64),
	)
	component := containerByImage(
		t,
		deployment.Spec.Template.Spec.Containers,
		"example/component@sha256:"+strings.Repeat("b", 64),
	)

	second := containerByImage(
		t,
		deployment.Spec.Template.Spec.Containers,
		"example/device-b@sha256:"+strings.Repeat("c", 64),
	)
	for name, container := range map[string]*k8scorev1.Container{"root": root, "second": second} {
		if container == nil || container.Resources.Requests.Cpu().String() != "250m" {
			t.Fatalf("%s primary resources = %#v", name, container)
		}
	}

	if component == nil || !component.Resources.Requests.Cpu().IsZero() {
		t.Fatalf("component unexpectedly inherited primary policy: %#v", component)
	}
}

func TestRenderMapsPerNodeArtifactPersistenceToKubernetesClaim(t *testing.T) {
	t.Parallel()

	deployment, err := clabernetesinternaldirectpod.Render(
		renderablePlan(),
		clabernetesinternaldirectpod.Options{
			Name: "device-a", Namespace: "lab-a", PlanConfigMapName: "device-a-plan-abc",
			InputConfigMapName: "device-a-plan-input-abc",
			PreparationImage:   "example/c9s:1", ConnectivityImage: "example/c9s:1",
			EnableContainerStopSignals: true,
			PersistentVolumeClaims:     map[string]string{"node-a": "device-a"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	for _, volume := range deployment.Spec.Template.Spec.Volumes {
		if volume.PersistentVolumeClaim != nil &&
			volume.PersistentVolumeClaim.ClaimName == "device-a" {
			if volume.EmptyDir != nil {
				t.Fatalf("persistent artifact volume also has EmptyDir source: %#v", volume)
			}

			return
		}
	}

	t.Fatalf("persistent artifact claim is absent: %#v", deployment.Spec.Template.Spec.Volumes)
}

func renderablePlan() clabernetesinternaldeviceplan.Plan {
	compatibility := clabernetesinternaldeviceplan.Compatibility{
		ContainerlabModule: "github.com/srl-labs/containerlab", ContainerlabVersion: "v0.78.0",
		RegistryDigest:    "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PlanSchemaVersion: clabernetesinternaldeviceplan.SchemaVersion,
	}
	dns := clabernetesinternaldeviceplan.DNSConfig{
		Servers: []string{
			"192.0.2.53",
		}, Search: []string{"lab.example"}, Options: []string{"ndots:1"},
	}

	return clabernetesinternaldeviceplan.Plan{
		SchemaVersion: clabernetesinternaldeviceplan.SchemaVersion,
		Compatibility: compatibility,
		InputDigest:   "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Planner: clabernetesinternaldeviceplan.PlannerIdentity{
			Name:     "clabernetes",
			Revision: "renderer-test",
		},
		Nodes: []clabernetesinternaldeviceplan.NodePlan{{
			ID: "node-a", Name: "device-a", Kind: "registry-driven",
			ContainerIDs:          []string{"node-a/root", "node-a/component"},
			ReadinessContainerIDs: []string{"node-a/root", "node-a/component"},
		}},
		Containers: []clabernetesinternaldeviceplan.ContainerPlan{
			{
				ID: "node-a/root", NodeID: "node-a", NamespaceOwnerID: "node-a/root",
				Image:       "example/device:1",
				ImageDigest: "sha256:" + strings.Repeat("a", 64),
				Entrypoint:  []string{"/usr/bin/device"}, Command: []string{"serve"},
				Environment: []clabernetesinternaldeviceplan.KeyValue{
					{Name: "ROLE", Value: "root"},
				},
				User: "1000:1000", WorkingDir: "/work", StopSignal: "SIGTERM",
				Security: clabernetesinternaldeviceplan.SecurityPlan{
					Privileged: true, CapabilitiesAdd: []string{"NET_ADMIN"},
					Devices: []clabernetesinternaldeviceplan.Device{{
						HostPath: "/dev/kvm", ContainerPath: "/dev/kvm", Permissions: "rwm",
					}},
					Sysctls: []clabernetesinternaldeviceplan.KeyValue{
						{Name: "net.ipv4.ip_forward", Value: "1"},
					},
					SeccompProfile: "RuntimeDefault",
				},
				Resources: clabernetesinternaldeviceplan.ResourcePlan{
					CPULimit:    "1",
					MemoryLimit: "1Gi",
				},
				DNS: dns,
				Healthcheck: &clabernetesinternaldeviceplan.Healthcheck{
					Test: []string{
						"CMD",
						"/usr/bin/health",
					}, Interval: int64(5e9), Timeout: int64(2e9), Retries: 3,
				},
				Required: true, MountIDs: []string{"mount/artifacts"},
			},
			{
				ID: "node-a/component", NodeID: "node-a", ComponentID: "component-a",
				NamespaceOwnerID: "node-a/root", Image: "example/component:1",
				ImageDigest: "sha256:" + strings.Repeat("b", 64), DNS: dns,
				Required: true, MountIDs: []string{"mount/run"},
			},
		},
		Volumes: []clabernetesinternaldeviceplan.VolumePlan{
			{
				ID:     "node-a/artifacts",
				NodeID: "node-a",
				Kind:   clabernetesinternaldeviceplan.VolumeArtifacts,
			},
			{
				ID:     "node-a/run",
				NodeID: "node-a",
				Kind:   clabernetesinternaldeviceplan.VolumeEmptyDir,
				Medium: "Memory",
				Size:   "64Mi",
			},
		},
		Mounts: []clabernetesinternaldeviceplan.MountPlan{
			{
				ID:          "mount/artifacts",
				ContainerID: "node-a/root",
				VolumeID:    "node-a/artifacts",
				Destination: "/etc/device",
			},
			{
				ID:          "mount/run",
				ContainerID: "node-a/component",
				VolumeID:    "node-a/run",
				Destination: "/run",
			},
		},
	}
}

func containerByImage(
	t *testing.T,
	containers []k8scorev1.Container,
	image string,
) *k8scorev1.Container {
	t.Helper()

	for index := range containers {
		if containers[index].Image == image {
			return &containers[index]
		}
	}

	return nil
}

func hasDownwardEnvironment(container k8scorev1.Container, name, fieldPath string) bool {
	for _, variable := range container.Env {
		if variable.Name == name && variable.ValueFrom != nil &&
			variable.ValueFrom.FieldRef != nil &&
			variable.ValueFrom.FieldRef.FieldPath == fieldPath {
			return true
		}
	}

	return false
}
