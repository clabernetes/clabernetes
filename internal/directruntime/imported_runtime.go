//nolint:ireturn,nlreturn,wsl_v5 // This file implements the imported generic runtime interface.
package directruntime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	clabernetesdeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	clabexec "github.com/srl-labs/containerlab/exec"
	clabruntime "github.com/srl-labs/containerlab/runtime"
	clabtypes "github.com/srl-labs/containerlab/types"
)

var _ clabruntime.ContainerRuntime = (*importedApplicationRuntime)(nil)

type importedApplicationRuntime struct {
	mu                   sync.Mutex
	plan                 clabernetesdeviceplan.Plan
	target               clabernetesdeviceplan.ContainerPlan
	containersByID       map[string]clabernetesdeviceplan.ContainerPlan
	managementByNode     map[string]clabernetesdeviceplan.ManagementInput
	images               map[string]*clabruntime.ImageInspect
	created              map[string]bool
	started              map[string]bool
	management           *clabtypes.MgmtNet
	hostsPath            string
	stdinPath            string
	logSocketPath        string
	runtimeConfig        clabruntime.RuntimeConfig
	boundaryFailure      error
	applicationLocal     bool
	networkNamespacePath string
}

func newImportedApplicationRuntime(
	input clabernetesdeviceplan.Input,
	plan clabernetesdeviceplan.Plan,
	targetContainerID string,
) (*importedApplicationRuntime, error) {
	runtime := &importedApplicationRuntime{
		plan: plan,
		containersByID: make(
			map[string]clabernetesdeviceplan.ContainerPlan,
			len(plan.Containers),
		),
		managementByNode: make(
			map[string]clabernetesdeviceplan.ManagementInput,
			len(input.Management),
		),
		images:           make(map[string]*clabruntime.ImageInspect, len(input.Images)*2),
		created:          map[string]bool{},
		started:          map[string]bool{},
		management:       &clabtypes.MgmtNet{},
		hostsPath:        "/etc/hosts",
		stdinPath:        "/proc/1/fd/0",
		logSocketPath:    ApplicationRuntimeSocketPath,
		applicationLocal: true,
	}
	for _, container := range plan.Containers {
		if container.RuntimeID == "" {
			return nil, fmt.Errorf("planned application container has no imported runtime identity")
		}
		if _, exists := runtime.containersByID[container.RuntimeID]; exists {
			return nil, fmt.Errorf("planned imported runtime identity is duplicated")
		}
		runtime.containersByID[container.RuntimeID] = container
		if container.ID == targetContainerID {
			runtime.target = container
		}
	}
	if runtime.target.ID == "" {
		return nil, fmt.Errorf("imported runtime target is absent from the plan")
	}
	for _, management := range input.Management {
		runtime.managementByNode[management.NodeID] = management
		if management.NodeID == runtime.target.NodeID {
			runtime.management = &clabtypes.MgmtNet{
				IPv4Gw: management.IPv4Gateway,
				IPv6Gw: management.IPv6Gateway,
			}
		}
	}
	for _, image := range input.Images {
		labels := make(map[string]string, len(image.Config.Labels))
		for _, label := range image.Config.Labels {
			labels[label.Name] = label.Value
		}
		inspection := &clabruntime.ImageInspect{
			Config: clabruntime.ImageConfig{Labels: labels},
		}
		runtime.images[image.SourceReference] = inspection
		runtime.images[image.DigestReference] = inspection
	}

	return runtime, nil
}

// NewImportedEndpointRuntime constructs the generic runtime used by the connectivity helper for
// imported endpoint deployment. The helper may expose the target Pod network namespace while it
// runs from a distinct host namespace, but it cannot accidentally execute commands or mutate
// files in its own helper container as though they belonged to the application container.
func NewImportedEndpointRuntime(
	input clabernetesdeviceplan.Input,
	plan clabernetesdeviceplan.Plan,
	targetContainerID,
	networkNamespacePath string,
) (clabruntime.ContainerRuntime, error) {
	runtime, err := newImportedApplicationRuntime(input, plan, targetContainerID)
	if err != nil {
		return nil, err
	}
	if filepath.Clean(networkNamespacePath) == "." || !filepath.IsAbs(networkNamespacePath) {
		return nil, fmt.Errorf("target application network namespace path must be absolute")
	}
	runtime.applicationLocal = false
	runtime.networkNamespacePath = filepath.Clean(networkNamespacePath)

	return runtime, nil
}

