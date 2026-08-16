package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	claberneteserrors "github.com/clabernetes/clabernetes/errors"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	clabernetesutil "github.com/clabernetes/clabernetes/util"
)

const (
	dockerDaemonConfig   = "/etc/docker/daemon.json"
	vfsStorageDriver     = "vfs"
	overlayStorageDriver = "overlay2"

	containerlabNodeNameLabel     = "clab-node-name"
	containerlabRootNodeNameLabel = "clab-root-node-name"
)

var (
	errEmptyNetworkModeTarget = errors.New("network mode has an empty container target")
	errNetworkTargetNotFound  = errors.New("network namespace target is not a discovered component")
	errNetworkTargetAmbiguous = errors.New("network namespace target is ambiguous")
	errNetworkNamespaceCycle  = errors.New("network namespace cycle")
	errComponentNotConnected  = errors.New("component is not connected to the namespace owner")
)

type dockerContainerInspect struct {
	ID     string `json:"Id"`
	Name   string `json:"Name"`
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	HostConfig struct {
		NetworkMode string `json:"NetworkMode"`
	} `json:"HostConfig"`
	NetworkSettings struct {
		SandboxKey string `json:"SandboxKey"`
	} `json:"NetworkSettings"`
}

type nodeContainers struct {
	containerIDs       map[string]string
	primaryContainerID string
}

func daemonConfigExists() bool {
	_, err := os.Stat(dockerDaemonConfig)

	return err == nil
}

type daemonConfig struct {
	StorageDriver      string               `json:"storage-driver"`
	InsecureRegistries []string             `json:"insecure-registries,omitempty"`
	Proxies            *daemonProxiesConfig `json:"proxies,omitempty"`
}

type daemonProxiesConfig struct {
	HTTPProxy  string `json:"http-proxy,omitempty"`
	HTTPSProxy string `json:"https-proxy,omitempty"`
	NoProxy    string `json:"no-proxy,omitempty"`
}

type dockerContainerState struct {
	Running    bool                   `json:"Running"`
	Paused     bool                   `json:"Paused"`
	Restarting bool                   `json:"Restarting"`
	Dead       bool                   `json:"Dead"`
	Health     *dockerContainerHealth `json:"Health,omitempty"`
}

type dockerContainerHealth struct {
	Status string `json:"Status"`
}

func getProxiesConfig() *daemonProxiesConfig {
	httpProxy := clabernetesutil.GetEnvStrOrDefault(
		clabernetesconstants.HTTPProxyEnv,
		os.Getenv(clabernetesconstants.HTTPProxyEnvLower),
	)
	httpsProxy := clabernetesutil.GetEnvStrOrDefault(
		clabernetesconstants.HTTPSProxyEnv,
		os.Getenv(clabernetesconstants.HTTPSProxyEnvLower),
	)
	noProxy := clabernetesutil.GetEnvStrOrDefault(
		clabernetesconstants.NoProxyEnv,
		os.Getenv(clabernetesconstants.NoProxyEnvLower),
	)

	if httpProxy == "" && httpsProxy == "" {
		return nil
	}

	return &daemonProxiesConfig{
		HTTPProxy:  httpProxy,
		HTTPSProxy: httpsProxy,
		NoProxy:    noProxy,
	}
}

func handleDockerDaemonConfig() error {
	config := daemonConfig{
		StorageDriver: vfsStorageDriver,
		Proxies:       getProxiesConfig(),
	}

	insecureRegistries := os.Getenv(clabernetesconstants.LauncherInsecureRegistries)
	if insecureRegistries != "" {
		config.InsecureRegistries = strings.Split(insecureRegistries, ",")
	}

	if config.Proxies == nil && config.InsecureRegistries == nil {
		// nothing to configure, leave the daemon config alone (unset)
		return nil
	}

	// if the pod is privileged we can run w/ overlayfs instead of vfs which should
	// be much more efficient size-wise if not also perofrmance-wise; this *does* assume
	// the hosts kernel supports overlayfs but that *should* be true almost everywhere at
	// this point in time... i hope :P
	if !strings.EqualFold(
		os.Getenv(clabernetesconstants.LauncherPrivilegedEnv),
		clabernetesconstants.True,
	) {
		config.StorageDriver = overlayStorageDriver
	}

	rendered, err := json.MarshalIndent(config, "", "    ")
	if err != nil {
		return err
	}

	err = os.WriteFile(
		dockerDaemonConfig,
		rendered,
		clabernetesconstants.PermissionsEveryoneReadWriteOwnerExecute,
	)
	if err != nil {
		return err
	}

	return nil
}

