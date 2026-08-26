//go:build linux

//nolint:err113,testpackage // dense fixture-driven tests exercise one boundary end to end.
package directruntime

import (
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestExecuteEndpointNamespaceRestoresBeforeReusingThread(t *testing.T) {
	t.Parallel()

	operationErr := errors.New("package operation failed")
	calls := []string{}

	err := executeEndpointNamespace(
		10,
		20,
		func() error {
			calls = append(calls, "operation")

			return operationErr
		},
		func(fd, _ int) error {
			calls = append(calls, "setns:"+strconv.Itoa(fd))

			return nil
		},
		func() { calls = append(calls, "lock") },
		func() { calls = append(calls, "unlock") },
	)
	if !errors.Is(err, operationErr) {
		t.Fatalf("executeEndpointNamespace() error = %v", err)
	}

	want := []string{"lock", "setns:20", "operation", "setns:10", "unlock"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("namespace execution calls = %#v, want %#v", calls, want)
	}
}

func TestExecuteEndpointNamespaceDiscardsThreadWhenRestoreFails(t *testing.T) {
	t.Parallel()

	calls := []string{}
	setnsCalls := 0

	err := executeEndpointNamespace(
		10,
		20,
		func() error {
			calls = append(calls, "operation")

			return nil
		},
		func(_, _ int) error {
			setnsCalls++

			calls = append(calls, "setns")

			if setnsCalls == 2 {
				return errors.New("restore failed")
			}

			return nil
		},
		func() { calls = append(calls, "lock") },
		func() { calls = append(calls, "unlock") },
	)
	if err == nil || !strings.Contains(err.Error(), "restore") {
		t.Fatalf("executeEndpointNamespace() error = %v", err)
	}

	if slices.Contains(calls, "unlock") {
		t.Fatalf("unsafe namespace thread was reused: %#v", calls)
	}
}

func TestExecuteEndpointNamespaceRestoresAfterOperationPanic(t *testing.T) {
	t.Parallel()

	calls := []string{}

	err := executeEndpointNamespace(
		10,
		20,
		func() error { panic("package panic") },
		func(_, _ int) error {
			calls = append(calls, "setns")

			return nil
		},
		func() { calls = append(calls, "lock") },
		func() { calls = append(calls, "unlock") },
	)
	if err == nil || !strings.Contains(err.Error(), "panicked") {
		t.Fatalf("executeEndpointNamespace() error = %v", err)
	}

	if !reflect.DeepEqual(calls, []string{"lock", "setns", "setns", "unlock"}) {
		t.Fatalf("panic namespace restoration calls = %#v", calls)
	}
}
