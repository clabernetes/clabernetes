package directruntime_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	clabernetesinternaldirectruntime "github.com/clabernetes/clabernetes/internal/directruntime"
)

func TestStartupGateRequiresMatchingPodIdentity(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	for _, admission := range []string{"", "previous-pod", "current-pod"} {
		if err := os.WriteFile(filepath.Join(directory, "uid"), []byte("current-pod"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "admitted"), []byte(admission), 0o600); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		err := clabernetesinternaldirectruntime.WaitStartupAdmission(ctx, directory)
		if admission == "current-pod" {
			if err != nil {
				t.Fatal(err)
			}
		} else if !errors.Is(err, context.Canceled) {
			t.Fatalf("unexpected gate result: %v", err)
		}
	}
}
