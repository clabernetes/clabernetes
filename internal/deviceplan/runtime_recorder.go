package deviceplan

import (
	"context"
	"fmt"
	"io"
	"maps"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	clabexec "github.com/srl-labs/containerlab/exec"
	clabruntime "github.com/srl-labs/containerlab/runtime"
	clabtypes "github.com/srl-labs/containerlab/types"
)

var _ clabruntime.ContainerRuntime = (*recordingRuntime)(nil)

type recordingRuntime struct {
	mu                   sync.Mutex
	mgmt                 *clabtypes.MgmtNet
	config               clabruntime.RuntimeConfig
	images               map[string]*clabruntime.ImageInspect
	calls                []string
	containers           []RecordedContainer
	execs                []RecordedExec
	copies               []RecordedCopy
	stdins               []RecordedStdin
	hostsPaths           map[string]string
	artifactRoot         string
	nextActionOrder      int
	recordingMutations   bool
	readinessObservation bool
	allowMissingImages   bool
	missingImages        map[string]bool
	failure              error
}

// RecordedContainer is one generic container creation observed through containerlab's runtime
// interface. RuntimeID is the value returned to the imported implementation and Started records a
// matching start request.
type RecordedContainer struct {
	RuntimeID string
	Config    *clabtypes.NodeConfig
	Started   bool
}

// RecordedExec is one process operation requested through the imported runtime boundary.
type RecordedExec struct {
	RuntimeID string
	Command   []string
	Wait      bool
	Order     int
}

// RecordedCopy is one file-copy operation requested through the imported runtime boundary.
// ArtifactPath identifies the source snapshot inside the controlled generated-artifact tree.
type RecordedCopy struct {
	RuntimeID    string
	Destination  string
	ArtifactPath string
	WriteMode    FileWriteMode
	Order        int
}

// RecordedStdin is one byte stream requested through the imported runtime boundary. The bytes are
// kept in the controlled artifact tree and represented in the plan only by artifact identity.
type RecordedStdin struct {
	RuntimeID    string
	ArtifactPath string
	Order        int
}

func newRecordingRuntime(
	images []ImageInput,
	management *ManagementInput,
	artifactRoot string,
) *recordingRuntime {
	runtime := &recordingRuntime{
		images:        map[string]*clabruntime.ImageInspect{},
		hostsPaths:    map[string]string{},
		missingImages: map[string]bool{},
		artifactRoot:  artifactRoot,
	}
	if management != nil {
		runtime.mgmt = &clabtypes.MgmtNet{
			IPv4Gw: management.IPv4Gateway,
			IPv6Gw: management.IPv6Gateway,
		}
	} else {
		runtime.mgmt = &clabtypes.MgmtNet{}
	}
	for _, image := range images {
		labels := map[string]string{}
		for _, label := range image.Config.Labels {
			labels[label.Name] = label.Value
		}
		inspect := &clabruntime.ImageInspect{Config: clabruntime.ImageConfig{Labels: labels}}
		runtime.images[image.SourceReference] = inspect
		runtime.images[image.DigestReference] = inspect
	}

	return runtime
}

func (r *recordingRuntime) AllowMissingImageMetadata() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.allowMissingImages = true
}

func (r *recordingRuntime) MissingImages() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]string, 0, len(r.missingImages))
	for reference := range r.missingImages {
		result = append(result, reference)
	}
	slices.Sort(result)

	return result
}

func (r *recordingRuntime) Calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.calls)
}

func (r *recordingRuntime) Failure() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.failure
}

func (r *recordingRuntime) BeginMutationRecording() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recordingMutations = true
}

func (r *recordingRuntime) BeginReadinessObservation() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.readinessObservation = true
}

func (r *recordingRuntime) Containers() []RecordedContainer {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.containers)
}

func (r *recordingRuntime) Execs() []RecordedExec {
	r.mu.Lock()
	defer r.mu.Unlock()

	result := slices.Clone(r.execs)
	for index := range result {
		result[index].Command = slices.Clone(result[index].Command)
	}

	return result
}

func (r *recordingRuntime) Copies() []RecordedCopy {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.copies)
}

func (r *recordingRuntime) Stdins() []RecordedStdin {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.stdins)
}

func (r *recordingRuntime) allowed(operation string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, operation)
}

