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
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"slices"
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

func (r Ref) String() string {
	return fmt.Sprintf("%s.%s", r.Pkg, strings.TrimPrefix(r.Ref, "*"))
}

// A Provider represents a constructor for a type.
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
	// IsGeneric indicates if this provider is a generic function
	IsGeneric bool
	// TypeParams holds the type parameters for generic providers
	TypeParams *types.TypeParamList
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
	// Documentation is the extracted function comments
	Documentation string
	// Package is the package that contains the function
	Package *packages.Package
	// OpenAPI is the OpenAPI operation spec for this endpoint
	OpenAPI *spec.Operation
}

func (a *API) Label(name string) string {
	for _, label := range a.Pattern.Labels {
		if label.Name == name {
			return label.Value
		}
	}
	return ""
}

// GenerateOpenAPIOperation creates an OpenAPI operation spec for this API endpoint
func (a *API) GenerateOpenAPIOperation(definitions spec.Definitions) *spec.Operation {
	operation := &spec.Operation{
		OperationProps: spec.OperationProps{
			Description: a.extractDocumentation(),
			Parameters:  a.generateParameters(definitions),
			Responses:   a.generateResponses(definitions),
			Tags:        []string{a.extractTag()},
		},
	}
	return operation
}

func (a *API) extractDocumentation() string {
	if a.Documentation != "" {
		return a.Documentation
	}
	return ""
}

func (a *API) extractTag() string {
	// Extract tag from package name or directive labels
	if tag := a.Label("tag"); tag != "" {
		return tag
	}
	return a.Package.Name
}

func (a *API) generateParameters(definitions spec.Definitions) []spec.Parameter {
	var parameters []spec.Parameter
	signature := a.Function.Signature()
	params := signature.Params()

	for i := range params.Len() {
		param := params.At(i)
		paramType := param.Type()
		paramName := param.Name()

		// Skip context parameters
		if isContextType(paramType) {
			continue
		}

		// Handle different parameter types
		if isStandardHTTPType(paramType) {
			continue // Skip standard HTTP types
		}

		if isBodyParameterStruct(paramType) {
			// Body parameter
			schema := a.generateSchemaFromType(paramType, definitions)
			parameters = append(parameters, spec.Parameter{
				ParamProps: spec.ParamProps{
					Name:     "body",
					In:       "body",
					Required: true,
					Schema:   schema,
				},
			})
		} else if isStringOrIntType(paramType) {
			// Path or query parameter
			parameterType := "string"
			if strings.Contains(paramType.String(), "int") {
				parameterType = "integer"
			}

			// Determine if it's a path parameter from the pattern
			inType := "query"
			if a.isPathParameter(paramName) {
				inType = "path"
			}

			parameters = append(parameters, spec.Parameter{
				ParamProps: spec.ParamProps{
					Name:     paramName,
					In:       inType,
					Required: inType == "path",
				},
				SimpleSchema: spec.SimpleSchema{
					Type: parameterType,
				},
			})
		}
	}

	return parameters
}

func (a *API) generateResponses(definitions spec.Definitions) *spec.Responses {
	responses := &spec.Responses{
		ResponsesProps: spec.ResponsesProps{
			StatusCodeResponses: make(map[int]spec.Response),
		},
	}

	signature := a.Function.Signature()
	results := signature.Results()

	if results.Len() == 0 {
		// No return value - 204 No Content
		responses.StatusCodeResponses[204] = spec.Response{
			ResponseProps: spec.ResponseProps{
				Description: "No Content",
			},
		}
	} else if results.Len() == 1 && isErrorType(results.At(0).Type()) {
		// Only error return - 204 No Content
		responses.StatusCodeResponses[204] = spec.Response{
			ResponseProps: spec.ResponseProps{
				Description: "No Content",
			},
		}
	} else if results.Len() >= 1 {
		firstResult := results.At(0)
		if !isErrorType(firstResult.Type()) {
			// Has a return value - 200 OK
			schema := a.generateSchemaFromType(firstResult.Type(), definitions)
			responses.StatusCodeResponses[200] = spec.Response{
				ResponseProps: spec.ResponseProps{
					Description: "Success",
					Schema:      schema,
				},
			}
		}
	}

	// Always add error responses
	responses.StatusCodeResponses[400] = spec.Response{
		ResponseProps: spec.ResponseProps{
			Description: "Bad Request",
		},
	}
	responses.StatusCodeResponses[500] = spec.Response{
		ResponseProps: spec.ResponseProps{
			Description: "Internal Server Error",
		},
	}

	return responses
}

func (a *API) isPathParameter(paramName string) bool {
	// Check if the parameter name is a wildcard in the parsed path structure
	return a.Pattern.Wildcard(paramName)
}

