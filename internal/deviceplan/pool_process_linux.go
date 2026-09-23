//nolint:err113,mnd // Process boundary uses local diagnostics, private modes and a fixed deadline ceiling.
package deviceplan

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// RunPoolIdle reaps orphaned request descendants while this worker is container PID 1.
func RunPoolIdle(ctx context.Context) error {
	children := make(chan os.Signal, 1)
	signal.Notify(children, syscall.SIGCHLD)
	defer signal.Stop(children)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-children:
			for {
				pid, err := unix.Wait4(-1, nil, unix.WNOHANG, nil)
				if pid <= 0 || err != nil {
					break
				}
			}
		}
	}
}

// RunPoolProcess supervises one fresh planner process. A kernel lock also excludes requests
// from a replacement controller until an old request has actually stopped. The child inherits
// that lock and dies with its supervisor. Neither cancellation nor a controller restart can
// release a slot while old imported hooks still run.
func RunPoolProcess(
	ctx context.Context,
	input *os.File,
	output, stderr io.Writer,
	revision string,
	deadline time.Duration,
) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}

	return runPoolProcess(
		ctx,
		input,
		output,
		stderr,
		revision,
		deadline,
		PoolScratchRoot,
		executable,
	)
}

func runPoolProcess(ctx context.Context, input *os.File, output, stderr io.Writer,
	revision string, deadline time.Duration, scratchRoot, executable string,
) error {
	if deadline <= 0 || deadline > 5*time.Minute {
		return errors.New("planner deadline must be between zero and five minutes")
	}
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	lock, err := os.OpenFile( //nolint:gosec // scratchRoot is fixed by production code and scoped by tests.
		filepath.Join(scratchRoot, "worker.lock"),
		os.O_CREATE|os.O_RDWR,
		0o600,
	)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return errors.New("planner worker is busy")
	}
	// A killed worker may leave private scratch behind. The exclusive lock proves there is
	// no previous request using it before cleanup or a new request receives any material.
	entries, err := os.ReadDir(scratchRoot)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "request-") {
			if err = os.RemoveAll(filepath.Join(scratchRoot, entry.Name())); err != nil {
				return err
			}
		}
	}
	root, err := os.MkdirTemp(scratchRoot, "request-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(root) }()
	bootstrap, err := readPoolBootstrapContext(ctx, input)
	if err != nil {
		return errors.New("cannot read planner request material")
	}
	if err = stagePoolBootstrap(root, bootstrap); err != nil {
		return err
	}
	//nolint:gosec // Re-exec the current binary with fixed arguments and no shell.
	command := exec.CommandContext(
		ctx,
		executable,
		"node-plan",
		"--session",
		"--input",
		"-",
		"--revision",
		revision,
		"--maxInputBytes",
		"8388608",
		"--payloads",
		filepath.Join(root, "payloads"),
		"--entropy",
		filepath.Join(root, "entropy"),
		"--certificates",
		filepath.Join(root, "certificates"),
	)
	command.Env = []string{"TMPDIR=" + filepath.Join(root, "tmp")}
	command.Dir = root
	command.Stdin, command.Stdout, command.Stderr = input, output, stderr
	command.ExtraFiles = []*os.File{lock}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	command.Cancel = func() error { return syscall.Kill(-command.Process.Pid, syscall.SIGKILL) }
	command.WaitDelay = time.Second
	if err = command.Start(); err != nil {
		return err
	}
	// Hooks must not leave background descendants after a successful or failed evaluation.
	defer func() { _ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL) }()

	return command.Wait()
}

func readPoolBootstrapContext(ctx context.Context, input *os.File) (PoolBootstrap, error) {
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = input.Close()
		close(closed)
	})
	// Complete any cancellation close before os/exec reads the file descriptor for the child.
	defer func() {
		if !stop() {
			<-closed
		}
	}()

	return readPoolBootstrap(input)
}
