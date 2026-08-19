package constants

import "time"

const (
	// DefaultClientOperationTimeout is the default timeout to use for all kubernetes client
	// operations (i.e. Get, Create, etc.).
	DefaultClientOperationTimeout = 2 * time.Second
)