func enableLegacyIPTables(ctx context.Context, logger io.Writer) error {
	updateCmd := exec.CommandContext(
		ctx,
		"update-alternatives",
		"--set",
		"iptables",
		"/usr/sbin/iptables-legacy",
	)

	updateCmd.Stdout = logger
	updateCmd.Stderr = logger

	err := updateCmd.Run()
	if err != nil {
		return err
	}

	return nil
}

func startDocker(ctx context.Context, logger io.Writer) error {
	var attempts int

	for {
		psCmd := exec.CommandContext(ctx, "docker", "ps")

		psCmd.Stdout = logger
		psCmd.Stderr = logger

		err := psCmd.Run()
		if err == nil {
			// exit 0, docker seems happy
			return nil
		}

		if attempts > maxDockerLaunchAttempts {
			return fmt.Errorf("%w: failed starting docker", claberneteserrors.ErrLaunch)
		}

		startCmd := exec.CommandContext(ctx, "service", "docker", "start")

		startCmd.Stdout = logger
		startCmd.Stderr = logger

		err = startCmd.Run()
		if err != nil {
			return err
		}

		time.Sleep(time.Second)

		attempts++
	}
}

func getContainerIDs(ctx context.Context, all bool) ([]string, error) {
	args := []string{"ps"}

	if all {
		args = append(args, "-a")
	}

	args = append(args, "--quiet")

	psCmd := exec.CommandContext(ctx, "docker", args...)

	output, err := psCmd.Output()
	if err != nil {
		return nil, err
	}

	containerIDLines := strings.Split(string(output), "\n")

	var containerIDs []string

	for _, line := range containerIDLines {
		trimmedLine := strings.TrimSpace(line)

		if trimmedLine != "" {
			containerIDs = append(containerIDs, trimmedLine)
		}
	}

	return containerIDs, nil
}

func printContainerLogs(
	ctx context.Context,
	logger claberneteslogging.Instance,
	containerIDs []string,
) {
	for _, containerID := range containerIDs {
		args := []string{
			"logs",
			containerID,
		}

		cmd := exec.CommandContext(ctx, "docker", args...) //nolint:gosec

		cmd.Stdout = logger
		cmd.Stderr = logger

		err := cmd.Run()
		if err != nil {
			logger.Warnf(
				"printing node logs for container id %q failed, err: %s", containerID, err,
			)
		}
	}
}

func tailContainerLogs(
	ctx context.Context,
	logger claberneteslogging.Instance,
	nodeLogger io.Writer,
	containerIDs []string,
) error {
	nodeLogFile, err := os.Create("node.log")
	if err != nil {
		return err
	}

	nodeOutWriter := io.MultiWriter(nodeLogger, nodeLogFile)

	for _, containerID := range containerIDs {
		go func(containerID string, nodeOutWriter io.Writer) {
			args := []string{
				"logs",
				"-f",
				containerID,
			}

			cmd := exec.CommandContext(ctx, "docker", args...) //nolint:gosec

			cmd.Stdout = nodeOutWriter
			cmd.Stderr = nodeOutWriter

			err = cmd.Run()
			if err != nil {
				logger.Warnf(
					"tailing node logs for container id %q failed, err: %s", containerID, err,
				)
			}
		}(containerID, nodeOutWriter)
	}

	return nil
}

func getContainerIDForNodeName(ctx context.Context, nodeName string) (string, error) {
	containerIDs, err := getContainerIDsForLabel(ctx, containerlabNodeNameLabel, nodeName)
	if err != nil {
		return "", err
	}

	if len(containerIDs) > 1 {
		return "", fmt.Errorf(
			"%w: found multiple containers for node %q",
			claberneteserrors.ErrInvalidData,
			nodeName,
		)
	}

	if len(containerIDs) == 0 {
		return "", nil
	}

	return containerIDs[0], nil
}

