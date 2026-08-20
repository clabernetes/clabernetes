//nolint:err113,gocyclo,mnd,nlreturn,noinlineerr,wsl_v5 // Manifest validation is clearest as compact fail-fast checks.
package compatibility

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/mod/modfile"
)

// Compatibility baseline schema and repository paths.
const (
	// BaselineSchemaVersion identifies the compatibility manifest schema.
	BaselineSchemaVersion = "v1alpha1"
	DefaultBaselinePath   = "compatibility/containerlab/baseline.json"
)

var (
	errInvalidBaseline    = errors.New("invalid compatibility baseline")
	errRepositoryMismatch = errors.New("compatibility repository mismatch")
	errModuleDownload     = errors.New("containerlab module download failed")
	errUpstreamMismatch   = errors.New("containerlab baseline mismatch")
)

// Baseline is the complete pinned compatibility and evidence inventory.
type Baseline struct {
	SchemaVersion     string               `json:"schemaVersion"`
	PlanSchemaVersion string               `json:"planSchemaVersion"`
	Containerlab      ContainerlabBaseline `json:"containerlab"`
	VersionReferences []VersionReference   `json:"versionReferences"`
	Capabilities      []Capability         `json:"capabilities"`
	Scenarios         []Scenario           `json:"scenarios"`
	Behaviors         []Behavior           `json:"behaviors"`
	Invalidation      Invalidation         `json:"invalidation"`
}

// VersionReference identifies a repository path containing the pinned upstream version.
type VersionReference struct {
	Path    string `json:"path"`
	Pattern string `json:"pattern"`
}

// ContainerlabBaseline pins the immutable upstream module and registry identity.
type ContainerlabBaseline struct {
	Module               string `json:"module"`
	Version              string `json:"version"`
	ModuleVersion        string `json:"moduleVersion"`
	ModuleSum            string `json:"moduleSum"`
	Commit               string `json:"commit"`
	RegistrySource       string `json:"registrySource"`
	RegistrySourceSHA256 string `json:"registrySourceSHA256"`
}

// Capability names one direct-runtime compatibility requirement.
type Capability struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

// Scenario names one compatibility-validation scenario.
type Scenario struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

// Behavior inventories one runtime behavior and its current realization.
type Behavior struct {
	ID                   string   `json:"id"`
	Category             string   `json:"category"`
	Inputs               []string `json:"inputs"`
	RequiredCapabilities []string `json:"requiredCapabilities"`
	Scenarios            []string `json:"scenarios"`
	SourcePaths          []string `json:"sourcePaths"`
	State                string   `json:"state"`
}

// Invalidation records the upstream boundaries that require compatibility review.
type Invalidation struct {
	Planner      string `json:"planner"`
	Renderer     string `json:"renderer"`
	Preparation  string `json:"preparation"`
	Connectivity string `json:"connectivity"`
}

// ModuleDownload is the relevant output of go mod download -json.
type ModuleDownload struct {
	Path    string       `json:"Path"`
	Version string       `json:"Version"`
	Sum     string       `json:"Sum"`
	Dir     string       `json:"Dir"`
	Origin  ModuleOrigin `json:"Origin"`
}

// ModuleOrigin contains the immutable repository commit reported by the Go command.
type ModuleOrigin struct {
	Hash string `json:"Hash"`
}

// LoadBaseline decodes and validates a compatibility manifest from path.
func LoadBaseline(path string) (*Baseline, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // The caller explicitly selects the baseline path.
	if err != nil {
		return nil, fmt.Errorf("reading compatibility baseline: %w", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()

	baseline := &Baseline{}
	if err = decoder.Decode(baseline); err != nil {
		return nil, fmt.Errorf("decoding compatibility baseline: %w", err)
	}

	var trailing any
	if err = decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("%w: trailing JSON values", errInvalidBaseline)
		}
		return nil, fmt.Errorf("decoding compatibility baseline trailer: %w", err)
	}

	if err := baseline.Validate(); err != nil {
		return nil, err
	}

	return baseline, nil
}