func (r *recordingRuntime) blocked(operation string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, operation)
	err := &Error{
		Code:     ErrorSideEffect,
		Behavior: operation,
		Message:  "imported code reached a forbidden runtime boundary",
	}
	if r.failure == nil {
		r.failure = err
	}

	return err
}

func (r *recordingRuntime) Init(...clabruntime.RuntimeOption) error {
	return r.blocked("runtime.Init")
}

func (r *recordingRuntime) Mgmt() *clabtypes.MgmtNet {
	r.allowed("runtime.Mgmt")

	return r.mgmt
}

func (r *recordingRuntime) WithConfig(config *clabruntime.RuntimeConfig) {
	r.allowed("runtime.WithConfig")
	if config != nil {
		r.config = *config
	}
}

func (r *recordingRuntime) WithMgmtNet(mgmt *clabtypes.MgmtNet) {
	r.allowed("runtime.WithMgmtNet")
	if mgmt != nil {
		copy := *mgmt
		copy.DriverOpts = maps.Clone(mgmt.DriverOpts)
		r.mgmt = &copy
	}
}

func (r *recordingRuntime) WithKeepMgmtNet() {
	r.allowed("runtime.WithKeepMgmtNet")
	r.config.KeepMgmtNet = true
}

func (r *recordingRuntime) CreateNet(context.Context) error {
	return r.blocked("runtime.CreateNet")
}

func (r *recordingRuntime) DeleteNet(context.Context) error {
	return r.blocked("runtime.DeleteNet")
}

func (r *recordingRuntime) PullImage(
	_ context.Context,
	imageName string,
	_ clabtypes.PullPolicyValue,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.recordingMutations {
		return r.blockedLocked("runtime.PullImage")
	}

	const operation = "runtime.PullImage"
	r.calls = append(r.calls, operation)
	if _, exists := r.images[imageName]; !exists {
		return r.failLocked(&Error{
			Code: ErrorMissingInput, Field: "images", Behavior: operation,
			Message: "explicit OCI metadata is unavailable for the requested image pull",
		})
	}

	return nil
}

func (r *recordingRuntime) CreateContainer(
	_ context.Context,
	config *clabtypes.NodeConfig,
) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.recordingMutations {
		return "", r.blockedLocked("runtime.CreateContainer")
	}
	if config == nil {
		return "", r.failLocked(&Error{
			Code: ErrorInvariant, Field: "container.config",
			Behavior: "runtime.CreateContainer", Message: "imported code supplied no container config",
		})
	}

	r.calls = append(r.calls, "runtime.CreateContainer")
	runtimeID := config.LongName
	if runtimeID == "" {
		runtimeID = config.ShortName
	}
	if runtimeID == "" {
		return "", r.failLocked(&Error{
			Code: ErrorInvariant, Field: "container.name",
			Behavior: "runtime.CreateContainer", Message: "imported container has no identity",
		})
	}
	for _, container := range r.containers {
		if container.RuntimeID == runtimeID {
			return "", r.failLocked(&Error{
				Code: ErrorInvariant, Field: "container.name",
				Behavior: "runtime.CreateContainer", Message: "imported container identity is duplicated",
			})
		}
	}
	r.containers = append(r.containers, RecordedContainer{RuntimeID: runtimeID, Config: config})

	return runtimeID, nil
}

func (r *recordingRuntime) StartContainer(
	_ context.Context,
	runtimeID string,
	_ clabruntime.Node,
) (any, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.recordingMutations {
		return nil, r.blockedLocked("runtime.StartContainer")
	}

	r.calls = append(r.calls, "runtime.StartContainer")
	for index := range r.containers {
		if r.containers[index].RuntimeID == runtimeID {
			r.containers[index].Started = true

			return runtimeID, nil
		}
	}

	return nil, r.failLocked(&Error{
		Code: ErrorInvariant, Field: "container.start",
		Behavior: "runtime.StartContainer", Message: "imported code started an unknown container",
	})
}

func (r *recordingRuntime) StopContainer(context.Context, string, clabtypes.Signal) error {
	return r.blocked("runtime.StopContainer")
}

func (r *recordingRuntime) PauseContainer(context.Context, string) error {
	return r.blocked("runtime.PauseContainer")
}

func (r *recordingRuntime) UnpauseContainer(context.Context, string) error {
	return r.blocked("runtime.UnpauseContainer")
}

