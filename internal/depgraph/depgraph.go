// Package depgraph builds a Zero's dependeny injection type graph.
package depgraph

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"hash/fnv"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/alecthomas/errors"
	"github.com/alecthomas/zero/internal/directiveparser"
	"github.com/alecthomas/zero/internal/strcase"
	"github.com/go-openapi/spec"
	"golang.org/x/mod/modfile"
	"golang.org/x/tools/go/packages"
)

// Ref represents a reference to a symbol.
type Ref struct {
	Pkg    string // database/sql
	Import string // "database/sql" or impe1d11ad6baa4124f "database/sql"
	Ref    string // *sql.DB or *impe1d11ad6baa4124f.DB
}

// String is the fully-qualified type reference, eg. *database/sql.DB
func (r Ref) String() string {
	ref := r.Ref
	if i := strings.LastIndex(ref, "."); i != -1 {
		ref = ref[i+1:]
	}
	ref = strings.TrimPrefix(ref, "*")
	if strings.HasPrefix(r.Ref, "*") {
		return "*" + r.Pkg + "." + ref
	}
	return r.Pkg + "." + ref
}

type graphOptions struct {
	require []Key
	// Additional package patterns to search for annotations.
	patterns   []string
	debug      bool
	buildFlags []string
}

type Option func(*graphOptions) error

func WithTypes(key ...TypeKey) Option {
	return func(o *graphOptions) error {
		for _, k := range key {
			o.require = append(o.require, k)
		}
		return nil
	}
}

func WithNodes(key ...NodeKey) Option {
	return func(o *graphOptions) error {
		for _, k := range key {
			o.require = append(o.require, k)
		}
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

// WithTags adds build tags to the Go toolchain flags.
func WithTags(tags ...string) Option {
	return func(o *graphOptions) error {
		o.buildFlags = append(o.buildFlags, "-tags="+strings.Join(tags, ","))
		return nil
	}
}

type Graph struct {
	Dest           *types.Package
	Providers      map[string][]*Provider // All providers including multi and generic
	Configs        map[string]*Config
	GenericConfigs map[string][]*Config // Generic configs by base type name
	APIs           []*API
	CronJobs       []*CronJob
	Subscriptions  []*Subscription
	Middleware     []*Middleware
	Missing        map[*types.Func][]types.Type
}

// Analyse statically loads Go packages, then analyses them for //zero:... annotations in order to build the
// Zero's dependency injection graph.
func Analyse(ctx context.Context, dest string, options ...Option) (*Graph, error) {
	var nodes []Node
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

	// Create a new FileSet for this analysis to avoid race conditions
	fileset := token.NewFileSet()
	cfg := &packages.Config{
		Logf:       logf,
		Fset:       fileset,
		BuildFlags: opts.buildFlags,
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedImports | packages.NeedTypes | packages.NeedSyntax |
			packages.NeedTypesInfo,
	}

	// If dest is an absolute path, set Dir to tell packages.Load which directory to use
	var destPattern string
	if filepath.IsAbs(dest) {
		cfg.Dir = dest
		destPattern = "."
	} else {
		destPattern = dest
	}
	opts.patterns = append(opts.patterns, "github.com/alecthomas/zero/providers/...")
	pkgs, err := packages.Load(cfg, append(opts.patterns, destPattern)...)
	if err != nil {
		return nil, errors.Errorf("failed to load packages: %w", err)
	}
	// No error and no packages returned because "go mod tidy" needs to be run...super annoying.
	// We'll run it and see if that fixes it.
	if len(pkgs) == 0 {
		cmd := exec.CommandContext(ctx, "go", "mod", "-C", dest, "tidy")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return nil, errors.Errorf("failed to run 'go mod -C %q tidy': %w", dest, err)
		}
		pkgs, err = packages.Load(cfg, append(opts.patterns, destPattern)...)
		if err != nil {
			return nil, errors.Errorf("failed to load packages: %w", err)
		}
		if len(pkgs) == 0 {
			return nil, errors.Errorf("failed to load any packages, try running 'go list -C %q' and checking for errors", dest)
		}
	}

	var destPkg *packages.Package
	for _, pkg := range pkgs {
		if opts.debug {
			for _, err := range pkg.Errors {
				log.Println(err)
			}
			for _, err := range pkg.TypeErrors {
				log.Println(err)
			}
		}
		if pkg.PkgPath == destImport {
			destPkg = pkg
		}
		pkgNodes, err := analysePackage(pkg, fileset)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, pkgNodes...)
	}
	if destPkg == nil {
		return nil, errors.Errorf("destination package %q not found", destImport)
	}

	// Create IR
	ir, err := NewIR(nodes, options...)
	if err != nil {
		return nil, err
	}

	// Build Graph from IR
	graph := &Graph{
		Dest:           destPkg.Types,
		Providers:      make(map[string][]*Provider),
		Configs:        make(map[string]*Config),
		GenericConfigs: make(map[string][]*Config),
		APIs:           make([]*API, 0),
		CronJobs:       make([]*CronJob, 0),
		Middleware:     make([]*Middleware, 0),
		Subscriptions:  make([]*Subscription, 0),
		Missing:        make(map[*types.Func][]types.Type),
	}

	// Populate from required nodes
	for node := range ir.RequiredNodes() {
		switch n := node.(type) {
		case *Provider:
			key := types.TypeString(n.Provides, nil)
			graph.Providers[key] = append(graph.Providers[key], n)
		case *Config:
			key := types.TypeString(n.Type, nil)
			graph.Configs[key] = n
		case *API:
			graph.APIs = append(graph.APIs, n)
		case *CronJob:
			graph.CronJobs = append(graph.CronJobs, n)
		case *Middleware:
			graph.Middleware = append(graph.Middleware, n)
		case *Subscription:
			graph.Subscriptions = append(graph.Subscriptions, n)
		}
	}

	return graph, nil
}

