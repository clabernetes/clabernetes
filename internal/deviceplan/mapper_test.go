//nolint:gocyclo // dense fixture-driven tests exercise one boundary end to end.
package deviceplan_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	clabernetesinternaldirectpod "github.com/clabernetes/clabernetes/internal/directpod"
	clabernetesinternaldirectruntime "github.com/clabernetes/clabernetes/internal/directruntime"
	k8scorev1 "k8s.io/api/core/v1"
)

func TestPlanAutomaticallyMapsGenericBehaviorForNewRegistryKind(t *testing.T) {
	t.Parallel()

	input := richSyntheticInput(t)
	adapter := clabernetesinternaldeviceplan.Adapter{
		Registry: newSyntheticRegistry(t),
		Revision: "automatic-kind-plan-v1",
	}

	first, err := adapter.Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	second, err := adapter.Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	firstJSON, err := first.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}

	secondJSON, err := second.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(firstJSON, secondJSON) {
		t.Fatalf("new-kind plan is nondeterministic:\n%s\n%s", firstJSON, secondJSON)
	}

	if got, want := first.Nodes[0].Kind, syntheticKind; got != want {
		t.Fatalf("planned kind = %q, want %q", got, want)
	}

	container := first.Containers[0]
	if container.RestartPolicy != "imported-default" || container.StartupDelay != 7 {
		t.Fatalf("imported generic defaults were lost: %#v", container)
	}

	if container.Security.Privileged ||
		!reflect.DeepEqual(container.Security.CapabilitiesAdd, []string{"NET_ADMIN"}) ||
		len(container.Security.Devices) != 1 {
		t.Fatalf("generic security mapping = %#v", container.Security)
	}

	if !containsKeyValue(container.Environment, "CLAB_INTFS", "1") ||
		!containsKeyValue(container.Environment, "USER_SETTING", "enabled") {
		t.Fatalf("generic environment mapping = %#v", container.Environment)
	}

	if got, want := len(first.Management), 1; got != want {
		t.Fatalf("management plans = %d, want %d", got, want)
	}

	if got, want := len(first.Interfaces), 1; got != want {
		t.Fatalf("interface plans = %d, want %d", got, want)
	}

	if first.Interfaces[0].Name != "mapped-eth1" || first.Interfaces[0].Alias != "eth1" {
		t.Fatalf("imported interface mapping = %#v", first.Interfaces[0])
	}

	if got, want := len(first.Files), 4; got != want ||
		!containsFileSource(first.Files, clabernetesinternaldeviceplan.FileSourcePayload) ||
		!containsFileSource(first.Files, clabernetesinternaldeviceplan.FileSourceGenerator) ||
		!containsArtifactKind(first.Files, clabernetesinternaldeviceplan.ArtifactDirectory) {
		t.Fatalf("generic file plans = %#v", first.Files)
	}

	if !containsActionKind(first.Actions, clabernetesinternaldeviceplan.ActionImportedPostDeploy) {
		t.Fatalf("generic lifecycle actions omit package-owned post-deploy: %#v", first.Actions)
	}

	if !containsActionKind(
		first.Actions,
		clabernetesinternaldeviceplan.ActionImportedDeployEndpoints,
	) {
		t.Fatalf(
			"generic lifecycle actions omit package-owned endpoint deployment: %#v",
			first.Actions,
		)
	}

	if !containsActionKind(first.Actions, clabernetesinternaldeviceplan.ActionImportedReadiness) {
		t.Fatalf("generic lifecycle actions omit imported readiness: %#v", first.Actions)
	}

	canonical, err := first.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(canonical), "package-derived-stdin") {
		t.Fatal("serialized plan contains imported stdin bytes")
	}

	if got, want := len(first.Mounts), 4; got != want {
		t.Fatalf("generic mounts = %#v, want %d", first.Mounts, want)
	}

	var tmpfsSizes []string

	for _, volume := range first.Volumes {
		if volume.Medium == "Memory" {
			tmpfsSizes = append(tmpfsSizes, volume.Size)
		}
	}

	slices.Sort(tmpfsSizes)

	if !reflect.DeepEqual(tmpfsSizes, []string{"128000000", "64000000"}) ||
		!containsActionKind(first.Actions, clabernetesinternaldeviceplan.ActionMount) {
		t.Fatalf(
			"generic tmpfs/shared-memory mapping = volumes %#v actions %#v",
			first.Volumes,
			first.Actions,
		)
	}
}