func (a *API) generateSchemaFromType(t types.Type, definitions spec.Definitions) *spec.Schema {
	schema := &spec.Schema{}

	// Remove pointer indirection
	for {
		if ptr, ok := t.(*types.Pointer); ok {
			t = ptr.Elem()
		} else {
			break
		}
	}

	switch typ := t.(type) {
	case *types.Basic:
		switch typ.Kind() {
		case types.String:
			schema.Type = []string{"string"}
		case types.Int, types.Int8, types.Int16, types.Int32, types.Int64,
			types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64:
			schema.Type = []string{"integer"}
		case types.Float32, types.Float64:
			schema.Type = []string{"number"}
		case types.Bool:
			schema.Type = []string{"boolean"}
		default:
			schema.Type = []string{"string"}
		}
	case *types.Struct:
		schema.Type = []string{"object"}
		schema.Properties = make(map[string]spec.Schema)

		for i := range typ.NumFields() {
			field := typ.Field(i)
			if field.Exported() {
				fieldName := getJSONFieldName(field, typ.Tag(i))
				if fieldName != "" {
					fieldSchema := a.generateSchemaFromType(field.Type(), definitions)
					schema.Properties[fieldName] = *fieldSchema
				}
			}
		}
	case *types.Slice:
		schema.Type = []string{"array"}
		itemSchema := a.generateSchemaFromType(typ.Elem(), definitions)
		schema.Items = &spec.SchemaOrArray{
			Schema: itemSchema,
		}
	case *types.Named:
		// For named types, create a reference to a shared definition
		typeName := typ.Obj().Name()
		pkg := typ.Obj().Pkg()
		var defName string
		if pkg != nil {
			defName = pkg.Name() + "." + typeName
		} else {
			defName = typeName
		}

		// Add to definitions if not already present
		if _, exists := definitions[defName]; !exists {
			underlyingSchema := a.generateSchemaFromType(typ.Underlying(), definitions)
			definitions[defName] = *underlyingSchema
		}

		// Return a reference schema
		schema.Ref = spec.MustCreateRef("#/definitions/" + defName)
		return schema
	default:
		// Fallback for unknown types
		schema.Type = []string{"object"}
	}

	return schema
}

// getJSONFieldName returns the JSON field name from the struct tag if present,
// otherwise returns the field name with the first letter lowercased.
func getJSONFieldName(field *types.Var, tag string) string {
	if tag != "" {
		structTag := reflect.StructTag(tag)
		if jsonTag := structTag.Get("json"); jsonTag != "" {
			// Parse the JSON tag - it might have options like "name,omitempty"
			parts := strings.Split(jsonTag, ",")
			if parts[0] == "-" {
				return "" // Field should be excluded
			}
			if parts[0] != "" {
				return parts[0]
			}
		}
	}

	// No JSON tag found, lowercase the first letter
	name := field.Name()
	if len(name) > 0 {
		return strings.ToLower(name[:1]) + name[1:]
	}
	return name
}

// CronJob represents a cron job method in the graph.
//
//	//zero:cron <schedule>
type CronJob struct {
	// Position is the position of the function declaration.
	Position token.Position
	// Schedule is the parsed cron schedule directive
	Schedule *directiveparser.DirectiveCron
	// Function is the function that handles the cron job
	Function *types.Func
	// Package is the package that contains the function
	Package *packages.Package
}

type Subscription struct {
	// Position is the position of the function declaration.
	Position token.Position
	// Function is the function that handles the subscription
	Function *types.Func
	// Package is the package that contains the function
	Package *packages.Package
	// TopicType is the event type extracted from pubsub.Event[T]
	TopicType types.Type
}

// Config represents command-line/file configuration. Config structs are annotated like so:
//
//	//zero:config [prefix="<prefix>"]
type Config struct {
	// Position of the type declaration.
	Position  token.Position
	Type      types.Type
	Directive *directiveparser.DirectiveConfig
	// IsGeneric indicates if this config is a generic type
	IsGeneric bool
	// TypeParams holds the type parameters for generic configs
	TypeParams *types.TypeParamList
}

// Middleware represents a function that is an HTTP middleware. Middleware functions are annotated like so:
//
//	//zero:middleware [<label>]
type Middleware struct {
	// Position is the position of the function declaration.
	Position token.Position
	// Directive is the parsed middleware directive
	Directive *directiveparser.DirectiveMiddleware
	// Function is the function that implements the middleware
	Function *types.Func
	// Package is the package that contains the function
	Package *packages.Package
	// Requires are the dependencies required by this middleware
	Requires []types.Type
	// Factory represents whether the middleware is a factory, or direct middleware function
	Factory bool
}

func (m *Middleware) Match(api *API) bool {
	if len(m.Directive.Labels) == 0 {
		return true
	}
	for _, label := range m.Directive.Labels {
		for _, apiLabel := range api.Pattern.Labels {
			if label == apiLabel.Name {
				return true
			}
		}
	}
	return false
}

type graphOptions struct {
	// Roots of the graph, defaulting to service endpoint receivers if nil.
	roots []string
	// Providers to pick to resolve duplicate providers.
	pick []string
	// Additional package patterns to search for annotations.
	patterns   []string
	debug      bool
	buildFlags []string
}

type Option func(*graphOptions) error

