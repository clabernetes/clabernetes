//nolint:nlreturn,wsl_v5 // AST traversal branches are clearer without whitespace-only expansion.
package compatibility

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const (
	containerlabModulePath = "github.com/srl-labs/containerlab"
	registerFunctionName   = "Register"
)

var errInvalidRegistrySource = errors.New("invalid containerlab registry source")

// Registration is one canonical containerlab kind and all aliases registered by the same
// initializer. Names[0] is the canonical kind used by the compatibility matrix.
type Registration struct {
	SourcePackage string   `json:"sourcePackage"`
	Names         []string `json:"names"`
}

// ExtractRegistry reads containerlab's authoritative core registration sequence and the kind
// names supplied by every registered node package. It deliberately parses source instead of
// importing containerlab: importing the current nodes package would pull runtime implementations
// into the compatibility verifier and could observe a different module than the declared baseline.
func ExtractRegistry(sourceRoot string) ([]Registration, error) {
	registerPath := filepath.Join(sourceRoot, "core", "register.go")
	fset := token.NewFileSet()

	registerFile, err := parser.ParseFile(fset, registerPath, nil, 0)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: parsing %s: %w",
			errInvalidRegistrySource,
			registerPath,
			err,
		)
	}

	imports := map[string]string{}

	for _, importSpec := range registerFile.Imports {
		importPath, unquoteErr := strconv.Unquote(importSpec.Path.Value)
		if unquoteErr != nil {
			return nil, fmt.Errorf(
				"%w: parsing import %s: %w",
				errInvalidRegistrySource,
				importSpec.Path.Value,
				unquoteErr,
			)
		}

		alias := filepath.Base(importPath)
		if importSpec.Name != nil {
			alias = importSpec.Name.Name
		}

		imports[alias] = importPath
	}

	registerAliases, err := registeredPackageAliases(registerFile)
	if err != nil {
		return nil, err
	}

	registrations := make([]Registration, 0, len(registerAliases))
	seenNames := map[string]string{}

	for _, alias := range registerAliases {
		importPath, ok := imports[alias]
		if !ok {
			return nil, fmt.Errorf(
				"%w: RegisterNodes uses unknown import alias %q",
				errInvalidRegistrySource,
				alias,
			)
		}

		relativePackage, ok := strings.CutPrefix(importPath, containerlabModulePath+"/")
		if !ok {
			return nil, fmt.Errorf(
				"%w: node package %q is outside %s",
				errInvalidRegistrySource,
				importPath,
				containerlabModulePath,
			)
		}

		names, namesErr := registeredKindNames(
			filepath.Join(sourceRoot, filepath.FromSlash(relativePackage)),
		)
		if namesErr != nil {
			return nil, fmt.Errorf("extracting %s: %w", importPath, namesErr)
		}

		for _, name := range names {
			if prior, duplicate := seenNames[name]; duplicate {
				return nil, fmt.Errorf(
					"%w: kind %q is registered by both %s and %s",
					errInvalidRegistrySource,
					name,
					prior,
					relativePackage,
				)
			}

			seenNames[name] = relativePackage
		}

		registrations = append(registrations, Registration{
			SourcePackage: relativePackage,
			Names:         names,
		})
	}

	slices.SortFunc(registrations, func(a, b Registration) int {
		return strings.Compare(a.Names[0], b.Names[0])
	})

	return registrations, nil
}

func registeredPackageAliases(file *ast.File) ([]string, error) {
	var registerNodes *ast.FuncDecl

	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == "RegisterNodes" {
			registerNodes = function
			break
		}
	}

	if registerNodes == nil || registerNodes.Body == nil {
		return nil, fmt.Errorf(
			"%w: core/register.go has no RegisterNodes body",
			errInvalidRegistrySource,
		)
	}

	aliases := []string{}

	ast.Inspect(registerNodes.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}

		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != registerFunctionName {
			return true
		}

		identifier, ok := selector.X.(*ast.Ident)
		if ok {
			aliases = append(aliases, identifier.Name)
		}

		return true
	})

	if len(aliases) == 0 {
		return nil, fmt.Errorf(
			"%w: RegisterNodes registers no node packages",
			errInvalidRegistrySource,
		)
	}

	return aliases, nil
}

//nolint:gocyclo // The traversal deliberately recognizes only a narrow registry AST grammar.
func registeredKindNames(packageDir string) ([]string, error) {
	fset := token.NewFileSet()
	packages, err := parser.ParseDir(fset, packageDir, func(info fs.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		return nil, fmt.Errorf("%w: parsing package: %w", errInvalidRegistrySource, err)
	}

	if len(packages) != 1 {
		return nil, fmt.Errorf(
			"%w: expected one package in %s, got %d",
			errInvalidRegistrySource,
			packageDir,
			len(packages),
		)
	}

	var packageAST *ast.Package
	for _, parsedPackage := range packages {
		packageAST = parsedPackage
	}

	values := packageValues(packageAST)
	var registrations [][]string

	for _, file := range packageAST.Files {
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Name.Name != registerFunctionName || function.Body == nil {
				continue
			}

			ast.Inspect(function.Body, func(node ast.Node) bool {
				call, callOK := node.(*ast.CallExpr)
				if !callOK || len(call.Args) == 0 {
					return true
				}

				selector, selectorOK := call.Fun.(*ast.SelectorExpr)
				if !selectorOK || selector.Sel.Name != registerFunctionName {
					return true
				}

				names, evalErr := evalStringSlice(call.Args[0], values, map[string]bool{})
				if evalErr == nil {
					registrations = append(registrations, names)
				}

				return true
			})
		}
	}

	if len(registrations) != 1 {
		return nil, fmt.Errorf(
			"%w: expected one evaluable registry call in %s, got %d",
			errInvalidRegistrySource,
			packageDir,
			len(registrations),
		)
	}

	if len(registrations[0]) == 0 {
		return nil, fmt.Errorf(
			"%w: empty kind-name registration in %s",
			errInvalidRegistrySource,
			packageDir,
		)
	}

	return registrations[0], nil
}