// Validate enforces schema, uniqueness, ordering, and cross-reference integrity.
//
//nolint:cyclop,funlen,gocognit,gocyclo,maintidx // This is one declarative manifest invariant pass.
func (b *Baseline) Validate() error {
	if b.SchemaVersion != BaselineSchemaVersion {
		return fmt.Errorf(
			"%w: schemaVersion is %q, want %q",
			errInvalidBaseline,
			b.SchemaVersion,
			BaselineSchemaVersion,
		)
	}
	if b.PlanSchemaVersion == "" {
		return fmt.Errorf("%w: planSchemaVersion is empty", errInvalidBaseline)
	}
	if b.Containerlab.Module != containerlabModulePath {
		return fmt.Errorf(
			"%w: containerlab module is %q, want %q",
			errInvalidBaseline,
			b.Containerlab.Module,
			containerlabModulePath,
		)
	}
	if b.Containerlab.Version == "" || b.Containerlab.ModuleVersion != "v"+b.Containerlab.Version {
		return fmt.Errorf(
			"%w: inconsistent containerlab version %q and moduleVersion %q",
			errInvalidBaseline,
			b.Containerlab.Version,
			b.Containerlab.ModuleVersion,
		)
	}
	for name, value := range map[string]string{
		"moduleSum":            b.Containerlab.ModuleSum,
		"commit":               b.Containerlab.Commit,
		"registrySource":       b.Containerlab.RegistrySource,
		"registrySourceSHA256": b.Containerlab.RegistrySourceSHA256,
	} {
		if value == "" {
			return fmt.Errorf("%w: containerlab.%s is empty", errInvalidBaseline, name)
		}
	}

	seenReferences := map[string]bool{}
	for _, reference := range b.VersionReferences {
		if reference.Path == "" || filepath.IsAbs(reference.Path) ||
			filepath.Clean(reference.Path) != reference.Path ||
			strings.HasPrefix(reference.Path, "..") {
			return fmt.Errorf(
				"%w: invalid version reference path %q",
				errInvalidBaseline,
				reference.Path,
			)
		}
		if seenReferences[reference.Path] {
			return fmt.Errorf(
				"%w: duplicate version reference path %q",
				errInvalidBaseline,
				reference.Path,
			)
		}
		seenReferences[reference.Path] = true
		if strings.Count(reference.Pattern, "{{version}}") != 1 {
			return fmt.Errorf(
				"%w: version reference %q pattern must contain {{version}} exactly once",
				errInvalidBaseline,
				reference.Path,
			)
		}
	}
	if len(b.VersionReferences) == 0 {
		return fmt.Errorf("%w: versionReferences is empty", errInvalidBaseline)
	}

	capabilities, err := uniqueIDs(
		"capability",
		b.Capabilities,
		func(value Capability) string { return value.ID },
	)
	if err != nil {
		return err
	}
	scenarios, err := uniqueIDs(
		"scenario",
		b.Scenarios,
		func(value Scenario) string { return value.ID },
	)
	if err != nil {
		return err
	}
	for _, capability := range b.Capabilities {
		if capability.Description == "" {
			return fmt.Errorf(
				"%w: capability %q has no description",
				errInvalidBaseline,
				capability.ID,
			)
		}
	}
	for _, scenario := range b.Scenarios {
		if scenario.Description == "" {
			return fmt.Errorf("%w: scenario %q has no description", errInvalidBaseline, scenario.ID)
		}
	}

	behaviorIDs, err := uniqueIDs(
		"behavior",
		b.Behaviors,
		func(value Behavior) string { return value.ID },
	)
	if err != nil {
		return err
	}
	_ = behaviorIDs
	allowedBehaviorCategory := map[string]bool{
		"node-intent":       true,
		"runtime":           true,
		"link":              true,
		"operation":         true,
		"entry-path":        true,
		"kubernetes-policy": true,
	}
	allowedBehaviorState := map[string]bool{
		"controller-native": true,
		"direct":            true,
		"partial":           true,
		"missing":           true,
	}
	for index := range b.Behaviors {
		behavior := &b.Behaviors[index]
		if !allowedBehaviorCategory[behavior.Category] {
			return fmt.Errorf(
				"%w: behavior %q has invalid category %q",
				errInvalidBaseline,
				behavior.ID,
				behavior.Category,
			)
		}
		if !allowedBehaviorState[behavior.State] {
			return fmt.Errorf(
				"%w: behavior %q has invalid state %q",
				errInvalidBaseline,
				behavior.ID,
				behavior.State,
			)
		}
		if len(behavior.Inputs) == 0 || len(behavior.RequiredCapabilities) == 0 ||
			len(behavior.Scenarios) == 0 ||
			len(behavior.SourcePaths) == 0 {
			return fmt.Errorf(
				"%w: behavior %q has an empty inventory column",
				errInvalidBaseline,
				behavior.ID,
			)
		}
		for _, capability := range behavior.RequiredCapabilities {
			if !capabilities[capability] {
				return fmt.Errorf(
					"%w: behavior %q references unknown capability %q",
					errInvalidBaseline,
					behavior.ID,
					capability,
				)
			}
		}
		for _, scenario := range behavior.Scenarios {
			if !scenarios[scenario] {
				return fmt.Errorf(
					"%w: behavior %q references unknown scenario %q",
					errInvalidBaseline,
					behavior.ID,
					scenario,
				)
			}
		}
		for _, sourcePath := range behavior.SourcePaths {
			if sourcePath == "" || filepath.IsAbs(sourcePath) ||
				filepath.Clean(sourcePath) != sourcePath ||
				strings.HasPrefix(sourcePath, "..") {
				return fmt.Errorf(
					"%w: behavior %q has invalid source path %q",
					errInvalidBaseline,
					behavior.ID,
					sourcePath,
				)
			}
		}
	}
	for name, value := range map[string]string{
		"planner":      b.Invalidation.Planner,
		"renderer":     b.Invalidation.Renderer,
		"preparation":  b.Invalidation.Preparation,
		"connectivity": b.Invalidation.Connectivity,
	} {
		if value == "" {
			return fmt.Errorf("%w: invalidation.%s is empty", errInvalidBaseline, name)
		}
	}

	return nil
}

