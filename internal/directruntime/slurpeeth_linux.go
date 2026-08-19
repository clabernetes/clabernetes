//go:build linux

//nolint:noinlineerr,wsl_v5 // Process supervision is clearer as compact state guards.
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
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/carlmontanari/slurpeeth/slurpeeth"
	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	"gopkg.in/yaml.v3"
)

const (
	slurpeethConfigFile  = "slurpeeth.yaml"
	slurpeethReadyFile   = "slurpeeth-ready"
	slurpeethStopTimeout = 5 * time.Second
	privateFileMode      = 0o600
)

var errDirectSlurpeeth = errors.New("direct slurpeeth runtime")

type slurpeethChild struct {
	command *exec.Cmd
	done    chan struct{}
	err     error
}

type processSlurpeethRuntime struct {
	stateDirectory string
	errors         chan error
	newCommand     func(configPath, readyPath string) (*exec.Cmd, error)

	mu     sync.Mutex
	child  *slurpeethChild
	digest string
}

func newSlurpeethRuntime(stateDirectory string) SlurpeethRuntime {
	return &processSlurpeethRuntime{
		stateDirectory: stateDirectory,
		errors:         make(chan error, 1),
		newCommand:     newSlurpeethCommand,
	}
}

func newSlurpeethCommand(configPath, readyPath string) (*exec.Cmd, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolving connectivity helper executable: %w", err)
	}
	command := exec.CommandContext( //nolint:gosec // os.Executable is this signed helper binary.
		context.Background(),
		executable,
		"device-runtime",
		"slurpeeth-daemon",
		"--config",
		configPath,
		"--ready",
		readyPath,
	)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}

	return command, nil
}

func (r *processSlurpeethRuntime) Reconcile(
	ctx context.Context,
	segments []SlurpeethSegment,
) error {
	if ctx == nil {
		return fmt.Errorf("%w: context is nil", errDirectSlurpeeth)
	}
	normalized, err := normalizeSlurpeethSegments(segments)
	if err != nil {
		return err
	}
	if len(normalized) == 0 {
		return r.stop()
	}
	raw, digest, err := renderSlurpeethConfig(normalized)
	if err != nil {
		return err
	}
	r.mu.Lock()
	current := r.child
	currentDigest := r.digest
	r.mu.Unlock()
	if current != nil && currentDigest == digest {
		return nil
	}
	if err = r.stop(); err != nil {
		return fmt.Errorf("stopping replaced slurpeeth process: %w", err)
	}
	configPath := filepath.Join(r.stateDirectory, slurpeethConfigFile)
	readyPath := filepath.Join(r.stateDirectory, slurpeethReadyFile)
	if err = writePrivateFileAtomically(configPath, raw); err != nil {
		return fmt.Errorf("writing slurpeeth configuration: %w", err)
	}
	if err = os.Remove(readyPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing stale slurpeeth readiness: %w", err)
	}
	command, err := r.newCommand(configPath, readyPath)
	if err != nil {
		return err
	}
	child := &slurpeethChild{command: command, done: make(chan struct{})}
	if err = command.Start(); err != nil {
		return fmt.Errorf("starting slurpeeth process: %w", err)
	}
	go func() {
		child.err = command.Wait()
		close(child.done)
	}()
	r.mu.Lock()
	r.child = child
	r.digest = digest
	r.mu.Unlock()
	go r.monitor(child)

	return nil
}

func (r *processSlurpeethRuntime) Ready() (bool, error) {
	r.mu.Lock()
	child := r.child
	digest := r.digest
	r.mu.Unlock()
	if digest == "" {
		return true, nil
	}
	if child == nil {
		return false, nil
	}
	select {
	case <-child.done:
		return false, nil
	default:
	}
	raw, err := os.ReadFile(filepath.Join(r.stateDirectory, slurpeethReadyFile))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reading slurpeeth readiness: %w", err)
	}
	if strings.TrimSpace(string(raw)) != digest {
		return false, fmt.Errorf("%w: process readiness digest does not match", errDirectSlurpeeth)
	}

	return true, nil
}

func (r *processSlurpeethRuntime) Errors() <-chan error {
	return r.errors
}

func (r *processSlurpeethRuntime) Close() error {
	return r.stop()
}

func (r *processSlurpeethRuntime) monitor(child *slurpeethChild) {
	<-child.done
	r.mu.Lock()
	unexpected := r.child == child
	if unexpected {
		r.child = nil
	}
	r.mu.Unlock()
	if !unexpected {
		return
	}
	err := child.err
	if err == nil {
		err = fmt.Errorf("%w: process stopped unexpectedly", errDirectSlurpeeth)
	} else {
		err = fmt.Errorf("slurpeeth process failed: %w", err)
	}
	select {
	case r.errors <- err:
	default:
	}
}

