package directpod_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	clabernetesinternaldirectpod "github.com/clabernetes/clabernetes/internal/directpod"
	k8scorev1 "k8s.io/api/core/v1"
)

func persistenceRenderOptions() clabernetesinternaldirectpod.Options {
	return clabernetesinternaldirectpod.Options{
		Name: "device-a", Namespace: "lab-a", PlanConfigMapName: "device-a-plan-abc",
		InputConfigMapName: "device-a-plan-input-abc",
		PreparationImage:   "example/c9s:1", ConnectivityImage: "example/c9s:1",
		EnableContainerStopSignals: true,
	}
}

func preparationContainer(
	t *testing.T,
	pod k8scorev1.PodSpec,
) *k8scorev1.Container {
	t.Helper()

	for index := range pod.InitContainers {
		if pod.InitContainers[index].Name == clabernetesinternaldirectpod.PreparationContainerName {
			return &pod.InitContainers[index]
		}
	}

	t.Fatalf("preparation init container is absent: %#v", pod.InitContainers)

	return nil
}

func TestRenderMarksPersistentNodesInPreparationArguments(t *testing.T) {
	t.Parallel()

	options := persistenceRenderOptions()
	options.PersistentVolumeClaims = map[string]string{"node-a": "device-a"}

	deployment, err := clabernetesinternaldirectpod.Render(renderablePlan(), options)
	if err != nil {
		t.Fatal(err)
	}

	preparation := preparationContainer(t, deployment.Spec.Template.Spec)

	index := slices.Index(preparation.Args, "--persistentNode")
	if index < 0 || index+1 >= len(preparation.Args) || preparation.Args[index+1] != "node-a" {
		t.Fatalf("preparation args have no persistent-node marker: %#v", preparation.Args)
	}
}

func TestRenderOmitsPersistentNodeMarkerWithoutClaims(t *testing.T) {
	t.Parallel()

	deployment, err := clabernetesinternaldirectpod.Render(
		renderablePlan(),
		persistenceRenderOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}

	preparation := preparationContainer(t, deployment.Spec.Template.Spec)

	if slices.Contains(preparation.Args, "--persistentNode") {
		t.Fatalf("ephemeral render carries a persistent-node marker: %#v", preparation.Args)
	}

	annotations := deployment.Spec.Template.Annotations
	if _, exists := annotations[clabernetesinternaldirectpod.DeviceStateResetsAnnotation]; exists {
		t.Fatalf("render without resets carries the reset annotation: %#v", annotations)
	}
}

func TestRenderProjectsDeviceStateResetsIntoPreparationAndTemplate(t *testing.T) {
	t.Parallel()

	options := persistenceRenderOptions()
	options.PersistentVolumeClaims = map[string]string{"node-a": "device-a"}
	options.DeviceStateResets = map[string]string{"node-a": "reset-1"}

	deployment, err := clabernetesinternaldirectpod.Render(renderablePlan(), options)
	if err != nil {
		t.Fatal(err)
	}

	preparation := preparationContainer(t, deployment.Spec.Template.Spec)

	index := slices.Index(preparation.Args, "--reset")
	if index < 0 || index+1 >= len(preparation.Args) ||
		preparation.Args[index+1] != "node-a=reset-1" {
		t.Fatalf("preparation args have no reset token: %#v", preparation.Args)
	}

	raw := deployment.Spec.Template.
		Annotations[clabernetesinternaldirectpod.DeviceStateResetsAnnotation]
	tokens := map[string]string{}

	if json.Unmarshal([]byte(raw), &tokens) != nil || tokens["node-a"] != "reset-1" {
		t.Fatalf("template reset annotation = %q", raw)
	}
}

func TestRenderMarksPersistentNodesInPostStartLifecycle(t *testing.T) {
	t.Parallel()

	plan := renderablePlan()
	plan.Actions = []clabernetesinternaldeviceplan.Action{{
		ID: "exec", Phase: clabernetesinternaldeviceplan.PhasePostStart, Order: 1,
		Target: clabernetesinternaldeviceplan.ActionTarget{
			NodeID: "node-a", ContainerID: "node-a/root",
		},
		Kind: clabernetesinternaldeviceplan.ActionExec,
		Exec: &clabernetesinternaldeviceplan.ExecAction{
			Command: []string{"/usr/bin/apply-config"}, Wait: true,
		},
	}}

	options := persistenceRenderOptions()
	options.PersistentVolumeClaims = map[string]string{"node-a": "device-a"}

	deployment, err := clabernetesinternaldirectpod.Render(plan, options)
	if err != nil {
		t.Fatal(err)
	}

	root := containerByImage(
		t,
		deployment.Spec.Template.Spec.Containers,
		"example/device@sha256:"+strings.Repeat("a", 64),
	)

	if root == nil || root.Lifecycle == nil || root.Lifecycle.PostStart == nil ||
		root.Lifecycle.PostStart.Exec == nil {
		t.Fatalf("PostStart lifecycle is absent: %#v", root)
	}

	command := root.Lifecycle.PostStart.Exec.Command

	index := slices.Index(command, "--persistentNode")
	if index < 0 || index+1 >= len(command) || command[index+1] != "node-a" {
		t.Fatalf("PostStart command has no persistent-node marker: %#v", command)
	}
}
