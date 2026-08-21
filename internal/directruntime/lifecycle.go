//nolint:err113,funlen,gocognit,gocyclo,mnd,wsl_v5 // Lifecycle execution fails closed at each typed action boundary.
package directruntime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	"golang.org/x/sys/unix"
)

const (
	maxLifecycleFileBytes    = 64 << 20
	maxLifecycleBinaryBytes  = 256 << 20
	applicationRestartMarker = "request"
)

// ApplicationRestartOperations is the narrow process boundary used to restart a kubelet-owned
// application container without a shell, runtime socket, or kind-specific command.
type ApplicationRestartOperations interface {
	SignalPID(pid int, signal syscall.Signal) error
}

type processRestartOperations struct{}

func (processRestartOperations) SignalPID(pid int, signal syscall.Signal) error {
	return unix.Kill(pid, signal)
}

// InstallLifecycleBinary atomically publishes the currently running c9s executable into a
// plan-owned shared volume. Device images therefore need neither a shell nor a preinstalled c9s
// binary for kubelet lifecycle hooks to execute typed runtime-neutral actions.
func InstallLifecycleBinary(destination string) error {
	destination = filepath.Clean(destination)
	if !filepath.IsAbs(destination) || destination == string(filepath.Separator) {
		return errors.New("lifecycle binary destination must be a scoped absolute path")
	}
	parent := filepath.Dir(destination)
	if info, err := os.Stat(parent); err != nil || !info.IsDir() {
		return errors.New("lifecycle binary destination parent is unavailable")
	}
	if existing, err := os.Lstat(destination); err == nil && existing.IsDir() {
		return errors.New("lifecycle binary destination is a directory")
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cannot inspect lifecycle binary destination: %w", err)
	}

	sourcePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot locate lifecycle binary source: %w", err)
	}
	source, err := os.Open(sourcePath) //nolint:gosec // The kernel supplies this process path.
	if err != nil {
		return fmt.Errorf("cannot open lifecycle binary source: %w", err)
	}
	defer func() { _ = source.Close() }()
	info, err := source.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("lifecycle binary source is not a regular file")
	}
	if info.Size() < 1 || info.Size() > maxLifecycleBinaryBytes {
		return errors.New("lifecycle binary source is outside the bounded size")
	}

	temporary, err := os.CreateTemp(parent, ".c9s-lifecycle-binary-")
	if err != nil {
		return fmt.Errorf("cannot create staged lifecycle binary: %w", err)
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	written, copyErr := io.Copy(
		temporary,
		io.LimitReader(source, maxLifecycleBinaryBytes+1),
	)
	if copyErr == nil && (written < 1 || written > maxLifecycleBinaryBytes) {
		copyErr = errors.New("copied lifecycle binary is outside the bounded size")
	}
	if copyErr == nil {
		copyErr = temporary.Chmod(0o555)
	}
	if copyErr == nil {
		copyErr = temporary.Sync()
	}
	if closeErr := temporary.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return fmt.Errorf("cannot stage lifecycle binary: %w", copyErr)
	}
	if err = os.Rename(temporaryName, destination); err != nil {
		return fmt.Errorf("cannot publish lifecycle binary: %w", err)
	}
	// Imported packages open CLI sessions through their container runtime's CLI; publish those
	// names as links to the lifecycle binary so the shim realizes them application-locally.
	for _, name := range runtimeCLINames {
		linkPath := filepath.Join(parent, name)
		if err = os.Remove(linkPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("cannot replace runtime CLI link %q: %w", name, err)
		}
		if err = os.Symlink(filepath.Base(destination), linkPath); err != nil {
			return fmt.Errorf("cannot publish runtime CLI link %q: %w", name, err)
		}
	}

	return nil
}

// RunApplicationRestart records one plan-scoped idempotency marker and signals the device PID 1.
// Kubernetes RestartPolicy=Always performs the actual direct-container restart in the same Pod.
func RunApplicationRestart(requestDigest, stateDirectory, stopSignal string) error {
	return RunApplicationRestartWithOperations(
		requestDigest,
		stateDirectory,
		stopSignal,
		processRestartOperations{},
	)
}

