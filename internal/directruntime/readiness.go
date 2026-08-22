//nolint:err113,gocognit,gocyclo,mnd // single-pass boundary logic with structured one-off diagnostics and protocol literals.
package directruntime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	"golang.org/x/crypto/ssh"
)

const (
	defaultOCIHealthcheckTimeout = 30 * time.Second
	applicationProbeTimeout      = 5 * time.Second
	maxProbePasswordBytes        = 64 << 10
)

// ReadinessChecks contains explicit kind-neutral NodeProfile application checks.
// SSHPasswordFile
// is a single Secret-projected file and its bytes never enter the plan or command line.
type ReadinessChecks struct {
	TCPPort         int
	SSHUsername     string
	SSHPort         int
	SSHPasswordFile string
}

// RunReadiness composes the image's OCI healthcheck with the imported containerlab Node's
// package-owned IsHealthy hook. It runs inside the direct application container, so both checks
// observe the actual device process and network namespace without Docker inspection.
func RunReadiness(
	ctx context.Context,
	input clabernetesinternaldeviceplan.Input,
	plan clabernetesinternaldeviceplan.Plan,
	containerID,
	scratchRoot,
	entropyRoot,
	revision string,
	checks ReadinessChecks,
) error {
	if ctx == nil {
		return errors.New("readiness context is nil")
	}

	if err := clabernetesinternaldeviceplan.ValidatePlanInputIdentity(input, plan); err != nil {
		return err
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
		return errors.New("readiness target container is absent from the plan")
	}

	prepareImportedRuntimeCLI(normalized, containerID)

	if err = runOCIHealthcheck(ctx, target.Healthcheck); err != nil {
		return fmt.Errorf("OCI healthcheck is not healthy: %w", err)
	}

	if err = (clabernetesinternaldeviceplan.Adapter{
		Revision: revision, EntropyRoot: entropyRoot,
		PodDNSServers: RuntimePodDNSServers(),
	}).CheckReadiness(
		ctx,
		input,
		normalized,
		containerID,
		scratchRoot,
	); err != nil {
		return err
	}

	return runApplicationChecks(ctx, input, normalized, containerID, checks)
}

func runApplicationChecks(
	ctx context.Context,
	input clabernetesinternaldeviceplan.Input,
	plan clabernetesinternaldeviceplan.Plan,
	containerID string,
	checks ReadinessChecks,
) error {
	if checks.TCPPort < 0 || checks.TCPPort > 65535 ||
		checks.SSHPort < 0 || checks.SSHPort > 65535 {
		return errors.New("application readiness port is invalid")
	}

	sshFields := 0

	for _, present := range []bool{
		checks.SSHUsername != "", checks.SSHPort != 0, checks.SSHPasswordFile != "",
	} {
		if present {
			sshFields++
		}
	}

	if sshFields != 0 && sshFields != 3 {
		return errors.New("SSH readiness configuration is incomplete")
	}

	if checks.TCPPort == 0 && sshFields == 0 {
		return nil
	}

	nodeID := ""

	for _, container := range plan.Containers {
		if container.ID == containerID {
			nodeID = container.NodeID

			break
		}
	}

	if nodeID == "" {
		return errors.New("application readiness target is absent from the plan")
	}

	host := "127.0.0.1"

	for _, management := range input.Management {
		if management.NodeID != nodeID {
			continue
		}

		if management.IPv4 != "" {
			host = addressWithoutPrefix(management.IPv4)
		} else if management.IPv6 != "" {
			host = addressWithoutPrefix(management.IPv6)
		}

		break
	}

	if checks.TCPPort != 0 {
		dialer := net.Dialer{Timeout: applicationProbeTimeout}

		connection, err := dialer.DialContext(
			ctx,
			"tcp",
			net.JoinHostPort(host, strconv.Itoa(checks.TCPPort)),
		)
		if err != nil {
			return fmt.Errorf("TCP application readiness failed: %w", err)
		}

		if err = connection.Close(); err != nil {
			return fmt.Errorf("closing TCP application readiness connection: %w", err)
		}
	}

	if sshFields == 0 {
		return nil
	}

	passwordPath := filepath.Clean(checks.SSHPasswordFile)
	if !filepath.IsAbs(passwordPath) || passwordPath == string(filepath.Separator) {
		return errors.New("SSH readiness password path must be scoped and absolute")
	}

	password, err := os.ReadFile(passwordPath)
	if err != nil || len(password) == 0 || len(password) > maxProbePasswordBytes {
		return errors.New("reading bounded SSH readiness password")
	}

	sshConfig := &ssh.ClientConfig{
		User: checks.SSHUsername,
		Auth: []ssh.AuthMethod{
			ssh.Password(string(password)),
			ssh.KeyboardInteractive(func(
				_, _ string,
				questions []string,
				_ []bool,
			) ([]string, error) {
				answers := make([]string, len(questions))
				for index := range answers {
					answers[index] = string(password)
				}

				return answers, nil
			}),
		},
		Timeout:         applicationProbeTimeout,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec // Readiness authenticates the peer.
	}

	client, err := ssh.Dial(
		"tcp",
		net.JoinHostPort(host, strconv.Itoa(checks.SSHPort)),
		sshConfig,
	)
	if err != nil {
		return fmt.Errorf("SSH application readiness failed: %w", err)
	}

	if err = client.Close(); err != nil {
		return fmt.Errorf("closing SSH application readiness connection: %w", err)
	}

	return nil
}

func addressWithoutPrefix(value string) string {
	address, _, found := strings.Cut(value, "/")
	if found {
		return address
	}

	return value
}

func runOCIHealthcheck(
	ctx context.Context,
	healthcheck *clabernetesinternaldeviceplan.Healthcheck,
) error {
	if healthcheck == nil || len(healthcheck.Test) == 0 ||
		strings.EqualFold(healthcheck.Test[0], "NONE") {
		return nil
	}

	command := slices.Clone(healthcheck.Test)
	switch strings.ToUpper(command[0]) {
	case "CMD":
		command = command[1:]
	case "CMD-SHELL":
		if len(command) < 2 {
			return errors.New("healthcheck shell command is empty")
		}

		command = []string{"/bin/sh", "-c", strings.Join(command[1:], " ")}
	}

	if len(command) == 0 || strings.TrimSpace(command[0]) == "" {
		return errors.New("healthcheck command is empty")
	}

	timeout := time.Duration(healthcheck.Timeout)
	if timeout == 0 {
		timeout = defaultOCIHealthcheckTimeout
	}

	if timeout < 0 {
		return errors.New("healthcheck timeout is negative")
	}

	commandContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	process := exec.CommandContext(commandContext, command[0], command[1:]...) //nolint:gosec
	process.Stdout = os.Stdout

	process.Stderr = os.Stderr
	if err := process.Run(); err != nil {
		return err
	}

	return nil
}
