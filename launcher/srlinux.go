package launcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"sort"
	"strings"
	"time"

	claberneteserrors "github.com/clabernetes/clabernetes/errors"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
	"github.com/google/shlex"
)

const (
	defaultContainerlabManagementNetwork = "clab"
	containerlabNodeKindLabel            = "clab-node-kind"
	containerlabNodeNameLabel            = "clab-node-name"
	containerlabNodeKindSRL              = "srl"
	containerlabNodeKindNokiaSRLinux     = "nokia_srlinux"
	forwardingProbeAddress               = "1.1.1.1"
	srLinuxForwardingPollInterval        = 500 * time.Millisecond
	srLinuxForwardingTimeout             = 2 * time.Minute
)

type commandRunner interface {
	run(ctx context.Context, args ...string) ([]byte, error)
}

type execCommandRunner struct{}

func (execCommandRunner) run(ctx context.Context, args ...string) ([]byte, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("%w: command is empty", claberneteserrors.ErrLaunch)
	}

	cmd := exec.CommandContext(ctx, args[0], args[1:]...) //nolint:gosec

	return cmd.Output()
}

func runShellCommand(
	ctx context.Context,
	runner commandRunner,
	command string,
) ([]byte, error) {
	args, err := shlex.Split(command)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: parsing command %q: %w",
			claberneteserrors.ErrLaunch,
			command,
			err,
		)
	}

	return runner.run(ctx, args...)
}

type dockerNetworkInspection struct {
	Gateway   string `json:"Gateway"`
	IPAddress string `json:"IPAddress"`
}

type dockerContainerInspection struct {
	ID     string `json:"Id"`
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	HostConfig struct {
		NetworkMode string `json:"NetworkMode"`
	} `json:"HostConfig"`
	NetworkSettings struct {
		Networks map[string]dockerNetworkInspection `json:"Networks"`
	} `json:"NetworkSettings"`
}

type srlinuxContainer struct {
	id      string
	name    string
	gateway string
	ip      string
}

func managementNetworkName(mgmt *clabernetesutilcontainerlab.MgmtNet) string {
	if mgmt != nil && mgmt.Network != "" {
		return mgmt.Network
	}

	return defaultContainerlabManagementNetwork
}

func selectSRLinuxContainers(
	containers []dockerContainerInspection,
	managementNetwork string,
) ([]srlinuxContainer, error) {
	selected := make([]srlinuxContainer, 0, len(containers))

	for _, container := range containers {
		kind := strings.ToLower(
			strings.TrimSpace(container.Config.Labels[containerlabNodeKindLabel]),
		)
		if kind != containerlabNodeKindSRL && kind != containerlabNodeKindNokiaSRLinux {
			continue
		}

		if unsupportedNetworkMode(container.HostConfig.NetworkMode) {
			continue
		}

		name := strings.TrimSpace(container.Config.Labels[containerlabNodeNameLabel])
		if name == "" {
			return nil, fmt.Errorf(
				"%w: "+
					"SR Linux container %q has no %s label",
				claberneteserrors.ErrInvalidData,
				container.ID,
				containerlabNodeNameLabel,
			)
		}

		network, ok := container.NetworkSettings.Networks[managementNetwork]
		if !ok {
			return nil, fmt.Errorf(
				"%w: "+
					"SR Linux node %q has no Docker network %q",
				claberneteserrors.ErrInvalidData,
				name,
				managementNetwork,
			)
		}

		if network.Gateway == "" {
			return nil, fmt.Errorf(
				"%w: "+
					"SR Linux node %q Docker network %q has no gateway",
				claberneteserrors.ErrInvalidData,
				name,
				managementNetwork,
			)
		}

		if network.IPAddress == "" {
			return nil, fmt.Errorf(
				"%w: "+
					"SR Linux node %q Docker network %q has no IPv4 address",
				claberneteserrors.ErrInvalidData,
				name,
				managementNetwork,
			)
		}

		if net.ParseIP(network.Gateway).To4() == nil {
			return nil, fmt.Errorf(
				"%w: "+
					"SR Linux node %q Docker network %q has invalid IPv4 gateway %q",
				claberneteserrors.ErrInvalidData,
				name,
				managementNetwork,
				network.Gateway,
			)
		}

		if net.ParseIP(network.IPAddress).To4() == nil {
			return nil, fmt.Errorf(
				"%w: "+
					"SR Linux node %q Docker network %q has invalid IPv4 address %q",
				claberneteserrors.ErrInvalidData,
				name,
				managementNetwork,
				network.IPAddress,
			)
		}

		selected = append(selected, srlinuxContainer{
			id:      container.ID,
			name:    name,
			gateway: network.Gateway,
			ip:      network.IPAddress,
		})
	}

	sort.Slice(selected, func(i, j int) bool {
		return selected[i].name < selected[j].name
	})

	return selected, nil
}

