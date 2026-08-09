package suite

import (
	"bytes"
	"testing"
	"time"

	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
)

func eventually(
	t *testing.T,
	pollInterval,
	maxTime time.Duration,
	getter func() []byte,
	expected []byte,
) {
	t.Helper()

	startTime := time.Now()

	var latestActual []byte

	ticker := time.NewTicker(pollInterval)

	for range ticker.C {
		if time.Since(startTime) > maxTime {
			clabernetestesthelper.FailOutput(t, latestActual, expected)
		}

		latestActual = getter()

		if bytes.Equal(
			latestActual,
			expected,
		) {
			return
		}
	}
}