// ParseTypeRef parses a type reference string into a Ref.
//
// A type reference string is in the form [*]<pkg>.<type>, eg. *net/http.ServeMux
func (g *Graph) ParseTypeRef(ref string) Ref {
	ptr := strings.HasPrefix(ref, "*")
	cut := strings.LastIndex(ref, ".")
	if cut == -1 {
		panic(fmt.Sprintf("invalid type reference: %s", ref))
	}
	pkg := strings.TrimPrefix(ref[:cut], "*")
	typ := ref[cut+1:]
	alias := g.ImportAlias(pkg)
	if pkg == g.Dest.Path() {
		if ptr {
			typ = "*" + typ
		}
		return Ref{
			Ref: typ,
		}
	}
	imp := pkg
	if alias != "" {
		imp = fmt.Sprintf("%s %q", alias, imp)
		typ = alias + "." + typ
	} else {
		typ = path.Base(pkg) + "." + typ
	}
	if ptr {
		typ = "*" + typ
	}
	return Ref{
		Pkg:    pkg,
		Import: imp,
		Ref:    typ,
	}
}

// TypeRef splits a type into its import alias+path and type reference.
//
// eg. *database/sql.DB would become
//
//	impc112c3711fba7de3 "database/sql"
//	*sql.DB
func (g *Graph) TypeRef(t types.Type) Ref {
	// Handle pointer types
	pointer := false
	if ptr, ok := t.(*types.Pointer); ok {
		pointer = true
		t = ptr.Elem()
	}

	var pkg, typeName string
	var imp, ref string

	// Extract package and type name directly from the type
	if named, ok := t.(*types.Named); ok {
		if named.Obj().Pkg() != nil {
			pkg = named.Obj().Pkg().Path()
			typeName = named.Obj().Name()

			// Handle generic types with type arguments
			if typeArgs := named.TypeArgs(); typeArgs != nil && typeArgs.Len() > 0 {
				typeName += "["
				for i := range typeArgs.Len() {
					argType := typeArgs.At(i)
					// Use types.TypeString for type arguments to avoid recursion
					argString := types.TypeString(argType, types.RelativeTo(g.Dest))
					typeName += argString
					if i < typeArgs.Len()-1 {
						typeName += ", "
					}
				}
				typeName += "]"
			}
		} else {
			// Built-in type
			typeName = named.Obj().Name()
		}
	} else {
		// For non-named types, fall back to string representation
		typ := types.TypeString(t, types.RelativeTo(g.Dest))
		typeName = typ
	}

	if pkg != "" {
		alias := g.ImportAlias(pkg)
		if alias != "" {
			imp = fmt.Sprintf("%s %q", alias, pkg)
			ref = alias + "." + typeName
		} else {
			// Standard library or same package
			if pkg == g.Dest.Path() {
				ref = typeName
			} else {
				// Standard library package - need to import it
				imp = fmt.Sprintf("%q", pkg)
				pkgName := path.Base(pkg)
				ref = pkgName + "." + typeName
			}
		}
	} else {
		ref = typeName
	}

	if pointer {
		ref = "*" + ref
	}

	return Ref{
		Pkg:    pkg,
		Import: imp,
		Ref:    ref,
	}
}

