//nolint:testpackage,wsl_v5 // Internal table tests cover the complete temporary mode surface.
package deviceruntime

import (
	"errors"
	"testing"
)

//nolint:tparallel // The small table is intentionally serialized for deterministic diagnostics.
func TestParseMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		want  Mode
	}{
		{name: "omitted defaults direct", want: ModeDirect},
		{name: "nested", value: "nested", want: ModeNested},
		{name: "direct", value: "direct", want: ModeDirect},
		{name: "surrounding whitespace", value: " direct ", want: ModeDirect},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseMode(test.value)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("ParseMode(%q) = %q, want %q", test.value, got, test.want)
			}
		})
	}
}

func TestParseModeRejectsImplicitFallback(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"auto", "DIRECT", "disabled", "nested-or-direct"} {
		_, err := ParseMode(value)
		if !errors.Is(err, ErrInvalidMode) {
			t.Errorf("ParseMode(%q) error = %v, want ErrInvalidMode", value, err)
		}
	}
}
