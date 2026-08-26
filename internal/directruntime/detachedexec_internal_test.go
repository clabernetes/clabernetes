package directruntime

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDetachedCommandAttachesNoStdioAndOwnsItsSession(t *testing.T) {
	t.Parallel()

	process := detachedCommand([]string{"sleep", "1"})

	if process.Stdout != nil || process.Stderr != nil || process.Stdin != nil {
		t.Fatalf(
			"detached command inherits lifecycle stdio: stdout=%v stderr=%v stdin=%v",
			process.Stdout, process.Stderr, process.Stdin,
		)
	}

	if process.SysProcAttr == nil || !process.SysProcAttr.Setsid {
		t.Fatalf("detached command shares the lifecycle session: %#v", process.SysProcAttr)
	}
}

func TestStartDetachedExecReturnsImmediatelyWhileChildSurvives(t *testing.T) {
	t.Parallel()

	marker := filepath.Join(t.TempDir(), "survived")

	started := time.Now()

	err := startDetachedExec([]string{"/bin/sh", "-c", "sleep 0.3 && touch " + marker})
	if err != nil {
		t.Fatal(err)
	}

	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("detached start waited on the child: %s", elapsed)
	}

	deadline := time.Now().Add(3 * time.Second)

	for {
		if _, statErr := os.Stat(marker); statErr == nil {
			return
		}

		if time.Now().After(deadline) {
			t.Fatal("released child did not survive to completion")
		}

		time.Sleep(50 * time.Millisecond)
	}
}