// FunctionRef returns a reference to a function, including import information if needed.
func (g *Graph) FunctionRef(fn *types.Func) Ref {
	name := fn.Name()
	pkg := fn.Pkg().Path()

	var imp, ref string
	if alias := g.ImportAlias(pkg); alias != "" {
		imp = fmt.Sprintf("%s %q", alias, pkg)
		ref = alias + "." + name
	} else {
		ref = name
	}

	return Ref{
		Pkg:    pkg,
		Import: imp,
		Ref:    ref,
	}
}

// ImportAlias returns an alias for the given package path, or "" if the package is the destination package.
func (g *Graph) ImportAlias(pkg string) string {
	if pkg == g.Dest.Path() {
		return ""
	}
	if _, isStdlib := stdlib[pkg]; isStdlib {
		return ""
	}
	aliasID := fnv.New64a()
	aliasID.Write([]byte(pkg))
	return fmt.Sprintf("imp%x", aliasID.Sum64())
}

// GetProviders returns all providers for a given type (both single and multi).
func (g *Graph) GetProviders(typeStr string) []*Provider {
	if providers, exists := g.Providers[typeStr]; exists {
		return providers
	}

	// Check generic providers by base type
	baseType := getBaseTypeNameFromString(typeStr)
	if genericProviders, exists := g.Providers[baseType]; exists {
		return genericProviders
	}

	return nil
}

// Graph returns the dependency graph as a map where keys are type strings
// and values are slices of their dependency type strings.
func (g *Graph) Graph() map[string][]string {
	result := make(map[string][]string)

	// Add all providers and their dependencies
	for typeStr, providers := range g.Providers {
		deps := make([]string, 0)
		for _, provider := range providers {
			for _, reqType := range provider.Requires() {
				depTypeStr := types.TypeString(reqType, types.RelativeTo(g.Dest))
				deps = append(deps, depTypeStr)
			}
		}
		result[typeStr] = deps
	}

	// Add configs (they have no dependencies)
	for typeStr := range g.Configs {
		if _, exists := result[typeStr]; !exists {
			result[typeStr] = []string{}
		}
	}

	// Add generic configs
	for baseType := range g.GenericConfigs {
		key := baseType + "[T]"
		if _, exists := result[key]; !exists {
			result[key] = []string{}
		}
	}

	return result
}

// GenerateOpenAPISpec creates a complete OpenAPI specification from all API endpoints
func (g *Graph) GenerateOpenAPISpec(title, version string) *spec.Swagger {
	swagger := &spec.Swagger{
		SwaggerProps: spec.SwaggerProps{
			Swagger: "2.0",
			Info: &spec.Info{
				InfoProps: spec.InfoProps{
					Title:   title,
					Version: version,
				},
			},
			Paths: &spec.Paths{
				Paths: make(map[string]spec.PathItem),
			},
			Definitions: make(spec.Definitions),
		},
	}

	// Group APIs by path and generate operations with shared definitions
	pathOperations := make(map[string]map[string]*spec.Operation)

	for _, api := range g.APIs {
		if api.Pattern == nil {
			continue
		}

		path := api.Pattern.Path()
		method := strings.ToLower(api.Pattern.Method)
		if method == "" {
			method = "get"
		}

		if pathOperations[path] == nil {
			pathOperations[path] = make(map[string]*spec.Operation)
		}

		// Generate operation with shared definitions
		operation := api.GenerateOpenAPIOperation(swagger.Definitions)
		pathOperations[path][method] = operation
	}

	// Convert to PathItems
	for path, operations := range pathOperations {
		pathItem := spec.PathItem{}

		if op, exists := operations["get"]; exists {
			pathItem.Get = op
		}
		if op, exists := operations["post"]; exists {
			pathItem.Post = op
		}
		if op, exists := operations["put"]; exists {
			pathItem.Put = op
		}
		if op, exists := operations["patch"]; exists {
			pathItem.Patch = op
		}
		if op, exists := operations["delete"]; exists {
			pathItem.Delete = op
		}
		if op, exists := operations["head"]; exists {
			pathItem.Head = op
		}
		if op, exists := operations["options"]; exists {
			pathItem.Options = op
		}

		swagger.Paths.Paths[path] = pathItem
	}

	return swagger
}

