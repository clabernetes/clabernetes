package testhelper

import (
	"bytes"
	"os/exec"
	"testing"
)

// YQCommand accepts some yaml content and returns it after executing the given yqPattern against
// it. The content is fed over stdin so quoting inside the document (for example quoted condition
// messages) can never break shell interpolation.
func YQCommand(t *testing.T, content []byte, yqPattern string) []byte {
	t.Helper()

	cmd := exec.CommandContext( //nolint:gosec
		t.Context(),
		"yq",
		yqPattern,
	)
	cmd.Stdin = bytes.NewReader(content)

	return Execute(t, cmd)
}
