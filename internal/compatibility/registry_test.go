//nolint:nlreturn,noinlineerr,testpackage,wsl_v5 // Internal tests exercise the source evaluator and manifest invariants.
package compatibility

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
)

func TestCommittedBaseline(t *testing.T) {
	t.Parallel()

	baseline, err := LoadBaseline(filepath.Join("..", "..", DefaultBaselinePath))
	if err != nil {
		t.Fatalf("LoadBaseline() error = %v", err)
	}

	if len(baseline.Capabilities) == 0 || len(baseline.Scenarios) == 0 ||
		len(baseline.Behaviors) == 0 {
		t.Fatal("generic compatibility inventory is empty")
	}
}

func TestGeneratedDocumentationMatchesBaseline(t *testing.T) {
	t.Parallel()

	baseline, err := LoadBaseline(filepath.Join("..", "..", DefaultBaselinePath))
	if err != nil {
		t.Fatal(err)
	}

	documentation, err := os.ReadFile(filepath.Join("..", "..", DefaultDocumentationPath))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(documentation, RenderDocumentation(baseline)) {
		t.Fatal("generated compatibility documentation is stale")
	}
}

func TestBaselineRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "baseline.json")
	raw := []byte(`{"schemaVersion":"v1alpha1","unknown":true}`)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadBaseline(path)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("LoadBaseline() error = %v, want unknown-field error", err)
	}
}

//nolint:tparallel // Cases mutate independent manifest clones in a deterministic order.
func TestBaselineValidationRejectsInvalidInventory(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		mutate   func(*Baseline)
		fragment string
	}{
		{
			name: "duplicate capability",
			mutate: func(b *Baseline) {
				b.Capabilities = append(b.Capabilities, b.Capabilities[0])
			},
			fragment: "duplicate capability ID",
		},
		{
			name: "unknown capability",
			mutate: func(b *Baseline) {
				b.Behaviors[0].RequiredCapabilities = []string{"unknown"}
			},
			fragment: "references unknown capability",
		},
		{
			name: "missing scenario",
			mutate: func(b *Baseline) {
				b.Behaviors[0].Scenarios = nil
			},
			fragment: "empty inventory column",
		},
		{
			name: "empty invalidation input",
			mutate: func(b *Baseline) {
				b.Invalidation.Planner = ""
			},
			fragment: "invalidation.planner is empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseline, err := LoadBaseline(filepath.Join("..", "..", DefaultBaselinePath))
			if err != nil {
				t.Fatal(err)
			}
			tt.mutate(baseline)

			err = baseline.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.fragment) {
				t.Fatalf("Validate() error = %v, want fragment %q", err, tt.fragment)
			}
		})
	}
}

func TestRepositoryVersionReferenceMismatch(t *testing.T) {
	t.Parallel()

	baseline, err := LoadBaseline(filepath.Join("..", "..", DefaultBaselinePath))
	if err != nil {
		t.Fatal(err)
	}
	baseline.VersionReferences = []VersionReference{
		{Path: "version.txt", Pattern: "containerlab {{version}}"},
	}
	baseline.Behaviors = nil

	root := t.TempDir()
	if err = os.WriteFile(filepath.Join(root, "version.txt"), []byte("containerlab 9.9.9"), 0o600); err != nil {
		t.Fatal(err)
	}

	err = baseline.VerifyRepository(root)
	if err == nil || !strings.Contains(err.Error(), `does not contain "containerlab 0.78.0"`) {
		t.Fatalf("VerifyRepository() error = %v, want version mismatch", err)
	}
}