// WithRoots selects a set of root types that will always be included in the graph.
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
	graph := &Graph{
		Providers:      make(map[string][]*Provider),
		Configs:        make(map[string]*Config),
		GenericConfigs: make(map[string][]*Config),
		APIs:           make([]*API, 0),
		CronJobs:       make([]*CronJob, 0),
		Middleware:     make([]*Middleware, 0),
		Missing:        make(map[*types.Func][]types.Type),
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

	providers := map[string][]*Provider{}
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
			graph.Dest = pkg.Types
		}
		err := analysePackage(pkg, graph, providers, fileset)
		if err != nil {
			return nil, err
		}
	}
	if graph.Dest == nil {
		return nil, errors.Errorf("destination package %q not found", destImport)
	}

	// If no roots provided, use API, Cron, and Subscription receivers as roots
	if opts.roots == nil {
		opts.roots = make([]string, 0, len(graph.APIs)+len(graph.CronJobs)+len(graph.Subscriptions))
		for _, api := range graph.APIs {
			if recv := api.Function.Signature().Recv(); recv != nil {
				receiverType := recv.Type()
				receiverTypeStr := types.TypeString(receiverType, nil)
				opts.roots = append(opts.roots, receiverTypeStr)
			}
		}
		for _, cron := range graph.CronJobs {
			if recv := cron.Function.Signature().Recv(); recv != nil {
				receiverType := recv.Type()
				receiverTypeStr := types.TypeString(receiverType, nil)
				opts.roots = append(opts.roots, receiverTypeStr)
			}
		}
		for _, subscription := range graph.Subscriptions {
			if recv := subscription.Function.Signature().Recv(); recv != nil {
				receiverType := recv.Type()
				receiverTypeStr := types.TypeString(receiverType, nil)
				opts.roots = append(opts.roots, receiverTypeStr)
			}
		}
	}
	if len(graph.APIs) > 0 {
		opts.roots = append(opts.roots, "*net/http.Server")
	}
	if len(graph.CronJobs) > 0 {
		opts.roots = append(opts.roots, "*github.com/alecthomas/zero/providers/cron.Scheduler")
	}
	if len(graph.Subscriptions) > 0 {
		opts.roots = append(opts.roots, "github.com/alecthomas/zero/providers/pubsub.Topic")
	}

	if err := pruneUnreferencedTypes(graph, opts.roots, providers, opts.pick); err != nil {
		return nil, errors.WithStack(err)
	}

	findMissingDependencies(graph)

	// Prune unreferenced providers and configs based on roots
	// if len(opts.roots) == 0 && len(graph.APIs) == 0 && len(graph.CronJobs) == 0 {
	// 	return nil, errors.Errorf("no root types provided and no API endpoints or cron jobs found")
	// }

	if err := checkForMissingRoots(graph, opts.roots); err != nil {
		return nil, errors.WithStack(err)
	}

	if err := checkForMissingProviders(graph, opts.pick); err != nil {
		return nil, errors.WithStack(err)
	}

	return graph, nil
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
			for _, reqType := range provider.Requires {
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

func analysePackage(pkg *packages.Package, graph *Graph, providers map[string][]*Provider, fset *token.FileSet) error {
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
					provider, err := createProvider(decl, pkg, directive, fset)
					if err != nil {
						return err
					}
					if provider != nil {
						if provider.IsGeneric {
							// For generic providers, store by base type name
							baseType := getBaseTypeName(provider.Provides)
							providers[baseType] = append(providers[baseType], provider)
						} else {
							key := types.TypeString(provider.Provides, nil)
							providers[key] = append(providers[key], provider)
						}
					}

				case *directiveparser.DirectiveAPI:
					api, err := createAPI(decl, pkg, directive, fset)
					if err != nil {
						return err
					}
					if api != nil {
						graph.APIs = append(graph.APIs, api)
					}

				case *directiveparser.DirectiveCron:
					cron, err := createCron(decl, pkg, directive, fset)
					if err != nil {
						return err
					}
					if cron != nil {
						graph.CronJobs = append(graph.CronJobs, cron)
					}

				case *directiveparser.DirectiveMiddleware:
					middleware, err := createMiddleware(decl, pkg, directive, fset)
					if err != nil {
						return err
					}
					if middleware != nil {
						graph.Middleware = append(graph.Middleware, middleware)
					}

				case *directiveparser.DirectiveSubscribe:
					subscription, err := createSubscription(decl, pkg, fset)
					if err != nil {
						return err
					}
					if subscription != nil {
						graph.Subscriptions = append(graph.Subscriptions, subscription)
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
							// Check if this is a generic config
							var typeParams *types.TypeParamList
							var isGeneric bool
							if named, ok := configType.(*types.Named); ok {
								typeParams = named.TypeParams()
								isGeneric = typeParams != nil && typeParams.Len() > 0
							}

							config := &Config{
								Position:   fset.Position(typeSpec.Pos()),
								Type:       configType,
								Directive:  directive,
								IsGeneric:  isGeneric,
								TypeParams: typeParams,
							}

							if isGeneric {
								// For generic configs, store by base type name
								baseType := getBaseTypeName(configType)
								graph.GenericConfigs[baseType] = append(graph.GenericConfigs[baseType], config)
							} else {
								key := types.TypeString(configType, nil)
								graph.Configs[key] = config
							}
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

	params := sig.Params()
	requiredTypes := make([]types.Type, params.Len())
	for i := range params.Len() {
		requiredTypes[i] = params.At(i).Type()
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
		Requires:   requiredTypes,
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

// checkReceiverDependency checks if a function's receiver type is provided and adds it to missing if not
func checkReceiverDependency(fn *types.Func, provided map[string]bool, graph *Graph) {
	sig := fn.Type().(*types.Signature)
	if sig.Recv() != nil {
		receiverType := sig.Recv().Type()
		key := types.TypeString(receiverType, nil)
		if !provided[key] && !isProvidedByConfig(receiverType, graph) && !canBeProvidedByGeneric(receiverType, graph) {
			// Check for duplicates before adding
			existing := graph.Missing[fn]
			isDuplicate := false
			for _, existingType := range existing {
				if types.Identical(existingType, receiverType) {
					isDuplicate = true
					break
				}
			}
			if !isDuplicate {
				graph.Missing[fn] = append(graph.Missing[fn], receiverType)
			}
		}
	}
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

func findMissingDependencies(graph *Graph) {
	provided := map[string]bool{
		// Builtin types
		"context.Context": true,
	}
	for key := range graph.Providers {
		provided[key] = true
	}
	for key := range graph.Configs {
		provided[key] = true
	}

	for _, providers := range graph.Providers {
		for _, provider := range providers {
			for _, required := range provider.Requires {
				key := types.TypeString(required, nil)
				if !provided[key] && !isProvidedByConfig(required, graph) && !canBeProvidedByGeneric(required, graph) {
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
	}

	// Check receiver types for APIs
	for _, api := range graph.APIs {
		checkReceiverDependency(api.Function, provided, graph)
	}

	// Check receiver types for CronJobs
	for _, cron := range graph.CronJobs {
		checkReceiverDependency(cron.Function, provided, graph)
	}

	// Check receiver types for Subscriptions
	for _, subscription := range graph.Subscriptions {
		checkReceiverDependency(subscription.Function, provided, graph)
	}

	// Check middleware dependencies
	for _, middleware := range graph.Middleware {
		for _, required := range middleware.Requires {
			key := types.TypeString(required, nil)
			if !provided[key] && !isProvidedByConfig(required, graph) && !canBeProvidedByGeneric(required, graph) {
				// Check for duplicates before adding
				existing := graph.Missing[middleware.Function]
				isDuplicate := false
				for _, existingType := range existing {
					if types.Identical(existingType, required) {
						isDuplicate = true
						break
					}
				}
				if !isDuplicate {
					graph.Missing[middleware.Function] = append(graph.Missing[middleware.Function], required)
				}
			}
		}
	}
}

func isProvidedByConfig(requiredType types.Type, graph *Graph) bool {
	// Check if the required type is directly provided as a config
	key := types.TypeString(requiredType, nil)
	if _, exists := graph.Configs[key]; exists {
		return true
	}

	// If required type is a pointer, check if the underlying type is a config
	if ptrType, ok := requiredType.(*types.Pointer); ok {
		underlyingKey := types.TypeString(ptrType.Elem(), nil)
		if _, exists := graph.Configs[underlyingKey]; exists {
			return true
		}
	}

	// Check if the required type can be provided by a generic config
	if canBeProvidedByGenericConfig(requiredType, graph) {
		return true
	}

	return false
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

// canBeProvidedByGenericConfig checks if a required type can be satisfied by a generic config
func canBeProvidedByGenericConfig(requiredType types.Type, graph *Graph) bool { //nolint
	baseType := getBaseTypeName(requiredType)

	// Check if we have generic configs for this base type
	configs, exists := graph.GenericConfigs[baseType]
	if !exists || len(configs) == 0 {
		return false
	}

	// For generic types, we need to check if the type arguments satisfy the constraints
	namedType, ok := requiredType.(*types.Named)
	if !ok {
		return false
	}

	typeArgs := namedType.TypeArgs()
	if typeArgs == nil || typeArgs.Len() == 0 {
		return false
	}

	// Check if any generic config can provide this concrete type
	for _, config := range configs {
		if config.TypeParams == nil || config.TypeParams.Len() == 0 {
			continue
		}

		// Check if type arguments satisfy the constraints
		if satisfiesConstraints(typeArgs, config.TypeParams) {
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

// Types used internally by Zero's generated API handling code.
var internalAPITypes = []string{
	"github.com/alecthomas/zero.ErrorEncoder",
	"github.com/alecthomas/zero.ResponseEncoder",
}

// pruneUnreferencedTypes removes providers and configs that are not transitively referenced from the given roots
func pruneUnreferencedTypes(graph *Graph, roots []string, providers map[string][]*Provider, pick []string) error {
	referenced := map[string]bool{}
	toProcess := slices.Clone(roots)
	if len(graph.APIs) > 0 {
		toProcess = append(toProcess, internalAPITypes...)
	}

	// Add subscription event types to ensure they are preserved
	for _, subscription := range graph.Subscriptions {
		if subscription.TopicType != nil {
			eventTypeStr := types.TypeString(subscription.TopicType, nil)
			toProcess = append(toProcess, eventTypeStr)
		}
	}
	ambiguousProviders := map[string][]*Provider{}

	// Transfer all generic providers to the graph immediately so they can be resolved when needed
	for key, providerList := range providers {
		for _, p := range providerList {
			if p.IsGeneric {
				// Store generic providers under their base type key for lookup
				graph.Providers[key] = append(graph.Providers[key], p)
			}
		}
	}

	// Create concrete topic providers for subscriptions (after generic providers are transferred)
	for _, subscription := range graph.Subscriptions {
		if subscription.TopicType != nil {
			baseType := "github.com/alecthomas/zero/providers/pubsub.Topic"
			if genericProviders, exists := graph.Providers[baseType]; exists && len(genericProviders) > 0 {
				// Find the pubsub.Topic generic type
				var pubsubTopicType *types.Named
				for _, provider := range genericProviders {
					for _, imp := range provider.Package.Imports {
						if imp.PkgPath == "github.com/alecthomas/zero/providers/pubsub" {
							if obj := imp.Types.Scope().Lookup("Topic"); obj != nil {
								if typeName, ok := obj.(*types.TypeName); ok {
									if named, ok := typeName.Type().(*types.Named); ok {
										pubsubTopicType = named
										break
									}
								}
							}
						}
					}
					if pubsubTopicType != nil {
						break
					}
				}

				if pubsubTopicType != nil {
					// Create the instantiated type pubsub.Topic[TopicType]
					topicType, err := types.Instantiate(nil, pubsubTopicType, []types.Type{subscription.TopicType}, false)
					if err == nil {
						topicTypeStr := types.TypeString(topicType, nil)
						// Check if this topic type already exists
						if _, exists := graph.Providers[topicTypeStr]; !exists {
							// Resolve the generic provider with the concrete topic type
							resolvedProvider := resolveGenericProviderWithType(graph, topicType, pick)
							if resolvedProvider != nil {
								// Add the concrete provider to the graph
								concreteTypeKey := types.TypeString(resolvedProvider.Provides, nil)
								graph.Providers[concreteTypeKey] = []*Provider{resolvedProvider}
								referenced[concreteTypeKey] = true

								// Also mark the base generic provider as referenced so it's not pruned
								baseType := "github.com/alecthomas/zero/providers/pubsub.Topic"
								referenced[baseType] = true

								// Add its requirements to be processed
								for _, required := range resolvedProvider.Requires {
									requiredKey := types.TypeString(required, nil)
									if !referenced[requiredKey] {
										toProcess = append(toProcess, requiredKey)
									}
								}
							}
						}
					}
				}
			}
		}
	}

	// Build function name to provider mapping for directive requirements
	// Key is "package.path/functionName" to ensure same-package requirements
	funcNameToProvider := map[string]*Provider{}
	for _, providerList := range providers {
		for _, p := range providerList {
			funcKey := p.Package.PkgPath + "/" + p.Function.Name()
			funcNameToProvider[funcKey] = p
		}
	}

	// Pre-process directive requirements to identify explicitly required providers
	explicitlyRequired := map[string]bool{}
	for _, providerList := range providers {
		for _, p := range providerList {
			for _, requiredFuncName := range p.Directive.Require {
				requiredFuncKey := p.Package.PkgPath + "/" + requiredFuncName
				if _, exists := funcNameToProvider[requiredFuncKey]; exists {
					explicitlyRequired[requiredFuncKey] = true
				} else {
					return errors.Errorf("provider %s requires %s, but %s is not a valid provider function in the same package", p.Function.Name(), requiredFuncName, requiredFuncName)
				}
			}
		}
	}

	// Validate multi-provider constraints first
	for key, providerList := range providers {
		if err := validateMultiProviderConstraints(key, providerList); err != nil {
			return err
		}
	}

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
			if isMultiProvider(providers) {
				// For multi-providers, include non-weak providers by default, plus any explicitly required weak providers
				var includedProviders []*Provider
				for _, p := range providers {
					funcKey := p.Package.PkgPath + "/" + p.Function.Name()
					if !p.Directive.Weak || explicitlyRequired[funcKey] {
						includedProviders = append(includedProviders, p)
					}
				}

				// If no non-weak providers exist and none are explicitly required, include all (weak) providers
				if len(includedProviders) == 0 {
					includedProviders = providers
				}

				graph.Providers[current] = includedProviders
				for _, p := range includedProviders {
					for _, required := range p.Requires {
						requiredKey := types.TypeString(required, nil)
						if !referenced[requiredKey] {
							toProcess = append(toProcess, requiredKey)
						}
					}
					// Handle directive requirements for multi-providers
					for _, requiredFuncName := range p.Directive.Require {
						// Only allow requiring functions from the same package
						requiredFuncKey := p.Package.PkgPath + "/" + requiredFuncName
						if requiredProvider, exists := funcNameToProvider[requiredFuncKey]; exists {
							requiredKey := types.TypeString(requiredProvider.Provides, nil)
							if !referenced[requiredKey] {
								toProcess = append(toProcess, requiredKey)
							}
						}
					}
				}
			} else {
				provider := pickProvider(providers, pick)
				if provider == nil {
					ambiguousProviders[current] = providers
				} else {
					graph.Providers[types.TypeString(provider.Provides, nil)] = []*Provider{provider}
					for _, required := range provider.Requires {
						requiredKey := types.TypeString(required, nil)
						if !referenced[requiredKey] {
							toProcess = append(toProcess, requiredKey)
						}
					}
					// Handle directive requirements
					for _, requiredFuncName := range provider.Directive.Require {
						// Only allow requiring functions from the same package
						requiredFuncKey := provider.Package.PkgPath + "/" + requiredFuncName
						if requiredProvider, exists := funcNameToProvider[requiredFuncKey]; exists {
							requiredKey := types.TypeString(requiredProvider.Provides, nil)
							if !referenced[requiredKey] {
								toProcess = append(toProcess, requiredKey)
							}
						}
					}
				}
			}
		} else {
			// Check if this type can be provided by a generic provider
			// First find the actual type object
			var concreteType types.Type
			for _, providers := range graph.Providers {
				for _, provider := range providers {
					for _, req := range provider.Requires {
						if types.TypeString(req, nil) == current {
							concreteType = req
							break
						}
					}
					if concreteType != nil {
						break
					}
				}
				if concreteType != nil {
					break
				}
			}
			// Also check APIs
			if concreteType == nil {
				for _, api := range graph.APIs {
					if recv := api.Function.Signature().Recv(); recv != nil {
						recvType := recv.Type()
						if types.TypeString(recvType, nil) == current {
							concreteType = recvType
							break
						}
					}
				}
			}
			// Also check middleware
			if concreteType == nil {
				for _, middleware := range graph.Middleware {
					for _, req := range middleware.Requires {
						if types.TypeString(req, nil) == current {
							concreteType = req
							break
						}
					}
					if concreteType != nil {
						break
					}
				}
			}

			if concreteType != nil {
				if resolvedProvider := resolveGenericProviderWithType(graph, concreteType, pick); resolvedProvider != nil {
					// Add the resolved generic provider as a concrete provider
					concreteTypeKey := types.TypeString(resolvedProvider.Provides, nil)
					graph.Providers[concreteTypeKey] = []*Provider{resolvedProvider}
					referenced[concreteTypeKey] = true

					// Also mark the base generic provider as referenced so it's not pruned
					baseType := getBaseTypeName(concreteType)
					referenced[baseType] = true

					// Add the generic provider's dependencies to processing queue
					for _, required := range resolvedProvider.Requires {
						requiredKey := types.TypeString(required, nil)
						if !referenced[requiredKey] {
							toProcess = append(toProcess, requiredKey)
						}
					}

					// Handle directive requirements for generic providers
					for _, requiredFuncName := range resolvedProvider.Directive.Require {
						requiredFuncKey := resolvedProvider.Package.PkgPath + "/" + requiredFuncName
						if requiredProvider, exists := funcNameToProvider[requiredFuncKey]; exists {
							requiredKey := types.TypeString(requiredProvider.Provides, nil)
							if !referenced[requiredKey] {
								toProcess = append(toProcess, requiredKey)
							}
						}
					}
				} else if resolvedConfig := resolveGenericConfigWithType(graph, concreteType); resolvedConfig != nil {
					// Add the resolved generic config as a concrete config
					graph.Configs[current] = resolvedConfig
				} else {
					// Check if there are ambiguous generic providers
					baseType := getBaseTypeName(concreteType)
					if genericProviders, exists := graph.Providers[baseType]; exists && len(genericProviders) > 0 {
						// Filter to providers that can actually provide this concrete type
						validProviders := make([]*Provider, 0)
						for _, genericProvider := range genericProviders {
							if canProvideConcreteTypeWithConstraints(concreteType, genericProvider) {
								validProviders = append(validProviders, genericProvider)
							}
						}
						if len(validProviders) > 0 {
							ambiguousProviders[current] = validProviders
						}
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

	// Remove unreferenced providers
	for key := range graph.Providers {
		if !referenced[key] {
			delete(graph.Providers, key)
		}
	}

	// Remove unreferenced configs
	for key := range graph.Configs {
		if !isConfigReferenced(key, referenced) {
			delete(graph.Configs, key)
		}
	}

	// Remove unreferenced generic configs
	for baseType := range graph.GenericConfigs {
		// Check if any concrete instantiation of this generic config is referenced
		isReferenced := false
		for refKey := range referenced {
			if strings.HasPrefix(refKey, baseType+"[") {
				isReferenced = true
				break
			}
		}
		if !isReferenced {
			delete(graph.GenericConfigs, baseType)
		}
	}

	// Remove unused middleware
	if len(graph.APIs) > 0 {
		// Collect all labels used by APIs
		usedLabels := make(map[string]bool)
		for _, api := range graph.APIs {
			if api.Pattern != nil && api.Pattern.Labels != nil {
				for _, label := range api.Pattern.Labels {
					usedLabels[label.Name] = true
				}
			}
		}

		// Filter middleware: keep if no labels (global) or if any label matches API labels
		var filteredMiddleware []*Middleware
		for _, mw := range graph.Middleware {
			if len(mw.Directive.Labels) == 0 {
				// Global middleware (no labels) - always keep
				filteredMiddleware = append(filteredMiddleware, mw)
			} else {
				// Check if any middleware label matches any API label
				hasMatchingLabel := false
				for _, label := range mw.Directive.Labels {
					if usedLabels[label] {
						hasMatchingLabel = true
						break
					}
				}
				if hasMatchingLabel {
					filteredMiddleware = append(filteredMiddleware, mw)
				}
			}
		}
		graph.Middleware = filteredMiddleware
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

	// For multi-providers, we don't pick a single provider - they all contribute
	if isMultiProvider(providers) {
		return providers[0] // Return first one as representative
	}

	var strong []*Provider
	for _, provider := range providers {
		if !provider.Directive.Weak {
			strong = append(strong, provider)
		}
		key := provider.Function.FullName()
		if slices.Contains(pick, key) {
			return provider
		}
	}
	if len(strong) == 1 {
		return strong[0]
	}
	return nil
}

// validateMultiProviderConstraints ensures that if one provider for a type is multi,
// all providers for that type must be multi.
func validateMultiProviderConstraints(typeKey string, providers []*Provider) error {
	if len(providers) <= 1 {
		return nil
	}

	hasMulti := false
	hasNonMulti := false

	for _, provider := range providers {
		if provider.Directive.Multi {
			hasMulti = true
		} else {
			hasNonMulti = true
		}
	}

	if hasMulti && hasNonMulti {
		var multiProviders []string
		var nonMultiProviders []string

		for _, provider := range providers {
			funcName := "unknown"
			if provider.Function != nil {
				funcName = provider.Function.FullName()
			}

			if provider.Directive.Multi {
				multiProviders = append(multiProviders, funcName)
			} else {
				nonMultiProviders = append(nonMultiProviders, funcName)
			}
		}

		return errors.Errorf("type %s has mixed multi and non-multi providers: multi=[%s], non-multi=[%s]",
			typeKey,
			strings.Join(multiProviders, ", "),
			strings.Join(nonMultiProviders, ", "))
	}

	return nil
}

// isMultiProvider returns true if all providers in the list are multi-providers.
func isMultiProvider(providers []*Provider) bool {
	if len(providers) == 0 {
		return false
	}

	for _, provider := range providers {
		if !provider.Directive.Multi {
			return false
		}
	}

	return true
}

// requireAppropriateScheduler automatically adds the appropriate scheduler provider
// to the pick list based on whether cron jobs are present in the graph.
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

// canBeProvidedByGeneric checks if a required type can be satisfied by a generic provider
func canBeProvidedByGeneric(requiredType types.Type, graph *Graph) bool { //nolint
	baseType := getBaseTypeName(requiredType)

	// Check if we have generic providers for this base type
	providers, exists := graph.Providers[baseType]
	if !exists || len(providers) == 0 {
		return false
	}

	// For generic types, we need to check if the type arguments satisfy the constraints
	namedType, ok := requiredType.(*types.Named)
	if !ok {
		return false
	}

	// Get type arguments from the required type
	typeArgs := namedType.TypeArgs()
	if typeArgs == nil || typeArgs.Len() == 0 {
		return false
	}

	// Check each provider to see if any can satisfy the constraints
	for _, provider := range providers {
		if provider.TypeParams == nil || provider.TypeParams.Len() == 0 {
			continue
		}

		// Check if type arguments satisfy the constraints
		if satisfiesConstraints(typeArgs, provider.TypeParams) {
			return true
		}
	}

	return false
}

// resolveGenericProviderWithType finds a suitable generic provider for a concrete type,
// applying the same weak provider logic as regular providers
func resolveGenericProviderWithType(graph *Graph, concreteType types.Type, pick []string) *Provider {
	baseType := getBaseTypeName(concreteType)
	genericProviders, exists := graph.Providers[baseType]
	if !exists || len(genericProviders) == 0 {
		return nil
	}

	// Filter to providers that can actually provide this concrete type
	validProviders := make([]*Provider, 0)
	for _, genericProvider := range genericProviders {
		if canProvideConcreteTypeWithConstraints(concreteType, genericProvider) {
			validProviders = append(validProviders, genericProvider)
		}
	}

	if len(validProviders) == 0 {
		return nil
	}

	// Apply the same logic as pickProvider for generic providers
	var selectedGenericProvider *Provider
	if len(validProviders) == 1 {
		selectedGenericProvider = validProviders[0]
	} else {
		// Check for explicit picks
		for _, provider := range validProviders {
			key := provider.Function.FullName()
			if slices.Contains(pick, key) {
				selectedGenericProvider = provider
				break
			}
		}

		if selectedGenericProvider == nil {
			// Filter to non-weak providers
			var strong []*Provider
			for _, provider := range validProviders {
				if !provider.Directive.Weak {
					strong = append(strong, provider)
				}
			}

			if len(strong) == 1 {
				selectedGenericProvider = strong[0]
			}
		}
	}

	if selectedGenericProvider == nil {
		// If we have zero or multiple non-weak providers, it's ambiguous
		return nil
	}

	// Create a new provider instance with the concrete type
	// Resolve requirements by substituting type parameters with concrete types
	resolvedRequires := make([]types.Type, len(selectedGenericProvider.Requires))
	copy(resolvedRequires, selectedGenericProvider.Requires)

	// Extract type arguments from the concrete type for substitution
	if namedType, ok := concreteType.(*types.Named); ok {
		if typeArgs := namedType.TypeArgs(); typeArgs != nil && typeArgs.Len() > 0 {
			// Substitute type parameters in requirements
			for i, req := range resolvedRequires {
				resolvedRequires[i] = substituteTypeParams(req, selectedGenericProvider.TypeParams, typeArgs)
			}
		}
	}

	concreteProvider := &Provider{
		Position:   selectedGenericProvider.Position,
		Directive:  selectedGenericProvider.Directive,
		Function:   selectedGenericProvider.Function,
		Package:    selectedGenericProvider.Package,
		Provides:   concreteType,
		Requires:   resolvedRequires,
		IsGeneric:  true, // Keep this flag to indicate it needs type instantiation
		TypeParams: selectedGenericProvider.TypeParams,
	}

	return concreteProvider
}

// canProvideConcreteTypeWithConstraints checks if a generic provider can provide a concrete type
// and validates type constraints
func canProvideConcreteTypeWithConstraints(concreteType types.Type, genericProvider *Provider) bool {
	if !genericProvider.IsGeneric {
		return false
	}

	// Check if base types match
	baseType := getBaseTypeName(concreteType)
	expectedBaseType := getBaseTypeName(genericProvider.Provides)

	if baseType != expectedBaseType {
		return false
	}

	// Extract type arguments from the concrete type
	namedType, ok := concreteType.(*types.Named)
	if !ok {
		return false
	}

	typeArgs := namedType.TypeArgs()
	if typeArgs == nil || typeArgs.Len() == 0 {
		return false
	}

	// Check if type arguments satisfy the constraints
	if genericProvider.TypeParams == nil || genericProvider.TypeParams.Len() == 0 {
		return false
	}

	return satisfiesConstraints(typeArgs, genericProvider.TypeParams)
}

// satisfiesConstraints checks if the provided type arguments satisfy the type parameter constraints
func satisfiesConstraints(typeArgs *types.TypeList, typeParams *types.TypeParamList) bool {
	if typeArgs.Len() != typeParams.Len() {
		return false
	}

	for i := range typeArgs.Len() {
		typeArg := typeArgs.At(i)
		typeParam := typeParams.At(i)

		// Get the constraint for this type parameter
		constraint := typeParam.Constraint()
		if constraint == nil {
			continue // No constraint means any type is acceptable
		}

		// Check if the type argument implements/satisfies the constraint
		if !types.Implements(typeArg, constraint.Underlying().(*types.Interface)) {
			return false
		}
	}

	return true
}

// resolveGenericConfigWithType finds a suitable generic config for a concrete type
func resolveGenericConfigWithType(graph *Graph, concreteType types.Type) *Config {
	baseType := getBaseTypeName(concreteType)
	genericConfigs, exists := graph.GenericConfigs[baseType]
	if !exists || len(genericConfigs) == 0 {
		return nil
	}

	// Filter to configs that can actually provide this concrete type
	validConfigs := make([]*Config, 0)
	for _, genericConfig := range genericConfigs {
		if canProvideConcreteConfigWithConstraints(concreteType, genericConfig) {
			validConfigs = append(validConfigs, genericConfig)
		}
	}

	if len(validConfigs) == 0 {
		return nil
	}

	// For now, just pick the first valid generic config
	// TODO: Apply similar logic as pickProvider if needed
	selectedGenericConfig := validConfigs[0]

	// Create a new concrete config instance with substituted prefix
	concreteConfig := &Config{
		Position:   selectedGenericConfig.Position,
		Type:       concreteType,
		Directive:  selectedGenericConfig.Directive,
		IsGeneric:  false,
		TypeParams: nil,
	}

	// Handle prefix substitution if the directive has a prefix
	if selectedGenericConfig.Directive.Prefix != "" {
		// Extract the type name for substitution
		var typeName string
		if named, ok := concreteType.(*types.Named); ok {
			if typeArgs := named.TypeArgs(); typeArgs != nil && typeArgs.Len() > 0 {
				// Get the first type argument for substitution
				firstArg := typeArgs.At(0)
				if namedArg, ok := firstArg.(*types.Named); ok {
					typeName = namedArg.Obj().Name()
				} else {
					typeName = types.TypeString(firstArg, nil)
				}
			}
		}

		if typeName != "" {
			kebabTypeName := toKebabCase(typeName)
			newPrefix := strings.ReplaceAll(selectedGenericConfig.Directive.Prefix, "${type}", kebabTypeName)
			concreteConfig.Directive = &directiveparser.DirectiveConfig{
				Prefix: newPrefix,
			}
		}
	}

	return concreteConfig
}

// canProvideConcreteConfigWithConstraints checks if a generic config can provide a concrete type
func canProvideConcreteConfigWithConstraints(concreteType types.Type, genericConfig *Config) bool {
	if genericConfig.TypeParams == nil || genericConfig.TypeParams.Len() == 0 {
		return false
	}

	namedType, ok := concreteType.(*types.Named)
	if !ok {
		return false
	}

	typeArgs := namedType.TypeArgs()
	if typeArgs == nil || typeArgs.Len() == 0 {
		return false
	}

	// Check if type arguments satisfy the constraints
	return satisfiesConstraints(typeArgs, genericConfig.TypeParams)
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

// substituteTypeParams substitutes type parameters in a type with concrete type arguments
func substituteTypeParams(t types.Type, typeParams *types.TypeParamList, typeArgs *types.TypeList) types.Type {
	switch typ := t.(type) {
	case *types.Named:
		// If this is a generic type with type arguments, substitute them
		if typ.TypeArgs() != nil && typ.TypeArgs().Len() > 0 {
			// Create new type arguments by substituting each one
			newArgs := make([]types.Type, typ.TypeArgs().Len())
			for i := range typ.TypeArgs().Len() {
				oldArg := typ.TypeArgs().At(i)
				newArgs[i] = substituteTypeParams(oldArg, typeParams, typeArgs)
			}

			// Create a new instantiated type with the substituted arguments
			inst, err := types.Instantiate(nil, typ.Origin(), newArgs, false)
			if err != nil {
				return typ // fallback to original type if instantiation fails
			}
			return inst
		}
		return typ

	case *types.TypeParam:
		// Find the corresponding concrete type argument
		for i := range typeParams.Len() {
			if typeParams.At(i) == typ {
				if i < typeArgs.Len() {
					return typeArgs.At(i)
				}
			}
		}
		return typ

	case *types.Pointer:
		// Recursively substitute in the element type
		elem := substituteTypeParams(typ.Elem(), typeParams, typeArgs)
		if elem != typ.Elem() {
			return types.NewPointer(elem)
		}
		return typ

	case *types.Slice:
		// Recursively substitute in the element type
		elem := substituteTypeParams(typ.Elem(), typeParams, typeArgs)
		if elem != typ.Elem() {
			return types.NewSlice(elem)
		}
		return typ

	case *types.Array:
		// Recursively substitute in the element type
		elem := substituteTypeParams(typ.Elem(), typeParams, typeArgs)
		if elem != typ.Elem() {
			return types.NewArray(elem, typ.Len())
		}
		return typ

	case *types.Map:
		// Recursively substitute in key and value types
		key := substituteTypeParams(typ.Key(), typeParams, typeArgs)
		elem := substituteTypeParams(typ.Elem(), typeParams, typeArgs)
		if key != typ.Key() || elem != typ.Elem() {
			return types.NewMap(key, elem)
		}
		return typ

	case *types.Chan:
		// Recursively substitute in the element type
		elem := substituteTypeParams(typ.Elem(), typeParams, typeArgs)
		if elem != typ.Elem() {
			return types.NewChan(typ.Dir(), elem)
		}
		return typ

	default:
		// For other types (basic types, interfaces, etc.), no substitution needed
		return typ
	}
}

func checkForMissingRoots(graph *Graph, roots []string) error {
	collected := map[string]bool{}
	for key := range graph.Configs {
		collected[key] = true
	}
	for _, configs := range graph.GenericConfigs {
		// TODO: add materialised types?
		config := configs[0]
		collected[normaliseType(config.Type)] = true
	}
	for key, providers := range graph.Providers {
		for _, provider := range providers {
			collected[normaliseType(provider.Provides)] = true
			// For generic providers, also add the base type key
			if provider.IsGeneric {
				collected[key] = true
			}
		}
	}

	for _, root := range roots {
		if !collected[root] {
			return fmt.Errorf("requested root %q not found in discovered provided types: %s", root, strings.Join(slices.Collect(maps.Keys(collected)), ", "))
		}
	}
	return nil
}

func checkForMissingProviders(graph *Graph, pick []string) error {
	if len(pick) == 0 {
		return nil
	}

	collected := map[string]bool{}
	// Collect all provider function names
	for _, providers := range graph.Providers {
		for _, provider := range providers {
			collected[provider.Function.FullName()] = true
		}
	}

	for _, providerRef := range pick {
		if !collected[providerRef] {
			return fmt.Errorf("requested provider %q not found in discovered provider functions: %s", providerRef, strings.Join(slices.Collect(maps.Keys(collected)), ", "))
		}
	}
	return nil
}

// For some reason, generic return types don't include constraints, but type definitions do, so we normalise the string
// representation to exclude them always.
func normaliseType(t types.Type) string {
	switch t := t.(type) {
	case *types.Pointer:
		return "*" + normaliseType(t.Elem())

	case *types.Basic:
		return t.String()

	case *types.Named:
		tp := ""
		if t.TypeParams() != nil {
			tp += "["
			i := 0
			for param := range t.TypeParams().TypeParams() {
				if i > 0 {
					tp += ", "
				}
				tp += param.Obj().Name()
				i++
			}
			tp += "]"
		}
		if t.Obj().Pkg() != nil {
			return t.Obj().Pkg().Path() + "." + t.Obj().Name() + tp
		}
		return t.Obj().Name() + tp

	case *types.Map:
		return "map[" + normaliseType(t.Key()) + "]" + normaliseType(t.Elem())

	case *types.Slice:
		return "[]" + normaliseType(t.Elem())

	default:
		panic(fmt.Sprintf("unknown type %T", t))
	}
}