// NewImportedApplicationRuntime constructs the generic package-to-application boundary used by
// imported lifecycle hooks. It is exported within the internal module so registry conformance
// tests can exercise future package kinds without adding c9s registrations.
func NewImportedApplicationRuntime(
	input clabernetesdeviceplan.Input,
	plan clabernetesdeviceplan.Plan,
	targetContainerID string,
) (clabruntime.ContainerRuntime, error) {
	return newImportedApplicationRuntime(input, plan, targetContainerID)
}

// NewImportedApplicationRuntimeWithLogSocket constructs the same application-local boundary with
// an explicit Pod-local broker path. Production uses ApplicationRuntimeSocketPath; the explicit
// form keeps package-driven conformance hermetic.
func NewImportedApplicationRuntimeWithLogSocket(
	input clabernetesdeviceplan.Input,
	plan clabernetesdeviceplan.Plan,
	targetContainerID,
	logSocketPath string,
) (clabruntime.ContainerRuntime, error) {
	runtime, err := newImportedApplicationRuntime(input, plan, targetContainerID)
	if err != nil {
		return nil, err
	}
	runtime.logSocketPath = filepath.Clean(logSocketPath)

	return runtime, nil
}

func (*importedApplicationRuntime) Init(...clabruntime.RuntimeOption) error { return nil }

func (r *importedApplicationRuntime) Mgmt() *clabtypes.MgmtNet {
	r.mu.Lock()
	defer r.mu.Unlock()
	copy := *r.management
	copy.DriverOpts = maps.Clone(r.management.DriverOpts)

	return &copy
}

func (r *importedApplicationRuntime) WithConfig(config *clabruntime.RuntimeConfig) {
	if config == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runtimeConfig = *config
}

func (r *importedApplicationRuntime) WithMgmtNet(management *clabtypes.MgmtNet) {
	if management == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	copy := *management
	copy.DriverOpts = maps.Clone(management.DriverOpts)
	r.management = &copy
}

func (r *importedApplicationRuntime) WithKeepMgmtNet() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runtimeConfig.KeepMgmtNet = true
}

func (r *importedApplicationRuntime) CreateNet(context.Context) error {
	return r.unsupportedCapability(
		"runtime.CreateNet",
		"runtime.network",
		"direct application lifecycle cannot create a container-runtime network",
	)
}

func (r *importedApplicationRuntime) DeleteNet(context.Context) error {
	return r.unsupportedCapability(
		"runtime.DeleteNet",
		"runtime.network",
		"direct application lifecycle cannot delete a container-runtime network",
	)
}

func (r *importedApplicationRuntime) PullImage(
	_ context.Context,
	imageName string,
	_ clabtypes.PullPolicyValue,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.images[imageName]; !exists {
		return fmt.Errorf("imported post-deploy requested image absent from immutable input")
	}

	return nil
}

func (r *importedApplicationRuntime) CreateContainer(
	_ context.Context,
	config *clabtypes.NodeConfig,
) (string, error) {
	if config == nil {
		return "", fmt.Errorf("imported post-deploy supplied no container configuration")
	}
	runtimeID := config.LongName
	if runtimeID == "" {
		runtimeID = config.ShortName
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	container, exists := r.containersByID[runtimeID]
	if !exists || container.NodeID != r.target.NodeID {
		return "", fmt.Errorf(
			"imported post-deploy requested a container outside the accepted target Node",
		)
	}
	r.created[runtimeID] = true

	return runtimeID, nil
}

func (r *importedApplicationRuntime) StartContainer(
	_ context.Context,
	runtimeID string,
	_ clabruntime.Node,
) (any, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.created[runtimeID] {
		return nil, fmt.Errorf("imported post-deploy started an unrecognized container")
	}
	r.started[runtimeID] = true

	return runtimeID, nil
}

func (r *importedApplicationRuntime) StopContainer(
	context.Context,
	string,
	clabtypes.Signal,
) error {
	return r.unsupportedCapability(
		"runtime.StopContainer",
		"runtime.containerLifecycle",
		"application-container stop must be realized by Kubernetes",
	)
}

func (r *importedApplicationRuntime) PauseContainer(context.Context, string) error {
	return r.unsupportedCapability(
		"runtime.PauseContainer",
		"runtime.containerLifecycle",
		"application-container pause has no direct Kubernetes realization",
	)
}

func (r *importedApplicationRuntime) UnpauseContainer(context.Context, string) error {
	return r.unsupportedCapability(
		"runtime.UnpauseContainer",
		"runtime.containerLifecycle",
		"application-container unpause has no direct Kubernetes realization",
	)
}

func (r *importedApplicationRuntime) ListContainers(
	_ context.Context,
	filters []*clabtypes.GenericFilter,
) ([]clabruntime.GenericContainer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]clabruntime.GenericContainer, 0, len(r.plan.Containers))
	for _, container := range r.plan.Containers {
		if !plannedContainerMatches(container, filters) {
			continue
		}
		management := r.managementByNode[container.NodeID]
		labels := make(map[string]string, len(container.Labels))
		for _, label := range container.Labels {
			labels[label.Name] = label.Value
		}
		result = append(result, clabruntime.GenericContainer{
			Names: []string{container.RuntimeID}, ID: container.RuntimeID,
			ShortID: container.RuntimeID, Image: container.Image,
			State: "running", Status: "running", Labels: labels,
			NetworkSettings: genericRuntimeManagement(management), Runtime: r,
		})
	}

	return result, nil
}