func TestPlanPreservesImportedDeploymentOperationOrder(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Nodes[0].Type = "deployment-operations-test"
	input.Nodes[0].Definition = mustJSON(t, map[string]any{
		"kind": syntheticKind, "type": input.Nodes[0].Type, "image": "example/future:1",
		"exec": []string{"user-post-start --apply"},
	})

	plan, err := (clabernetesinternaldeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "deployment-operation-plan-v1",
	}).Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	postStart := []clabernetesinternaldeviceplan.Action{}

	for _, action := range plan.Actions {
		if action.Phase == clabernetesinternaldeviceplan.PhasePostStart &&
			action.Target.NodeID == input.Nodes[0].ID {
			postStart = append(postStart, action)
		}
	}

	wantKinds := []clabernetesinternaldeviceplan.ActionKind{
		clabernetesinternaldeviceplan.ActionFile,
		clabernetesinternaldeviceplan.ActionWriteStdin,
		clabernetesinternaldeviceplan.ActionExec,
		clabernetesinternaldeviceplan.ActionImportedPostDeploy,
		clabernetesinternaldeviceplan.ActionExec,
	}
	if len(postStart) != len(wantKinds) {
		t.Fatalf("post-start deployment operations = %#v", postStart)
	}

	for index, kind := range wantKinds {
		if postStart[index].Kind != kind || postStart[index].Order != index {
			t.Fatalf(
				"post-start operation %d = %#v, want kind %q order %d",
				index,
				postStart[index],
				kind,
				index,
			)
		}
	}

	if postStart[0].File == nil ||
		postStart[0].File.Destination != "/etc/imported-deploy.conf" ||
		postStart[1].WriteStdin == nil || postStart[2].Exec == nil ||
		postStart[2].Exec.Wait ||
		!reflect.DeepEqual(
			postStart[2].Exec.Command,
			[]string{"package-deploy-command", "--apply"},
		) || postStart[4].Exec == nil || !postStart[4].Exec.Wait {
		t.Fatalf("typed imported deployment operations = %#v", postStart)
	}

	canonical, err := plan.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(canonical), "package-deploy-stdin") {
		t.Fatal("serialized plan contains imported stdin bytes")
	}
}