func packageValues(parsedPackage *ast.Package) map[string]ast.Expr {
	values := map[string]ast.Expr{}

	for _, file := range parsedPackage.Files {
		for _, declaration := range file.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || (general.Tok != token.CONST && general.Tok != token.VAR) {
				continue
			}

			for _, rawSpec := range general.Specs {
				spec, specOK := rawSpec.(*ast.ValueSpec)
				if !specOK {
					continue
				}

				for index, name := range spec.Names {
					if index < len(spec.Values) {
						values[name.Name] = spec.Values[index]
					} else if len(spec.Values) == 1 {
						values[name.Name] = spec.Values[0]
					}
				}
			}
		}
	}

	return values
}

func evalStringSlice(
	expression ast.Expr,
	values map[string]ast.Expr,
	visiting map[string]bool,
) ([]string, error) {
	switch value := expression.(type) {
	case *ast.CompositeLit:
		result := make([]string, 0, len(value.Elts))
		for _, element := range value.Elts {
			text, err := evalString(element, values, visiting)
			if err != nil {
				return nil, err
			}
			result = append(result, text)
		}
		return result, nil
	case *ast.Ident:
		if visiting[value.Name] {
			return nil, fmt.Errorf("%w: cyclic identifier %q", errInvalidRegistrySource, value.Name)
		}
		resolved, ok := values[value.Name]
		if !ok {
			return nil, fmt.Errorf(
				"%w: unknown identifier %q",
				errInvalidRegistrySource,
				value.Name,
			)
		}
		visiting[value.Name] = true
		defer delete(visiting, value.Name)
		return evalStringSlice(resolved, values, visiting)
	default:
		return nil, fmt.Errorf(
			"%w: unsupported string-slice expression %T",
			errInvalidRegistrySource,
			expression,
		)
	}
}

func evalString(
	expression ast.Expr,
	values map[string]ast.Expr,
	visiting map[string]bool,
) (string, error) {
	switch value := expression.(type) {
	case *ast.BasicLit:
		if value.Kind != token.STRING {
			return "", fmt.Errorf(
				"%w: unsupported literal kind %s",
				errInvalidRegistrySource,
				value.Kind,
			)
		}
		return strconv.Unquote(value.Value)
	case *ast.Ident:
		if visiting[value.Name] {
			return "", fmt.Errorf("%w: cyclic identifier %q", errInvalidRegistrySource, value.Name)
		}
		resolved, ok := values[value.Name]
		if !ok {
			return "", fmt.Errorf("%w: unknown identifier %q", errInvalidRegistrySource, value.Name)
		}
		visiting[value.Name] = true
		defer delete(visiting, value.Name)
		return evalString(resolved, values, visiting)
	case *ast.BinaryExpr:
		if value.Op != token.ADD {
			return "", fmt.Errorf(
				"%w: unsupported string operator %s",
				errInvalidRegistrySource,
				value.Op,
			)
		}
		left, err := evalString(value.X, values, visiting)
		if err != nil {
			return "", err
		}
		right, err := evalString(value.Y, values, visiting)
		if err != nil {
			return "", err
		}
		return left + right, nil
	default:
		return "", fmt.Errorf(
			"%w: unsupported string expression %T",
			errInvalidRegistrySource,
			expression,
		)
	}
}

// RegistryDigest returns the digest of a normalized registry. Registration order and alias order
// do not affect it; canonical identity and source-package changes do.
func RegistryDigest(registrations []Registration) (string, error) {
	normalized := make([]Registration, len(registrations))

	for index, registration := range registrations {
		if len(registration.Names) == 0 {
			return "", fmt.Errorf(
				"%w: registration %d has no names",
				errInvalidRegistrySource,
				index,
			)
		}

		aliases := slices.Clone(registration.Names[1:])
		slices.Sort(aliases)

		normalized[index] = Registration{
			SourcePackage: registration.SourcePackage,
			Names:         append([]string{registration.Names[0]}, aliases...),
		}
	}

	slices.SortFunc(normalized, func(a, b Registration) int {
		return strings.Compare(a.Names[0], b.Names[0])
	})

	raw, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("marshaling normalized registry: %w", err)
	}

	digest := sha256.Sum256(raw)

	return "sha256:" + hex.EncodeToString(digest[:]), nil
}