func (r *importedApplicationRuntime) GetNSPath(
	_ context.Context,
	runtimeID string,
) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	container, exists := r.containersByID[runtimeID]
	if !exists || container.NamespaceOwnerID != r.target.NamespaceOwnerID {
		return "", fmt.Errorf("imported post-deploy requested a foreign network namespace")
	}
	if r.networkNamespacePath != "" {
		return r.networkNamespacePath, nil
	}
	if r.applicationLocal {
		// Imported post-deploy hooks execute inside the target application container. Package
		// operations that temporarily enter its namespace can therefore use the current one.
		return "/proc/self/ns/net", nil
	}

	return "", r.unsupportedCapabilityLocked(
		"runtime.GetNSPath",
		"runtime.networkNamespace",
		"a distinct host worker and target application network namespace are required",
	)
}

// DirectEndpointLifecycleBoundary reports whether this runtime has the distinct host worker and
// target application namespace required by imported endpoint hooks.
func (r *importedApplicationRuntime) DirectEndpointLifecycleBoundary() bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	return !r.applicationLocal && r.networkNamespacePath != ""
}

func (r *importedApplicationRuntime) LogNonRunningContainerOutput(context.Context, string) {
	_ = r.unsupportedCapability(
		"runtime.LogNonRunningContainerOutput",
		"runtime.logs",
		"application-container logs must be read through Kubernetes",
	)
}

func (r *importedApplicationRuntime) Exec(
	ctx context.Context,
	runtimeID string,
	command *clabexec.ExecCmd,
) (*clabexec.ExecResult, error) {
	if !r.applicationLocal {
		return nil, r.unsupportedCapability(
			"runtime.Exec",
			"runtime.applicationExec",
			"endpoint lifecycle requires an application-container execution boundary",
		)
	}
	if err := r.requireLocalContainer(runtimeID); err != nil {
		return nil, err
	}
	if command == nil || len(command.GetCmd()) == 0 {
		return nil, fmt.Errorf("imported post-deploy exec has no command")
	}
	result := clabexec.NewExecResult(command)
	process := exec.CommandContext(
		ctx,
		command.GetCmd()[0],
		command.GetCmd()[1:]...,
	) //nolint:gosec // Command originates in the pinned imported package/input lifecycle.
	var stdout, stderr bytes.Buffer
	process.Stdout = &stdout
	process.Stderr = &stderr
	err := process.Run()
	result.SetStdOut(stdout.Bytes())
	result.SetStdErr(stderr.Bytes())
	if err == nil {
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.SetReturnCode(exitErr.ExitCode())

		return result, nil
	}

	return nil, err
}

func (r *importedApplicationRuntime) ExecNotWait(
	ctx context.Context,
	runtimeID string,
	command *clabexec.ExecCmd,
) error {
	if !r.applicationLocal {
		return r.unsupportedCapability(
			"runtime.ExecNotWait",
			"runtime.applicationExec",
			"endpoint lifecycle requires an application-container execution boundary",
		)
	}
	if err := r.requireLocalContainer(runtimeID); err != nil {
		return err
	}
	if command == nil || len(command.GetCmd()) == 0 {
		return fmt.Errorf("imported post-deploy exec has no command")
	}
	process := exec.CommandContext(
		ctx,
		command.GetCmd()[0],
		command.GetCmd()[1:]...,
	) //nolint:gosec // Command originates in the pinned imported package/input lifecycle.
	process.Stdout = os.Stdout
	process.Stderr = os.Stderr
	if err := process.Start(); err != nil {
		return err
	}

	return process.Process.Release()
}