func TestNewRegistryKindLifecycleFlowsThroughGenericDirectPodRenderer(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Nodes[0].Name = "future-device"
	input.Nodes[0].Type = "renderable-test"
	input.Nodes[0].Definition = mustJSON(t, map[string]any{
		"kind": syntheticKind, "type": "renderable-test", "image": "example/future:1",
	})
	input.Images[0].Config.Entrypoint = []string{"/usr/bin/future-device"}
	input.Images[0].Config.Command = []string{"serve"}

	plan, err := (clabernetesinternaldeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "new-kind-direct-pod-v1",
	}).Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	deployment, err := clabernetesinternaldirectpod.Render(
		*plan,
		clabernetesinternaldirectpod.Options{
			Name: input.Nodes[0].Name, Namespace: "future-lab",
			PlanConfigMapName: "future-plan", InputConfigMapName: "future-input",
			PreparationImage: "example/c9s:1", ConnectivityImage: "example/c9s:1",
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if len(deployment.Spec.Template.Spec.Containers) != 1 {
		t.Fatalf(
			"new package kind application containers = %#v",
			deployment.Spec.Template.Spec.Containers,
		)
	}

	application := deployment.Spec.Template.Spec.Containers[0]
	if application.Image != "example/future@sha256:"+strings.Repeat("a", 64) ||
		!slices.Contains(application.Command, "launch") ||
		application.Lifecycle == nil || application.Lifecycle.PostStart == nil ||
		application.Lifecycle.PostStart.Exec == nil ||
		!slices.Contains(application.Lifecycle.PostStart.Exec.Command, plan.Containers[0].ID) {
		t.Fatalf("new package kind lifecycle rendering = %#v", application)
	}

	foundMountAction := false

	for _, action := range plan.Actions {
		if action.Kind == clabernetesinternaldeviceplan.ActionMount && action.Mount != nil &&
			action.Mount.Filesystem == "tmpfs" &&
			slices.Contains(action.Mount.Options, "noexec") {
			foundMountAction = true
		}
	}

	if !foundMountAction {
		t.Fatalf("new package kind tmpfs operation = %#v", plan.Actions)
	}

	if !slices.Contains(
		deployment.Spec.Template.Spec.InitContainers[0].Args,
		"--lifecycleBinary",
	) {
		t.Fatalf(
			"new package kind preparation helper = %#v",
			deployment.Spec.Template.Spec.InitContainers[0],
		)
	}

	if application.StartupProbe == nil || application.ReadinessProbe == nil ||
		application.StartupProbe.Exec == nil || application.ReadinessProbe.Exec == nil ||
		!slices.Contains(application.ReadinessProbe.Exec.Command, "readiness") ||
		!hasReadOnlyMountAt(application, "/var/lib/clabernetes/lifecycle-input") ||
		!hasWritableMountAt(application, "/var/lib/clabernetes/lifecycle-scratch") {
		t.Fatalf("new package kind readiness rendering = %#v", application)
	}
}

func TestGeneratedSymlinkFlowsThroughGenericDirectPodRenderer(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Nodes[0].Type = "symlink-artifact-test"
	input.Nodes[0].Definition = mustJSON(t, map[string]string{
		"kind": syntheticKind, "type": input.Nodes[0].Type, "image": "example/future:1",
	})

	plan, err := (clabernetesinternaldeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "symlink-render-v1",
	}).Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	if err = clabernetesinternaldirectruntime.ValidatePlanCapabilities(*plan); err != nil {
		t.Fatal(err)
	}

	deployment, err := clabernetesinternaldirectpod.Render(
		*plan,
		clabernetesinternaldirectpod.Options{
			Name: input.Nodes[0].Name, Namespace: "future-lab",
			PlanConfigMapName: "future-plan", InputConfigMapName: "future-input",
			PreparationImage: "example/c9s:1", ConnectivityImage: "example/c9s:1",
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if len(deployment.Spec.Template.Spec.Containers) != 1 ||
		!hasReadOnlyMountAt(deployment.Spec.Template.Spec.Containers[0], "/etc/generated") {
		t.Fatalf(
			"generic symbolic-link artifact mount = %#v",
			deployment.Spec.Template.Spec.Containers,
		)
	}
}

func hasReadOnlyMountAt(container k8scorev1.Container, destination string) bool {
	for _, mount := range container.VolumeMounts {
		if mount.MountPath == destination {
			return mount.ReadOnly
		}
	}

	return false
}

func hasWritableMountAt(container k8scorev1.Container, destination string) bool {
	for _, mount := range container.VolumeMounts {
		if mount.MountPath == destination {
			return !mount.ReadOnly
		}
	}

	return false
}

func TestPlanMapsImportedPreparationFromExplicitPayloadWithoutFieldDispatch(t *testing.T) {
	t.Parallel()

	content := []byte("startup from ConfigMap\n")
	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Nodes[0].Type = "payload-workspace-test"
	input.Nodes[0].Definition = mustJSON(t, map[string]any{
		"kind": syntheticKind, "type": "payload-workspace-test",
		"image": "example/future:1", "startup-config": "/inputs/startup.cfg",
	})
	input.Payloads = []clabernetesinternaldeviceplan.PayloadInput{{
		ID: "startup-input", NodeID: "node-a", Kind: clabernetesinternaldeviceplan.PayloadConfigMap,
		Reference: "lab/device-config:startup.cfg", Digest: clabernetesinternaldeviceplan.Digest(content),
		Destination: "/inputs/startup.cfg", Mode: 0o444,
	}}
	payloadRoot := t.TempDir()

	sourceRoot := filepath.Join(
		payloadRoot,
		clabernetesinternaldeviceplan.ArtifactNodeDirectory(input.Payloads[0].ID),
	)
	if err := os.MkdirAll(sourceRoot, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(sourceRoot, "source"), content, 0o444); err != nil { //nolint:gosec // test fixture permissions.
		t.Fatal(err)
	}

	plan, err := (clabernetesinternaldeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "payload-plan-v1", PayloadRoot: payloadRoot,
	}).Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	if !containsFileSource(plan.Files, clabernetesinternaldeviceplan.FileSourcePayload) ||
		!containsFileSource(plan.Files, clabernetesinternaldeviceplan.FileSourceGenerator) {
		t.Fatalf("payload-driven preparation plan = %#v", plan.Files)
	}

	raw, err := plan.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(raw), payloadRoot) ||
		bytes.Contains(raw, content) {
		t.Fatalf("plan leaks worker payload path or bytes: %s", raw)
	}
}

func containsActionKind(
	actions []clabernetesinternaldeviceplan.Action,
	kind clabernetesinternaldeviceplan.ActionKind,
) bool {
	for _, action := range actions {
		if action.Kind == kind {
			return true
		}
	}

	return false
}

func containsRecordedCopy(actions []clabernetesinternaldeviceplan.Action, destination string) bool {
	for _, action := range actions {
		if action.Kind == clabernetesinternaldeviceplan.ActionFile &&
			action.Phase == clabernetesinternaldeviceplan.PhasePostStart &&
			action.File != nil && action.File.Destination == destination {
			return true
		}
	}

	return false
}

