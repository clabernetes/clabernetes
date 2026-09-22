package directpod_test

import (
	"slices"
	"testing"

	clabernetesinternaldirectpod "github.com/clabernetes/clabernetes/internal/directpod"
	clabernetesinternaldirectruntime "github.com/clabernetes/clabernetes/internal/directruntime"
	k8scorev1 "k8s.io/api/core/v1"
)

func TestLifecycleCacheIsMountedOnlyByPreparation(t *testing.T) {
	t.Parallel()
	deployment, err := clabernetesinternaldirectpod.Render(
		renderablePlan(),
		clabernetesinternaldirectpod.Options{
			Name: "device-a", Namespace: "lab-a", PlanConfigMapName: "plan",
			InputConfigMapName: "input", PreparationImage: "example/c9s:1",
			ConnectivityImage: "example/c9s:1", EnableContainerStopSignals: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	pod := deployment.Spec.Template.Spec
	cacheName := ""
	for _, volume := range pod.Volumes {
		if volume.HostPath != nil &&
			volume.HostPath.Path == clabernetesinternaldirectruntime.LifecycleBinaryCachePath {
			cacheName = volume.Name
		}
	}
	if cacheName == "" {
		t.Fatal("runtime binary cache volume is missing")
	}
	mounted := 0
	containers := append(slices.Clone(pod.InitContainers), pod.Containers...)
	for _, container := range containers {
		for _, mount := range container.VolumeMounts {
			if mount.Name != cacheName {
				continue
			}
			mounted++
			if container.RestartPolicy != nil ||
				!slices.ContainsFunc(pod.InitContainers, func(init k8scorev1.Container) bool {
					return init.Name == container.Name
				}) ||
				!slices.Contains(container.Args, "--lifecycleBinaryCache") {
				t.Fatalf("cache exposed to non-preparation container %q", container.Name)
			}
		}
	}
	if mounted != 1 {
		t.Fatalf("cache mounted by %d containers, want only preparation", mounted)
	}
}
