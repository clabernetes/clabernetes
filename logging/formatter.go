package logging

import (
	"fmt"
	"strings"

	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
)

const (
	colorStop               = "\033[0m"
	klogMessageMinimumParts = 5
)

var colorMap = map[string]string{ //nolint:gochecknoglobals
	clabernetesconstants.Debug:    "\033[34m",
	clabernetesconstants.Info:     "\033[32m",
	clabernetesconstants.Warn:     "\033[33m",
	clabernetesconstants.Critical: "\033[31m",
}

// Formatter is a type representing a valid logging formatter function. It should accept a logging
// instance, a level string, and a message string, returning a formatted message string.
type Formatter func(i Instance, l, m string) string

func shortenString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}

	return fmt.Sprintf("%s*", s[:maxLen-1])
}

// DefaultFormatter is the default logging instance formatter -- this formatter simply adds colors
// to the log message based on log level.
func DefaultFormatter(i Instance, l, m string) string {
	switch l {
	case clabernetesconstants.Debug:
		l = colorMap[clabernetesconstants.Debug] + strings.ToUpper(l) + colorStop
	case clabernetesconstants.Info:
		l = colorMap[clabernetesconstants.Info] + strings.ToUpper(l) + colorStop
	case clabernetesconstants.Warn:
		l = colorMap[clabernetesconstants.Warn] + strings.ToUpper(l) + colorStop
	case clabernetesconstants.Critical, clabernetesconstants.Fatal:
		l = colorMap[clabernetesconstants.Critical] + strings.ToUpper(l) + colorStop
	}

	maxNameLen := 25

	// pad the level extra for the ansi code magic
	return fmt.Sprintf("%17s | %25s | %s", l, shortenString(i.GetName(), maxNameLen), m)
}

// DefaultKlogFormatter is the default logging instance formatter for *klog* logs. Ortica redirects
// klog logs through its own logging manager to give us more control, this formatter is applied by
// default to those messages only.
func DefaultKlogFormatter(i Instance, l, m string) string {
	_ = l

	parts := strings.Fields(m)

	if len(parts) < klogMessageMinimumParts {
		// obviously nicer at some point, just not sure what other styles/formats klog may
		// output at this point.
		panic(fmt.Sprintf("failed parsing '%s'", m))
	}

	newM := fmt.Sprintf(
		"klog %s: %s",
		strings.TrimRight(parts[3], "]"),
		strings.Join(parts[4:], " "),
	)

	return DefaultFormatter(i, clabernetesconstants.Info, newM)
}