func TestPlanFailsClosedByGenericCapabilityNotKindIdentity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		code     clabernetesinternaldeviceplan.ErrorCode
		field    string
		behavior string
		value    any
	}{
		{
			name:     "environment file",
			code:     clabernetesinternaldeviceplan.ErrorUnsupported,
			field:    "env-files",
			behavior: "environment-file",
			value:    []string{"/input/env"},
		},
		{
			name:     "management address",
			code:     clabernetesinternaldeviceplan.ErrorMissingInput,
			field:    "mgmt-ipv4",
			behavior: "management-allocation",
			value:    "192.0.2.10/24",
		},
		{
			name:     "network namespace",
			code:     clabernetesinternaldeviceplan.ErrorMissingInput,
			field:    "network-mode",
			behavior: "network-namespace",
			value:    "host",
		},
		{
			name:     "runtime",
			code:     clabernetesinternaldeviceplan.ErrorUnsupported,
			field:    "runtime",
			behavior: "container-runtime",
			value:    "some-runtime",
		},
		{
			name:     "credentials",
			code:     clabernetesinternaldeviceplan.ErrorUnsupported,
			field:    "credentials",
			behavior: "credentials",
			value:    map[string]any{"username": "operator"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			input := singleNodeInput(syntheticKind, "example/future:1")
			definition := map[string]any{
				"kind":   syntheticKind,
				"image":  "example/future:1",
				tt.field: tt.value,
			}
			input.Nodes[0].Definition = mustJSON(t, definition)
			_, err := (clabernetesinternaldeviceplan.Adapter{
				Registry: newSyntheticRegistry(t),
				Revision: "generic-capability-v1",
			}).Plan(context.Background(), input)

			var planningErr *clabernetesinternaldeviceplan.Error
			if !errors.As(err, &planningErr) ||
				planningErr.Code != tt.code ||
				planningErr.Behavior != tt.behavior {
				t.Fatalf("Plan() error = %#v, want %s behavior %q", err, tt.code, tt.behavior)
			}

			if planningErr.NodeID != "node-a" {
				t.Fatalf("Plan() NodeID = %q, want node-a", planningErr.NodeID)
			}
		})
	}
}

func TestPlanMapsResolvedGroupAndDefinitionManagementWithoutKindDispatch(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Nodes = append(input.Nodes, clabernetesinternaldeviceplan.NodeInput{
		ID: "node-b", Name: "future-b", Kind: syntheticKind, GroupOwner: "node-a",
		Definition: mustJSON(t, map[string]any{
			"kind": syntheticKind, "image": "example/future:1",
			"network-mode": "container:router", "mgmt-ipv4": "192.0.2.11/24",
		}),
	})
	input.Images = append(input.Images, clabernetesinternaldeviceplan.ImageInput{
		NodeID: "node-b", SourceReference: "example/future:1",
		DigestReference: "example/future@sha256:" + strings.Repeat("a", 64),
		Platform:        clabernetesinternaldeviceplan.Platform{OS: "linux", Architecture: "amd64"},
	})
	input.Management = []clabernetesinternaldeviceplan.ManagementInput{
		{NodeID: "node-a", IPv4: "192.0.2.10/24"},
		{NodeID: "node-b", IPv4: "192.0.2.11/24"},
	}

	plan, err := (clabernetesinternaldeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "generic-group-v1",
	}).Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	if len(plan.Containers) != 2 ||
		plan.Containers[1].NamespaceOwnerID != plan.Containers[0].ID {
		t.Fatalf("group namespace mapping = %#v", plan.Containers)
	}

	if len(plan.Management) != 2 || plan.Management[1].IPv4 != "192.0.2.11/24" {
		t.Fatalf("management mapping = %#v", plan.Management)
	}
}

