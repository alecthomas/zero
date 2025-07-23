// Package depgraph builds a Zero's dependeny injection type graph.
package depgraph

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"hash/fnv"
	"log"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/alecthomas/errors"
	"github.com/alecthomas/zero/internal/directiveparser"
	"golang.org/x/mod/modfile"
	"golang.org/x/tools/go/packages"
)

type Provider struct {
	// Position is the position of the function declaration.
	Position  token.Position
	Directive *directiveparser.DirectiveProvider
	// Function is the function that provides the type.
	Function *types.Func
	// Package is the package that contains the function.
	Package  *packages.Package
	Provides types.Type
	Requires []types.Type
}

// API represents a method that is an exposed API endpoint. API endpoints are annotated like so:
//
//	//zero:api [<method>] [<host>]/[<path>] [<option>[=<value>] ...]
type API struct {
	// Position is the position of the function declaration.
	Position token.Position
	// Pattern is the parsed HTTP mux pattern
	Pattern *directiveparser.DirectiveAPI
	// Function is the function that handles the API
	Function *types.Func
	// Package is the package that contains the function
	Package *packages.Package
	// Options is a map of options for the API endpoint
	Options map[string]string
}

type graphOptions struct {
	// Roots of the graph, defaulting to service endpoint receivers if nil.
	roots []string
	// Providers to pick to resolve duplicate providers.
	pick []string
	// Additional package patterns to search for annotations.
	patterns []string
	debug    bool
}

type Option func(*graphOptions) error

// WithRoots selects a set of root types for the graph.
func WithRoots(roots ...string) Option {
	return func(o *graphOptions) error {
		o.roots = roots
		return nil
	}
}

// WithProviders selects a provider for a type if multiple are available.
func WithProviders(pick ...string) Option {
	return func(o *graphOptions) error {
		o.pick = pick
		return nil
	}
}

// WithPatterns adds additional package patterns to search for annotations.
func WithPatterns(patterns ...string) Option {
	return func(o *graphOptions) error {
		o.patterns = patterns
		return nil
	}
}

// WithDebug enables debug logging.
func WithDebug(enable bool) Option {
	return func(o *graphOptions) error {
		o.debug = enable
		return nil
	}
}

func WithOptions(options ...Option) Option {
	return func(o *graphOptions) error {
		for _, opt := range options {
			err := opt(o)
			if err != nil {
				return errors.WithStack(err)
			}
		}
		return nil
	}
}

type Graph struct {
	Dest      *types.Package
	Providers map[string]*Provider
	Configs   map[string]types.Type
	APIs      []*API
	Missing   map[*types.Func][]types.Type
}

// Analyse statically loads Go packages, then analyses them for //zero:... annotations in order to build the
// Zero's dependency injection graph.
func Analyse(dest string, options ...Option) (*Graph, error) {
	graph := &Graph{
		Providers: make(map[string]*Provider),
		Configs:   make(map[string]types.Type),
		APIs:      make([]*API, 0),
		Missing:   make(map[*types.Func][]types.Type),
	}
	opts := &graphOptions{}
	for _, opt := range options {
		err := opt(opts)
		if err != nil {
			return nil, errors.WithStack(err)
		}
	}

	destImport, err := importPathForDir(dest)
	if err != nil {
		return nil, errors.Errorf("failed to determine import path for destination directory %s: %w", dest, err)
	}

	var logf func(string, ...any)
	if opts.debug {
		logf = log.Printf
	}

	cfg := &packages.Config{
		Logf: logf,
		Fset: fset,
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedImports | packages.NeedTypes | packages.NeedSyntax |
			packages.NeedTypesInfo,
	}
	opts.patterns = append(opts.patterns, "github.com/alecthomas/zero/providers/...")
	pkgs, err := packages.Load(cfg, append(opts.patterns, dest)...)
	if err != nil {
		return nil, errors.Errorf("failed to load packages: %w", err)
	}

	providers := map[string][]*Provider{}
	for _, pkg := range pkgs {
		if pkg.PkgPath == destImport {
			graph.Dest = pkg.Types
		}
		err := analysePackage(pkg, graph, providers)
		if err != nil {
			return nil, err
		}
	}
	if graph.Dest == nil {
		return nil, errors.Errorf("destination package %q not found", destImport)
	}

	// If no roots provided, use API receivers as roots
	if opts.roots == nil {
		opts.roots = make([]string, 0, len(graph.APIs))
		for _, api := range graph.APIs {
			if recv := api.Function.Signature().Recv(); recv != nil {
				receiverType := recv.Type()
				receiverTypeStr := types.TypeString(receiverType, nil)
				opts.roots = append(opts.roots, receiverTypeStr)
			}
		}
	}
	if err := pruneUnreferencedTypes(graph, opts.roots, providers, opts.pick); err != nil {
		return nil, errors.WithStack(err)
	}

	findMissingDependencies(graph)

	// Prune unreferenced providers and configs based on roots
	if len(opts.roots) == 0 {
		return nil, errors.Errorf("no root types provided and no API endpoints found")
	}

	return graph, nil
}

