//go:build !linux

package directruntime

import "fmt"

// EnsureDeviceNetns is unsupported outside Linux. The isolated direct device runtime is a
// Linux network-namespace feature.
func EnsureDeviceNetns(string) error {
	return fmt.Errorf("private device network namespaces require Linux")
}

// CleanupDeviceNetns is a no-op outside Linux.
func CleanupDeviceNetns(string) error {
	return nil
}

type deviceNetworkNamespace struct{}

func openDeviceNetworkNamespace(string) (*deviceNetworkNamespace, error) {
	return nil, fmt.Errorf("private device network namespaces require Linux")
}

func (d *deviceNetworkNamespace) Close() error {
	return nil
}
