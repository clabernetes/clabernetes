package logging

import (
	"fmt"
	"strings"

	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
)

// ValidateLogLevel accepts a string l and returns a case-normalized log level corresponding to l,
// if l is a valid log level, or an error.
func ValidateLogLevel(l string) (string, error) {
	ll := strings.ToLower(l)

	switch ll {
	case clabernetesconstants.Info:
		return clabernetesconstants.Info, nil
	case clabernetesconstants.Debug:
		return clabernetesconstants.Debug, nil
	case clabernetesconstants.Critical:
		return clabernetesconstants.Critical, nil
	case clabernetesconstants.Disabled:
		return clabernetesconstants.Disabled, nil
	default:
		return "", fmt.Errorf("%w: logging level '%s' uknown", ErrLoggingInstance, l)
	}
}