// TypeString splits a type into its import alias+path and type reference.
//
// eg. *database/sql.DB would become
//
//	impc112c3711fba7de3 "database/sql"
//	*sql.DB
func (g *Graph) TypeString(t types.Type) (imp, typ string) {
	typ = types.TypeString(t, types.RelativeTo(g.Dest))
	if parts := strings.Split(typ, "."); len(parts) > 1 {
		imp = strings.TrimPrefix(strings.Join(parts[:len(parts)-1], "."), "*")
	}
	pointer := strings.HasPrefix(typ, "*")
	if strings.Contains(typ, "/") {
		typ = path.Base(typ)
	}
	if imp != "" {
		alias := g.ImportAlias(imp)
		imp = fmt.Sprintf("%s %q", alias, imp)
		_, typ, _ = strings.Cut(typ, ".")
		typ = alias + "." + typ
		if pointer {
			typ = "*" + typ
		}
	}
	return
}

// ImportAlias returns an alias for the given package path, or "" if the package is the destination package.
func (g *Graph) ImportAlias(pkg string) string {
	if pkg == g.Dest.Path() {
		return ""
	}
	aliasID := fnv.New64a()
	aliasID.Write([]byte(pkg))
	return fmt.Sprintf("imp%x", aliasID.Sum64())
}

// Graph returns the dependency graph as a map where keys are type strings
// and values are slices of their dependency type strings.
func (g *Graph) Graph() map[string][]string {
	result := make(map[string][]string)

	// Add providers and their dependencies
	for typeStr, provider := range g.Providers {
		deps := make([]string, 0, len(provider.Requires))
		for _, reqType := range provider.Requires {
			depTypeStr := types.TypeString(reqType, types.RelativeTo(g.Dest))
			deps = append(deps, depTypeStr)
		}
		result[typeStr] = deps
	}

	// Add configs (they have no dependencies)
	for typeStr := range g.Configs {
		if _, exists := result[typeStr]; !exists {
			result[typeStr] = []string{}
		}
	}

	return result
}

var fset = token.NewFileSet()

// Parse a directive from a comment. Will return (nil, nil) if a directive is not found.
func parseDirective(doc *ast.CommentGroup) (directiveparser.Directive, error) {
	if doc == nil {
		return nil, nil
	}
	for _, comment := range doc.List {
		if strings.HasPrefix(comment.Text, "//zero:") {
			return directiveparser.Parse(comment.Text[2:])
		}
	}
	return nil, nil
}

func analysePackage(pkg *packages.Package, graph *Graph, providers map[string][]*Provider) error {
	for _, file := range pkg.Syntax {
		for _, decl := range file.Decls {
			switch decl := decl.(type) {
			case *ast.FuncDecl:
				directive, err := parseDirective(decl.Doc)
				if err != nil {
					return errors.Errorf("%s: %w", fset.Position(decl.Pos()), err)
				} else if directive == nil {
					continue
				}
				switch directive := directive.(type) {
				case *directiveparser.DirectiveProvider:
					provider, err := createProvider(decl, pkg, directive)
					if err != nil {
						return err
					}
					if provider != nil {
						key := types.TypeString(provider.Provides, nil)
						providers[key] = append(providers[key], provider)
					}

				case *directiveparser.DirectiveAPI:
					api, err := createAPI(decl, pkg, directive)
					if err != nil {
						return err
					}
					if api != nil {
						graph.APIs = append(graph.APIs, api)
					}
				}

			case *ast.GenDecl:
				directive, err := parseDirective(decl.Doc)
				if err != nil {
					return errors.Errorf("%s: %s", fset.Position(decl.Pos()), err)
				} else if directive == nil {
					continue
				}
				for _, spec := range decl.Specs {
					typeSpec, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					switch directive := directive.(type) {
					case *directiveparser.DirectiveConfig:
						configType := pkg.TypesInfo.TypeOf(typeSpec.Name)
						if configType != nil {
							key := types.TypeString(configType, nil)
							graph.Configs[key] = configType
						}

					default:
						return errors.Errorf("%s: %s: unknown directive type", fset.Position(typeSpec.Pos()), directive)
					}
				}
			}
		}
	}
	return nil
}