func TestPlanRecordsWhetherImagePullPolicyCameFromNodeIntent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		definition map[string]any
		explicit   bool
		policy     string
	}{
		{
			name: "package default",
			definition: map[string]any{
				"kind": syntheticKind, "image": "example/future:1",
			},
			policy: "IfNotPresent",
		},
		{
			name: "explicit node policy",
			definition: map[string]any{
				"kind": syntheticKind, "image": "example/future:1", "image-pull-policy": "Never",
			},
			explicit: true,
			policy:   "Never",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			input := singleNodeInput(syntheticKind, "example/future:1")
			input.Nodes[0].Definition = mustJSON(t, test.definition)

			plan, err := (clabernetesinternaldeviceplan.Adapter{
				Registry: newSyntheticRegistry(t), Revision: "image-pull-origin-v1",
			}).Plan(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}

			if len(plan.Containers) != 1 {
				t.Fatalf("planned containers = %#v", plan.Containers)
			}

			container := plan.Containers[0]
			if container.ImagePullPolicyExplicit != test.explicit ||
				container.ImagePullPolicy != test.policy {
				t.Fatalf(
					"image pull policy = %q explicit=%t, want %q explicit=%t",
					container.ImagePullPolicy,
					container.ImagePullPolicyExplicit,
					test.policy,
					test.explicit,
				)
			}
		})
	}
}

func TestPlanMapsExtendedNodeVocabularyWithoutKindDispatch(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Nodes[0].Definition = mustJSON(t, map[string]any{
		"kind":          syntheticKind,
		"image":         "example/future:1",
		"startup-delay": 7,
		"cpu":           1.5,
		"memory":        "512m",
		"aliases":       []string{"future-alt"},
		"healthcheck": map[string]any{
			"test":         []string{"CMD", "true"},
			"interval":     10,
			"timeout":      3,
			"retries":      2,
			"start-period": 5,
		},
	})

	plan, err := (clabernetesinternaldeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "extended-vocabulary-v1",
	}).Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	if len(plan.Containers) != 1 || len(plan.Nodes) != 1 {
		t.Fatalf("planned containers/nodes = %#v / %#v", plan.Containers, plan.Nodes)
	}

	container := plan.Containers[0]
	if container.StartupDelay != 7 {
		t.Fatalf("startup delay = %d, want 7", container.StartupDelay)
	}

	if container.Resources.CPULimit != "1.5" || container.Resources.MemoryLimit != "512000000" {
		t.Fatalf(
			"resources = %q cpu / %q memory, want 1.5 / 512000000 bytes",
			container.Resources.CPULimit,
			container.Resources.MemoryLimit,
		)
	}

	if container.Healthcheck == nil ||
		!slices.Equal(container.Healthcheck.Test, []string{"CMD", "true"}) ||
		container.Healthcheck.Interval != int64(10*time.Second) ||
		container.Healthcheck.Timeout != int64(3*time.Second) ||
		container.Healthcheck.Retries != 2 ||
		container.Healthcheck.StartPeriod != int64(5*time.Second) {
		t.Fatalf("healthcheck = %#v, want declared healthcheck contract", container.Healthcheck)
	}

	if !slices.Equal(plan.Nodes[0].Aliases, []string{"future-alt"}) {
		t.Fatalf("aliases = %#v, want [future-alt]", plan.Nodes[0].Aliases)
	}
}

func TestPlanFailsClosedOnUnparseableMemoryLimit(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Nodes[0].Definition = mustJSON(t, map[string]any{
		"kind": syntheticKind, "image": "example/future:1", "memory": "watermelon",
	})

	_, err := (clabernetesinternaldeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "extended-vocabulary-v1",
	}).Plan(context.Background(), input)
	if err == nil || !strings.Contains(err.Error(), "memory") {
		t.Fatalf("expected structured memory planning failure, got %v", err)
	}
}

func TestPlanMapsRecordedComponentContainersWithoutKindDispatch(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Nodes[0].Type = "multi-container-test"
	input.Nodes[0].Definition = mustJSON(t, map[string]any{
		"kind": syntheticKind, "type": "multi-container-test", "image": "example/future:1",
	})
	input.Images = append(input.Images, clabernetesinternaldeviceplan.ImageInput{
		NodeID:          "node-a",
		ComponentID:     "component-a",
		SourceReference: "example/future-component:1",
		DigestReference: "example/future-component@sha256:" + strings.Repeat("b", 64),
		Platform:        clabernetesinternaldeviceplan.Platform{OS: "linux", Architecture: "amd64"},
	})

	plan, err := (clabernetesinternaldeviceplan.Adapter{
		Registry: newSyntheticRegistry(t),
		Revision: "generic-components-v1",
	}).Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	if got, want := len(plan.Nodes[0].ContainerIDs), 2; got != want {
		t.Fatalf("planned container IDs = %#v, want %d", plan.Nodes[0].ContainerIDs, want)
	}

	if got, want := len(plan.Containers), 2; got != want {
		t.Fatalf("planned containers = %#v, want %d", plan.Containers, want)
	}

	var root, component *clabernetesinternaldeviceplan.ContainerPlan

	for index := range plan.Containers {
		container := &plan.Containers[index]
		if container.Image == "example/future-component:1" {
			component = container
		} else {
			root = container
		}
	}

	if root == nil || component == nil {
		t.Fatalf("component images were not preserved: %#v", plan.Containers)
	}

	if component.ComponentID != "component-a" || component.NamespaceOwnerID != root.ID {
		t.Fatalf("component namespace mapping = %#v, root = %#v", component, root)
	}

	if got, want := plan.Nodes[0].ReadinessContainerIDs, []string{root.ID}; !reflect.DeepEqual(
		got,
		want,
	) {
		t.Fatalf("package component readiness inventory = %#v, want %#v", got, want)
	}
}