func (r *recordingRuntime) ListContainers(
	_ context.Context,
	filters []*clabtypes.GenericFilter,
) ([]clabruntime.GenericContainer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.recordingMutations {
		return nil, r.blockedLocked("runtime.ListContainers")
	}

	const operation = "runtime.ListContainers"
	r.calls = append(r.calls, operation)
	result := make([]clabruntime.GenericContainer, 0, len(r.containers))
	for _, recorded := range r.containers {
		if !recordedContainerMatches(recorded, filters) {
			continue
		}
		state := "created"
		if recorded.Started {
			state = "running"
		}
		management := genericManagement(recorded.Config)
		result = append(result, clabruntime.GenericContainer{
			Names: []string{recorded.RuntimeID}, ID: recorded.RuntimeID,
			ShortID: recorded.RuntimeID, Image: recorded.Config.Image,
			State: state, Status: state, Labels: maps.Clone(recorded.Config.Labels),
			NetworkSettings: management, Runtime: r,
			Ports: slices.Clone(recorded.Config.ResultingPortBindings),
		})
	}

	return result, nil
}

func (r *recordingRuntime) GetNSPath(context.Context, string) (string, error) {
	return "", r.blocked("runtime.GetNSPath")
}

func (r *recordingRuntime) LogNonRunningContainerOutput(context.Context, string) {
	_ = r.blocked("runtime.LogNonRunningContainerOutput")
}

func (r *recordingRuntime) Exec(
	_ context.Context,
	runtimeID string,
	command *clabexec.ExecCmd,
) (*clabexec.ExecResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.recordingMutations {
		return nil, r.blockedLocked("runtime.Exec")
	}
	if err := r.recordExecLocked(runtimeID, command, true); err != nil {
		return nil, err
	}

	return clabexec.NewExecResult(command), nil
}

func (r *recordingRuntime) ExecNotWait(
	_ context.Context,
	runtimeID string,
	command *clabexec.ExecCmd,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.recordingMutations {
		return r.blockedLocked("runtime.ExecNotWait")
	}

	return r.recordExecLocked(runtimeID, command, false)
}

func (r *recordingRuntime) DeleteContainer(context.Context, string) error {
	return r.blocked("runtime.DeleteContainer")
}

func (r *recordingRuntime) Config() clabruntime.RuntimeConfig {
	r.allowed("runtime.Config")

	return r.config
}

func (r *recordingRuntime) GetName() string {
	r.allowed("runtime.GetName")

	return "clabernetes-plan-recorder"
}

func (r *recordingRuntime) GetHostsPath(_ context.Context, runtimeID string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.recordingMutations {
		return "", r.blockedLocked("runtime.GetHostsPath")
	}

	const operation = "runtime.GetHostsPath"
	r.calls = append(r.calls, operation)
	if !r.knownContainerLocked(runtimeID) {
		return "", r.failLocked(&Error{
			Code: ErrorInvariant, Field: "hosts.container", Behavior: operation,
			Message: "imported hosts-file access targets an unknown container",
		})
	}
	if existing := r.hostsPaths[runtimeID]; existing != "" {
		return filepath.Join(r.artifactRoot, filepath.FromSlash(existing)), nil
	}
	artifactPath := filepath.ToSlash(filepath.Join(
		".clabernetes-runtime-hosts",
		shortDigest(runtimeID),
	))
	controlledPath := filepath.Join(r.artifactRoot, filepath.FromSlash(artifactPath))
	if err := os.MkdirAll(filepath.Dir(controlledPath), 0o700); err != nil {
		return "", r.failLocked(&Error{
			Code: ErrorSideEffect, Field: "hosts.snapshot", Behavior: operation,
			Message: "cannot create a controlled hosts-file artifact directory", cause: err,
		})
	}
	file, err := os.OpenFile(
		controlledPath,
		os.O_WRONLY|os.O_CREATE|os.O_EXCL,
		0o666,
	) //nolint:gosec // Imported hook expects an appendable hosts file in private scratch.
	if err != nil {
		return "", r.failLocked(&Error{
			Code: ErrorSideEffect, Field: "hosts.snapshot", Behavior: operation,
			Message: "cannot create a controlled hosts-file artifact", cause: err,
		})
	}
	if err = file.Close(); err != nil {
		return "", r.failLocked(&Error{
			Code: ErrorSideEffect, Field: "hosts.snapshot", Behavior: operation,
			Message: "cannot close a controlled hosts-file artifact", cause: err,
		})
	}
	r.hostsPaths[runtimeID] = artifactPath
	r.copies = append(r.copies, RecordedCopy{
		RuntimeID: runtimeID, Destination: "/etc/hosts", ArtifactPath: artifactPath,
		WriteMode: FileWriteAppend, Order: r.takeActionOrderLocked(),
	})

	return controlledPath, nil
}