func createProvider(fn *ast.FuncDecl, pkg *packages.Package, directive *directiveparser.DirectiveProvider) (*Provider, error) {
	obj := pkg.TypesInfo.ObjectOf(fn.Name)
	if obj == nil {
		return nil, nil
	}

	funcObj, ok := obj.(*types.Func)
	if !ok {
		return nil, nil
	}

	sig := funcObj.Type().(*types.Signature)
	results := sig.Results()

	if results.Len() == 0 || results.Len() > 2 {
		return nil, errors.Errorf("provider function %s must return (T) or (T, error)", fn.Name.Name)
	}

	var providedType types.Type
	if results.Len() == 1 {
		providedType = results.At(0).Type()
	} else {
		providedType = results.At(0).Type()
		errorType := results.At(1).Type()
		if !isErrorType(errorType) {
			return nil, errors.Errorf("provider function %s second return value must be error", fn.Name.Name)
		}
	}

	params := sig.Params()
	requiredTypes := make([]types.Type, params.Len())
	for i := range params.Len() {
		requiredTypes[i] = params.At(i).Type()
	}

	return &Provider{
		Directive: directive,
		Function:  funcObj,
		Package:   pkg,
		Position:  fset.Position(fn.Pos()),
		Provides:  providedType,
		Requires:  requiredTypes,
	}, nil
}

func createAPI(fn *ast.FuncDecl, pkg *packages.Package, directive *directiveparser.DirectiveAPI) (*API, error) {
	// API annotations are only valid on methods (functions with receivers)
	if fn.Recv == nil {
		return nil, errors.Errorf("//zero:api annotation is only valid on methods, not functions: %s", fn.Name.Name)
	}

	obj := pkg.TypesInfo.ObjectOf(fn.Name)
	if obj == nil {
		return nil, errors.Errorf("failed to retrieve object for function %s", fn.Name.Name)
	}

	funcObj, ok := obj.(*types.Func)
	if !ok {
		return nil, nil
	}

	signature := funcObj.Signature()
	results := signature.Results()
	switch results.Len() {
	case 0, 1:
	case 2:
		secondResult := results.At(1).Type()
		if !isErrorType(secondResult) {
			return nil, errors.Errorf("function %s second return value must be error", fn.Name.Name)
		}
	default:
		return nil, errors.Errorf("function %s can only return one or two values", fn.Name.Name)
	}

	// Validate parameter types
	params := signature.Params()
	var bodyParamCount int
	for i := range params.Len() {
		param := params.At(i)
		paramType := param.Type()
		paramName := param.Name()

		if !isValidAPIParameterType(paramType, paramName, directive, &bodyParamCount) {
			return nil, errors.Errorf("invalid parameter type for API method %s: parameter %s of type %s is not allowed",
				fn.Name.Name, paramName, types.TypeString(paramType, nil))
		}
	}

	if bodyParamCount > 1 {
		return nil, errors.Errorf("API method %s can only have one struct parameter for request body/query parameters", fn.Name.Name)
	}

	return &API{
		Pattern:  directive,
		Function: funcObj,
		Package:  pkg,
		Position: fset.Position(fn.Pos()),
	}, nil
}

func isValidAPIParameterType(paramType types.Type, paramName string, directive *directiveparser.DirectiveAPI, bodyParamCount *int) bool {
	// Check if it's one of the allowed standard HTTP types
	if isStandardHTTPType(paramType) {
		return true
	}

	if isStringOrIntType(paramType) || implementsTextUnmarshaler(paramType) {
		return directive.Wildcard(paramName)
	}

	// Check if it's a struct type (for request body/query parameters)
	if isBodyParameterStruct(paramType) {
		*bodyParamCount++
		return true
	}

	return false
}

