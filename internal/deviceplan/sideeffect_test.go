package deviceplan_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
)

func TestAdapterTreatsHostPathsAsIntentAndDoesNotMutateNetworking(t *testing.T) {
	workspace := t.TempDir()
	missingStartup := filepath.Join(workspace, "missing-startup.cfg")
	missingLicense := filepath.Join(workspace, "missing-license.key")
	missingBind := filepath.Join(workspace, "missing-bind")

	definition, err := json.Marshal(map[string]any{
		"kind":           syntheticKind,
		"image":          "example/future:1",
		"startup-config": missingStartup,
		"license":        missingLicense,
		"binds":          []string{missingBind + ":/configuration"},
	})
	if err != nil {
		t.Fatal(err)
	}

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Nodes[0].Definition = definition

	beforeFiles, err := os.ReadDir(workspace)
	if err != nil {
		t.Fatal(err)
	}

	beforeNamespace, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		t.Fatal(err)
	}

	beforeInterfaces, err := interfaceNames()
	if err != nil {
		t.Fatal(err)
	}

	adapter := clabernetesinternaldeviceplan.Adapter{
		Registry: newSyntheticRegistry(t),
		Revision: "side-effect-test",
	}

	_, err = adapter.Evaluate(context.Background(), input)
	if err == nil {
		t.Fatal("adapter accepted an implicit host startup path")
	}

	planningErr := &clabernetesinternaldeviceplan.Error{}
	if !errors.As(err, &planningErr) ||
		planningErr.Code != clabernetesinternaldeviceplan.ErrorMissingInput ||
		planningErr.Behavior != "imported-startup-config" {
		t.Fatalf("implicit host path error = %#v", err)
	}

	afterFiles, err := os.ReadDir(workspace)
	if err != nil {
		t.Fatal(err)
	}

	afterNamespace, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		t.Fatal(err)
	}

	afterInterfaces, err := interfaceNames()
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(beforeFiles, afterFiles) {
		t.Fatalf(
			"adapter mutated filesystem intent directory: before=%v after=%v",
			beforeFiles,
			afterFiles,
		)
	}

	if beforeNamespace != afterNamespace {
		t.Fatalf(
			"adapter changed process network namespace: before=%q after=%q",
			beforeNamespace,
			afterNamespace,
		)
	}

	if !reflect.DeepEqual(beforeInterfaces, afterInterfaces) {
		t.Fatalf(
			"adapter mutated host interfaces: before=%v after=%v",
			beforeInterfaces,
			afterInterfaces,
		)
	}
}

func interfaceNames() ([]string, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(interfaces))
	for _, intf := range interfaces {
		names = append(names, intf.Name)
	}

	return names, nil
}