func getContainerIDsForLabel(ctx context.Context, label, value string) ([]string, error) {
	psCmd := exec.CommandContext( //nolint:gosec
		ctx,
		"docker",
		"ps",
		"--all",
		"--quiet",
		"--filter",
		fmt.Sprintf("label=%s=%s", label, value),
	)

	output, err := psCmd.Output()
	if err != nil {
		return nil, err
	}

	return strings.Fields(string(output)), nil
}

func getNodeContainers(ctx context.Context, nodeName string) (*nodeContainers, error) {
	containerID, err := getContainerIDForNodeName(ctx, nodeName)
	if err != nil {
		return nil, err
	}

	if containerID != "" {
		return &nodeContainers{
			containerIDs:       map[string]string{nodeName: containerID},
			primaryContainerID: containerID,
		}, nil
	}

	componentIDs, err := getContainerIDsForLabel(ctx, containerlabRootNodeNameLabel, nodeName)
	if err != nil {
		return nil, err
	}

	if len(componentIDs) == 0 {
		return &nodeContainers{containerIDs: map[string]string{}}, nil
	}

	inspectArgs := append([]string{"inspect"}, componentIDs...)
	inspectCmd := exec.CommandContext(ctx, "docker", inspectArgs...) //nolint:gosec

	output, err := inspectCmd.Output()
	if err != nil {
		return nil, err
	}

	inspected := []*dockerContainerInspect{}

	err = json.Unmarshal(output, &inspected)
	if err != nil {
		return nil, fmt.Errorf("failed decoding docker component containers: %w", err)
	}

	return resolveComponentContainers(nodeName, inspected)
}

func resolveComponentContainers( //nolint:gocyclo
	rootNodeName string,
	inspected []*dockerContainerInspect,
) (*nodeContainers, error) {
	resolved := &nodeContainers{containerIDs: map[string]string{}}
	containersByID := make(map[string]*dockerContainerInspect, len(inspected))
	networkTargets := make(map[string]string, len(inspected))
	sandboxKey := ""

	for _, container := range inspected {
		if container == nil || container.ID == "" {
			return nil, fmt.Errorf(
				"%w: component container metadata for node %q has an empty container id",
				claberneteserrors.ErrInvalidData,
				rootNodeName,
			)
		}

		componentName := container.Config.Labels[containerlabNodeNameLabel]
		if componentName == "" {
			return nil, fmt.Errorf(
				"%w: component container %q for node %q has no %q label",
				claberneteserrors.ErrInvalidData,
				container.ID,
				rootNodeName,
				containerlabNodeNameLabel,
			)
		}

		if existingID := resolved.containerIDs[componentName]; existingID != "" {
			return nil, fmt.Errorf(
				"%w: found multiple component containers named %q for node %q",
				claberneteserrors.ErrInvalidData,
				componentName,
				rootNodeName,
			)
		}

		resolved.containerIDs[componentName] = container.ID
		containersByID[container.ID] = container

		if container.NetworkSettings.SandboxKey != "" {
			if sandboxKey == "" {
				sandboxKey = container.NetworkSettings.SandboxKey
			} else if sandboxKey != container.NetworkSettings.SandboxKey {
				return nil, fmt.Errorf(
					"%w: component containers for node %q do not share a network namespace",
					claberneteserrors.ErrInvalidData,
					rootNodeName,
				)
			}
		}

		networkTarget, targetErr := containerNetworkModeTarget(container.HostConfig.NetworkMode)
		if targetErr != nil {
			return nil, fmt.Errorf(
				"%w: component container %q for node %q: %w",
				claberneteserrors.ErrInvalidData,
				container.ID,
				rootNodeName,
				targetErr,
			)
		}

		if networkTarget == "" {
			if resolved.primaryContainerID != "" {
				return nil, fmt.Errorf(
					"%w: found multiple network namespace owners for component node %q",
					claberneteserrors.ErrInvalidData,
					rootNodeName,
				)
			}

			resolved.primaryContainerID = container.ID
		} else {
			networkTargets[container.ID] = networkTarget
		}
	}

	if resolved.primaryContainerID == "" {
		return nil, fmt.Errorf(
			"%w: no network namespace owner found for component node %q",
			claberneteserrors.ErrInvalidData,
			rootNodeName,
		)
	}

	for containerID, networkTarget := range networkTargets {
		targetID, targetErr := resolveComponentContainerReference(
			networkTarget,
			containersByID,
		)
		if targetErr != nil {
			return nil, fmt.Errorf(
				"%w: component container %q for node %q: %w",
				claberneteserrors.ErrInvalidData,
				containerID,
				rootNodeName,
				targetErr,
			)
		}

		networkTargets[containerID] = targetID
	}

	for containerID := range containersByID {
		err := validateComponentNamespace(
			containerID,
			resolved.primaryContainerID,
			networkTargets,
			map[string]struct{}{},
		)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: component containers for node %q: %w",
				claberneteserrors.ErrInvalidData,
				rootNodeName,
				err,
			)
		}
	}

	return resolved, nil
}

