// Package deviceruntime declares shared error identities for the direct device runtime.
package deviceruntime

import "errors"

// ErrDirectRuntimeUnavailable classifies a Node the controller could not realize as a direct
// device workload; it is a fail-closed preflight error, never a fallback trigger.
var ErrDirectRuntimeUnavailable = errors.New("direct device runtime is not available")
