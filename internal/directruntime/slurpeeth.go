package directruntime

import "context"

// SlurpeethSegment is one generic TCP-backed point-to-point segment owned by the direct
// connectivity helper. It contains no device-kind information.
type SlurpeethSegment struct {
	Owner       string
	ID          uint16
	Interface   string
	Destination string
}

// SlurpeethRuntime owns the one slurpeeth transport process for a direct Pod. Reconcile replaces
// the complete desired segment set; an empty set stops the process.
type SlurpeethRuntime interface {
	Reconcile(ctx context.Context, segments []SlurpeethSegment) error
	Ready() (bool, error)
	Errors() <-chan error
	Close() error
}
