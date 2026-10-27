//nolint:err113,testpackage // the client-close boundary is exercised through unexported fakes.
package directruntime

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	packetCaptureHelperEnv   = "C9S_PACKET_CAPTURE_HELPER"
	packetCaptureHelperStdin = "stdin"
	packetCaptureHelperPipe  = "stdout"
)

// TestMain lets the process tests run this test binary as a packet-capture process.
func TestMain(m *testing.M) {
	if mode := os.Getenv(packetCaptureHelperEnv); mode != "" {
		os.Exit(runPacketCaptureHelperProcess(mode))
	}

	os.Exit(m.Run())
}

// runPacketCaptureHelperProcess captures from a source that never runs dry, for an hour, to
// the process's real stdout, the way the packet-capture command does.
func runPacketCaptureHelperProcess(mode string) int {
	PreparePacketCaptureProcess()

	options := PacketCaptureOptions{
		PacketCaptureSelector: PacketCaptureSelector{NodeName: "future-device"},
		Interfaces:            []string{"package-a"},
		Format:                PacketCaptureFormatPCAPNG,
		Duration:              time.Hour,
	}
	if mode == packetCaptureHelperStdin {
		options.ClientInput = os.Stdin
	}

	err := runPacketCapture(
		context.Background(),
		packetCaptureTestPlan(),
		options,
		os.Stdout,
		os.Stderr,
		func(string, int) (packetCaptureSource, error) { return &endlessPacketSource{}, nil },
	)
	if err != nil {
		return 1
	}

	return 0
}

// endlessPacketSource yields a packet every few milliseconds until the capture ends.
type endlessPacketSource struct{}

func (endlessPacketSource) ReadPacket(ctx context.Context) (capturedPacket, error) {
	select {
	case <-ctx.Done():
		return capturedPacket{}, ctx.Err()
	case <-time.After(5 * time.Millisecond):
	}

	return capturedPacket{Timestamp: time.Now(), Data: []byte{1, 2, 3, 4}}, nil
}

func (endlessPacketSource) Close() error { return nil }

// TestPacketCaptureProcessStopsWhenItsClientGoesAway runs a capture as its own process with
// pipes for stdin and stdout, as an exec in the clabwire container has. A client that closes
// stdin (with --stop-on-stdin-close) or stops reading stdout ends the process promptly, with
// exit status 0 and a final Succeeded audit record, instead of leaving it running or killing
// it with SIGPIPE.
func TestPacketCaptureProcessStopsWhenItsClientGoesAway(t *testing.T) {
	t.Parallel()

	for mode, wantReason := range map[string]string{
		packetCaptureHelperStdin: PacketCaptureReasonClientClosed + ": stdin reached EOF",
		packetCaptureHelperPipe:  PacketCaptureReasonClientClosed + ": writing stdout:",
	} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()

			runClientGoneProcess(t, mode, wantReason)
		})
	}
}

// runClientGoneProcess starts the capture process, waits for packets, makes its client go away
// in the given mode, and checks that it ends promptly and cleanly.
func runClientGoneProcess(t *testing.T, mode, wantReason string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), os.Args[0]) //nolint:gosec // the test binary itself.
	// The race detector otherwise holds every exiting process for a second.
	cmd.Env = append(os.Environ(), packetCaptureHelperEnv+"="+mode,
		"GORACE=atexit_sleep_ms=0")

	var stderr bytes.Buffer

	cmd.Stderr = &stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}

	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}

	// The capture is running once packets arrive.
	reader := bufio.NewReader(stdout)
	if _, err = io.ReadFull(reader, make([]byte, 256)); err != nil {
		t.Fatalf("reading the capture: %v: %s", err, stderr.String())
	}

	if mode == packetCaptureHelperStdin {
		go func() { _, _ = io.Copy(io.Discard, reader) }()

		_ = stdin.Close()
	} else {
		_ = stdout.Close()
	}

	closed := time.Now()
	done := make(chan error, 1)

	go func() { done <- cmd.Wait() }()

	select {
	case err = <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()

		t.Fatalf("capture process still runs 5s after its client went away")
	}

	if err != nil {
		t.Fatalf("capture process = %v: %s", err, stderr.String())
	}

	records := decodePacketCaptureAudit(t, stderr.Bytes())
	final := records[len(records)-1]

	if final.Status != "Succeeded" || !strings.HasPrefix(final.Reason, wantReason) ||
		final.Packets == 0 {
		t.Fatalf("final audit record = %#v", final)
	}

	if mode == packetCaptureHelperPipe && !strings.Contains(final.Reason, "broken pipe") {
		t.Fatalf("stdout reason %q does not name the broken pipe", final.Reason)
	}

	t.Logf("process ended %s after its client went away", time.Since(closed))
}

