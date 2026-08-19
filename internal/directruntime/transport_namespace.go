package directruntime

import (
	"context"
	"os"
)

const (
	// ApplicationTransportNamespaceSocketPath is the Pod-local descriptor handoff used when an
	// application moves the kubelet-assigned Pod transport interface after startup.
	ApplicationTransportNamespaceSocketPath =
		"/var/run/clabernetes/runtime-api/transport-namespace.sock"
	applicationNetworkNamespaceRoot = "/run/netns"
)

// transportNamespaceBroker accepts only a namespace that contains the exact downward-API Pod
// address. The received file descriptor is the capability; no device namespace name is trusted.
type transportNamespaceBroker interface {
	Updates() <-chan *os.File
	Errors() <-chan error
	Close() error
}

// PublishApplicationTransportNamespace locates the namespace containing podAddress from inside an
// application lifecycle worker and transfers its open descriptor to the Pod connectivity helper.
func PublishApplicationTransportNamespace(
	ctx context.Context,
	socketPath,
	podAddress string,
) error {
	return publishApplicationTransportNamespace(
		ctx,
		socketPath,
		podAddress,
		applicationNetworkNamespaceRoot,
	)
}
