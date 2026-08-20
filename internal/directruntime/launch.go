//nolint:err113,gocyclo // single-pass boundary logic with structured one-off diagnostics and protocol literals.
package directruntime

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
)

// conventionalNoFileLimit is the open-file bound containerlab's supported container runtime
// gives every kind. Kubernetes container runtimes may grant the kernel maximum instead, which
// breaks imported processes whose fork/exec helpers iterate the whole descriptor range, so the
// launch boundary restores the conventional bound before handing over to the image's process.
const conventionalNoFileLimit = 1_048_576

// LaunchOperations is the narrow application-container boundary used before replacing the c9s
// helper with the image's real process. It deliberately describes generic filesystem and process
// operations and contains no containerlab kind vocabulary.
type LaunchOperations interface {
	Delay(duration time.Duration) error
	MountFilesystem(source, destination, filesystem string, options []string) error
	LimitOpenFiles(limit uint64) error
	Exec(argv []string) error
}

// RunLaunch applies synchronous pre-start operations and replaces the helper with the image's
// real process. This prevents a kubelet PostStart race for mount security options.
func RunLaunch(plan clabernetesinternaldeviceplan.Plan, containerID string) error {
	return RunLaunchWithOperations(plan, containerID, newLaunchOperations())
}

// RunLaunchWithOperations exposes the generic syscall seam for deterministic tests.
func RunLaunchWithOperations(
	plan clabernetesinternaldeviceplan.Plan,
	containerID string,
	operations LaunchOperations,
) error {
	if operations == nil {
		return errors.New("application launch operations are nil")
	}

	normalized, err := clabernetesinternaldeviceplan.NormalizePlan(plan)
	if err != nil {
		return err
	}

	var target *clabernetesinternaldeviceplan.ContainerPlan

	for index := range normalized.Containers {
		if normalized.Containers[index].ID == containerID {
			target = &normalized.Containers[index]

			break
		}
	}

	if target == nil {
		return errors.New("application launch target is absent from the plan")
	}

	if target.StartupDelay > 0 {
		if uint64(target.StartupDelay) > uint64((1<<63-1)/time.Second) {
			return errors.New("application startup delay exceeds runtime duration limits")
		}

		if err = operations.Delay(time.Duration(target.StartupDelay) * time.Second); err != nil {
			return fmt.Errorf("waiting for application startup delay: %w", err)
		}
	}

	mounts := make(map[string]clabernetesinternaldeviceplan.MountPlan, len(normalized.Mounts))
	for _, mount := range normalized.Mounts {
		mounts[mount.ID] = mount
	}

	for _, action := range normalized.Actions {
		if action.Phase != clabernetesinternaldeviceplan.PhasePreStart ||
			action.Target.ContainerID != containerID ||
			action.Kind != clabernetesinternaldeviceplan.ActionMount {
			continue
		}

		if action.Target.NodeID != target.NodeID || action.Mount == nil {
			return fmt.Errorf("pre-start action %q crosses application ownership", action.ID)
		}

		mount, exists := mounts[action.Mount.MountID]
		if !exists || mount.ContainerID != containerID {
			return fmt.Errorf("pre-start action %q references a foreign mount", action.ID)
		}

		if err = operations.MountFilesystem(
			action.Mount.Source,
			mount.Destination,
			action.Mount.Filesystem,
			slices.Clone(action.Mount.Options),
		); err != nil {
			return fmt.Errorf("pre-start action %q failed: %w", action.ID, err)
		}
	}

	entrypoint := slices.Clone(target.Entrypoint)
	if len(entrypoint) == 0 {
		entrypoint = slices.Clone(target.ImageEntrypoint)
	}

	command := slices.Clone(target.Command)
	if len(command) == 0 {
		command = slices.Clone(target.ImageCommand)
	}

	//nolint:gocritic // the result deliberately extends a different base slice.
	argv := append(
		entrypoint,
		command...) //nolint:gocritic // OCI argv is entrypoint plus command by contract.
	if len(argv) == 0 || strings.TrimSpace(argv[0]) == "" {
		return errors.New("application image and plan provide no executable command")
	}

	if err = operations.LimitOpenFiles(conventionalNoFileLimit); err != nil {
		return fmt.Errorf("bounding application open files: %w", err)
	}

	if err = operations.Exec(argv); err != nil {
		return fmt.Errorf("starting application process: %w", err)
	}

	return nil
}
