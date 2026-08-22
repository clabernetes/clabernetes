package directruntime

import (
	"context"
	"testing"
	"time"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
)

func TestRunOCIHealthcheckUsesDeclaredCommandForms(t *testing.T) {
	t.Parallel()

	for _, healthcheck := range []*clabernetesinternaldeviceplan.Healthcheck{
		{Test: []string{"CMD", "/bin/true"}, Timeout: int64(time.Second)},
		{Test: []string{"CMD-SHELL", "test", "1", "=", "1"}, Timeout: int64(time.Second)},
		{Test: []string{"NONE"}},
	} {
		if err := runOCIHealthcheck(context.Background(), healthcheck); err != nil {
			t.Fatalf("runOCIHealthcheck(%#v) error = %v", healthcheck, err)
		}
	}
}

func TestRunOCIHealthcheckPropagatesFailure(t *testing.T) {
	t.Parallel()

	err := runOCIHealthcheck(context.Background(), &clabernetesinternaldeviceplan.Healthcheck{
		Test: []string{"CMD", "/bin/false"}, Timeout: int64(time.Second),
	})
	if err == nil {
		t.Fatal("runOCIHealthcheck() accepted a failing command")
	}
}
