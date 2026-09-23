//nolint:testpackage // Verify classification at the controller's retry boundary.
package node

import (
	"errors"
	"fmt"
	"testing"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	apimachineryschema "k8s.io/apimachinery/pkg/runtime/schema"
)

func TestDirectDependencyPendingPreservesRealFailures(t *testing.T) {
	t.Parallel()
	pending := planInputError(
		clabernetesinternaldeviceplan.ErrorMissingInput,
		"links.peer",
		"waiting for endpoint inventory",
	)
	for _, test := range []struct {
		name    string
		err     error
		pending bool
	}{
		{"success", nil, false},
		{"pool busy", ErrPlannerPoolBusy, true},
		{"link not accepted", pending, true},
		{"wrapped wait", fmt.Errorf("compile: %w", pending), true},
		{"stale identity", planInputError(clabernetesinternaldeviceplan.ErrorInvariant, "links.peer", "stale UID"), false},
		{"missing unrelated input", planInputError(clabernetesinternaldeviceplan.ErrorMissingInput, "nodes.primary", "missing UID"), false},
		{"update race", apimachineryerrors.NewConflict(apimachineryschema.GroupResource{Resource: "configmaps"}, "peer", errInjectedNodeConflict), true},
		{"create race", apimachineryerrors.NewAlreadyExists(apimachineryschema.GroupResource{Resource: "configmaps"}, "peer"), true},
		{"failed status reporting", errors.Join(pending, errInjectedNodeConflict), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := directDependencyPending(test.err); got != test.pending {
				t.Fatalf("pending=%t for %v", got, test.err)
			}
		})
	}
}
