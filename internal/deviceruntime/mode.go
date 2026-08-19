//nolint:nlreturn,noinlineerr,wsl_v5 // Small value parsing reads best as compact guard clauses.
package deviceruntime

import (
	"errors"
	"fmt"
	"strings"
)

// Mode selects the temporary node-device runtime during migration.
type Mode string

// Supported temporary runtime modes.
const (
	// ModeNested preserves the launcher-managed nested runtime.
	ModeNested Mode = "nested"
	ModeDirect Mode = "direct"
)

// Runtime-mode validation and migration-state errors.
var (
	// ErrInvalidMode classifies unsupported runtime-mode configuration.
	ErrInvalidMode              = errors.New("invalid device runtime mode")
	ErrDirectRuntimeUnavailable = errors.New("direct device runtime is not available")
)

// ParseMode validates the temporary development-mode setting. An omitted value
// preserves the existing nested runtime during migration; every non-empty value
// must be explicit and valid.
func ParseMode(value string) (Mode, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return ModeNested, nil
	}

	mode := Mode(value)
	if err := mode.Validate(); err != nil {
		return "", err
	}
	return mode, nil
}

// Validate rejects every runtime mode except the two explicit migration modes.
func (m Mode) Validate() error {
	switch m {
	case ModeNested, ModeDirect:
		return nil
	default:
		return fmt.Errorf("%w %q: want %q or %q", ErrInvalidMode, m, ModeNested, ModeDirect)
	}
}