func unsupportedNetworkMode(mode string) bool {
	mode = strings.ToLower(strings.TrimSpace(mode))

	return mode == "host" || mode == "none" || strings.HasPrefix(mode, "container:")
}

func discoverSRLinuxContainers(
	ctx context.Context,
	runner commandRunner,
	nodeContainerIDs map[string]string,
	managementNetwork string,
) ([]srlinuxContainer, error) {
	nodeNames := sortedContainerNames(nodeContainerIDs)
	containerIDs := make([]string, 0, len(nodeNames))

	for _, nodeName := range nodeNames {
		containerIDs = append(containerIDs, nodeContainerIDs[nodeName])
	}

	containers, err := inspectDockerContainers(ctx, runner, containerIDs)
	if err != nil {
		return nil, err
	}

	return selectSRLinuxContainers(containers, managementNetwork)
}

func (c *clabernetes) configureSRLinuxForwarding() error {
	return configureSRLinuxForwarding(
		c.ctx,
		execCommandRunner{},
		c.nodeContainerIDs,
		c.managementNetwork,
		srLinuxForwardingPollInterval,
		srLinuxForwardingTimeout,
	)
}

func configureSRLinuxForwarding(
	ctx context.Context,
	runner commandRunner,
	nodeContainerIDs map[string]string,
	managementNetwork string,
	pollInterval,
	timeout time.Duration,
) error {
	containers, err := discoverSRLinuxContainers(
		ctx,
		runner,
		nodeContainerIDs,
		managementNetwork,
	)
	if err != nil {
		return err
	}

	for _, container := range containers {
		err = applySRLinuxForwarding(
			ctx,
			runner,
			container,
			pollInterval,
			timeout,
		)
		if err != nil {
			return err
		}
	}

	return nil
}

func waitForSRLinuxInterfaces(
	ctx context.Context,
	runner commandRunner,
	container srlinuxContainer,
	pollInterval,
	timeout time.Duration,
) error {
	checks := []struct {
		name    string
		command string
	}{
		{
			name:    "srbase-mgmt namespace",
			command: fmt.Sprintf("docker exec %s ip netns exec srbase-mgmt true", container.id),
		},
		{
			name: "srbase-mgmt mgmt0.0 interface",
			command: fmt.Sprintf(
				"docker exec %s ip netns exec srbase-mgmt ip link show dev mgmt0.0",
				container.id,
			),
		},
		{
			name: "root namespace mgmt0 interface",
			command: fmt.Sprintf(
				"docker exec %s ip link show dev mgmt0",
				container.id,
			),
		},
		{
			name: "root namespace mgmt0-0 interface",
			command: fmt.Sprintf(
				"docker exec %s ip link show dev mgmt0-0",
				container.id,
			),
		},
	}

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	for {
		missing := ""

		for _, check := range checks {
			_, err := runShellCommand(ctx, runner, check.command)
			if err != nil {
				missing = check.name

				break
			}
		}

		if missing == "" {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf(
				"waiting for SR Linux node %q %s canceled: %w",
				container.name,
				missing,
				ctx.Err(),
			)
		case <-deadline.C:
			return fmt.Errorf(
				"%w: "+
					"timed out after %s waiting for SR Linux node %q %s",
				claberneteserrors.ErrLaunch,
				timeout,
				container.name,
				missing,
			)
		case <-time.After(pollInterval):
		}
	}
}

