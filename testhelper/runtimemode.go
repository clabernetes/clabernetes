package testhelper

import (
	"os"
	"testing"
)

// DeviceRuntimeModeEnv selects which manager runtime the e2e suites assume is deployed; the
// e2e make targets export it alongside the matching Helm value.
const DeviceRuntimeModeEnv = "C9S_E2E_DEVICE_RUNTIME_MODE"

// DeviceRuntimeMode returns the runtime mode the e2e cluster was deployed with, defaulting to
// the chart default of "nested" when unset.
func DeviceRuntimeMode() string {
	mode := os.Getenv(DeviceRuntimeModeEnv)
	if mode == "" {
		return "nested"
	}

	return mode
}

// SkipUnlessDeviceRuntimeMode skips the test when the deployed manager runtime does not match
// the mode whose golden fixtures and behavior the test asserts.
func SkipUnlessDeviceRuntimeMode(t *testing.T, mode string) {
	t.Helper()

	if DeviceRuntimeMode() != mode {
		t.Skipf("test requires %s=%s, deployed mode is %q", DeviceRuntimeModeEnv, mode, DeviceRuntimeMode())
	}
}