func isStandardHTTPType(t types.Type) bool {
	switch t := t.(type) {
	case *types.Pointer:
		// Check for *http.Request
		if named, ok := t.Elem().(*types.Named); ok {
			obj := named.Obj()
			return obj.Name() == "Request" && obj.Pkg() != nil && obj.Pkg().Path() == "net/http"
		}
	case *types.Named:
		obj := t.Obj()
		if obj.Pkg() == nil {
			return false
		}

		// Check for http.ResponseWriter
		if obj.Name() == "ResponseWriter" && obj.Pkg().Path() == "net/http" {
			return true
		}

		// Check for context.Context
		if obj.Name() == "Context" && obj.Pkg().Path() == "context" {
			return true
		}

		// Check for io.Reader
		if obj.Name() == "Reader" && obj.Pkg().Path() == "io" {
			return true
		}
	}

	return false
}

func isStringOrIntType(t types.Type) bool {
	if basic, ok := t.(*types.Basic); ok {
		return basic.Kind() == types.String ||
			basic.Kind() == types.Int ||
			basic.Kind() == types.Int8 ||
			basic.Kind() == types.Int16 ||
			basic.Kind() == types.Int32 ||
			basic.Kind() == types.Int64 ||
			basic.Kind() == types.Uint ||
			basic.Kind() == types.Uint8 ||
			basic.Kind() == types.Uint16 ||
			basic.Kind() == types.Uint32 ||
			basic.Kind() == types.Uint64
	}
	return false
}

func implementsTextUnmarshaler(t types.Type) bool {
	// Look for UnmarshalText method
	if hasMethod(t, "UnmarshalText") {
		return true
	}

	// Also check pointer type
	if ptr := types.NewPointer(t); hasMethod(ptr, "UnmarshalText") {
		return true
	}

	return false
}

func hasMethod(t types.Type, methodName string) bool {
	if named, ok := t.(*types.Named); ok {
		for i := range named.NumMethods() {
			method := named.Method(i)
			if method.Name() == methodName {
				// Check if it has the right signature: UnmarshalText([]byte) error
				sig := method.Type().(*types.Signature)
				if sig.Params().Len() == 1 && sig.Results().Len() == 1 {
					paramType := sig.Params().At(0).Type()
					resultType := sig.Results().At(0).Type()

					// Check if parameter is []byte
					if slice, ok := paramType.(*types.Slice); ok {
						if elem, ok := slice.Elem().(*types.Basic); ok && elem.Kind() == types.Byte {
							// Check if result is error
							if isErrorType(resultType) {
								return true
							}
						}
					}
				}
			}
		}
	}
	return false
}

func isBodyParameterStruct(t types.Type) bool {
	// Handle pointer to struct
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}

	// Check if it's a named struct type
	if named, ok := t.(*types.Named); ok {
		// Builtin type
		if _, ok := stdlib[named.Obj().Pkg().Path()]; ok {
			return false
		}
		if _, ok := named.Underlying().(*types.Struct); ok {
			return true
		}
	}

	// Check if it's an anonymous struct
	if _, ok := t.(*types.Struct); ok {
		return true
	}

	return false
}

func isErrorType(t types.Type) bool {
	named, ok := t.(*types.Named)
	if !ok {
		return false
	}
	return named.Obj().Name() == "error" && named.Obj().Pkg() == nil
}

func findMissingDependencies(graph *Graph) {
	provided := make(map[string]bool)
	for key := range graph.Providers {
		provided[key] = true
	}
	for key := range graph.Configs {
		provided[key] = true
	}

	for _, provider := range graph.Providers {
		for _, required := range provider.Requires {
			key := types.TypeString(required, nil)
			if !provided[key] && !isProvidedByConfig(required, graph.Configs) {
				// Check for duplicates before adding
				existing := graph.Missing[provider.Function]
				isDuplicate := false
				for _, existingType := range existing {
					if types.Identical(existingType, required) {
						isDuplicate = true
						break
					}
				}
				if !isDuplicate {
					graph.Missing[provider.Function] = append(graph.Missing[provider.Function], required)
				}
			}
		}
	}

	// Check API receiver types
	for _, api := range graph.APIs {
		sig := api.Function.Type().(*types.Signature)
		if sig.Recv() != nil {
			receiverType := sig.Recv().Type()
			key := types.TypeString(receiverType, nil)
			if !provided[key] && !isProvidedByConfig(receiverType, graph.Configs) {
				// Check for duplicates before adding
				existing := graph.Missing[api.Function]
				isDuplicate := false
				for _, existingType := range existing {
					if types.Identical(existingType, receiverType) {
						isDuplicate = true
						break
					}
				}
				if !isDuplicate {
					graph.Missing[api.Function] = append(graph.Missing[api.Function], receiverType)
				}
			}
		}
	}
}