func containerNetworkModeTarget(networkMode string) (string, error) {
	networkMode = strings.TrimSpace(networkMode)
	if !strings.HasPrefix(networkMode, "container:") {
		return "", nil
	}

	target := strings.TrimPrefix(networkMode, "container:")
	if target == "" {
		return "", errEmptyNetworkModeTarget
	}

	return target, nil
}

func resolveComponentContainerReference(
	reference string,
	containersByID map[string]*dockerContainerInspect,
) (string, error) {
	reference = strings.TrimPrefix(strings.TrimSpace(reference), "/")
	matches := make([]string, 0, 1)

	for containerID, container := range containersByID {
		componentName := container.Config.Labels[containerlabNodeNameLabel]
		containerName := strings.TrimPrefix(container.Name, "/")

		if reference == containerID ||
			strings.HasPrefix(containerID, reference) ||
			reference == componentName ||
			reference == containerName {
			matches = append(matches, containerID)
		}
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf(
			"%w: %q",
			errNetworkTargetNotFound,
			reference,
		)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("%w: %q", errNetworkTargetAmbiguous, reference)
	}
}

func validateComponentNamespace(
	containerID,
	primaryContainerID string,
	networkTargets map[string]string,
	visiting map[string]struct{},
) error {
	if containerID == primaryContainerID {
		return nil
	}

	if _, alreadyVisiting := visiting[containerID]; alreadyVisiting {
		return fmt.Errorf("%w includes container %q", errNetworkNamespaceCycle, containerID)
	}

	targetID, hasTarget := networkTargets[containerID]
	if !hasTarget {
		return fmt.Errorf(
			"%w: %q",
			errComponentNotConnected,
			containerID,
		)
	}

	visiting[containerID] = struct{}{}
	err := validateComponentNamespace(
		targetID,
		primaryContainerID,
		networkTargets,
		visiting,
	)
	delete(visiting, containerID)

	return err
}

func getContainerAddr(ctx context.Context, containerID string) (string, error) {
	inspectCmd := exec.CommandContext( //nolint: gosec
		ctx,
		"docker",
		"inspect",
		"--format",
		"{{range.NetworkSettings.Networks}}{{.IPAddress}}{{end}}",
		containerID,
	)

	output, err := inspectCmd.Output()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(output)), nil
}

// getContainerReadiness reports the only readiness contract Docker exposes generically: the
// container must be running and, when its image defines a Docker healthcheck, that healthcheck
// must be healthy. It deliberately does not infer application ports or device-kind behavior.
func getContainerReadiness(ctx context.Context, containerID string) (bool, error) {
	inspectCmd := exec.CommandContext( //nolint:gosec
		ctx,
		"docker",
		"inspect",
		"--format",
		"{{json .State}}",
		containerID,
	)

	output, err := inspectCmd.Output()
	if err != nil {
		return false, err
	}

	return parseContainerReadiness(output)
}

func parseContainerReadiness(value []byte) (bool, error) {
	state := &dockerContainerState{}

	err := json.Unmarshal(value, state)
	if err != nil {
		return false, fmt.Errorf("failed decoding docker container state: %w", err)
	}

	if !state.Running || state.Paused || state.Restarting || state.Dead {
		return false, nil
	}

	if state.Health == nil {
		return true, nil
	}

	return strings.EqualFold(state.Health.Status, "healthy"), nil
}