func (r *importedApplicationRuntime) DeleteContainer(context.Context, string) error {
	return r.unsupportedCapability(
		"runtime.DeleteContainer",
		"runtime.containerLifecycle",
		"application-container deletion must be realized by Kubernetes",
	)
}

func (r *importedApplicationRuntime) Config() clabruntime.RuntimeConfig {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.runtimeConfig
}

func (*importedApplicationRuntime) GetName() string { return "clabernetes-direct-application" }

func (r *importedApplicationRuntime) GetHostsPath(
	_ context.Context,
	runtimeID string,
) (string, error) {
	if !r.applicationLocal {
		return "", r.unsupportedCapability(
			"runtime.GetHostsPath",
			"runtime.applicationFilesystem",
			"endpoint lifecycle requires an application-container filesystem boundary",
		)
	}
	if err := r.requireLocalContainer(runtimeID); err != nil {
		return "", err
	}

	return r.hostsPath, nil
}

func (r *importedApplicationRuntime) GetContainerStatus(
	_ context.Context,
	runtimeID string,
) clabruntime.ContainerStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.containersByID[runtimeID]; exists {
		return clabruntime.Running
	}

	return clabruntime.NotFound
}

func (r *importedApplicationRuntime) IsHealthy(
	_ context.Context,
	runtimeID string,
) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, exists := r.containersByID[runtimeID]

	return exists, nil
}

func (r *importedApplicationRuntime) WriteToStdinNoWait(
	_ context.Context,
	runtimeID string,
	content []byte,
) error {
	if !r.applicationLocal {
		return r.unsupportedCapability(
			"runtime.WriteToStdinNoWait",
			"runtime.applicationStdin",
			"endpoint lifecycle requires an application-container standard-input boundary",
		)
	}
	if err := r.requireLocalContainer(runtimeID); err != nil {
		return err
	}
	stdin, err := os.OpenFile(r.stdinPath, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("opening direct application stdin: %w", err)
	}
	if _, err = stdin.Write(content); err == nil {
		err = stdin.Close()
	} else {
		_ = stdin.Close()
	}

	return err
}

func (*importedApplicationRuntime) CheckConnection(context.Context) error { return nil }

func (r *importedApplicationRuntime) GetRuntimeSocket() (string, error) {
	return "", r.unsupportedCapability(
		"runtime.GetRuntimeSocket",
		"runtime.controlSocket",
		"direct application containers expose no nested runtime control socket",
	)
}

func (r *importedApplicationRuntime) GetCooCBindMounts() clabtypes.Binds {
	_ = r.unsupportedCapability(
		"runtime.GetCooCBindMounts",
		"runtime.containerOfContainerMounts",
		"container-of-container bind mounts have no direct-Pod realization",
	)

	return nil
}

func (r *importedApplicationRuntime) StreamLogs(
	ctx context.Context,
	runtimeID string,
) (io.ReadCloser, error) {
	if !r.applicationLocal {
		return nil, r.unsupportedCapability(
			"runtime.StreamLogs",
			"runtime.logs",
			"endpoint lifecycle cannot stream application-container logs",
		)
	}
	if err := r.requireNodeContainer(runtimeID); err != nil {
		return nil, err
	}

	return openApplicationLogStream(ctx, r.logSocketPath, runtimeID)
}

func (r *importedApplicationRuntime) StreamEvents(
	context.Context,
	clabruntime.EventStreamOptions,
) (<-chan clabruntime.ContainerEvent, <-chan error, error) {
	return nil, nil, r.unsupportedCapability(
		"runtime.StreamEvents",
		"runtime.events",
		"application-container event streaming requires the Kubernetes runtime boundary",
	)
}

func (r *importedApplicationRuntime) InspectImage(
	_ context.Context,
	imageName string,
) (*clabruntime.ImageInspect, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	image := r.images[imageName]
	if image == nil {
		return nil, fmt.Errorf("image metadata is absent from immutable input")
	}
	copy := *image
	copy.Config.Labels = maps.Clone(image.Config.Labels)

	return &copy, nil
}