func applySRLinuxForwarding(
	ctx context.Context,
	runner commandRunner,
	container srlinuxContainer,
	pollInterval,
	timeout time.Duration,
) error {
	err := waitForSRLinuxInterfaces(ctx, runner, container, pollInterval, timeout)
	if err != nil {
		return err
	}

	commands := []string{
		fmt.Sprintf(
			"docker exec %s ip route replace %s dev mgmt0 scope link",
			container.id,
			container.gateway,
		),
		fmt.Sprintf(
			"docker exec %s ip route replace default via %s dev mgmt0",
			container.id,
			container.gateway,
		),
		fmt.Sprintf(
			"docker exec %s ip route replace %s dev mgmt0-0 scope link",
			container.id,
			container.ip,
		),
		fmt.Sprintf(
			"docker exec %s sysctl -w net.ipv4.ip_forward=1",
			container.id,
		),
	}

	for _, command := range commands {
		_, err = runShellCommand(ctx, runner, command)
		if err != nil {
			return fmt.Errorf(
				"configuring SR Linux node %q with %q: %w",
				container.name,
				command,
				err,
			)
		}
	}

	err = verifySRLinuxForwarding(ctx, runner, container)
	if err != nil {
		return fmt.Errorf("verifying SR Linux node %q forwarding: %w", container.name, err)
	}

	return nil
}

func verifySRLinuxForwarding(
	ctx context.Context,
	runner commandRunner,
	container srlinuxContainer,
) error {
	routeChecks := []struct {
		name    string
		command string
	}{
		{
			name: "management default route",
			command: fmt.Sprintf(
				"docker exec %s ip route get %s",
				container.id,
				forwardingProbeAddress,
			),
		},
		{
			name: "management peer route",
			command: fmt.Sprintf(
				"docker exec %s ip route get %s",
				container.id,
				container.ip,
			),
		},
	}

	for _, check := range routeChecks {
		_, err := runShellCommand(ctx, runner, check.command)
		if err != nil {
			return fmt.Errorf(
				"%w: %s command failed: %w",
				claberneteserrors.ErrLaunch,
				check.name,
				err,
			)
		}
	}

	_, err := runShellCommand(
		ctx,
		runner,
		fmt.Sprintf(
			`docker exec %s sh -ec 'test "$(sysctl -n net.ipv4.ip_forward)" = 1'`,
			container.id,
		),
	)
	if err != nil {
		return fmt.Errorf("IPv4 forwarding is not enabled: %w", err)
	}

	return nil
}

func inspectDockerContainers(
	ctx context.Context,
	runner commandRunner,
	containerIDs []string,
) ([]dockerContainerInspection, error) {
	if len(containerIDs) == 0 {
		return nil, nil
	}

	output, err := runShellCommand(
		ctx,
		runner,
		fmt.Sprintf("docker inspect --type container %s", strings.Join(containerIDs, " ")),
	)
	if err != nil {
		return nil, fmt.Errorf("inspecting docker containers %q: %w", containerIDs, err)
	}

	containers := []dockerContainerInspection{}

	err = json.Unmarshal(output, &containers)
	if err != nil {
		return nil, fmt.Errorf("decoding docker inspection for %q: %w", containerIDs, err)
	}

	if len(containers) != len(containerIDs) {
		return nil, fmt.Errorf(
			"%w: "+
				"inspecting docker containers %q returned %d containers, expected %d",
			claberneteserrors.ErrLaunch,
			containerIDs,
			len(containers),
			len(containerIDs),
		)
	}

	return containers, nil
}
