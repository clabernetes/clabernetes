package directruntime

import (
	"context"
	"net"
	"time"
)

// networkNamespaceDialContext returns a dial function that creates every socket in the supplied
// network namespace. Entering the namespace around an HTTP or resolver call is insufficient:
// both packages may create the actual socket in another goroutine after the caller returns.
func networkNamespaceDialContext(
	networkNamespace EndpointNamespace,
) func(context.Context, string, string) (net.Conn, error) {
	resolverDialer := &net.Dialer{FallbackDelay: -1 * time.Nanosecond}
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialInNetworkNamespace(
				ctx,
				networkNamespace,
				resolverDialer,
				network,
				address,
			)
		},
	}
	dialer := &net.Dialer{
		FallbackDelay: -1 * time.Nanosecond,
		Resolver:      resolver,
	}

	return func(ctx context.Context, network, address string) (net.Conn, error) {
		return dialInNetworkNamespace(
			ctx,
			networkNamespace,
			dialer,
			network,
			address,
		)
	}
}

func dialInNetworkNamespace(
	ctx context.Context,
	networkNamespace EndpointNamespace,
	dialer *net.Dialer,
	network,
	address string,
) (net.Conn, error) {
	if networkNamespace == nil {
		return dialer.DialContext(ctx, network, address)
	}

	var connection net.Conn

	err := networkNamespace.Execute(func() error {
		var dialErr error

		connection, dialErr = dialer.DialContext(ctx, network, address)

		return dialErr
	})
	if err != nil && connection != nil {
		_ = connection.Close()
		connection = nil
	}

	return connection, err
}
