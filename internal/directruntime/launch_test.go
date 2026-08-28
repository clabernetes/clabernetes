package directruntime_test

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	clabernetesinternaldirectruntime "github.com/clabernetes/clabernetes/internal/directruntime"
)

var errHostsFileSealed = errors.New("hosts file is sealed")

type recordingLaunchOperations struct {
	source      string
	destination string
	filesystem  string
	options     []string
	delays      []time.Duration
	argv        []string
	fileLimits  []uint64
	hostname    string
	files       map[string][]byte
	updateErr   error
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

func (r *recordingLaunchOperations) UpdateFile(
	path string,
	update func(current []byte) (updated []byte, write bool),
) error {
	if r.updateErr != nil {
		return r.updateErr
	}

	updated, write := update(r.files[path])
	if write {
		if r.files == nil {
			r.files = map[string][]byte{}
		}

		r.files[path] = updated
	}

	return nil
}

func (r *recordingLaunchOperations) ReadFile(path string) ([]byte, error) {
	content, exists := r.files[path]
	if !exists {
		return nil, os.ErrNotExist
	}

	return content, nil
}

func (r *recordingLaunchOperations) Hostname() (string, error) {
	if r.hostname == "" {
		return "test-host", nil
	}

	return r.hostname, nil
}

func (r *recordingLaunchOperations) LimitOpenFiles(limit uint64) error {
	r.fileLimits = append(r.fileLimits, limit)

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
	plan.Volumes = []clabernetesinternaldeviceplan.VolumePlan{{
		ID: "tmpfs", NodeID: "node-a", Kind: clabernetesinternaldeviceplan.VolumeEmptyDir,
		Medium: "Memory", Size: "8000000",
	}}
	plan.Mounts = []clabernetesinternaldeviceplan.MountPlan{{
		ID: "tmpfs/mount", ContainerID: "container-a", VolumeID: "tmpfs",
		Destination: "/run/package",
	}}
	plan.Containers[0].MountIDs = []string{"tmpfs/mount"}
	plan.Actions = []clabernetesinternaldeviceplan.Action{{
		ID: "mount", Phase: clabernetesinternaldeviceplan.PhasePreStart,
		Target: clabernetesinternaldeviceplan.ActionTarget{
			NodeID: "node-a", ContainerID: "container-a",
		},
		Kind: clabernetesinternaldeviceplan.ActionMount,
		Mount: &clabernetesinternaldeviceplan.MountAction{
			MountID: "tmpfs/mount", Filesystem: "tmpfs", Source: "tmpfs",
			Options: []string{"rw", "nosuid", "nodev", "noexec", "size=8000000"},
		},
	}}

	operations := &recordingLaunchOperations{}
	if err := clabernetesinternaldirectruntime.RunLaunchWithOperations(
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

func TestRunLaunchPrependsNodeIdentityAheadOfKubeletHostsEntry(t *testing.T) {
	t.Parallel()

	kubeletHosts := "# Kubernetes-managed hosts file.\n" +
		"127.0.0.1\tlocalhost\n" +
		"172.16.79.171\tnode-a\n" +
		"# Entries added by HostAliases.\n" +
		"172.90.90.42\tpeer-b\n"

	plan := lifecycleTestPlan()
	plan.Containers[0].ImageCommand = []string{"run"}
	plan.Management = []clabernetesinternaldeviceplan.ManagementPlan{{
		ID: "management/node-a", NodeID: "node-a", InterfaceName: "mgmt0",
		IPv4: "172.90.90.41/24", IPv6: "fd00:90::41/64",
	}}

	operations := &recordingLaunchOperations{
		hostname: "node-a",
		files:    map[string][]byte{"/etc/hosts": []byte(kubeletHosts)},
	}
	if err := clabernetesinternaldirectruntime.RunLaunchWithOperations(
		plan,
		"container-a",
		operations,
	); err != nil {
		t.Fatal(err)
	}

	want := "172.90.90.41\tnode-a\t# c9s-node-identity\n" +
		"fd00:90::41\tnode-a\t# c9s-node-identity\n" +
		kubeletHosts
	if got := string(operations.files["/etc/hosts"]); got != want {
		t.Fatalf("hosts content = %q, want %q", got, want)
	}

	// A container restart re-runs the launch against already-owned content; the rewrite must
	// be idempotent rather than stacking identity lines.
	if err := clabernetesinternaldirectruntime.RunLaunchWithOperations(
		plan,
		"container-a",
		operations,
	); err != nil {
		t.Fatal(err)
	}

	if got := string(operations.files["/etc/hosts"]); got != want {
		t.Fatalf("hosts content after relaunch = %q, want %q", got, want)
	}
}

func TestRunLaunchRealizesPeerDirectoryAheadOfKubeletContent(t *testing.T) {
	t.Parallel()

	directory, err := clabernetesinternaldirectruntime.RenderPeerDirectory(
		[]clabernetesinternaldirectruntime.PeerIdentity{
			// The own node's name is identity-owned, but its component alias still resolves.
			{Name: "node-a", IPv4: "172.90.90.41", Aliases: []string{"node-a-a"}},
			{Name: "r9", IPv4: "172.90.90.19", IPv6: "fd00:90::19"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	kubeletHosts := "# Kubernetes-managed hosts file.\n172.16.79.171\tnode-a\n"

	plan := lifecycleTestPlan()
	plan.Containers[0].ImageCommand = []string{"run"}
	plan.Management = []clabernetesinternaldeviceplan.ManagementPlan{{
		ID: "management/node-a", NodeID: "node-a", InterfaceName: "mgmt0",
		IPv4: "172.90.90.41/24",
	}}

	operations := &recordingLaunchOperations{
		hostname: "node-a",
		files: map[string][]byte{
			"/etc/hosts": []byte(kubeletHosts),
			"/var/lib/clabernetes/peer-directory/peers.json": directory,
		},
	}
	if err = clabernetesinternaldirectruntime.RunLaunchWithOperations(
		plan,
		"container-a",
		operations,
	); err != nil {
		t.Fatal(err)
	}

	want := "172.90.90.41\tnode-a\t# c9s-node-identity\n" +
		"172.90.90.41\tnode-a-a\t# c9s-peer\n" +
		"172.90.90.19\tr9\t# c9s-peer\n" +
		"fd00:90::19\tr9\t# c9s-peer\n" +
		kubeletHosts
	if got := string(operations.files["/etc/hosts"]); got != want {
		t.Fatalf("hosts content = %q, want %q", got, want)
	}
}

func TestRunLaunchSkipsNodeIdentityWithoutManagementAddress(t *testing.T) {
	t.Parallel()

	plan := lifecycleTestPlan()
	plan.Containers[0].ImageCommand = []string{"run"}

	operations := &recordingLaunchOperations{hostname: "node-a"}
	if err := clabernetesinternaldirectruntime.RunLaunchWithOperations(
		plan,
		"container-a",
		operations,
	); err != nil {
		t.Fatal(err)
	}

	if len(operations.files) != 0 {
		t.Fatalf("hosts files written without a management address: %#v", operations.files)
	}
}

func TestRunLaunchStartsApplicationWhenNodeIdentityRewriteFails(t *testing.T) {
	t.Parallel()

	plan := lifecycleTestPlan()
	plan.Containers[0].ImageCommand = []string{"run"}
	plan.Management = []clabernetesinternaldeviceplan.ManagementPlan{{
		ID: "management/node-a", NodeID: "node-a", InterfaceName: "mgmt0",
		IPv4: "172.90.90.41/24",
	}}

	operations := &recordingLaunchOperations{
		hostname:  "node-a",
		updateErr: errHostsFileSealed,
	}
	if err := clabernetesinternaldirectruntime.RunLaunchWithOperations(
		plan,
		"container-a",
		operations,
	); err != nil {
		t.Fatal(err)
	}

	if want := []string{"run"}; !reflect.DeepEqual(operations.argv, want) {
		t.Fatalf("application argv = %#v, want %#v", operations.argv, want)
	}
}

func TestRunLaunchRejectsCrossNodeMount(t *testing.T) {
	t.Parallel()

	plan := lifecycleTestPlan()
	plan.Containers[0].ImageCommand = []string{"run"}
	plan.Volumes = []clabernetesinternaldeviceplan.VolumePlan{{
		ID: "tmpfs", NodeID: "node-a", Kind: clabernetesinternaldeviceplan.VolumeEmptyDir,
	}}
	plan.Mounts = []clabernetesinternaldeviceplan.MountPlan{{
		ID: "tmpfs/mount", ContainerID: "container-a", VolumeID: "tmpfs", Destination: "/run",
	}}
	plan.Containers[0].MountIDs = []string{"tmpfs/mount"}
	plan.Actions = []clabernetesinternaldeviceplan.Action{{
		ID: "mount", Phase: clabernetesinternaldeviceplan.PhasePreStart,
		Target: clabernetesinternaldeviceplan.ActionTarget{
			NodeID: "foreign", ContainerID: "container-a",
		},
		Kind: clabernetesinternaldeviceplan.ActionMount,
		Mount: &clabernetesinternaldeviceplan.MountAction{
			MountID: "tmpfs/mount", Filesystem: "tmpfs", Source: "tmpfs",
		},
	}}

	err := clabernetesinternaldirectruntime.RunLaunchWithOperations(
		plan,
		"container-a",
		&recordingLaunchOperations{},
	)
	if err == nil || !strings.Contains(err.Error(), "unknown Node") {
		t.Fatalf("RunLaunchWithOperations() error = %v", err)
	}
}