func TestContainerlabModulePin(t *testing.T) {
	t.Parallel()

	const module = "github.com/srl-labs/containerlab"

	tests := []struct {
		name     string
		module   string
		fragment string
	}{
		{
			name:   "exact direct pin",
			module: "require github.com/srl-labs/containerlab v0.78.0\n",
		},
		{
			name:     "missing",
			module:   "require example.com/other v1.0.0\n",
			fragment: "does not directly require",
		},
		{
			name:     "wrong version",
			module:   "require github.com/srl-labs/containerlab v0.79.0\n",
			fragment: "v0.79.0, want v0.78.0",
		},
		{
			name:     "indirect",
			module:   "require github.com/srl-labs/containerlab v0.78.0 // indirect\n",
			fragment: "indirectly",
		},
		{
			name: "local replacement",
			module: "require github.com/srl-labs/containerlab v0.78.0\n" +
				"replace github.com/srl-labs/containerlab => ../containerlab\n",
			fragment: "requires the unmodified module",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			raw := []byte("module example.com/test\n\ngo 1.25.0\n\n" + tt.module)
			err := verifyContainerlabModuleFile(raw, module, "v0.78.0")
			if tt.fragment == "" {
				if err != nil {
					t.Fatalf("verifyContainerlabModuleFile() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.fragment) {
				t.Fatalf(
					"verifyContainerlabModuleFile() error = %v, want fragment %q",
					err,
					tt.fragment,
				)
			}
		})
	}
}

func TestAPIFieldsAreInventoried(t *testing.T) {
	t.Parallel()

	baseline, err := LoadBaseline(filepath.Join("..", "..", DefaultBaselinePath))
	if err != nil {
		t.Fatal(err)
	}

	inputs := map[string]bool{}
	for _, behavior := range baseline.Behaviors {
		for _, input := range behavior.Inputs {
			inputs[input] = true
		}
	}

	for _, apiType := range []struct {
		prefix string
		typeOf reflect.Type
	}{
		{prefix: "Node.spec.", typeOf: reflect.TypeFor[clabernetesapisv1alpha1.NodeSpec]()},
		{prefix: "Link.spec.", typeOf: reflect.TypeFor[clabernetesapisv1alpha1.LinkSpec]()},
		{prefix: "LauncherProfile.spec.", typeOf: reflect.TypeFor[clabernetesapisv1alpha1.LauncherProfileSpec]()},
		{prefix: "Config.spec.", typeOf: reflect.TypeFor[clabernetesapisv1alpha1.ConfigSpec]()},
	} {
		for _, field := range topLevelJSONFields(apiType.typeOf) {
			inventoryName := apiType.prefix + field
			if !inputs[inventoryName] {
				t.Errorf(
					"API field %q is absent from the compatibility behavior inventory",
					inventoryName,
				)
			}
		}
	}
}

func topLevelJSONFields(typeOf reflect.Type) []string {
	for typeOf.Kind() == reflect.Pointer {
		typeOf = typeOf.Elem()
	}

	fields := []string{}
	for field := range typeOf.Fields() {
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "-" {
			continue
		}
		if name == "" && field.Anonymous {
			fields = append(fields, topLevelJSONFields(field.Type)...)
			continue
		}
		if name != "" {
			fields = append(fields, name)
		}
	}

	return fields
}

func TestExtractRegistry(t *testing.T) {
	t.Parallel()

	got, err := ExtractRegistry(filepath.Join("testdata", "upstream"))
	if err != nil {
		t.Fatalf("ExtractRegistry() error = %v", err)
	}

	want := []Registration{
		{SourcePackage: "nodes/bar", Names: []string{"bar"}},
		{SourcePackage: "nodes/foo", Names: []string{"foo", "foo_alias"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ExtractRegistry() = %#v, want %#v", got, want)
	}
}

func TestCompareRegistrationsReportsAddedRemovedAndRemapped(t *testing.T) {
	t.Parallel()

	expected := []Registration{
		{SourcePackage: "nodes/foo", Names: []string{"foo", "old_alias", "moved"}},
	}
	actual := []Registration{
		{SourcePackage: "nodes/foo", Names: []string{"foo", "new_alias"}},
		{SourcePackage: "nodes/bar", Names: []string{"bar", "moved"}},
	}

	problems := strings.Join(CompareRegistrations(expected, actual), "\n")
	for _, fragment := range []string{
		`added kind "bar"`,
		`added kind "new_alias"`,
		`remapped kind "moved"`,
		`removed kind "old_alias"`,
	} {
		if !strings.Contains(problems, fragment) {
			t.Errorf("problems %q do not contain %q", problems, fragment)
		}
	}
}

func TestRegistryDigestIgnoresOrdering(t *testing.T) {
	t.Parallel()

	first := []Registration{
		{SourcePackage: "nodes/foo", Names: []string{"foo", "z", "a"}},
		{SourcePackage: "nodes/bar", Names: []string{"bar"}},
	}
	second := []Registration{
		{SourcePackage: "nodes/bar", Names: []string{"bar"}},
		{SourcePackage: "nodes/foo", Names: []string{"foo", "a", "z"}},
	}

	firstDigest, err := RegistryDigest(first)
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := RegistryDigest(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest != secondDigest {
		t.Fatalf("digests differ: %q != %q", firstDigest, secondDigest)
	}
}