func (r *processSlurpeethRuntime) stop() error {
	r.mu.Lock()
	child := r.child
	r.child = nil
	r.digest = ""
	r.mu.Unlock()
	if child == nil {
		return nil
	}
	if err := child.command.Process.Signal(syscall.SIGTERM); err != nil &&
		!errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("signaling slurpeeth process: %w", err)
	}
	select {
	case <-child.done:
		if child.err != nil {
			var exitErr *exec.ExitError
			if !errors.As(child.err, &exitErr) ||
				(!exitErr.Success() && !processExitedFromSignal(exitErr, syscall.SIGTERM)) {
				return fmt.Errorf("waiting for slurpeeth process: %w", child.err)
			}
		}

		return nil
	case <-time.After(slurpeethStopTimeout):
		if err := child.command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("killing unresponsive slurpeeth process: %w", err)
		}
		<-child.done

		return fmt.Errorf("%w: process did not stop before its deadline", errDirectSlurpeeth)
	}
}

func processExitedFromSignal(exitErr *exec.ExitError, expected syscall.Signal) bool {
	if exitErr == nil || exitErr.ProcessState == nil {
		return false
	}
	status, ok := exitErr.ProcessState.Sys().(syscall.WaitStatus)

	return ok && status.Signaled() && status.Signal() == expected
}

func normalizeSlurpeethSegments(segments []SlurpeethSegment) ([]SlurpeethSegment, error) {
	normalized := slices.Clone(segments)
	slices.SortFunc(normalized, func(left, right SlurpeethSegment) int {
		if left.ID != right.ID {
			return int(left.ID) - int(right.ID)
		}

		return strings.Compare(left.Owner, right.Owner)
	})
	owners := map[string]bool{}
	ids := map[uint16]bool{}
	interfaces := map[string]bool{}
	for _, segment := range normalized {
		if segment.Owner == "" || segment.ID == 0 ||
			!validLinuxInterfaceName(segment.Interface) || segment.Destination == "" ||
			owners[segment.Owner] || ids[segment.ID] || interfaces[segment.Interface] {
			return nil, fmt.Errorf(
				"%w: segment identity is invalid or duplicated",
				errDirectSlurpeeth,
			)
		}
		address := strings.TrimSuffix(strings.TrimPrefix(segment.Destination, "["), "]")
		if net.ParseIP(address) == nil {
			return nil, fmt.Errorf(
				"%w: destination is not a resolved IP address",
				errDirectSlurpeeth,
			)
		}
		owners[segment.Owner] = true
		ids[segment.ID] = true
		interfaces[segment.Interface] = true
	}

	return normalized, nil
}

func renderSlurpeethConfig(
	segments []SlurpeethSegment,
) (raw []byte, digest string, returnErr error) {
	config := slurpeeth.Config{Segments: make([]slurpeeth.Segment, 0, len(segments))}
	for _, segment := range segments {
		config.Segments = append(config.Segments, slurpeeth.Segment{
			Name: segment.Owner, ID: segment.ID,
			Interfaces:   []string{segment.Interface},
			Destinations: []string{segment.Destination},
		})
	}
	raw, err := yaml.Marshal(config)
	if err != nil {
		return nil, "", fmt.Errorf(
			"serializing slurpeeth configuration: %w",
			err,
		)
	}

	return raw, clabernetesinternaldeviceplan.Digest(raw), nil
}

func writePrivateFileAtomically(path string, raw []byte) error {
	directory := filepath.Dir(filepath.Clean(path))
	temporary, err := os.CreateTemp(directory, ".slurpeeth-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = os.Remove(temporaryPath)
	}()
	if _, err = temporary.Write(raw); err == nil {
		err = temporary.Chmod(privateFileMode)
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}

	return os.Rename(temporaryPath, filepath.Clean(path))
}

// RunSlurpeethDaemon owns the generic packet/TCP transport in a dedicated helper child process.
// The parent connectivity process supervises this child and replaces it when the complete segment
// set changes.
func RunSlurpeethDaemon(configPath, readyPath string) error {
	cleanConfig := filepath.Clean(configPath)
	cleanReady := filepath.Clean(readyPath)
	if !filepath.IsAbs(cleanConfig) || !filepath.IsAbs(cleanReady) ||
		cleanConfig == string(filepath.Separator) || cleanReady == string(filepath.Separator) {
		return fmt.Errorf("%w: daemon paths must be scoped absolute paths", errDirectSlurpeeth)
	}
	raw, err := os.ReadFile(cleanConfig)
	if err != nil {
		return fmt.Errorf("reading slurpeeth daemon configuration: %w", err)
	}
	config := slurpeeth.Config{}
	if err = yaml.Unmarshal(raw, &config); err != nil {
		return fmt.Errorf("parsing slurpeeth daemon configuration: %w", err)
	}
	digest := clabernetesinternaldeviceplan.Digest(raw)

	return runDirectSlurpeethDaemon(config, cleanReady, digest)
}