// RunApplicationRestartWithOperations exposes the process seam for deterministic tests.
func RunApplicationRestartWithOperations(
	requestDigest,
	stateDirectory,
	stopSignal string,
	operations ApplicationRestartOperations,
) error {
	if !validRevisionDigest(requestDigest) {
		return errors.New("application restart request digest is invalid")
	}
	stateDirectory = filepath.Clean(stateDirectory)
	if !filepath.IsAbs(stateDirectory) || stateDirectory == string(filepath.Separator) {
		return errors.New("application restart state directory must be a scoped absolute path")
	}
	if operations == nil {
		return errors.New("application restart operations are nil")
	}
	if err := os.MkdirAll(stateDirectory, 0o750); err != nil {
		return fmt.Errorf("creating application restart state: %w", err)
	}
	markerPath := filepath.Join(stateDirectory, applicationRestartMarker)
	if raw, err := os.ReadFile(markerPath); err == nil {
		if strings.TrimSpace(string(raw)) == requestDigest {
			return nil
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("reading application restart marker: %w", err)
	}
	signalName := strings.ToUpper(strings.TrimSpace(stopSignal))
	if signalName == "" {
		signalName = "SIGTERM"
	}
	signal := unix.SignalNum(signalName)
	if signal == 0 || signal == syscall.SIGKILL {
		return fmt.Errorf("application stop signal %q is unsupported", stopSignal)
	}
	if err := operations.SignalPID(1, signal); err != nil {
		return fmt.Errorf("signaling application PID 1: %w", err)
	}
	temporary, err := os.CreateTemp(stateDirectory, ".restart-request-")
	if err != nil {
		return fmt.Errorf("creating application restart marker: %w", err)
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if _, err = temporary.WriteString(requestDigest + "\n"); err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("writing application restart marker: %w", err)
	}
	if err = os.Rename(temporaryName, markerPath); err != nil {
		return fmt.Errorf("publishing application restart marker: %w", err)
	}

	return nil
}

// RunLifecycle executes typed lifecycle actions that need no imported package rehydration.
func RunLifecycle(
	ctx context.Context,
	plan clabernetesinternaldeviceplan.Plan,
	phase clabernetesinternaldeviceplan.ActionPhase,
	containerID,
	artifactRoot string,
) error {
	return runLifecycle(ctx, clabernetesinternaldeviceplan.Input{},
		plan, phase, containerID, artifactRoot,
		"", "", "", "", false)
}

// RunLifecycleWithImported executes one plan phase from inside the target application container.
// The immutable Input is required before any opaque imported package hook may run.
func RunLifecycleWithImported(
	ctx context.Context,
	input clabernetesinternaldeviceplan.Input,
	plan clabernetesinternaldeviceplan.Plan,
	phase clabernetesinternaldeviceplan.ActionPhase,
	containerID,
	artifactRoot,
	scratchRoot,
	certificateRoot,
	entropyRoot,
	revision string,
) error {
	return runLifecycle(ctx, input, plan, phase, containerID, artifactRoot, scratchRoot,
		certificateRoot, entropyRoot, revision, true)
}

func runLifecycle(
	ctx context.Context,
	input clabernetesinternaldeviceplan.Input,
	plan clabernetesinternaldeviceplan.Plan,
	phase clabernetesinternaldeviceplan.ActionPhase,
	containerID,
	artifactRoot,
	scratchRoot,
	certificateRoot,
	entropyRoot,
	revision string,
	validateInput bool,
) error {
	if ctx == nil {
		return errors.New("lifecycle context is nil")
	}
	if phase != clabernetesinternaldeviceplan.PhasePostStart &&
		phase != clabernetesinternaldeviceplan.PhaseSave {
		return fmt.Errorf("lifecycle phase %q is not executable in an application container", phase)
	}
	normalized, err := clabernetesinternaldeviceplan.NormalizePlan(plan)
	if err != nil {
		return err
	}
	if validateInput {
		if err = clabernetesinternaldeviceplan.ValidatePlanInputIdentity(input,
			normalized); err != nil {
			return err
		}
	}
	containerExists := false
	containerNodeID := ""
	for _, container := range normalized.Containers {
		if container.ID == containerID {
			containerExists = true
			containerNodeID = container.NodeID

			break
		}
	}
	if !containerExists {
		return errors.New("lifecycle target container is absent from the plan")
	}
	if validateInput {
		prepareImportedRuntimeCLI(normalized, containerID)
	}
	root := filepath.Clean(artifactRoot)
	if !filepath.IsAbs(root) || root == string(filepath.Separator) {
		return errors.New("lifecycle artifact root must be a scoped absolute path")
	}
	files := make(map[string]clabernetesinternaldeviceplan.FilePlan, len(normalized.Files))
	for _, file := range normalized.Files {
		files[file.ID] = file
	}
	actions := slices.Clone(normalized.Actions)
	slices.SortStableFunc(actions, func(left, right clabernetesinternaldeviceplan.Action) int {
		if left.Order != right.Order {
			return left.Order - right.Order
		}

		return strings.Compare(left.ID, right.ID)
	})
	for _, action := range actions {
		if action.Phase != phase || action.Target.ContainerID != containerID {
			continue
		}
		if action.Target.NodeID != containerNodeID {
			return fmt.Errorf("lifecycle action %q crosses logical Node ownership", action.ID)
		}
		switch action.Kind {
		case clabernetesinternaldeviceplan.ActionImportedPostDeploy:
			if !validateInput {
				return fmt.Errorf(
					"lifecycle action %q requires immutable imported input",
					action.ID,
				)
			}
			runtime, runtimeErr := newImportedApplicationRuntime(input, normalized, containerID)
			if runtimeErr != nil {
				return fmt.Errorf("lifecycle action %q failed: %w", action.ID, runtimeErr)
			}
			if err = (clabernetesinternaldeviceplan.Adapter{
				Revision: revision, EntropyRoot: entropyRoot,
				PodDNSServers: RuntimePodDNSServers(),
			}).RunPostDeploy(
				ctx,
				input,
				normalized,
				containerID,
				scratchRoot,
				root,
				certificateRoot,
				runtime,
			); err != nil {
				return fmt.Errorf("lifecycle action %q failed: %w", action.ID, err)
			}
		case clabernetesinternaldeviceplan.ActionExec:
			if err = runLifecycleExec(ctx, action); err != nil {
				return fmt.Errorf("lifecycle action %q failed: %w", action.ID, err)
			}
		case clabernetesinternaldeviceplan.ActionFile:
			if err = runLifecycleFile(action, files, root); err != nil {
				return fmt.Errorf("lifecycle action %q failed: %w", action.ID, err)
			}
		case clabernetesinternaldeviceplan.ActionWriteStdin:
			if err = runLifecycleStdin(action, files, root); err != nil {
				return fmt.Errorf("lifecycle action %q failed: %w", action.ID, err)
			}
		case clabernetesinternaldeviceplan.ActionSave:
			if !validateInput || action.Save == nil ||
				action.Save.Method != clabernetesinternaldeviceplan.SaveMethodImported {
				return fmt.Errorf(
					"lifecycle action %q requires immutable imported save input",
					action.ID,
				)
			}
			runtime, runtimeErr := newImportedApplicationRuntime(input, normalized, containerID)
			if runtimeErr != nil {
				return fmt.Errorf("lifecycle action %q failed: %w", action.ID, runtimeErr)
			}
			if err = (clabernetesinternaldeviceplan.Adapter{
				Revision: revision, EntropyRoot: entropyRoot,
				PodDNSServers: RuntimePodDNSServers(),
			}).RunSave(
				ctx,
				input,
				normalized,
				containerID,
				root,
				runtime,
			); err != nil {
				return fmt.Errorf("lifecycle action %q failed: %w", action.ID, err)
			}
		default:
			return fmt.Errorf(
				"lifecycle action %q has unsupported application-container operation %q",
				action.ID,
				action.Kind,
			)
		}
	}

	return nil
}

func runLifecycleExec(ctx context.Context, action clabernetesinternaldeviceplan.Action) error {
	if action.Exec == nil || len(action.Exec.Command) == 0 {
		return errors.New("exec payload is incomplete")
	}
	commandContext := ctx
	cancel := func() {}
	if action.Exec.TimeoutSeconds > 0 {
		commandContext, cancel = context.WithTimeout(
			ctx,
			time.Duration(action.Exec.TimeoutSeconds)*time.Second,
		)
	}
	defer cancel()
	command := slices.Clone(action.Exec.Command)
	if action.Exec.Shell {
		command = []string{"/bin/sh", "-c", strings.Join(command, " ")}
	}
	process := exec.CommandContext(commandContext, command[0], command[1:]...) //nolint:gosec
	process.Stdout = os.Stdout
	process.Stderr = os.Stderr
	if !action.Exec.Wait {
		return process.Start()
	}

	return process.Run()
}

func runLifecycleFile(
	action clabernetesinternaldeviceplan.Action,
	files map[string]clabernetesinternaldeviceplan.FilePlan,
	artifactRoot string,
) error {
	if action.File == nil {
		return errors.New("file payload is incomplete")
	}
	file, content, err := readLifecycleFile(action.File.FileID, files, artifactRoot)
	if err != nil {
		return err
	}
	destination := action.File.Destination
	if destination == "" {
		destination = file.Destination
	}
	destination = filepath.Clean(destination)
	if !filepath.IsAbs(destination) || destination == string(filepath.Separator) {
		return errors.New("file destination must be a scoped absolute path")
	}
	parent := filepath.Dir(destination)
	if info, statErr := os.Stat(parent); statErr != nil || !info.IsDir() {
		return errors.New("file destination parent is unavailable")
	}
	if existing, statErr := os.Lstat(destination); statErr == nil &&
		existing.Mode()&os.ModeSymlink != 0 {
		return errors.New("file destination is a symbolic link")
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return fmt.Errorf("cannot inspect file destination: %w", statErr)
	}
	if action.File.WriteMode == clabernetesinternaldeviceplan.FileWriteAppend {
		return appendLifecycleFile(destination, content)
	}
	temporary, err := os.CreateTemp(parent, ".c9s-lifecycle-")
	if err != nil {
		return fmt.Errorf("cannot create staged lifecycle file: %w", err)
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if _, err = temporary.Write(content); err == nil {
		err = temporary.Chmod(os.FileMode(file.Mode))
	}
	if err == nil && (file.UID != nil || file.GID != nil) {
		uid, gid := -1, -1
		if file.UID != nil {
			uid = int(*file.UID)
		}
		if file.GID != nil {
			gid = int(*file.GID)
		}
		err = temporary.Chown(uid, gid)
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("cannot stage lifecycle file: %w", err)
	}
	if err = os.Rename(temporaryName, destination); err == nil {
		return nil
	} else if !errors.Is(err, syscall.EBUSY) {
		return fmt.Errorf("cannot publish lifecycle file: %w", err)
	}

	// A kubelet- or runtime-mounted regular file cannot be atomically replaced. Fall back only
	// for that generic kernel condition after all path and symlink checks have succeeded.
	return writeLifecycleMountedFile(destination, content)
}

func appendLifecycleFile(destination string, content []byte) error {
	//nolint:gosec // reads are confined to plan-scoped roots.
	file, err := os.OpenFile(
		destination,
		os.O_WRONLY|os.O_APPEND,
		0,
	) //nolint:gosec // reads are confined to plan-scoped roots.
	if err != nil {
		return fmt.Errorf("cannot open lifecycle append destination: %w", err)
	}
	_, err = file.Write(content)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("cannot append lifecycle destination: %w", err)
	}

	return nil
}

func writeLifecycleMountedFile(destination string, content []byte) error {
	//nolint:gosec // reads are confined to plan-scoped roots.
	file, err := os.OpenFile(
		destination,
		os.O_WRONLY|os.O_TRUNC,
		0,
	) //nolint:gosec // reads are confined to plan-scoped roots.
	if err != nil {
		return fmt.Errorf("cannot open lifecycle destination: %w", err)
	}
	_, err = file.Write(content)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("cannot write lifecycle destination: %w", err)
	}

	return nil
}

