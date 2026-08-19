package directruntime

import (
	"fmt"
	"slices"
	"strings"
	"time"

	clabernetesdeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
)

// LaunchOperations is the narrow application-container boundary used before replacing the c9s
// helper with the image's real process. It deliberately describes generic filesystem and process
// operations and contains no containerlab kind vocabulary.
type LaunchOperations interface {
	Delay(duration time.Duration) error
	MountFilesystem(source, destination, filesystem string, options []string) error
	Exec(argv []string) error
}

// RunLaunch applies synchronous pre-start operations and replaces the helper with the image's
// real process. This prevents a kubelet PostStart race for mount security options.
func RunLaunch(plan clabernetesdeviceplan.Plan, containerID string) error {
	return RunLaunchWithOperations(plan, containerID, newLaunchOperations())
}

// RunLaunchWithOperations exposes the generic syscall seam for deterministic tests.
func RunLaunchWithOperations(
	plan clabernetesdeviceplan.Plan,
	containerID string,
	operations LaunchOperations,
) error {
	if operations == nil {
		return fmt.Errorf("application launch operations are nil")
	}
	normalized, err := clabernetesdeviceplan.NormalizePlan(plan)
	if err != nil {
		return err
	}
	var target *clabernetesdeviceplan.ContainerPlan
	for index := range normalized.Containers {
		if normalized.Containers[index].ID == containerID {
			target = &normalized.Containers[index]

			break
		}
	}
	if target == nil {
		return fmt.Errorf("application launch target is absent from the plan")
	}
	if target.StartupDelay > 0 {
		if uint64(target.StartupDelay) > uint64((1<<63-1)/time.Second) {
			return fmt.Errorf("application startup delay exceeds runtime duration limits")
		}
		if err = operations.Delay(time.Duration(target.StartupDelay) * time.Second); err != nil {
			return fmt.Errorf("waiting for application startup delay: %w", err)
		}
	}
	mounts := make(map[string]clabernetesdeviceplan.MountPlan, len(normalized.Mounts))
	for _, mount := range normalized.Mounts {
		mounts[mount.ID] = mount
	}
	for _, action := range normalized.Actions {
		if action.Phase != clabernetesdeviceplan.PhasePreStart ||
			action.Target.ContainerID != containerID ||
			action.Kind != clabernetesdeviceplan.ActionMount {
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
	argv := append(entrypoint, command...)
	if len(argv) == 0 || strings.TrimSpace(argv[0]) == "" {
		return fmt.Errorf("application image and plan provide no executable command")
	}
	if err = operations.Exec(argv); err != nil {
		return fmt.Errorf("starting application process: %w", err)
	}

	return nil
}