func TestPlanTargetsImportedHooksAtPackageDeclaredExecContainer(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Nodes[0].Type = "component-exec-target-test"
	input.Nodes[0].Definition = mustJSON(t, map[string]any{
		"kind": syntheticKind, "type": input.Nodes[0].Type, "image": "example/future:1",
	})
	input.Images = append(input.Images, clabernetesinternaldeviceplan.ImageInput{
		NodeID:          "node-a",
		ComponentID:     "component-a",
		SourceReference: "example/future-component:1",
		DigestReference: "example/future-component@sha256:" + strings.Repeat("b", 64),
		Platform:        clabernetesinternaldeviceplan.Platform{OS: "linux", Architecture: "amd64"},
	})

	plan, err := (clabernetesinternaldeviceplan.Adapter{
		Registry: newSyntheticRegistry(t),
		Revision: "exec-target-v1",
	}).Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	var declared string

	for index := range plan.Containers {
		if plan.Containers[index].Image == "example/future-component:1" {
			declared = plan.Containers[index].ID
		}
	}

	if declared == "" {
		t.Fatalf("declared exec-target container was not planned: %#v", plan.Containers)
	}

	verified := 0

	for _, action := range plan.Actions {
		switch action.Kind { //nolint:exhaustive // Only imported hook targeting is asserted here.
		case clabernetesinternaldeviceplan.ActionImportedPostDeploy,
			clabernetesinternaldeviceplan.ActionImportedReadiness:
			if action.Target.ContainerID != declared {
				t.Fatalf(
					"imported hook %s targets %s, want package-declared %s",
					action.Kind,
					action.Target.ContainerID,
					declared,
				)
			}

			verified++
		}
	}

	if verified != 2 {
		t.Fatalf("imported hook actions verified = %d, want 2", verified)
	}
}

func TestPlanFailsByGenericCapabilityForCrossContainerDeploymentOrder(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Nodes[0].Type = "multi-container-operations-test"
	input.Nodes[0].Definition = mustJSON(t, map[string]any{
		"kind": syntheticKind, "type": input.Nodes[0].Type, "image": "example/future:1",
	})
	input.Images = append(input.Images, clabernetesinternaldeviceplan.ImageInput{
		NodeID:          "node-a",
		ComponentID:     "component-a",
		SourceReference: "example/future-component:1",
		DigestReference: "example/future-component@sha256:" + strings.Repeat("b", 64),
		Platform:        clabernetesinternaldeviceplan.Platform{OS: "linux", Architecture: "amd64"},
	})
	_, err := (clabernetesinternaldeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "cross-container-operation-plan-v1",
	}).Plan(context.Background(), input)

	var capabilityErr *clabernetesinternaldeviceplan.Error
	if !errors.As(err, &capabilityErr) ||
		capabilityErr.Code != clabernetesinternaldeviceplan.ErrorUnsupported ||
		capabilityErr.NodeID != input.Nodes[0].ID ||
		capabilityErr.Field != "deployment.operations.target" ||
		capabilityErr.Behavior != "cross-container-lifecycle" {
		t.Fatalf("Plan() capability error = %#v, %v", capabilityErr, err)
	}
}

