package directruntime_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	clabernetesdeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	clabernetesdirectruntime "github.com/clabernetes/clabernetes/internal/directruntime"
)

type recordingLaunchOperations struct {
	source      string
	destination string
	filesystem  string
	options     []string
	delays      []time.Duration
	argv        []string
}

func (r *recordingLaunchOperations) Delay(duration time.Duration) error {
	r.delays = append(r.delays, duration)

	return nil
}

func (r *recordingLaunchOperations) MountFilesystem(
	source,
	destination,
	filesystem string,
	options []string,
) error {
	r.source = source
	r.destination = destination
	r.filesystem = filesystem
	r.options = append([]string(nil), options...)

	return nil
}

func (r *recordingLaunchOperations) Exec(argv []string) error {
	r.argv = append([]string(nil), argv...)

	return nil
}

func TestRunLaunchAppliesGenericMountBeforeImageProcess(t *testing.T) {
	t.Parallel()

	plan := lifecycleTestPlan()
	plan.Containers[0].ImageEntrypoint = []string{"/usr/bin/image-entrypoint"}
	plan.Containers[0].ImageCommand = []string{"serve", "--foreground"}
	plan.Containers[0].StartupDelay = 7
	plan.Volumes = []clabernetesdeviceplan.VolumePlan{{
		ID: "tmpfs", NodeID: "node-a", Kind: clabernetesdeviceplan.VolumeEmptyDir,
		Medium: "Memory", Size: "8000000",
	}}
	plan.Mounts = []clabernetesdeviceplan.MountPlan{{
		ID: "tmpfs/mount", ContainerID: "container-a", VolumeID: "tmpfs",
		Destination: "/run/package",
	}}
	plan.Containers[0].MountIDs = []string{"tmpfs/mount"}
	plan.Actions = []clabernetesdeviceplan.Action{{
		ID: "mount", Phase: clabernetesdeviceplan.PhasePreStart,
		Target: clabernetesdeviceplan.ActionTarget{
			NodeID: "node-a", ContainerID: "container-a",
		},
		Kind: clabernetesdeviceplan.ActionMount,
		Mount: &clabernetesdeviceplan.MountAction{
			MountID: "tmpfs/mount", Filesystem: "tmpfs", Source: "tmpfs",
			Options: []string{"rw", "nosuid", "nodev", "noexec", "size=8000000"},
		},
	}}
	operations := &recordingLaunchOperations{}
	if err := clabernetesdirectruntime.RunLaunchWithOperations(
		plan,
		"container-a",
		operations,
	); err != nil {
		t.Fatal(err)
	}
	if operations.source != "tmpfs" || operations.filesystem != "tmpfs" ||
		operations.destination != "/run/package" ||
		!reflect.DeepEqual(
			operations.options,
			[]string{"rw", "nosuid", "nodev", "noexec", "size=8000000"},
		) {
		t.Fatalf("mount operation = %#v", operations)
	}
	if !reflect.DeepEqual(operations.delays, []time.Duration{7 * time.Second}) {
		t.Fatalf("startup delays = %#v", operations.delays)
	}
	if want := []string{"/usr/bin/image-entrypoint", "serve", "--foreground"}; !reflect.DeepEqual(
		operations.argv,
		want,
	) {
		t.Fatalf("application argv = %#v, want %#v", operations.argv, want)
	}
}

func TestRunLaunchRejectsCrossNodeMount(t *testing.T) {
	t.Parallel()

	plan := lifecycleTestPlan()
	plan.Containers[0].ImageCommand = []string{"run"}
	plan.Volumes = []clabernetesdeviceplan.VolumePlan{{
		ID: "tmpfs", NodeID: "node-a", Kind: clabernetesdeviceplan.VolumeEmptyDir,
	}}
	plan.Mounts = []clabernetesdeviceplan.MountPlan{{
		ID: "tmpfs/mount", ContainerID: "container-a", VolumeID: "tmpfs", Destination: "/run",
	}}
	plan.Containers[0].MountIDs = []string{"tmpfs/mount"}
	plan.Actions = []clabernetesdeviceplan.Action{{
		ID: "mount", Phase: clabernetesdeviceplan.PhasePreStart,
		Target: clabernetesdeviceplan.ActionTarget{
			NodeID: "foreign", ContainerID: "container-a",
		},
		Kind: clabernetesdeviceplan.ActionMount,
		Mount: &clabernetesdeviceplan.MountAction{
			MountID: "tmpfs/mount", Filesystem: "tmpfs", Source: "tmpfs",
		},
	}}
	err := clabernetesdirectruntime.RunLaunchWithOperations(
		plan,
		"container-a",
		&recordingLaunchOperations{},
	)
	if err == nil || !strings.Contains(err.Error(), "unknown Node") {
		t.Fatalf("RunLaunchWithOperations() error = %v", err)
	}
}