func (r *recordingRuntime) GetContainerStatus(
	_ context.Context,
	runtimeID string,
) clabruntime.ContainerStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.readinessObservation && runtimeID != "" {
		r.calls = append(r.calls, "runtime.GetContainerStatus")

		return clabruntime.Running
	}
	if !r.recordingMutations {
		_ = r.blockedLocked("runtime.GetContainerStatus")

		return clabruntime.NotFound
	}
	r.calls = append(r.calls, "runtime.GetContainerStatus")
	for _, container := range r.containers {
		if container.RuntimeID != runtimeID {
			continue
		}
		if container.Started {
			return clabruntime.Running
		}

		return clabruntime.Created
	}

	return clabruntime.NotFound
}

func (r *recordingRuntime) IsHealthy(_ context.Context, runtimeID string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.readinessObservation && runtimeID != "" {
		r.calls = append(r.calls, "runtime.IsHealthy")

		return true, nil
	}
	if !r.recordingMutations {
		return false, r.blockedLocked("runtime.IsHealthy")
	}
	r.calls = append(r.calls, "runtime.IsHealthy")
	for _, container := range r.containers {
		if container.RuntimeID == runtimeID {
			return container.Started, nil
		}
	}

	return false, r.failLocked(&Error{
		Code: ErrorInvariant, Field: "health.container", Behavior: "runtime.IsHealthy",
		Message: "imported health observation targets an unknown container",
	})
}

func (r *recordingRuntime) WriteToStdinNoWait(
	_ context.Context,
	runtimeID string,
	content []byte,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.recordingMutations {
		return r.blockedLocked("runtime.WriteToStdinNoWait")
	}

	const operation = "runtime.WriteToStdinNoWait"
	r.calls = append(r.calls, operation)
	if !r.knownContainerLocked(runtimeID) {
		return r.failLocked(&Error{
			Code: ErrorInvariant, Field: "stdin.container", Behavior: operation,
			Message: "imported stdin write targets an unknown container",
		})
	}
	if len(content) == 0 {
		return r.failLocked(&Error{
			Code: ErrorInvariant, Field: "stdin.content", Behavior: operation,
			Message: "imported stdin write has no content",
		})
	}
	artifactPath := filepath.ToSlash(filepath.Join(
		".clabernetes-runtime-stdin",
		fmt.Sprintf("%06d", len(r.stdins)),
	))
	if err := r.writeArtifactLocked(
		operation,
		"stdin.snapshot",
		artifactPath,
		content,
		0o600,
	); err != nil {
		return err
	}
	r.stdins = append(r.stdins, RecordedStdin{
		RuntimeID: runtimeID, ArtifactPath: artifactPath, Order: r.takeActionOrderLocked(),
	})

	return nil
}

func (r *recordingRuntime) CheckConnection(context.Context) error {
	return r.blocked("runtime.CheckConnection")
}

func (r *recordingRuntime) GetRuntimeSocket() (string, error) {
	return "", r.blocked("runtime.GetRuntimeSocket")
}

func (r *recordingRuntime) GetCooCBindMounts() clabtypes.Binds {
	_ = r.blocked("runtime.GetCooCBindMounts")

	return nil
}

func (r *recordingRuntime) StreamLogs(context.Context, string) (io.ReadCloser, error) {
	return nil, r.blocked("runtime.StreamLogs")
}

func (r *recordingRuntime) StreamEvents(
	context.Context,
	clabruntime.EventStreamOptions,
) (<-chan clabruntime.ContainerEvent, <-chan error, error) {
	return nil, nil, r.blocked("runtime.StreamEvents")
}