// VerifyRepository checks every declared baseline-version reference and inventory source path.
// Keeping the reference list in the baseline makes version coupling reviewable instead of hiding
// more release pins in tests, Dockerfiles, or user documentation.
func (b *Baseline) VerifyRepository(root string) error {
	problems := []string{}

	moduleFilePath := filepath.Join(root, "go.mod")
	moduleFile, err := os.ReadFile( //nolint:gosec // reads are confined to plan-scoped roots.
		moduleFilePath,
	)
	if err != nil {
		problems = append(problems, fmt.Sprintf("go.mod cannot be read: %v", err))
	} else if moduleErr := verifyContainerlabModuleFile(
		moduleFile,
		b.Containerlab.Module,
		b.Containerlab.ModuleVersion,
	); moduleErr != nil {
		problems = append(problems, moduleErr.Error())
	}

	for _, reference := range b.VersionReferences {
		path := filepath.Join(root, reference.Path)
		raw, err := os.ReadFile(path) //nolint:gosec // Paths come from the validated manifest.
		if err != nil {
			problems = append(
				problems,
				fmt.Sprintf("version reference %q cannot be read: %v", reference.Path, err),
			)
			continue
		}

		expected := strings.ReplaceAll(reference.Pattern, "{{version}}", b.Containerlab.Version)
		if !bytes.Contains(raw, []byte(expected)) {
			problems = append(
				problems,
				fmt.Sprintf("version reference %q does not contain %q", reference.Path, expected),
			)
		}
	}

	seenSources := map[string]bool{}
	for index := range b.Behaviors {
		behavior := &b.Behaviors[index]
		for _, sourcePath := range behavior.SourcePaths {
			if seenSources[sourcePath] {
				continue
			}
			seenSources[sourcePath] = true
			if _, err := os.Stat(filepath.Join(root, sourcePath)); err != nil {
				problems = append(
					problems,
					fmt.Sprintf("behavior source %q cannot be inspected: %v", sourcePath, err),
				)
			}
		}
	}

	currentInvalidation, err := ComputeInvalidation(root)
	if err != nil {
		problems = append(problems, fmt.Sprintf("computing invalidation digests: %v", err))
	} else {
		for component, pair := range map[string][2]string{
			"planner":      {b.Invalidation.Planner, currentInvalidation.Planner},
			"renderer":     {b.Invalidation.Renderer, currentInvalidation.Renderer},
			"preparation":  {b.Invalidation.Preparation, currentInvalidation.Preparation},
			"connectivity": {b.Invalidation.Connectivity, currentInvalidation.Connectivity},
		} {
			if pair[0] != pair[1] {
				problems = append(
					problems,
					fmt.Sprintf(
						"invalidation.%s is stale (recorded %s, current %s): the %s "+
							"implementation changed, so recorded conformance evidence is "+
							"retired; re-run the affected conformance and refresh with "+
							"go run ./cmd/compatibility -mode refresh-invalidation",
						component,
						pair[0],
						pair[1],
						component,
					),
				)
			}
		}
	}

	documentationPath := filepath.Join(root, DefaultDocumentationPath)
	// The supplied root intentionally scopes this repository read.
	//nolint:gosec
	documentation, err := os.ReadFile(
		documentationPath,
	)
	if err != nil {
		problems = append(
			problems,
			fmt.Sprintf(
				"generated compatibility documentation %q cannot be read: %v",
				DefaultDocumentationPath,
				err,
			),
		)
	} else if !bytes.Equal(documentation, RenderDocumentation(b)) {
		problems = append(
			problems,
			fmt.Sprintf(
				"generated compatibility documentation %q is stale; "+
					"run make generate-containerlab-compatibility",
				DefaultDocumentationPath,
			),
		)
	}

	if len(problems) > 0 {
		slices.Sort(problems)
		return fmt.Errorf(
			"%w:\n- %s",
			errRepositoryMismatch,
			strings.Join(problems, "\n- "),
		)
	}

	return nil
}

