package testhelper

import (
	"os/exec"
	"testing"
)

const (
	kubectl            = "kubectl"
	kubectlWaitTimeout = "120s"
)

// Operation represents a kubectl operation type, i.e. apply or delete.
type Operation string

const (
	// Apply is the apply kubectl operation.
	Apply Operation = "apply"
	// Delete is the delete kubectl operation.
	Delete Operation = "delete"
	// Create is the create kubectl operation.
	Create Operation = "create"
	// Get is the get kubectl operation.
	Get Operation = "get"
)

func kubectlNamespace(t *testing.T, operation Operation, namespace string) {
	t.Helper()

	cmd := exec.CommandContext( //nolint:gosec
		t.Context(),
		kubectl,
		string(operation),
		"namespace",
		namespace,
	)

	err := cmd.Run()
	if err != nil {
		t.Fatalf("error executing kubectl command, error: '%s'", err)
	}
}

// KubectlCreateNamespace execs a kubectl create namespace.
func KubectlCreateNamespace(t *testing.T, namespace string) {
	t.Helper()

	kubectlNamespace(t, Create, namespace)
}

// KubectlDeleteNamespace execs a kubectl delete namespace.
func KubectlDeleteNamespace(t *testing.T, namespace string) {
	t.Helper()

	kubectlNamespace(t, Delete, namespace)
}

// KubectlFileOp execs a kubectl operation on a file (i.e. apply/delete).
func KubectlFileOp(t *testing.T, operation Operation, namespace, fileName string) {
	t.Helper()

	cmd := exec.CommandContext( //nolint:gosec
		t.Context(),
		kubectl,
		string(operation),
		"--namespace",
		namespace,
		"-f",
		fileName,
	)

	_ = Execute(t, cmd)
}

// KubectlGetOp runs get on the given object, returning the yaml output.
func KubectlGetOp(t *testing.T, kind, namespace, name string) []byte {
	t.Helper()

	cmd := exec.CommandContext( //nolint:gosec
		t.Context(),
		kubectl,
		string(Get),
		kind,
		"--namespace",
		namespace,
		name,
		"-o",
		"yaml",
	)

	return Execute(t, cmd)
}

// KubectlWaitForCreate waits for an asserted resource to exist before its contents are compared.
// This prevents an initial NotFound response from aborting the suite before reconciliation has
// produced the resource.
func KubectlWaitForCreate(t *testing.T, kind, namespace, name string) {
	t.Helper()

	cmd := exec.CommandContext( //nolint:gosec
		t.Context(),
		kubectl,
		"wait",
		"--for=create",
		"--timeout="+kubectlWaitTimeout,
		"--namespace",
		namespace,
		kind+"/"+name,
	)

	_ = Execute(t, cmd)
}