func runLifecycleStdin(
	action clabernetesinternaldeviceplan.Action,
	files map[string]clabernetesinternaldeviceplan.FilePlan,
	artifactRoot string,
) error {
	if action.WriteStdin == nil {
		return errors.New("stdin payload is incomplete")
	}
	_, content, err := readLifecycleFile(action.WriteStdin.FileID, files, artifactRoot)
	if err != nil {
		return err
	}
	stdin, err := os.OpenFile("/proc/1/fd/0", os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("container stdin is unavailable: %w", err)
	}
	if _, err = stdin.Write(content); err == nil {
		err = stdin.Close()
	} else {
		_ = stdin.Close()
	}
	if err != nil {
		return fmt.Errorf("cannot write container stdin: %w", err)
	}

	return nil
}

func readLifecycleFile(
	fileID string,
	files map[string]clabernetesinternaldeviceplan.FilePlan,
	artifactRoot string,
) (clabernetesinternaldeviceplan.FilePlan, []byte, error) {
	file, exists := files[fileID]
	if !exists {
		return clabernetesinternaldeviceplan.FilePlan{},
			nil,
			errors.New("file identity is absent from the plan")
	}
	if file.ArtifactKind != clabernetesinternaldeviceplan.ArtifactRegular {
		return clabernetesinternaldeviceplan.FilePlan{},
			nil,
			errors.New("non-regular artifact is not readable lifecycle content")
	}
	if file.ArtifactPath == "" || filepath.IsAbs(file.ArtifactPath) {
		return clabernetesinternaldeviceplan.FilePlan{},
			nil,
			errors.New("file source path is not scoped")
	}
	nodeRoot := filepath.Join(
		artifactRoot,
		clabernetesinternaldeviceplan.ArtifactNodeDirectory(file.NodeID),
	)
	source := filepath.Join(nodeRoot, filepath.FromSlash(file.ArtifactPath))
	relative, err := filepath.Rel(nodeRoot, source)
	if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return clabernetesinternaldeviceplan.FilePlan{},
			nil,
			errors.New("file source escapes its Node root")
	}
	sourceFile, err := os.Open(source) //nolint:gosec // Confined to a mounted plan-owned root.
	if err != nil {
		return clabernetesinternaldeviceplan.FilePlan{}, nil, fmt.Errorf(
			"cannot open lifecycle source: %w",
			err,
		)
	}
	defer func() { _ = sourceFile.Close() }()
	content, err := io.ReadAll(io.LimitReader(sourceFile, maxLifecycleFileBytes+1))
	if err != nil || len(content) > maxLifecycleFileBytes {
		return clabernetesinternaldeviceplan.FilePlan{},
			nil,
			errors.New("cannot read bounded lifecycle source")
	}
	digest := clabernetesinternaldeviceplan.Digest(content)
	if digest != file.Digest && !runtimeGeneratorContent(file, digest, artifactRoot) {
		return clabernetesinternaldeviceplan.FilePlan{},
			nil,
			errors.New("lifecycle source digest differs from plan")
	}

	return file, content, nil
}

// runtimeGeneratorContent reports whether content is the preparation-recorded runtime render of
// a generator file: preparation re-renders generator files with the Pod's runtime management
// identity and records their digests beside the staged artifacts.
func runtimeGeneratorContent(
	file clabernetesinternaldeviceplan.FilePlan,
	digest,
	artifactRoot string,
) bool {
	if file.SourceKind != clabernetesinternaldeviceplan.FileSourceGenerator &&
		file.SourceKind != clabernetesinternaldeviceplan.FileSourceCertificate {
		return false
	}

	return clabernetesinternaldeviceplan.LoadRuntimeArtifactDigests(
		artifactRoot,
		file.NodeID,
	)[file.ArtifactPath] == digest
}
