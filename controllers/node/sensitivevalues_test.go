package node //nolint:testpackage // tests exercise the sensitive-value screening boundary

import (
	"reflect"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
)

func TestScreenSensitiveValuesDropsOnlyValuesDeclaredOnTheNode(t *testing.T) {
	t.Parallel()

	node := planInputTestNode("router", "uid-router", "arista_ceos", "registry.example/ceos:1")
	node.Spec.Type = "spine"
	node.Spec.StartupConfig = "hostname router\nusername admin privilege 15 secret 0 admin\n"

	input, err := CompilePlanInput(PlanInputCompileRequest{
		Primary:       node,
		GroupMembers:  []string{node.GetName()},
		NodesByName:   map[string]*clabernetesapisv1alpha1.Node{node.GetName(): node},
		Compatibility: planInputTestCompatibility(),
	})
	if err != nil {
		t.Fatal(err)
	}

	declared := declaredNodeText(input)

	got := screenSensitiveValues(
		declared,
		[][]byte{
			[]byte("admin"),       // probe password restated by the startup-config
			[]byte("router"),      // Node name
			[]byte("arista_ceos"), // kind
			[]byte("spine"),       // type
			[]byte("registry.example/ceos:1"),
		},
		[][]byte{
			[]byte(""),
			[]byte("s3cr3t-probe"),
			[]byte("registry-token"),
			[]byte("Admin"), // case matters: only a verbatim match is public
		},
	)

	want := [][]byte{[]byte("s3cr3t-probe"), []byte("registry-token"), []byte("Admin")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("screenSensitiveValues() = %q, want %q", got, want)
	}

	if got = screenSensitiveValues(nil, want); !reflect.DeepEqual(got, want) {
		t.Fatalf("screenSensitiveValues() without declared text = %q, want %q", got, want)
	}
}