func TestPlanPreservesImportedManagementInterfaceDefault(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Management = []clabernetesinternaldeviceplan.ManagementInput{{
		NodeID: "node-a", IPv4: "192.0.2.10/24", IPv4Gateway: "192.0.2.1",
	}}

	plan, err := (clabernetesinternaldeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "generic-management-v1",
	}).Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	// An allocated identity is realized by interposition; the imported interface default flows
	// into the derived device-leg contract instead of the direct interface selection.
	if len(plan.Management) != 1 ||
		plan.Management[0].InterfaceSelector !=
			clabernetesinternaldeviceplan.ManagementInterfaceInterposed ||
		plan.Management[0].Interposition == nil ||
		plan.Management[0].Interposition.DeviceInterface != "imported-mgmt" {
		t.Fatalf("management plan = %#v, want imported interface contract", plan.Management)
	}

	// Without an allocation the package-declared interface identity still rides the plan.
	unallocated := singleNodeInput(syntheticKind, "example/future:1")

	unallocatedPlan, err := (clabernetesinternaldeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "generic-management-v1",
	}).Plan(context.Background(), unallocated)
	if err != nil {
		t.Fatal(err)
	}

	if len(unallocatedPlan.Management) != 1 ||
		unallocatedPlan.Management[0].InterfaceName != "imported-mgmt" ||
		unallocatedPlan.Management[0].Interposition != nil {
		t.Fatalf(
			"unallocated management plan = %#v, want imported interface default",
			unallocatedPlan.Management,
		)
	}
}

func TestPlanMergesManagementInboundPortsIntoInterposition(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Nodes[0].Definition = mustJSON(t, map[string]any{
		"kind": syntheticKind, "image": "example/future:1", "ports": []string{"9000/tcp"},
	})
	input.Management = []clabernetesinternaldeviceplan.ManagementInput{{
		NodeID: "node-a", IPv4: "192.0.2.10/24", IPv4Gateway: "192.0.2.1",
		InboundPorts: []clabernetesinternaldeviceplan.Port{
			// A declared container port is authoritative -- the matching controller port must
			// not duplicate its translation.
			{Number: 9000, Protocol: "TCP"},
			{Number: 22, Protocol: "TCP"},
			{Number: 161, Protocol: "UDP"},
		},
	}}

	plan, err := (clabernetesinternaldeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "management-inbound-ports-v1",
	}).Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	if len(plan.Management) != 1 || plan.Management[0].Interposition == nil {
		t.Fatalf("management plan = %#v, want interposition contract", plan.Management)
	}

	ports := slices.Clone(plan.Management[0].Interposition.InboundPorts)
	slices.SortFunc(ports, func(left, right clabernetesinternaldeviceplan.ManagementPortMap) int {
		if compared := strings.Compare(left.Protocol, right.Protocol); compared != 0 {
			return compared
		}

		return int(left.PodPort) - int(right.PodPort)
	})

	want := []clabernetesinternaldeviceplan.ManagementPortMap{
		{Protocol: "tcp", PodPort: 22, DevicePort: 22},
		{Protocol: "tcp", PodPort: 9000, DevicePort: 9000},
		{Protocol: "udp", PodPort: 161, DevicePort: 161},
	}
	if !reflect.DeepEqual(ports, want) {
		t.Fatalf("interposition inbound ports = %#v, want %#v", ports, want)
	}
}

func TestPlanCarriesManagementMeshIntoInterposition(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Management = []clabernetesinternaldeviceplan.ManagementInput{{
		NodeID: "node-a", IPv4: "192.0.2.10/24", IPv4Gateway: "192.0.2.1",
		Mesh: &clabernetesinternaldeviceplan.ManagementMesh{
			TunnelID:    16_000_001,
			GatewayMAC:  "02:c9:aa:bb:cc:dd",
			PeerService: "c9s-management-mesh",
		},
	}}

	plan, err := (clabernetesinternaldeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "management-mesh-v1",
	}).Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	if len(plan.Management) != 1 || plan.Management[0].Interposition == nil ||
		!reflect.DeepEqual(plan.Management[0].Interposition.Mesh, input.Management[0].Mesh) {
		t.Fatalf("management plan = %#v, want carried mesh contract", plan.Management)
	}
}

func TestPlanRejectsInvalidManagementMesh(t *testing.T) {
	t.Parallel()

	invalid := []clabernetesinternaldeviceplan.ManagementMesh{
		{TunnelID: 0, GatewayMAC: "02:c9:aa:bb:cc:dd", PeerService: "c9s-management-mesh"},
		{TunnelID: 1 << 24, GatewayMAC: "02:c9:aa:bb:cc:dd", PeerService: "c9s-management-mesh"},
		{TunnelID: 7, GatewayMAC: "not-a-mac", PeerService: "c9s-management-mesh"},
		{TunnelID: 7, GatewayMAC: "02:c9:aa:bb:cc:dd", PeerService: ""},
	}

	for index, mesh := range invalid {
		input := singleNodeInput(syntheticKind, "example/future:1")
		input.Management = []clabernetesinternaldeviceplan.ManagementInput{{
			NodeID: "node-a", IPv4: "192.0.2.10/24", IPv4Gateway: "192.0.2.1",
			Mesh: &mesh,
		}}

		_, err := (clabernetesinternaldeviceplan.Adapter{
			Registry: newSyntheticRegistry(t), Revision: "management-mesh-v1",
		}).Plan(context.Background(), input)

		var planErr *clabernetesinternaldeviceplan.Error
		if !errors.As(err, &planErr) ||
			planErr.Code != clabernetesinternaldeviceplan.ErrorInvalidInput ||
			!strings.HasPrefix(planErr.Field, "management[0].mesh") {
			t.Fatalf("Plan() case %d error = %#v, %v", index, planErr, err)
		}
	}
}

