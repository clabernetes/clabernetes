//go:build linux

package directruntime

import (
	"bytes"
	"testing"
)

func TestTerminalQueryFilterRemovesCapabilityQueries(t *testing.T) {
	t.Parallel()

	filter := &terminalQueryFilter{output: &bytes.Buffer{}}

	// The exact boot byte stream observed from an interactive NOS CLI on a fresh terminal:
	// background-color query, cursor-position query, device-status query, then the prompt —
	// everything terminal-directed is removed, never answered.
	forwarded := filter.filter([]byte("\x1b]11;?\x1b\\\x1b[6n\x1b[5nceos1>"))
	if string(forwarded) != "ceos1>" {
		t.Fatalf("filtered stream = %q, want the bare prompt", forwarded)
	}
	// Sequences split across reads reassemble; unrelated CSI output passes through.
	filter = &terminalQueryFilter{output: &bytes.Buffer{}}
	first := filter.filter([]byte("before\x1b[5"))
	second := filter.filter([]byte("nafter\x1b[1mBOLD"))
	if string(first)+string(second) != "beforeafter\x1b[1mBOLD" {
		t.Fatalf("split filtering = %q + %q", first, second)
	}
}