func isProvidedByConfig(requiredType types.Type, configs map[string]types.Type) bool {
	// Check if the required type is directly provided as a config
	key := types.TypeString(requiredType, nil)
	if _, exists := configs[key]; exists {
		return true
	}

	// If required type is a pointer, check if the underlying type is a config
	if ptrType, ok := requiredType.(*types.Pointer); ok {
		underlyingKey := types.TypeString(ptrType.Elem(), nil)
		if _, exists := configs[underlyingKey]; exists {
			return true
		}
	}

	return false
}

func importPathForDir(dir string) (string, error) {
	if !modfile.IsDirectoryPath(dir) {
		return dir, nil
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return "", errors.Errorf("failed to get absolute path for directory %s: %w", dir, err)
	}
	dir = root
	// Search up directories for go.mod file
	for {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			break
		}
		if root == "/" {
			return "", errors.Errorf("couldn't find a go.mod file above %s", dir)
		}
		root = filepath.Dir(root)
	}
	dir, err = filepath.Rel(root, dir)
	if err != nil {
		return "", errors.Errorf("failed to get relative path for directory %s: %w", dir, err)
	}
	goModPath := filepath.Join(root, "go.mod")
	data, err := os.ReadFile(goModPath) //nolint
	if err != nil {
		return "", errors.Errorf("failed to read go.mod file at %s: %w", goModPath, err)
	}
	mod, err := modfile.Parse(goModPath, data, nil)
	if err != nil {
		return "", errors.Errorf("failed to parse go.mod file at %s: %w", goModPath, err)
	}
	return path.Join(mod.Module.Mod.Path, dir), nil
}

// pruneUnreferencedTypes removes providers and configs that are not transitively referenced from the given roots
func pruneUnreferencedTypes(graph *Graph, roots []string, providers map[string][]*Provider, pick []string) error {
	referenced := map[string]bool{}
	toProcess := slices.Clone(roots)
	ambiguousProviders := map[string][]*Provider{}

	// Transitive closure: find all referenced types
	for len(toProcess) > 0 {
		current := toProcess[0]
		toProcess = toProcess[1:]

		if referenced[current] {
			continue
		}
		referenced[current] = true

		// If this type has a provider, add its dependencies
		if providers, exists := providers[current]; exists {
			provider := pickProvider(providers, pick)
			if provider == nil {
				ambiguousProviders[current] = providers
			} else {
				graph.Providers[types.TypeString(provider.Provides, nil)] = provider
				for _, required := range provider.Requires {
					requiredKey := types.TypeString(required, nil)
					if !referenced[requiredKey] {
						toProcess = append(toProcess, requiredKey)
					}
				}
			}
		}
	}

	for key := range ambiguousProviders {
		if !referenced[key] {
			delete(ambiguousProviders, key)
		}
	}

	// Return an error for the first ambiguous provider
	for key, providers := range ambiguousProviders {
		providerKeys := make([]string, 0, len(providers))
		for _, provider := range providers {
			providerKey := provider.Function.FullName()
			providerKeys = append(providerKeys, providerKey)
		}
		return fmt.Errorf("ambiguous providers for type %s: %s", key, strings.Join(providerKeys, ", "))
	}

	// Remove unreferenced providers
	for key := range providers {
		if !referenced[key] {
			delete(providers, key)
		}
	}

	// Remove unreferenced configs
	for key := range graph.Configs {
		if !isConfigReferenced(key, referenced) {
			delete(graph.Configs, key)
		}
	}
	return nil
}

func isConfigReferenced(configKey string, referenced map[string]bool) bool {
	return referenced[configKey] || referenced["*"+configKey]
}

// Picks a single provider from a list of providers.
//
// Disambiguates through two mechanisms:
//
//  1. If there is only a single provider, it is chosen.
//  2. If a provider matches a specific pick, it is chosen.
//  3. If there is a single non-weak provider, it is chosen.
func pickProvider(providers []*Provider, pick []string) *Provider {
	if len(providers) == 1 {
		return providers[0]
	}
	var strong []*Provider
	for _, provider := range providers {
		if !provider.Directive.Weak {
			strong = append(strong, provider)
		}
		key := provider.Function.FullName()
		for _, pick := range pick {
			if key == pick {
				return provider
			}
		}
	}
	if len(strong) == 1 {
		return strong[0]
	}
	return nil
}
