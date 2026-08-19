//go:build linux

package directruntime

import (
	"errors"
	"fmt"
	"os"
	"runtime"

	clabernetesdeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	"golang.org/x/sys/unix"
)

type linuxEndpointNamespace struct {
	target *os.File
	host   *os.File
}

func openEndpointNamespace(hostPath string) (EndpointNamespace, error) {
	if hostPath == "" {
		return nil, endpointNamespaceCapabilityError("host network namespace path is empty")
	}
	target, err := os.Open("/proc/self/ns/net")
	if err != nil {
		return nil, endpointNamespaceCapabilityError("target Pod network namespace is unavailable")
	}
	host, err := os.Open(hostPath) //nolint:gosec // Path is a fixed read-only host namespace mount.
	if err != nil {
		_ = target.Close()

		return nil, endpointNamespaceCapabilityError("host network namespace mount is unavailable")
	}
	targetInfo, targetErr := target.Stat()
	hostInfo, hostErr := host.Stat()
	if targetErr != nil || hostErr != nil {
		_ = target.Close()
		_ = host.Close()

		return nil, endpointNamespaceCapabilityError("network namespace identity is unavailable")
	}
	if os.SameFile(targetInfo, hostInfo) {
		_ = target.Close()
		_ = host.Close()

		return nil, endpointNamespaceCapabilityError(
			"host and target Pod network namespaces are not distinct",
		)
	}

	return &linuxEndpointNamespace{target: target, host: host}, nil
}

func (n *linuxEndpointNamespace) TargetPath() string {
	return fmt.Sprintf("/proc/self/fd/%d", n.target.Fd())
}

func (n *linuxEndpointNamespace) Execute(operation func() error) error {
	if operation == nil {
		return fmt.Errorf("endpoint namespace operation is nil")
	}

	return executeEndpointNamespace(
		int(n.target.Fd()),
		int(n.host.Fd()),
		operation,
		unix.Setns,
		runtime.LockOSThread,
		runtime.UnlockOSThread,
	)
}

func executeEndpointNamespace(
	targetFD,
	hostFD int,
	operation func() error,
	setns func(int, int) error,
	lockOSThread,
	unlockOSThread func(),
) error {
	result := make(chan error, 1)
	go func() {
		lockOSThread()
		enteredHost := false
		reuseThread := true
		var operationErr error
		defer func() {
			if recovered := recover(); recovered != nil {
				operationErr = fmt.Errorf("endpoint namespace operation panicked")
			}
			var restoreErr error
			if enteredHost {
				if err := setns(targetFD, unix.CLONE_NEWNET); err != nil {
					reuseThread = false
					restoreErr = endpointNamespaceCapabilityError(
						"cannot restore the target Pod network namespace",
					)
				}
			}
			if reuseThread {
				unlockOSThread()
			}
			result <- errors.Join(operationErr, restoreErr)
		}()

		if err := setns(hostFD, unix.CLONE_NEWNET); err != nil {
			operationErr = endpointNamespaceCapabilityError(
				"cannot enter the worker host network namespace",
			)

			return
		}
		enteredHost = true
		operationErr = operation()
	}()

	return <-result
}

func (n *linuxEndpointNamespace) Close() error {
	return errors.Join(n.target.Close(), n.host.Close())
}

func endpointNamespaceCapabilityError(message string) error {
	return &clabernetesdeviceplan.Error{
		Code:  clabernetesdeviceplan.ErrorUnsupported,
		Field: "runtime.networkNamespace", Behavior: "host-network-namespace",
		Message: message,
	}
}
