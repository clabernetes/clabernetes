//nolint:testpackage // Exercise the supervisor with a scoped scratch root and test executables.
package deviceplan

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPoolProcessExcludesOverlappingRequestsAndCancelsBlockedInput(t *testing.T) {
	root := t.TempDir()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	defer func() { _ = writer.Close() }()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runPoolProcess(ctx, reader, io.Discard, io.Discard, "test", time.Minute, root, "/bin/true")
	}()
	deadline := time.Now().Add(time.Second)
	for {
		entries, readErr := os.ReadDir(root)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if len(entries) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("supervisor did not create a request workspace")
		}
		time.Sleep(time.Millisecond)
	}
	if err = runPoolProcess(t.Context(), reader, io.Discard, io.Discard, "test", time.Minute, root, "/bin/true"); err == nil ||
		!strings.Contains(err.Error(), "busy") {
		t.Fatalf("overlapping request was not excluded: %v", err)
	}
	cancel()
	select {
	case err = <-done:
		if err == nil {
			t.Fatal("cancelled incomplete input succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not unblock bootstrap input")
	}
	assertPoolWorkspaceClean(t, root)
}

//nolint:gosec // The private temporary shell fixture must be executable.
func TestPoolProcessDeadlineCleansWorkspaceAndReleasesWorker(t *testing.T) {
	root := t.TempDir()
	stale := filepath.Join(root, "request-stale")
	if err := os.Mkdir(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "wait")

	if err := os.WriteFile(script, []byte("#!/bin/sh\n/bin/sleep 60 &\nwait\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, executable := range []string{script, "/bin/true"} {
		input, err := os.CreateTemp(t.TempDir(), "input")
		if err != nil {
			t.Fatal(err)
		}
		if err = WritePoolBootstrap(input, testPoolBootstrap()); err != nil {
			t.Fatal(err)
		}
		if _, err = input.Seek(0, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		err = runPoolProcess(
			t.Context(),
			input,
			io.Discard,
			io.Discard,
			"test",
			200*time.Millisecond,
			root,
			executable,
		)
		_ = input.Close()
		if (executable == script) != (err != nil) {
			t.Fatalf("unexpected process result for %s: %v", executable, err)
		}
		assertPoolWorkspaceClean(t, root)
	}
}

func assertPoolWorkspaceClean(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "worker.lock" {
		t.Fatalf("request material remained after execution: %v", entries)
	}
}
