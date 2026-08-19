package directruntime

import (
	"context"
	"net"
	"testing"
)

type fakeNetworkDialNamespace struct {
	executions int
}

func (*fakeNetworkDialNamespace) TargetPath() string { return "/target/netns" }

func (f *fakeNetworkDialNamespace) Execute(operation func() error) error {
	f.executions++

	return operation()
}

func (*fakeNetworkDialNamespace) Close() error { return nil }

func TestNetworkNamespaceDialerCreatesSocketInsideNamespace(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	networkNamespace := &fakeNetworkDialNamespace{}
	connection, err := networkNamespaceDialContext(networkNamespace)(
		context.Background(),
		"tcp4",
		listener.Addr().String(),
	)
	if err != nil {
		t.Fatal(err)
	}
	_ = connection.Close()

	if networkNamespace.executions != 1 {
		t.Fatalf("network namespace executions = %d, want 1", networkNamespace.executions)
	}
}