func (r *importedApplicationRuntime) CopyToContainer(
	_ context.Context,
	runtimeID,
	destination,
	source string,
) error {
	if !r.applicationLocal {
		return r.unsupportedCapability(
			"runtime.CopyToContainer",
			"runtime.applicationFilesystem",
			"endpoint lifecycle requires an application-container filesystem boundary",
		)
	}
	if err := r.requireLocalContainer(runtimeID); err != nil {
		return err
	}
	if destination == "" || source == "" {
		return fmt.Errorf("imported post-deploy copy has an empty path")
	}
	content, err := os.ReadFile(
		source,
	) //nolint:gosec // Source is selected by the imported hook in its scoped Node workspace.
	if err != nil {
		return err
	}
	info, err := os.Stat(source)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("imported post-deploy copy source is not a regular file")
	}
	destination = filepath.Clean(destination)
	if !filepath.IsAbs(destination) || destination == string(filepath.Separator) {
		return fmt.Errorf("imported post-deploy copy destination must be a scoped absolute path")
	}
	if existing, statErr := os.Lstat(destination); statErr == nil &&
		existing.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("imported post-deploy copy destination is a symbolic link")
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return statErr
	}
	parent := filepath.Dir(destination)
	if parentInfo, statErr := os.Stat(parent); statErr != nil || !parentInfo.IsDir() {
		return fmt.Errorf("imported post-deploy copy destination parent is unavailable")
	}
	temporary, err := os.CreateTemp(parent, ".c9s-imported-copy-")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if _, err = temporary.Write(content); err == nil {
		// The imported container runtime realizes CopyToContainer with a fixed world-readable
		// mode regardless of the source file's permissions (its tar header is always 0666);
		// package hooks stage configuration from private temp files and then read it back as
		// unprivileged device users, so preserving the source mode breaks them.
		err = temporary.Chmod(0o666)
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}

	if err = os.Rename(temporaryName, destination); err == nil {
		return nil
	} else if !errors.Is(err, syscall.EBUSY) {
		return err
	}

	return writeLifecycleMountedFile(destination, content)
}

func (r *importedApplicationRuntime) requireLocalContainer(runtimeID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	container, exists := r.containersByID[runtimeID]
	if !exists {
		return fmt.Errorf("imported post-deploy targets an unknown container")
	}
	if container.ID != r.target.ID {
		return fmt.Errorf(
			"imported post-deploy operation targets another application container",
		)
	}

	return nil
}

func (r *importedApplicationRuntime) requireNodeContainer(runtimeID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	container, exists := r.containersByID[runtimeID]
	if !exists {
		return fmt.Errorf("imported post-deploy targets an unknown container")
	}
	if container.NodeID != r.target.NodeID {
		return fmt.Errorf("imported post-deploy operation targets another logical Node")
	}

	return nil
}

// BoundaryFailure reports the first generic runtime capability that the application-local
// worker could not realize. Imported hooks sometimes log and intentionally swallow runtime
// errors; retaining the failure here prevents c9s from silently claiming partial compatibility.
func (r *importedApplicationRuntime) BoundaryFailure() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.boundaryFailure
}

func (r *importedApplicationRuntime) unsupportedCapability(
	operation,
	field,
	message string,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.unsupportedCapabilityLocked(operation, field, message)
}

func (r *importedApplicationRuntime) unsupportedCapabilityLocked(
	operation,
	field,
	message string,
) error {
	err := &clabernetesdeviceplan.Error{
		Code: clabernetesdeviceplan.ErrorUnsupported, Field: field,
		Behavior: operation, Message: message,
	}
	if r.boundaryFailure == nil {
		r.boundaryFailure = err
	}

	return err
}

func plannedContainerMatches(
	container clabernetesdeviceplan.ContainerPlan,
	filters []*clabtypes.GenericFilter,
) bool {
	labels := make(map[string]string, len(container.Labels))
	for _, label := range container.Labels {
		labels[label.Name] = label.Value
	}
	for _, filter := range filters {
		if filter == nil {
			continue
		}
		switch filter.FilterType {
		case "name":
			if filter.Match != container.RuntimeID {
				return false
			}
		case "label":
			value, exists := labels[filter.Field]
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

func genericRuntimeManagement(
	management clabernetesdeviceplan.ManagementInput,
) clabruntime.GenericMgmtIPs {
	result := clabruntime.GenericMgmtIPs{
		IPv4Gw: management.IPv4Gateway,
		IPv6Gw: management.IPv6Gateway,
	}
	result.IPv4addr, result.IPv4pLen = splitRuntimePrefix(management.IPv4)
	result.IPv6addr, result.IPv6pLen = splitRuntimePrefix(management.IPv6)

	return result
}

func splitRuntimePrefix(value string) (string, int) {
	address, prefix, found := strings.Cut(value, "/")
	if !found {
		return value, 0
	}
	bits := 0
	_, _ = fmt.Sscanf(prefix, "%d", &bits)

	return address, bits
}
