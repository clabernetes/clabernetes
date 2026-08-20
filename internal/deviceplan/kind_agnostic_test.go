//nolint:gocognit,gocyclo // dense fixture-driven tests exercise one boundary end to end.
package deviceplan_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestDirectRuntimeSourceContainsNoContainerlabKindKnowledge prevents the generic integration from
// growing a second kind catalog. The check is structural rather than name-based so a containerlab
// dependency bump cannot fail merely because a new kind happens to match ordinary c9s vocabulary.
func TestDirectRuntimeSourceContainsNoContainerlabKindKnowledge(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	legacyNestedFiles := map[string]bool{
		filepath.Clean("../../controllers/node/deployment.go"): true,
	}

	for _, directory := range []string{
		".",
		"../compatibility",
		"../directpod",
		"../directruntime",
		"../ocimetadata",
		"../../cmd/compatibility",
		"../../controllers/node",
		"../../controllers/topology",
	} {
		entries, err := os.ReadDir(directory)
		if err != nil {
			t.Fatal(err)
		}

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") ||
				strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}

			filename := filepath.Clean(filepath.Join(directory, entry.Name()))
			if legacyNestedFiles[filename] {
				continue
			}

			parsed, parseErr := parser.ParseFile(fset, filename, nil, 0)
			if parseErr != nil {
				t.Fatal(parseErr)
			}

			for _, imported := range parsed.Imports {
				path, unquoteErr := strconv.Unquote(imported.Path.Value)
				if unquoteErr != nil {
					t.Fatal(unquoteErr)
				}

				if strings.HasPrefix(path, "github.com/srl-labs/containerlab/nodes/") {
					t.Errorf("%s imports concrete node implementation %q", filename, path)
				}
			}

			assertNoKindDispatch(t, fset, parsed)
		}
	}
}

func assertNoKindDispatch(t *testing.T, fset *token.FileSet, parsed *ast.File) {
	t.Helper()

	kindConstants := map[string]bool{}

	ast.Inspect(parsed, func(node ast.Node) bool {
		spec, ok := node.(*ast.ValueSpec)
		if !ok {
			return true
		}

		for index, name := range spec.Names {
			if kindKnowledgeName(name.Name) && index < len(spec.Values) &&
				nonEmptyStringLiteral(spec.Values[index]) {
				kindConstants[name.Name] = true
			}
		}

		return true
	})

	ast.Inspect(parsed, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.BinaryExpr:
			if value.Op != token.EQL && value.Op != token.NEQ {
				return true
			}

			if (expressionCarriesKindIdentity(value.X) && kindIdentityValue(value.Y, kindConstants)) ||
				(expressionCarriesKindIdentity(value.Y) && kindIdentityValue(value.X, kindConstants)) {
				reportKindDispatch(t, fset, value.Pos())
			}
		case *ast.SwitchStmt:
			if !expressionCarriesKindIdentity(value.Tag) {
				return true
			}

			for _, statement := range value.Body.List {
				clause, ok := statement.(*ast.CaseClause)
				if !ok {
					continue
				}

				for _, expression := range clause.List {
					if kindIdentityValue(expression, kindConstants) {
						reportKindDispatch(t, fset, expression.Pos())
					}
				}
			}
		case *ast.ValueSpec:
			for index, name := range value.Names {
				if kindKnowledgeName(name.Name) && index < len(value.Values) &&
					containsNonEmptyStringLiteral(value.Values[index]) {
					reportKindDispatch(t, fset, value.Pos())
				}
			}
		case *ast.AssignStmt:
			for index, left := range value.Lhs {
				identifier, ok := left.(*ast.Ident)
				if ok && kindKnowledgeName(identifier.Name) && index < len(value.Rhs) &&
					containsNonEmptyStringLiteral(value.Rhs[index]) {
					reportKindDispatch(t, fset, value.Pos())
				}
			}
		}

		return true
	})
}

func expressionCarriesKindIdentity(expression ast.Expr) bool {
	if expression == nil {
		return false
	}

	result := false

	ast.Inspect(expression, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.Ident:
			result = result || kindKnowledgeName(value.Name)
		case *ast.SelectorExpr:
			field := strings.ToLower(value.Sel.Name)
			result = result || ((field == "kind" || field == "type") && nodeIdentityReceiver(value.X)) ||
				(field != "kind" && field != "type" && kindKnowledgeName(value.Sel.Name))

			return false
		}

		return !result
	})

	return result
}

func kindIdentityValue(expression ast.Expr, kindConstants map[string]bool) bool {
	if nonEmptyStringLiteral(expression) {
		return true
	}

	identifier, ok := expression.(*ast.Ident)

	return ok && kindConstants[identifier.Name]
}

func kindKnowledgeName(name string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(name, "_", ""))

	return normalized == "kind" || normalized == "kinds" || normalized == "nodekind" ||
		normalized == "nodetype" || normalized == "vendor" || normalized == "vendors" ||
		normalized == "aliases" || strings.HasPrefix(normalized, "kindhandler") ||
		strings.HasPrefix(
			normalized,
			"vendor",
		) || strings.Contains(normalized, "containerlabkind") ||
		strings.Contains(normalized, "importedkind") || strings.Contains(normalized, "kindalias")
}

func nodeIdentityReceiver(expression ast.Expr) bool {
	result := false

	ast.Inspect(expression, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if !ok {
			return true
		}

		name := strings.ToLower(identifier.Name)
		result = result || strings.Contains(name, "node") || name == "input" ||
			strings.Contains(name, "definition") || strings.Contains(name, "config")

		return !result
	})

	return result
}

func nonEmptyStringLiteral(expression ast.Expr) bool {
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return false
	}

	value, err := strconv.Unquote(literal.Value)

	return err == nil && value != ""
}

func containsNonEmptyStringLiteral(node ast.Node) bool {
	result := false

	ast.Inspect(node, func(current ast.Node) bool {
		expression, ok := current.(ast.Expr)
		if ok && nonEmptyStringLiteral(expression) {
			result = true
		}

		return !result
	})

	return result
}

func reportKindDispatch(t *testing.T, fset *token.FileSet, position token.Pos) {
	t.Helper()

	location := fset.Position(position)
	t.Errorf(
		"%s:%d dispatches on containerlab kind, type, vendor, or alias identity",
		location.Filename,
		location.Line,
	)
}
