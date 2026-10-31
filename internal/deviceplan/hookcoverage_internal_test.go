package deviceplan

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	clabnodes "github.com/srl-labs/containerlab/nodes"
	clabruntime "github.com/srl-labs/containerlab/runtime"
	clabtypes "github.com/srl-labs/containerlab/types"
)

// resolvedImportedMethodSite is one parsed planning call site an inventory entry names.
type resolvedImportedMethodSite struct {
	file     string
	function string
}

func TestImportedLinuxPostDeployReachesNamespaceOverrides(t *testing.T) {
	t.Parallel()

	node, err := NewContainerlabRegistry().NewNodeOfKind("linux")
	if err != nil {
		t.Fatal(err)
	}
	runtime := &coverageNamespaceRuntime{}
	if err = node.Init(&clabtypes.NodeConfig{ShortName: "coverage", LongName: "coverage"},
		clabnodes.WithRuntime(runtime)); err != nil {
		t.Fatal(err)
	}
	if err = node.PostDeploy(t.Context(), &clabnodes.PostDeployParams{}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(runtime.calls, []string{"GetContainerStatus", "GetNSPath"}) {
		t.Fatalf("imported namespace calls = %v", runtime.calls)
	}
	for _, entry := range ImportedCoverage() {
		if slices.Contains(runtime.calls, entry.Method) &&
			(entry.State != CoverageExercised || entry.Reach != ReachImported) {
			t.Errorf("reachable imported method %s.%s is classified %s/%s",
				entry.Interface, entry.Method, entry.State, entry.Reach)
		}
	}
}

var errCoverageNamespaceObserved = errors.New("namespace observation complete")

type coverageNamespaceRuntime struct {
	clabruntime.ContainerRuntime

	calls []string
}

func (r *coverageNamespaceRuntime) GetContainerStatus(
	context.Context,
	string,
) clabruntime.ContainerStatus {
	r.calls = append(r.calls, "GetContainerStatus")

	return clabruntime.Running
}

func (r *coverageNamespaceRuntime) GetNSPath(context.Context, string) (string, error) {
	r.calls = append(r.calls, "GetNSPath")

	// Stop before entering a namespace; this test only observes imported method dispatch.
	return "", errCoverageNamespaceObserved
}

// TestImportedCoverageInventoryIsComplete proves the committed inventory classifies exactly the
// live imported interface surface and carries the evidence each state requires.
func TestImportedCoverageInventoryIsComplete(t *testing.T) {
	t.Parallel()

	coverage := ImportedCoverage()
	if err := ValidateImportedCoverage(coverage); err != nil {
		t.Fatalf("imported coverage inventory is invalid: %v", err)
	}
	summary := SummarizeImportedCoverage(coverage)
	t.Logf(
		"imported interface coverage: %d methods, %d exercised (%d direct, %d imported), "+
			"%d refused, %d unused",
		summary.Total,
		summary.Exercised,
		summary.Direct,
		summary.Imported,
		summary.Refused,
		summary.Unused,
	)
	for _, refusal := range summary.Refusals {
		t.Logf("refused: %s.%s (reliance %s) -- %s",
			refusal.Interface, refusal.Method, refusal.Reliance, refusal.Reason)
	}
}

// TestImportedCoverageDirectSitesInvokeTheirMethod proves every method classified as directly
// exercised is still invoked where the inventory says it is, so a classification cannot survive the
// call site it documents.
func TestImportedCoverageDirectSitesInvokeTheirMethod(t *testing.T) {
	t.Parallel()

	coverage := ImportedCoverage()
	if err := verifyImportedCoverageSites(coverage); err != nil {
		t.Fatalf("imported coverage call sites do not match the inventory: %v", err)
	}
}

// TestImportedCoverageDirectSiteDriftFails proves a direct classification whose call site no longer
// invokes the method fails verification.
func TestImportedCoverageDirectSiteDriftFails(t *testing.T) {
	t.Parallel()

	coverage := ImportedCoverage()
	mutated := false
	for index := range coverage {
		if coverage[index].Method != "Config" || coverage[index].State != CoverageExercised {
			continue
		}
		// importedNodeMap does not read a node configuration, so the claim must fail.
		coverage[index].Site = "adapter.go:importedNodeMap"
		mutated = true

		break
	}
	if !mutated {
		t.Fatal("inventory no longer classifies nodes.Node.Config as directly exercised")
	}
	if err := verifyImportedCoverageSites(coverage); err == nil {
		t.Fatal("verification accepted a direct classification whose call site invokes nothing")
	}
}

// TestImportedCoverageRejectsUnclassifiedMethod proves a method added to the imported interface
// without a classification fails verification by name.
func TestImportedCoverageRejectsUnclassifiedMethod(t *testing.T) {
	t.Parallel()

	reflected := ImportedInterfaceMethods()
	if len(reflected) == 0 {
		t.Fatal("imported interfaces expose no methods")
	}
	dropped := reflected[0]
	coverage := []ImportedMethodCoverage{}
	for _, entry := range ImportedCoverage() {
		if entry.Interface == dropped.Interface && entry.Method == dropped.Method {
			continue
		}
		coverage = append(coverage, entry)
	}
	err := ValidateImportedCoverage(coverage)
	if err == nil {
		t.Fatalf(
			"verification accepted an inventory without %s.%s",
			dropped.Interface,
			dropped.Method,
		)
	}
	if !strings.Contains(err.Error(), dropped.Interface+"."+dropped.Method) {
		t.Fatalf("verification error does not name the unclassified method: %v", err)
	}
}

// TestImportedCoverageRejectsStaleClassification proves a classification that no longer matches the
// imported surface fails verification instead of silently granting coverage.
func TestImportedCoverageRejectsStaleClassification(t *testing.T) {
	t.Parallel()

	coverage := append(ImportedCoverage(), ImportedMethodCoverage{
		Interface: importedInterfaceNode,
		Method:    "NoSuchImportedMethod",
		State:     CoverageUnused,
		Reason:    "invented for verification",
	})
	err := ValidateImportedCoverage(coverage)
	if err == nil || !strings.Contains(err.Error(), "NoSuchImportedMethod") {
		t.Fatalf(
			"verification accepted a classification absent from the imported interface: %v",
			err,
		)
	}
}

// TestImportedCoverageRequiresRefusalEvidence proves refusals must state a reason and how kind
// reliance is derived, so a refusal cannot quietly drop behavior for every kind.
func TestImportedCoverageRequiresRefusalEvidence(t *testing.T) {
	t.Parallel()

	coverage := ImportedCoverage()
	mutated := false
	for index := range coverage {
		if coverage[index].State != CoverageRefused {
			continue
		}
		coverage[index].Reliance = ""
		mutated = true

		break
	}
	if !mutated {
		t.Fatal("inventory records no refused imported behavior")
	}

	err := ValidateImportedCoverage(coverage)
	if err == nil {
		t.Fatal("verification accepted a refusal with no reliance attribution")
	}
	if _, ok := errors.AsType[*importedCoverageError](err); !ok {
		t.Fatalf("verification error has an unexpected type: %T", err)
	}
}

// verifyImportedCoverageSites parses every planning call site a direct classification names and
// proves the enclosing function still invokes the method.
func verifyImportedCoverageSites(coverage []ImportedMethodCoverage) error {
	parsed := map[string]*ast.File{}
	fset := token.NewFileSet()
	problems := []string{}
	for _, entry := range coverage {
		if entry.State != CoverageExercised || entry.Reach != ReachDirect {
			continue
		}
		site, parseErr := parseImportedMethodSite(entry.Site)
		if parseErr != nil {
			problems = append(problems, entry.Interface+"."+entry.Method+": "+parseErr.Error())

			continue
		}
		file, ok := parsed[site.file]
		if !ok {
			parsedFile, readErr := parser.ParseFile(
				fset,
				site.file,
				nil,
				parser.SkipObjectResolution,
			)
			if readErr != nil {
				problems = append(problems, entry.Interface+"."+entry.Method+
					": cannot parse call site file: "+readErr.Error())

				continue
			}
			file = parsedFile
			parsed[site.file] = file
		}
		declaration := importedSiteFunction(file, site.function)
		if declaration == nil {
			problems = append(problems, entry.Interface+"."+entry.Method+
				": call site function "+site.function+" is absent from "+site.file)

			continue
		}
		if !importedSiteInvokes(declaration, entry.Method) {
			problems = append(problems, entry.Interface+"."+entry.Method+
				": call site "+entry.Site+" does not invoke it")
		}
	}
	if len(problems) == 0 {
		return nil
	}

	return &importedCoverageError{Problems: problems}
}

func parseImportedMethodSite(site string) (resolvedImportedMethodSite, error) {
	file, function, found := strings.Cut(site, ":")
	if !found || file == "" || function == "" {
		return resolvedImportedMethodSite{}, fmt.Errorf( //nolint:err113 // test-local diagnostic.
			"call site %q is not file.go:function", site,
		)
	}
	cleaned := filepath.Clean(file)
	if filepath.Dir(cleaned) != "." || strings.HasSuffix(cleaned, "_test.go") {
		return resolvedImportedMethodSite{}, fmt.Errorf( //nolint:err113 // test-local diagnostic.
			"call site %q must name a production file in the planning package", site,
		)
	}

	return resolvedImportedMethodSite{file: cleaned, function: function}, nil
}

func importedSiteFunction(file *ast.File, name string) *ast.FuncDecl {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != name || function.Body == nil {
			continue
		}

		return function
	}

	return nil
}