func TestPlanRejectsInvalidManagementInboundPort(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Management = []clabernetesinternaldeviceplan.ManagementInput{{
		NodeID: "node-a", IPv4: "192.0.2.10/24", IPv4Gateway: "192.0.2.1",
		InboundPorts: []clabernetesinternaldeviceplan.Port{{Number: 0, Protocol: "TCP"}},
	}}

	_, err := (clabernetesinternaldeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "management-inbound-ports-v1",
	}).Plan(context.Background(), input)

	var planErr *clabernetesinternaldeviceplan.Error
	if !errors.As(err, &planErr) ||
		planErr.Code != clabernetesinternaldeviceplan.ErrorInvalidInput ||
		planErr.Field != "management[0].inboundPorts[0]" {
		t.Fatalf("Plan() error = %#v, %v", planErr, err)
	}
}

func richSyntheticInput(t *testing.T) clabernetesinternaldeviceplan.Input {
	t.Helper()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Nodes[0].Definition = mustJSON(t, map[string]any{
		"kind":            syntheticKind,
		"image":           "example/future:1",
		"group":           "routers",
		"position":        "1,2",
		"aliases":         []string{"future-a"},
		"startup-delay":   7,
		"entrypoint":      "/usr/bin/future --foreground",
		"cmd":             "serve --port 9000",
		"exec":            []string{"/usr/bin/configure --once"},
		"env":             map[string]string{"USER_SETTING": "enabled"},
		"labels":          map[string]string{"role": "router"},
		"privileged":      false,
		"cap-add":         []string{"NET_ADMIN"},
		"devices":         []string{"/dev/net/tun:/dev/net/tun:rwm"},
		"security-opts":   []string{"seccomp=RuntimeDefault"},
		"sysctls":         map[string]string{"net.ipv4.ip_forward": "1"},
		"tmpfs":           map[string]string{"/run": "rw,nosuid,size=64M"},
		"shm-size":        "128M",
		"ports":           []string{"9000/tcp"},
		"cpu":             1.5,
		"cpu-set":         "0-1",
		"memory":          "2GiB",
		"link-apply-mode": "live",
	})
	input.Images[0].Config = clabernetesinternaldeviceplan.ImageConfig{
		WorkingDir: "/work",
		StopSignal: "SIGTERM",
		Ports:      []clabernetesinternaldeviceplan.Port{{Number: 161, Protocol: "UDP"}},
	}
	input.Payloads = []clabernetesinternaldeviceplan.PayloadInput{{
		ID: "payload-a", NodeID: "node-a", Kind: clabernetesinternaldeviceplan.PayloadConfigMap,
		Reference: "lab/future:startup.cfg", Destination: "/etc/future/startup.cfg",
	}}
	input.Management = []clabernetesinternaldeviceplan.ManagementInput{{
		NodeID: "node-a", InterfaceName: "mgmt0", IPv4: "192.0.2.10/24",
		IPv4Gateway: "192.0.2.1",
	}}
	input.Interfaces = []clabernetesinternaldeviceplan.InterfaceInput{{
		ID: "interface-a", NodeID: "node-a", Name: "eth1", LinkID: "link-a",
		Connectivity: "same-pod", MTU: 1500,
	}}

	return input
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()

	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}

	return raw
}

func containsKeyValue(values []clabernetesinternaldeviceplan.KeyValue, name, value string) bool {
	for _, candidate := range values {
		if candidate.Name == name && candidate.Value == value {
			return true
		}
	}

	return false
}

func containsFileSource(
	files []clabernetesinternaldeviceplan.FilePlan,
	source clabernetesinternaldeviceplan.FileSourceKind,
) bool {
	for _, file := range files {
		if file.SourceKind == source {
			return true
		}
	}

	return false
}

func containsArtifactKind(
	files []clabernetesinternaldeviceplan.FilePlan,
	kind clabernetesinternaldeviceplan.ArtifactKind,
) bool {
	for _, file := range files {
		if file.ArtifactKind == kind {
			return true
		}
	}

	return false
}