func (r *recordingRuntime) InspectImage(
	_ context.Context,
	imageName string,
) (*clabruntime.ImageInspect, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, "runtime.InspectImage")
	image, ok := r.images[imageName]
	if !ok {
		if r.allowMissingImages {
			r.missingImages[imageName] = true

			return &clabruntime.ImageInspect{}, nil
		}
		err := &Error{
			Code:     ErrorMissingInput,
			Field:    "images",
			Behavior: "runtime.InspectImage",
			Message:  "explicit OCI metadata is unavailable for the requested image",
		}
		if r.failure == nil {
			r.failure = err
		}

		return nil, err
	}
	copy := *image
	copy.Config.Labels = maps.Clone(image.Config.Labels)

	return &copy, nil
}

func (r *recordingRuntime) CopyToContainer(
	_ context.Context,
	runtimeID,
	destination,
	source string,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.recordingMutations {
		return r.blockedLocked("runtime.CopyToContainer")
	}

	const operation = "runtime.CopyToContainer"
	r.calls = append(r.calls, operation)
	if !r.knownContainerLocked(runtimeID) {
		return r.failLocked(&Error{
			Code: ErrorInvariant, Field: "copy.container", Behavior: operation,
			Message: "imported runtime copy targets an unknown container",
		})
	}
	if destination == "" || source == "" {
		return r.failLocked(&Error{
			Code: ErrorInvariant, Field: "copy.path", Behavior: operation,
			Message: "imported runtime copy has an empty source or destination",
		})
	}
	info, err := os.Lstat(source)
	if err != nil {
		return r.failLocked(&Error{
			Code: ErrorMissingInput, Field: "copy.source", Behavior: operation,
			Message: "imported runtime copy source is unavailable", cause: err,
		})
	}
	if !info.Mode().IsRegular() {
		return r.failLocked(&Error{
			Code: ErrorUnsupported, Field: "copy.source", Behavior: operation,
			Message: "imported runtime copy source is not a regular file",
		})
	}
	content, err := os.ReadFile(
		source,
	) //nolint:gosec // Source is selected by imported hook in a disposable planning process.
	if err != nil {
		return r.failLocked(&Error{
			Code: ErrorMissingInput, Field: "copy.source", Behavior: operation,
			Message: "imported runtime copy source cannot be read", cause: err,
		})
	}
	artifactPath := ""
	relative, relativeErr := filepath.Rel(r.artifactRoot, source)
	if relativeErr == nil && relative != "." && relative != ".." &&
		!filepath.IsAbs(relative) && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		artifactPath = filepath.ToSlash(relative)
		r.copies = append(r.copies, RecordedCopy{
			RuntimeID: runtimeID, Destination: destination, ArtifactPath: artifactPath,
			WriteMode: FileWriteReplace, Order: r.takeActionOrderLocked(),
		})

		return nil
	}
	artifactPath = filepath.ToSlash(filepath.Join(
		".clabernetes-runtime-copies",
		fmt.Sprintf("%06d", len(r.copies)),
	))
	snapshotPath := filepath.Join(r.artifactRoot, filepath.FromSlash(artifactPath))
	if err = os.MkdirAll(filepath.Dir(snapshotPath), 0o700); err != nil {
		return r.failLocked(&Error{
			Code: ErrorSideEffect, Field: "copy.snapshot", Behavior: operation,
			Message: "cannot create a controlled runtime-copy artifact directory", cause: err,
		})
	}
	file, err := os.OpenFile(
		snapshotPath,
		os.O_WRONLY|os.O_CREATE|os.O_EXCL,
		info.Mode().Perm(),
	) //nolint:gosec // Imported mode is preserved inside private scratch.
	if err != nil {
		return r.failLocked(&Error{
			Code: ErrorSideEffect, Field: "copy.snapshot", Behavior: operation,
			Message: "cannot create a controlled runtime-copy artifact", cause: err,
		})
	}
	if _, err = file.Write(content); err == nil {
		err = file.Close()
	} else {
		_ = file.Close()
	}
	if err != nil {
		return r.failLocked(&Error{
			Code: ErrorSideEffect, Field: "copy.snapshot", Behavior: operation,
			Message: "cannot write a controlled runtime-copy artifact", cause: err,
		})
	}
	r.copies = append(r.copies, RecordedCopy{
		RuntimeID: runtimeID, Destination: destination, ArtifactPath: artifactPath,
		WriteMode: FileWriteReplace, Order: r.takeActionOrderLocked(),
	})

	return nil
}