// Parse a directive from a comment. Will return (nil, nil) if a directive is not found.
func parseDirective(doc *ast.CommentGroup) (directiveparser.Directive, error) {
	if doc == nil {
		return nil, nil
	}
	for _, comment := range doc.List {
		if strings.HasPrefix(comment.Text, "//zero:") {
			return errors.WithStack2(directiveparser.Parse(comment.Text[2:]))
		}
	}
	return nil, nil
}

func analysePackage(pkg *packages.Package, fset *token.FileSet) ([]Node, error) {
	var nodes []Node
	for _, file := range pkg.Syntax {
		for _, decl := range file.Decls {
			switch decl := decl.(type) {
			case *ast.FuncDecl:
				directive, err := parseDirective(decl.Doc)
				if err != nil {
					return nil, errors.Errorf("%s: %w", fset.Position(decl.Pos()), err)
				} else if directive == nil {
					continue
				}
				switch directive := directive.(type) {
				case *directiveparser.DirectiveProvider:
					provider, err := createProvider(decl, pkg, directive, fset)
					if err != nil {
						return nil, err
					}
					if provider != nil {
						nodes = append(nodes, provider)
					}

				case *directiveparser.DirectiveAPI:
					api, err := createAPI(decl, pkg, directive, fset)
					if err != nil {
						return nil, err
					}
					if api != nil {
						nodes = append(nodes, api)
					}

				case *directiveparser.DirectiveCron:
					cron, err := createCron(decl, pkg, directive, fset)
					if err != nil {
						return nil, err
					}
					if cron != nil {
						nodes = append(nodes, cron)
					}

				case *directiveparser.DirectiveMiddleware:
					middleware, err := createMiddleware(decl, pkg, directive, fset)
					if err != nil {
						return nil, err
					}
					if middleware != nil {
						nodes = append(nodes, middleware)
					}

				case *directiveparser.DirectiveSubscribe:
					subscription, err := createSubscription(decl, pkg, fset)
					if err != nil {
						return nil, err
					}
					if subscription != nil {
						nodes = append(nodes, subscription)
					}
				}

			case *ast.GenDecl:
				directive, err := parseDirective(decl.Doc)
				if err != nil {
					return nil, errors.Errorf("%s: %s", fset.Position(decl.Pos()), err)
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
						configType, ok := pkg.TypesInfo.TypeOf(typeSpec.Name).(*types.Named)
						if !ok {
							return nil, errors.Errorf("%s: //zero:config must be applied to a named struct", fset.Position(typeSpec.Pos()))
						}

						// Check if this is a generic config
						var typeParams *types.TypeParamList
						var isGeneric bool
						typeParams = configType.TypeParams()
						isGeneric = typeParams != nil && typeParams.Len() > 0

						config := &Config{
							Position:   fset.Position(typeSpec.Pos()),
							Package:    pkg,
							Type:       configType,
							Directive:  directive,
							IsGeneric:  isGeneric,
							TypeParams: typeParams,
						}

						nodes = append(nodes, config)

					default:
						return nil, errors.Errorf("%s: %s: unknown directive type", fset.Position(typeSpec.Pos()), directive)
					}
				}
			}
		}
	}
	return nodes, nil
}

func createProvider(fn *ast.FuncDecl, pkg *packages.Package, directive *directiveparser.DirectiveProvider, fset *token.FileSet) (*Provider, error) {
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

	// Check if this is a generic function
	typeParams := sig.TypeParams()
	isGeneric := typeParams != nil && typeParams.Len() > 0

	return &Provider{
		Directive:  directive,
		Function:   funcObj,
		Package:    pkg,
		Position:   fset.Position(fn.Pos()),
		Provides:   providedType,
		IsGeneric:  isGeneric,
		TypeParams: typeParams,
	}, nil
}