// TestRunPacketCaptureEndsWhenClientClosesStdin ends a capture through its client input like a
// reached bound: the call succeeds, the packets read so far are in the stream, and the final
// record says why the capture ended.
func TestRunPacketCaptureEndsWhenClientClosesStdin(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		close      func(*io.PipeWriter)
		wantReason string
	}{
		"EOF": {
			close:      func(writer *io.PipeWriter) { _ = writer.Close() },
			wantReason: "client closed: stdin reached EOF",
		},
		"read error": {
			close: func(writer *io.PipeWriter) {
				_ = writer.CloseWithError(errors.New("stream reset"))
			},
			wantReason: "client closed: reading stdin: stream reset",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			input, client := io.Pipe()
			source := newInterleavingPacketSource('a', 3)

			var capture, audit bytes.Buffer

			errs := make(chan error, 1)

			go func() {
				errs <- runPacketCapture(
					context.Background(),
					packetCaptureTestPlan(),
					PacketCaptureOptions{
						PacketCaptureSelector: PacketCaptureSelector{NodeName: "future-device"},
						Interfaces:            []string{"package-a"},
						Format:                PacketCaptureFormatPCAPNG,
						Duration:              time.Hour,
						ClientInput:           input,
					},
					&capture,
					&audit,
					func(string, int) (packetCaptureSource, error) { return source, nil },
				)
			}()

			// Bytes the client sends are discarded; only the end of its input counts.
			if _, err := client.Write([]byte("keepalive")); err != nil {
				t.Fatal(err)
			}

			waitForDrainedSource(t, source)
			test.close(client)

			select {
			case err := <-errs:
				if err != nil {
					t.Fatalf("capture ended by its client = %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("capture still runs after its client closed stdin")
			}

			if !source.isClosed() {
				t.Fatal("interface was not closed")
			}

			if packets := len(parsePCAPNG(t, capture.Bytes()).packets); packets != 3 {
				t.Fatalf("stream carries %d packets, want 3", packets)
			}

			records := decodePacketCaptureAudit(t, audit.Bytes())
			final := records[len(records)-1]

			if len(records) != 2 || final.Status != "Succeeded" ||
				final.Reason != test.wantReason || final.Packets != 3 {
				t.Fatalf("audit = %#v", records)
			}
		})
	}
}

// TestRunPacketCaptureWithoutClientInputIgnoresStdin keeps the behaviour of callers that do not
// opt in: a capture runs to its bound and its record carries no reason.
func TestRunPacketCaptureWithoutClientInputIgnoresStdin(t *testing.T) {
	t.Parallel()

	var audit bytes.Buffer

	err := runPacketCapture(
		context.Background(),
		packetCaptureTestPlan(),
		PacketCaptureOptions{
			PacketCaptureSelector: PacketCaptureSelector{NodeName: "future-device"},
			Interfaces:            []string{"package-a"},
			Duration:              50 * time.Millisecond,
		},
		io.Discard,
		&audit,
		func(string, int) (packetCaptureSource, error) {
			return newInterleavingPacketSource('a', 2), nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	records := decodePacketCaptureAudit(t, audit.Bytes())
	if final := records[len(records)-1]; final.Status != "Succeeded" || final.Reason != "" {
		t.Fatalf("final record = %#v", final)
	}
}

// closingWriter accepts the first writes and then fails every write with err.
type closingWriter struct {
	accept int
	err    error
	buffer bytes.Buffer
}

func (w *closingWriter) Write(data []byte) (int, error) {
	if w.accept == 0 {
		return 0, w.err
	}

	w.accept--

	return w.buffer.Write(data)
}

// TestRunPacketCaptureEndsWhenStdoutCloses ends a capture whose stdout lost its reader (a
// broken pipe, or a closed or invalid descriptor) as one whose client went away, whether that
// happens at the header or at a packet. Any other write failure still fails the capture.
func TestRunPacketCaptureEndsWhenStdoutCloses(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		accept     int
		err        error
		wantStatus string
		wantReason string
	}{
		"EPIPE at a packet": {
			accept: 2, err: syscall.EPIPE, wantStatus: "Succeeded",
			wantReason: "client closed: writing stdout: broken pipe",
		},
		"EBADF at a packet": {
			accept: 2, err: &os.PathError{Op: "write", Path: "/dev/stdout", Err: syscall.EBADF},
			wantStatus: "Succeeded",
			wantReason: "client closed: writing stdout: write /dev/stdout: bad file descriptor",
		},
		"closed file at a packet": {
			accept: 1, err: os.ErrClosed, wantStatus: "Succeeded",
			wantReason: "client closed: writing stdout: file already closed",
		},
		"EPIPE at the header": {
			accept: 0, err: syscall.EPIPE, wantStatus: "Succeeded",
			wantReason: "client closed: writing stdout: broken pipe",
		},
		"other write failure": {
			accept: 1, err: errors.New("disk full"), wantStatus: "Failed",
			wantReason: "writing captured packet: disk full",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			output := &closingWriter{accept: test.accept, err: test.err}

			var audit bytes.Buffer

			done := make(chan error, 1)

			go func() {
				done <- runPacketCapture(
					context.Background(),
					packetCaptureTestPlan(),
					PacketCaptureOptions{
						PacketCaptureSelector: PacketCaptureSelector{NodeName: "future-device"},
						Interfaces:            []string{"package-a"},
						Format:                PacketCaptureFormatPCAPNG,
						Duration:              time.Hour,
					},
					output,
					&audit,
					func(string, int) (packetCaptureSource, error) {
						return endlessPacketSource{}, nil
					},
				)
			}()

			var err error

			select {
			case err = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("capture still runs after its stdout closed")
			}

			if (err == nil) != (test.wantStatus == "Succeeded") {
				t.Fatalf("capture error = %v, want status %s", err, test.wantStatus)
			}

			records := decodePacketCaptureAudit(t, audit.Bytes())
			final := records[len(records)-1]

			if final.Status != test.wantStatus || final.Reason != test.wantReason {
				t.Fatalf("final record = %#v", final)
			}

			if want := max(test.accept-1, 0); final.Packets != want {
				t.Fatalf("final record counts %d packets, want %d", final.Packets, want)
			}
		})
	}
}

// waitForDrainedSource waits until the capture read every queued packet of source.
func waitForDrainedSource(t *testing.T, source *interleavingPacketSource) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadline) {
		source.mu.Lock()
		remaining := len(source.packets)
		source.mu.Unlock()

		if remaining == 0 {
			// The last packet read may still be on its way to the stream.
			time.Sleep(20 * time.Millisecond)

			return
		}

		time.Sleep(time.Millisecond)
	}

	t.Fatalf("source still holds packets after %s", 5*time.Second)
}
