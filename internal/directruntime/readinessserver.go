package directruntime

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
)

const (
	// ConnectivityReadinessPath is the HTTP path the kubelet probes for connectivity readiness.
	ConnectivityReadinessPath = "/readyz"
	// connectivityReadinessHeaderTimeout bounds a probe request header.
	connectivityReadinessHeaderTimeout = 5 * time.Second
)

// connectivityReadinessServer answers the kubelet's startup and readiness probes over HTTP on
// the Pod address. The answer is the same readiness evaluation the connectivity-ready command
// performs; serving it from the running sidecar spares the node one runtime-binary exec per
// second per Pod, which is what bounded device Pod density before.
type connectivityReadinessServer struct {
	listener net.Listener
	server   *http.Server
}

// startConnectivityReadinessServer binds the readiness endpoint on the Pod address; without a
// Pod address (tests, non-Kubernetes contexts) there is nothing to probe and nothing starts.
func startConnectivityReadinessServer(
	plan clabernetesinternaldeviceplan.Plan,
	options ConnectivityOptions,
) *connectivityReadinessServer {
	if options.PodAddress == "" {
		return nil
	}

	address := net.JoinHostPort(
		options.PodAddress,
		strconv.Itoa(clabernetesconstants.ConnectivityReadinessPort),
	)

	//nolint:noctx // The endpoint lives for the whole sidecar, not one context.
	listener, err := net.Listen("tcp", address)
	if err != nil {
		// The Pod stays unready and the kubelet reports the refused probe, which names the
		// problem better than a sidecar restart loop would.
		fmt.Fprintf(
			os.Stderr,
			"connectivity: readiness probes will fail, cannot listen on %s: %v\n",
			address,
			err,
		)

		return nil
	}

	handler := http.NewServeMux()
	handler.HandleFunc(
		ConnectivityReadinessPath,
		func(writer http.ResponseWriter, request *http.Request) {
			if request.Method != http.MethodGet {
				http.Error(writer, "method is not allowed", http.StatusMethodNotAllowed)

				return
			}

			if err := ConnectivityReadyWithRevision(
				plan,
				options.StateDirectory,
				options.ConnectivityRevisionPath,
			); err != nil {
				http.Error(writer, err.Error(), http.StatusServiceUnavailable)

				return
			}

			writer.WriteHeader(http.StatusOK)
			_, _ = writer.Write([]byte("ok\n"))
		},
	)

	ready := &connectivityReadinessServer{
		listener: listener,
		server: &http.Server{
			Handler:           handler,
			ReadHeaderTimeout: connectivityReadinessHeaderTimeout,
		},
	}

	go func() {
		_ = ready.server.Serve(listener)
	}()

	return ready
}

// Close stops answering probes.
func (s *connectivityReadinessServer) Close() error {
	if s == nil {
		return nil
	}

	err := s.server.Close()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}

	return err
}
