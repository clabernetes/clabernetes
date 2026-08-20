package directpod_test

import (
	"strings"
	"testing"

	clabernetesdirectpod "github.com/clabernetes/clabernetes/internal/directpod"
)

// TestRenderShipsNoNestedRuntimeSurface is the negative half of the nested-runtime removal: a
// rendered direct workload must not reference the retired launcher image, its mode selector, or
// a container runtime socket. The nested launcher is deleted, so any reappearance of these
// identifiers is a regression, not a configuration choice.
func TestRenderShipsNoNestedRuntimeSurface(t *testing.T) {
	t.Parallel()

	deployment, err := clabernetesdirectpod.Render(renderablePlan(), clabernetesdirectpod.Options{
		Name: "device-a", Namespace: "lab-a", PlanConfigMapName: "device-a-plan-abc",
		InputConfigMapName:                "device-a-plan-input-abc",
		ConnectivityRevisionConfigMapName: "device-a-connectivity",
		PreparationImage:                  "example/c9s@sha256:1111",
		ConnectivityImage:                 "example/c9s@sha256:1111",
		EnableContainerStopSignals:        true,
	})
	if err != nil {
		t.Fatal(err)
	}

	spec := deployment.Spec.Template.Spec

	for _, container := range append(spec.InitContainers, spec.Containers...) {
		if strings.Contains(container.Image, "clabernetes-launcher") {
			t.Fatalf("container %q references the retired launcher image", container.Name)
		}

		for _, env := range container.Env {
			switch env.Name {
			case "DEVICE_RUNTIME_MODE", "LAUNCHER_IMAGE":
				t.Fatalf(
					"container %q carries retired nested-runtime env %q",
					container.Name,
					env.Name,
				)
			}
		}
	}

	for _, volume := range spec.Volumes {
		if volume.HostPath == nil {
			continue
		}

		if strings.Contains(volume.HostPath.Path, "docker.sock") ||
			strings.Contains(volume.HostPath.Path, "containerd.sock") {
			t.Fatalf(
				"volume %q mounts a container runtime socket: %s",
				volume.Name,
				volume.HostPath.Path,
			)
		}
	}
}
