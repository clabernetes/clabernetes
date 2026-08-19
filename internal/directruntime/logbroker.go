package directruntime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// ApplicationRuntimeSocketPath is mounted into application lifecycle workers without the
	// Kubernetes credentials held by the connectivity sidecar.
	ApplicationRuntimeSocketPath = "/var/run/clabernetes/runtime-api/runtime.sock"
	applicationLogPath           = "/v1/logs"
	maxBrokerErrorBytes          = 4096
)

// ApplicationLogStreamer supplies Kubernetes-owned logs to a Pod-local runtime broker.
type ApplicationLogStreamer interface {
	StreamLogs(context.Context, string) (io.ReadCloser, error)
}

// ApplicationLogBroker exposes only accepted application log targets over a Pod-local socket.
type ApplicationLogBroker struct {
	socketPath string
	listener   net.Listener
	server     *http.Server
	errors     chan error
	closeOnce  sync.Once
	closeErr   error
}

// StartApplicationLogBroker starts a plan-scoped broker without exposing Kubernetes credentials
// to application containers.
func StartApplicationLogBroker(
	ctx context.Context,
	socketPath string,
	targets map[string]string,
	streamer ApplicationLogStreamer,
) (*ApplicationLogBroker, error) {
	if ctx == nil {
		return nil, fmt.Errorf("application log broker context is nil")
	}
	if streamer == nil {
		return nil, fmt.Errorf("application log streamer is nil")
	}
	socketPath = filepath.Clean(socketPath)
	root := filepath.Dir(socketPath)
	if !filepath.IsAbs(socketPath) || socketPath == string(filepath.Separator) ||
		root == string(filepath.Separator) {
		return nil, fmt.Errorf("application log broker socket must have a scoped absolute path")
	}
	accepted := make(map[string]string, len(targets))
	for runtimeID, containerName := range targets {
		if strings.TrimSpace(runtimeID) == "" || strings.TrimSpace(containerName) == "" {
			return nil, fmt.Errorf("application log broker target identity is incomplete")
		}
		accepted[runtimeID] = containerName
	}
	if len(accepted) == 0 {
		return nil, fmt.Errorf("application log broker has no accepted targets")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("creating application log broker directory: %w", err)
	}
	if info, err := os.Lstat(socketPath); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("application log broker path is occupied by a non-socket")
		}
		if err = os.Remove(socketPath); err != nil {
			return nil, fmt.Errorf("removing stale application log broker socket: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspecting application log broker socket: %w", err)
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("listening on application log broker socket: %w", err)
	}
	//nolint:gosec // Non-root application UIDs must reach this Pod-local, allowlisted socket.
	if err = os.Chmod(socketPath, 0o666); err != nil {
		// The socket is mounted into package-selected application users in this Pod only; its
		// plan allowlist is the authorization boundary and no Kubernetes credential is exposed.
		_ = listener.Close()
		_ = os.Remove(socketPath)

		return nil, fmt.Errorf("setting application log broker socket permissions: %w", err)
	}

	handler := http.NewServeMux()
	handler.HandleFunc(applicationLogPath, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			http.Error(writer, "method is not allowed", http.StatusMethodNotAllowed)

			return
		}
		runtimeID := request.URL.Query().Get("runtimeID")
		containerName, exists := accepted[runtimeID]
		if !exists {
			http.Error(
				writer,
				"runtime target is not present in the accepted plan",
				http.StatusNotFound,
			)

			return
		}
		logs, streamErr := streamer.StreamLogs(request.Context(), containerName)
		if streamErr != nil {
			http.Error(writer, "application log stream is unavailable", http.StatusBadGateway)

			return
		}
		defer logs.Close()
		writer.Header().Set("Content-Type", "application/octet-stream")
		writer.WriteHeader(http.StatusOK)
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
		_, _ = io.Copy(writer, logs)
	})

	broker := &ApplicationLogBroker{
		socketPath: socketPath,
		listener:   listener,
		server: &http.Server{
			Handler:           handler,
			ReadHeaderTimeout: 5 * time.Second,
			MaxHeaderBytes:    8 << 10,
		},
		errors: make(chan error, 1),
	}
	go func() {
		serveErr := broker.server.Serve(listener)
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) &&
			!errors.Is(serveErr, net.ErrClosed) {
			broker.errors <- serveErr
		}
		close(broker.errors)
	}()
	go func() {
		<-ctx.Done()
		_ = broker.Close()
	}()

	return broker, nil
}

// SocketPath returns the scoped Unix socket path exposed to accepted lifecycle workers.
func (b *ApplicationLogBroker) SocketPath() string {
	if b == nil {
		return ""
	}

	return b.socketPath
}

// Errors reports an unexpected serving failure. Normal context cancellation closes the channel.
func (b *ApplicationLogBroker) Errors() <-chan error {
	if b == nil {
		closed := make(chan error)
		close(closed)

		return closed
	}

	return b.errors
}

// Close stops the broker and removes its Unix socket.
func (b *ApplicationLogBroker) Close() error {
	if b == nil {
		return nil
	}
	b.closeOnce.Do(func() {
		b.closeErr = b.server.Close()
		if err := b.listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			b.closeErr = errors.Join(b.closeErr, err)
		}
		if err := os.Remove(b.socketPath); err != nil && !os.IsNotExist(err) {
			b.closeErr = errors.Join(b.closeErr, err)
		}
	})

	return b.closeErr
}

type applicationLogStream struct {
	io.ReadCloser
	transport *http.Transport
}

func (s *applicationLogStream) Close() error {
	err := s.ReadCloser.Close()
	s.transport.CloseIdleConnections()

	return err
}

func openApplicationLogStream(
	ctx context.Context,
	socketPath,
	runtimeID string,
) (io.ReadCloser, error) {
	if ctx == nil {
		return nil, fmt.Errorf("application log stream context is nil")
	}
	socketPath = filepath.Clean(socketPath)
	if !filepath.IsAbs(socketPath) || socketPath == string(filepath.Separator) ||
		strings.TrimSpace(runtimeID) == "" {
		return nil, fmt.Errorf("application log stream identity is incomplete")
	}
	dialer := &net.Dialer{}
	transport := &http.Transport{
		DisableCompression: true,
		DialContext: func(dialContext context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(dialContext, "unix", socketPath)
		},
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		"http://unix"+applicationLogPath+"?runtimeID="+url.QueryEscape(runtimeID),
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("creating application log stream request: %w", err)
	}
	response, err := (&http.Client{Transport: transport}).Do(request)
	if err != nil {
		transport.CloseIdleConnections()

		return nil, fmt.Errorf("opening application log stream: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(response.Body, maxBrokerErrorBytes))
		_ = response.Body.Close()
		transport.CloseIdleConnections()

		return nil, fmt.Errorf(
			"opening application log stream: %s",
			strings.TrimSpace(string(raw)),
		)
	}

	return &applicationLogStream{ReadCloser: response.Body, transport: transport}, nil
}