func verifyContainerlabModuleFile(raw []byte, module, version string) error {
	parsed, err := modfile.Parse("go.mod", raw, nil)
	if err != nil {
		return fmt.Errorf("go.mod cannot be parsed: %w", err)
	}

	for _, replacement := range parsed.Replace {
		if replacement.Old.Path == module {
			return fmt.Errorf(
				"go.mod replaces %s; the compatibility baseline requires the unmodified module",
				module,
			)
		}
	}

	for _, requirement := range parsed.Require {
		if requirement.Mod.Path != module {
			continue
		}
		if requirement.Mod.Version != version {
			return fmt.Errorf(
				"go.mod requires %s %s, want %s",
				module,
				requirement.Mod.Version,
				version,
			)
		}
		if requirement.Indirect {
			return fmt.Errorf("go.mod requires %s indirectly, want a direct dependency", module)
		}

		return nil
	}

	return fmt.Errorf("go.mod does not directly require %s %s", module, version)
}

func uniqueIDs[T any](kind string, values []T, id func(T) string) (map[string]bool, error) {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		identifier := id(value)
		if identifier == "" {
			return nil, fmt.Errorf("%w: empty %s ID", errInvalidBaseline, kind)
		}
		if result[identifier] {
			return nil, fmt.Errorf("%w: duplicate %s ID %q", errInvalidBaseline, kind, identifier)
		}
		result[identifier] = true
	}
	return result, nil
}

// DownloadModule obtains immutable Go module metadata and its extracted source directory.
func DownloadModule(ctx context.Context, module, version string) (*ModuleDownload, error) {
	// Arguments are passed directly to go without a shell; module and version cannot add flags.
	command := exec.CommandContext( //nolint:gosec
		ctx,
		"go",
		"mod",
		"download",
		"-json",
		module+"@"+version,
	)
	raw, err := command.Output()
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return nil, fmt.Errorf(
				"%w %s@%s: %s: %w",
				errModuleDownload,
				module,
				version,
				strings.TrimSpace(string(exitError.Stderr)),
				err,
			)
		}
		return nil, fmt.Errorf("%w %s@%s: %w", errModuleDownload, module, version, err)
	}

	download := &ModuleDownload{}
	if err = json.Unmarshal(raw, download); err != nil {
		return nil, fmt.Errorf("decoding go mod download output: %w", err)
	}
	if download.Dir == "" {
		return nil, fmt.Errorf(
			"%w %s@%s returned no source directory",
			errModuleDownload,
			module,
			version,
		)
	}

	return download, nil
}