func (r *recordingRuntime) blockedLocked(operation string) error {
	r.calls = append(r.calls, operation)

	return r.failLocked(&Error{
		Code: ErrorSideEffect, Behavior: operation,
		Message: "imported code reached a forbidden runtime boundary",
	})
}

func (r *recordingRuntime) failLocked(err error) error {
	if r.failure == nil {
		r.failure = err
	}

	return err
}

func (r *recordingRuntime) recordExecLocked(
	runtimeID string,
	command *clabexec.ExecCmd,
	wait bool,
) error {
	operation := "runtime.ExecNotWait"
	if wait {
		operation = "runtime.Exec"
	}
	r.calls = append(r.calls, operation)
	if command == nil || len(command.GetCmd()) == 0 {
		return r.failLocked(&Error{
			Code: ErrorInvariant, Field: "exec.command", Behavior: operation,
			Message: "imported runtime exec has no command",
		})
	}
	if !r.knownContainerLocked(runtimeID) {
		return r.failLocked(&Error{
			Code: ErrorInvariant, Field: "exec.container", Behavior: operation,
			Message: "imported runtime exec targets an unknown container",
		})
	}
	r.execs = append(r.execs, RecordedExec{
		RuntimeID: runtimeID,
		Command:   slices.Clone(command.GetCmd()),
		Wait:      wait,
		Order:     r.takeActionOrderLocked(),
	})

	return nil
}

func (r *recordingRuntime) knownContainerLocked(runtimeID string) bool {
	for _, container := range r.containers {
		if container.RuntimeID == runtimeID {
			return true
		}
	}

	return false
}

func (r *recordingRuntime) takeActionOrderLocked() int {
	order := r.nextActionOrder
	r.nextActionOrder++

	return order
}

func (r *recordingRuntime) writeArtifactLocked(
	operation,
	field,
	artifactPath string,
	content []byte,
	mode os.FileMode,
) error {
	controlledPath := filepath.Join(r.artifactRoot, filepath.FromSlash(artifactPath))
	if err := os.MkdirAll(filepath.Dir(controlledPath), 0o700); err != nil {
		return r.failLocked(&Error{
			Code: ErrorSideEffect, Field: field, Behavior: operation,
			Message: "cannot create a controlled runtime artifact directory", cause: err,
		})
	}
	file, err := os.OpenFile(controlledPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return r.failLocked(&Error{
			Code: ErrorSideEffect, Field: field, Behavior: operation,
			Message: "cannot create a controlled runtime artifact", cause: err,
		})
	}
	if _, err = file.Write(content); err == nil {
		err = file.Close()
	} else {
		_ = file.Close()
	}
	if err != nil {
		return r.failLocked(&Error{
			Code: ErrorSideEffect, Field: field, Behavior: operation,
			Message: "cannot write a controlled runtime artifact", cause: err,
		})
	}

	return nil
}

func recordedContainerMatches(
	container RecordedContainer,
	filters []*clabtypes.GenericFilter,
) bool {
	for _, filter := range filters {
		if filter == nil {
			continue
		}
		switch filter.FilterType {
		case "name":
			if filter.Match != container.RuntimeID && filter.Match != container.Config.LongName &&
				filter.Match != container.Config.ShortName {
				return false
			}
		case "label":
			value, exists := container.Config.Labels[filter.Field]
			switch filter.Operator {
			case "exists":
				if !exists {
					return false
				}
			case "=", "":
				if !exists || value != filter.Match {
					return false
				}
			case "!=":
				if exists && value == filter.Match {
					return false
				}
			default:
				return false
			}
		default:
			return false
		}
	}

	return true
}

func genericManagement(config *clabtypes.NodeConfig) clabruntime.GenericMgmtIPs {
	result := clabruntime.GenericMgmtIPs{
		IPv4Gw: config.MgmtIPv4Gateway,
		IPv6Gw: config.MgmtIPv6Gateway,
	}
	result.IPv4addr, result.IPv4pLen = splitManagementPrefix(config.MgmtIPv4Address)
	result.IPv6addr, result.IPv6pLen = splitManagementPrefix(config.MgmtIPv6Address)

	return result
}

func splitManagementPrefix(value string) (string, int) {
	if value == "" {
		return "", 0
	}
	address, network, err := net.ParseCIDR(value)
	if err != nil {
		return value, 0
	}
	prefix, _ := network.Mask.Size()

	return address.String(), prefix
}