func createAPI(fn *ast.FuncDecl, pkg *packages.Package, directive *directiveparser.DirectiveAPI, fset *token.FileSet) (*API, error) {
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

	// Check if receiver is a config type - configs cannot have API methods
	if sig := signature; sig.Recv() != nil {
		receiverType := sig.Recv().Type()
		if isConfigType(receiverType, pkg) {
			return nil, errors.Errorf("//zero:api annotation cannot be used on config types: %s", fn.Name.Name)
		}
	}

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

	// Extract documentation from function comments
	var documentation string
	if fn.Doc != nil {
		documentation = strings.TrimSpace(fn.Doc.Text())
	}

	api := &API{
		Pattern:       directive,
		Function:      funcObj,
		Documentation: documentation,
		Package:       pkg,
		Position:      fset.Position(fn.Pos()),
	}

	// Generate OpenAPI operation spec
	// OpenAPI operation will be generated during spec generation with shared definitions

	return api, nil
}

func createCron(fn *ast.FuncDecl, pkg *packages.Package, directive *directiveparser.DirectiveCron, fset *token.FileSet) (*CronJob, error) {
	// Cron annotations are only valid on methods (functions with receivers)
	if fn.Recv == nil {
		return nil, errors.Errorf("//zero:cron annotation is only valid on methods, not functions: %s", fn.Name.Name)
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

	// Check if receiver is a config type - configs cannot have cron methods
	if sig := signature; sig.Recv() != nil {
		receiverType := sig.Recv().Type()
		if isConfigType(receiverType, pkg) {
			return nil, errors.Errorf("//zero:cron annotation cannot be used on config types: %s", fn.Name.Name)
		}
	}

	// Validate exact signature: Cron(context.Context) error
	params := signature.Params()
	if params.Len() != 1 {
		return nil, errors.Errorf("cron method %s must have exactly one parameter of type context.Context", fn.Name.Name)
	}

	// Check first parameter is context.Context
	param := params.At(0)
	paramType := param.Type()
	if !isContextType(paramType) {
		return nil, errors.Errorf("cron method %s first parameter must be context.Context, got %s", fn.Name.Name, types.TypeString(paramType, nil))
	}

	// Validate return type is error
	results := signature.Results()
	if results.Len() != 1 {
		return nil, errors.Errorf("cron method %s must return exactly one value of type error", fn.Name.Name)
	}

	returnType := results.At(0).Type()
	if !isErrorType(returnType) {
		return nil, errors.Errorf("cron method %s must return error, got %s", fn.Name.Name, types.TypeString(returnType, nil))
	}

	return &CronJob{
		Schedule: directive,
		Function: funcObj,
		Package:  pkg,
		Position: fset.Position(fn.Pos()),
	}, nil
}

func createSubscription(fn *ast.FuncDecl, pkg *packages.Package, fset *token.FileSet) (*Subscription, error) {
	// Subscription annotations are only valid on methods (functions with receivers)
	if fn.Recv == nil {
		return nil, errors.Errorf("//zero:subscribe annotation is only valid on methods, not functions: %s", fn.Name.Name)
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

	// Check if receiver is a config type - configs cannot have subscription methods
	if sig := signature; sig.Recv() != nil {
		receiverType := sig.Recv().Type()
		if isConfigType(receiverType, pkg) {
			return nil, errors.Errorf("//zero:subscribe annotation cannot be used on config types: %s", fn.Name.Name)
		}
	}

	// Validate exact signature: Method(context.Context, pubsub.Event[T]) error
	params := signature.Params()
	if params.Len() != 2 {
		return nil, errors.Errorf("subscription method %s must have exactly two parameters: context.Context and pubsub.Event[T]", fn.Name.Name)
	}

	// Check first parameter is context.Context
	param := params.At(0)
	paramType := param.Type()
	if !isContextType(paramType) {
		return nil, errors.Errorf("subscription method %s first parameter must be context.Context, got %s", fn.Name.Name, types.TypeString(paramType, nil))
	}

	// Check second parameter is pubsub.Event[T]
	eventParam := params.At(1)
	eventType := eventParam.Type()

	// Extract the event type from pubsub.Event[T]
	payloadType, err := extractEventPayloadType(eventType)
	if err != nil {
		return nil, errors.Errorf("subscription method %s second parameter must be pubsub.Event[T], got %s: %v", fn.Name.Name, types.TypeString(eventType, nil), err)
	}

	// Validate return type is error
	results := signature.Results()
	if results.Len() != 1 {
		return nil, errors.Errorf("subscription method %s must return exactly one value of type error", fn.Name.Name)
	}

	returnType := results.At(0).Type()
	if !isErrorType(returnType) {
		return nil, errors.Errorf("subscription method %s must return error, got %s", fn.Name.Name, types.TypeString(returnType, nil))
	}

	return &Subscription{
		Function:  funcObj,
		Package:   pkg,
		Position:  fset.Position(fn.Pos()),
		TopicType: payloadType,
	}, nil
}

func extractEventPayloadType(eventType types.Type) (types.Type, error) {
	// Remove pointer if present
	if ptr, ok := eventType.(*types.Pointer); ok {
		eventType = ptr.Elem()
	}

	// Check if it's a named type
	named, ok := eventType.(*types.Named)
	if !ok {
		return nil, errors.Errorf("expected named type, got %T", eventType)
	}

	// Check if the type is from the pubsub package and named "Event"
	obj := named.Obj()
	if obj == nil || obj.Pkg() == nil {
		return nil, errors.Errorf("type has no package information")
	}

	if obj.Pkg().Path() != "github.com/alecthomas/zero/providers/pubsub" || obj.Name() != "Event" {
		return nil, errors.Errorf("expected pubsub.Event, got %s.%s", obj.Pkg().Path(), obj.Name())
	}

	// Check if it has type arguments (is generic instantiation)
	typeArgs := named.TypeArgs()
	if typeArgs == nil || typeArgs.Len() != 1 {
		return nil, errors.Errorf("pubsub.Event must have exactly one type argument")
	}

	return typeArgs.At(0), nil
}

func createMiddleware(fn *ast.FuncDecl, pkg *packages.Package, directive *directiveparser.DirectiveMiddleware, fset *token.FileSet) (*Middleware, error) {
	obj := pkg.TypesInfo.ObjectOf(fn.Name)
	if obj == nil {
		return nil, errors.Errorf("failed to retrieve object for function %s", fn.Name.Name)
	}

	funcObj, ok := obj.(*types.Func)
	if !ok {
		return nil, nil
	}

	signature := funcObj.Signature()

	// Validate middleware function signature
	// Middleware should be either:
	// 1. func(http.Handler) http.Handler - direct middleware
	// 2. func(...deps) func(http.Handler) http.Handler - middleware factory
	// 3. func(...deps) zero.Middleware - middleware factory returning zero.Middleware type

	if !isValidMiddlewareSignature(signature) {
		return nil, errors.Errorf("invalid middleware function signature for %s: must be func(http.Handler) http.Handler or func(...deps) func(http.Handler) http.Handler", fn.Name.Name)
	}

	// Analyze dependencies for middleware factory functions
	var requires []types.Type
	params := signature.Params()

	// Check if this is a middleware factory (not a direct middleware)
	if !isDirectMiddleware(signature) {
		labelNames := make(map[string]bool)
		for _, label := range directive.Labels {
			labelNames[label] = true
		}

		for i := range params.Len() {
			param := params.At(i)
			paramType := param.Type()
			paramName := param.Name()

			// String/int parameters must be labels
			if isStringOrIntType(paramType) {
				if !labelNames[paramName] {
					return nil, errors.Errorf("parameter %s of type %s in middleware %s must match a label name", paramName, paramType.String(), fn.Name.Name)
				}
			} else {
				// Non-string/int parameters are dependencies
				requires = append(requires, paramType)
			}
		}
	}

	middleware := &Middleware{
		Position:  fset.Position(fn.Pos()),
		Directive: directive,
		Function:  funcObj,
		Package:   pkg,
		Requires:  requires,
		Factory:   !isDirectMiddleware(signature),
	}

	return middleware, nil
}

func isValidMiddlewareSignature(sig *types.Signature) bool {
	results := sig.Results()

	// Must return exactly one value
	if results.Len() != 1 {
		return false
	}

	returnType := results.At(0).Type()

	// Check if it returns http.Handler (direct middleware)
	if isHTTPHandlerType(returnType) {
		params := sig.Params()
		// Must take exactly one parameter of type http.Handler
		return params.Len() == 1 && isHTTPHandlerType(params.At(0).Type())
	}

	// Check if it returns a function that returns http.Handler (middleware factory)
	if funcSig, ok := returnType.(*types.Signature); ok {
		funcResults := funcSig.Results()
		if funcResults.Len() == 1 && isHTTPHandlerType(funcResults.At(0).Type()) {
			funcParams := funcSig.Params()
			// The returned function must take exactly one http.Handler parameter
			return funcParams.Len() == 1 && isHTTPHandlerType(funcParams.At(0).Type())
		}
	}

	// Check if it returns zero.Middleware type (if such a type exists)
	if named, ok := returnType.(*types.Named); ok {
		obj := named.Obj()
		if obj.Name() == "Middleware" && obj.Pkg() != nil && obj.Pkg().Path() == "github.com/alecthomas/zero" {
			return true
		}
	}

	return false
}

func isDirectMiddleware(sig *types.Signature) bool {
	results := sig.Results()
	if results.Len() != 1 {
		return false
	}

	returnType := results.At(0).Type()
	if isHTTPHandlerType(returnType) {
		params := sig.Params()
		return params.Len() == 1 && isHTTPHandlerType(params.At(0).Type())
	}

	return false
}

func isHTTPHandlerType(t types.Type) bool {
	if named, ok := t.(*types.Named); ok {
		obj := named.Obj()
		return obj.Name() == "Handler" && obj.Pkg() != nil && obj.Pkg().Path() == "net/http"
	}
	return false
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

func isContextType(t types.Type) bool {
	named, ok := t.(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	return obj.Name() == "Context" && obj.Pkg() != nil && obj.Pkg().Path() == "context"
}

// isConfigType checks if a receiver type is a config type by looking for the //zero:config directive
func isConfigType(receiverType types.Type, pkg *packages.Package) bool {
	// Remove pointer indirection
	for {
		if ptr, ok := receiverType.(*types.Pointer); ok {
			receiverType = ptr.Elem()
		} else {
			break
		}
	}

	// Check if it's a named type
	named, ok := receiverType.(*types.Named)
	if !ok {
		return false
	}

	// Get the type name
	typeName := named.Obj().Name()

	// Look through the package's AST to find type declarations with //zero:config
	for _, file := range pkg.Syntax {
		for _, decl := range file.Decls {
			if genDecl, ok := decl.(*ast.GenDecl); ok && genDecl.Tok == token.TYPE {
				for _, spec := range genDecl.Specs {
					if typeSpec, ok := spec.(*ast.TypeSpec); ok && typeSpec.Name.Name == typeName {
						// Check if this type has a //zero:config directive
						if genDecl.Doc != nil {
							for _, comment := range genDecl.Doc.List {
								if strings.HasPrefix(comment.Text, "//zero:config") {
									return true
								}
							}
						}
					}
				}
			}
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

// getBaseTypeName extracts the base type name from a type, handling generic types.
// For example, "Topic[T]" becomes "Topic", "*Service" becomes "*Service"
func getBaseTypeName(t types.Type) string {
	// Handle pointer types
	if ptr, ok := t.(*types.Pointer); ok {
		return "*" + getBaseTypeName(ptr.Elem())
	}

	// Handle named types (including generic instances)
	if named, ok := t.(*types.Named); ok {
		if named.Obj().Pkg() != nil {
			return named.Obj().Pkg().Path() + "." + named.Obj().Name()
		}
		return named.Obj().Name()
	}

	// For other types, fall back to string representation
	return types.TypeString(t, nil)
}

// getBaseTypeNameFromString extracts base type name from a type string
// For example, "pkg.Topic[User]" becomes "pkg.Topic"
func getBaseTypeNameFromString(typeStr string) string {
	// Find the first '[' to strip generic type arguments
	if idx := strings.Index(typeStr, "["); idx != -1 {
		return typeStr[:idx]
	}
	return typeStr
}

// toKebabCase converts a type name to kebab-case using strcase.
// For example: "MyService" -> "my-service", "HTTPClient" -> "http-client"
func toKebabCase(typeName string) string {
	parts := strcase.Split(typeName)
	for i, part := range parts {
		parts[i] = strings.ToLower(part)
	}
	return strings.Join(parts, "-")
}
