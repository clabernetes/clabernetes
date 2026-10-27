package deviceplan

import (
	"errors"
	"testing"

	clabtypes "github.com/srl-labs/containerlab/types"
)

func TestDeploymentConditionsSeeContainerlabContainerName(t *testing.T) {
	t.Parallel()

	hookErr := errors.New("condition not met")

	for _, want := range []error{nil, hookErr} {
		config := &clabtypes.NodeConfig{ShortName: "a", LongName: "a"}

		var seen string

		err := withContainerlabContainerName(config, "lab", func() error {
			seen = config.LongName

			return want
		})
		if !errors.Is(err, want) {
			t.Fatalf("hook error = %v, want %v", err, want)
		}

		if seen != "clab-lab-a" {
			t.Fatalf("hook saw container name %q, want %q", seen, "clab-lab-a")
		}

		if config.LongName != "a" {
			t.Fatalf("container name after the hook = %q, want the c9s name %q",
				config.LongName, "a")
		}
	}
}