func importedSiteInvokes(function *ast.FuncDecl, method string) bool {
	found := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if selector, direct := call.Fun.(*ast.SelectorExpr); direct && selector.Sel.Name == method {
			found = true
		}
		// Imported checks may pass the method as a callback to the panic boundary.
		if helper, named := call.Fun.(*ast.Ident); named && helper.Name == "invokeImported" {
			for _, argument := range call.Args {
				if selector, callback := argument.(*ast.SelectorExpr); callback &&
					selector.Sel.Name == method {
					found = true
				}
			}
		}

		return !found
	})

	return found
}

func TestImportedCoverageRequiresInvocationOrBoundaryCallback(t *testing.T) {
	t.Parallel()

	for _, example := range []struct {
		body string
		want bool
	}{
		{"node.CheckInterfaceName()", true},
		{"invokeImported(node.CheckInterfaceName)", true},
		{"_ = node.CheckInterfaceName", false},
	} {
		file, err := parser.ParseFile(token.NewFileSet(), "example.go",
			"package example; func check() {"+example.body+"}", 0)
		if err != nil {
			t.Fatal(err)
		}
		if got := importedSiteInvokes(importedSiteFunction(file, "check"), "CheckInterfaceName"); got != example.want {
			t.Fatalf("invocation check for %q = %v, want %v", example.body, got, example.want)
		}
	}
}