// FileSHA256 returns a sha256-prefixed digest for the explicitly selected file.
func FileSHA256(path string) (string, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // The caller explicitly selects the source file.
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

// VerifyUpstream checks immutable source identity first, then reports registry differences.
func (b *Baseline) VerifyUpstream(download *ModuleDownload) error {
	problems := []string{}

	if download.Path != b.Containerlab.Module {
		problems = append(
			problems,
			fmt.Sprintf("module path is %q, want %q", download.Path, b.Containerlab.Module),
		)
	}
	if download.Version != b.Containerlab.ModuleVersion {
		problems = append(
			problems,
			fmt.Sprintf(
				"module version is %q, want %q",
				download.Version,
				b.Containerlab.ModuleVersion,
			),
		)
	}
	if download.Sum != b.Containerlab.ModuleSum {
		problems = append(
			problems,
			fmt.Sprintf("module sum is %q, want %q", download.Sum, b.Containerlab.ModuleSum),
		)
	}
	if download.Origin.Hash != b.Containerlab.Commit {
		problems = append(
			problems,
			fmt.Sprintf(
				"module commit is %q, want %q",
				download.Origin.Hash,
				b.Containerlab.Commit,
			),
		)
	}

	sourceDigest, err := FileSHA256(
		filepath.Join(download.Dir, filepath.FromSlash(b.Containerlab.RegistrySource)),
	)
	if err != nil {
		return fmt.Errorf("hashing registry source: %w", err)
	}
	if sourceDigest != b.Containerlab.RegistrySourceSHA256 {
		problems = append(
			problems,
			fmt.Sprintf(
				"registry source digest is %q, want %q",
				sourceDigest,
				b.Containerlab.RegistrySourceSHA256,
			),
		)
	}

	actual, err := ExtractRegistry(download.Dir)
	if err != nil {
		return err
	}
	if len(actual) == 0 {
		problems = append(problems, "live imported registry is empty")
	}

	if len(problems) > 0 {
		return fmt.Errorf(
			"%w:\n- %s",
			errUpstreamMismatch,
			strings.Join(problems, "\n- "),
		)
	}

	return nil
}

// CompareRegistrations reports added, removed, and remapped registry names.
func CompareRegistrations(expected, actual []Registration) []string {
	expectedNames := registrationOwners(expected)
	actualNames := registrationOwners(actual)
	problems := []string{}

	for name, actualOwner := range actualNames {
		expectedOwner, ok := expectedNames[name]
		if !ok {
			problems = append(
				problems,
				fmt.Sprintf(
					"added kind %q (canonical %q, source %q)",
					name,
					actualOwner.canonical,
					actualOwner.sourcePackage,
				),
			)
			continue
		}
		if expectedOwner != actualOwner {
			problems = append(
				problems,
				fmt.Sprintf(
					"remapped kind %q from canonical %q/source %q to canonical %q/source %q",
					name,
					expectedOwner.canonical,
					expectedOwner.sourcePackage,
					actualOwner.canonical,
					actualOwner.sourcePackage,
				),
			)
		}
	}
	for name, expectedOwner := range expectedNames {
		if _, ok := actualNames[name]; !ok {
			problems = append(
				problems,
				fmt.Sprintf(
					"removed kind %q (canonical %q, source %q)",
					name,
					expectedOwner.canonical,
					expectedOwner.sourcePackage,
				),
			)
		}
	}

	slices.Sort(problems)
	return problems
}

type registrationOwner struct {
	canonical     string
	sourcePackage string
}

func registrationOwners(registrations []Registration) map[string]registrationOwner {
	result := map[string]registrationOwner{}
	for _, registration := range registrations {
		if len(registration.Names) == 0 {
			continue
		}
		owner := registrationOwner{
			canonical:     registration.Names[0],
			sourcePackage: registration.SourcePackage,
		}
		for _, name := range registration.Names {
			result[name] = owner
		}
	}
	return result
}

// SaveBaseline writes a validated baseline back to path with the canonical encoding used by
// the committed manifest.
func SaveBaseline(path string, baseline *Baseline) error {
	encoded, err := json.MarshalIndent(baseline, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding baseline: %w", err)
	}
	encoded = append(encoded, '\n')

	//nolint:gosec // The manifest is repository content, not a secret.
	return os.WriteFile(path, encoded, 0o644)
}
