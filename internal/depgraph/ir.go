package depgraph

import (
	"fmt"
	"go/token"
	"go/types"
	"hash/fnv"
	"iter"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/alecthomas/errors"
)

// Node represents a node in the dependency graph
type Node interface {
	// NodePosition returns the position of the node in the source code.
	NodePosition() token.Position
	// NodeKey returns the fully-qualified reference to this node.
	//
	// eg. for the cron.Scheduler provider it would return
	// github.com/alecthomas/zero/providers/cron.NewScheduler
	NodeKey() Key
	// NodeRequires returns the types and providers this node requires
	NodeRequires() []Key
	// NodeRequiredBy returns the types and providers that require this node.
	//
	// For example a Provider will insert a dependency from the type to the provider.
	NodeRequiredBy() []Key
	// NodeProvides returns the type this node provides, if any.
	//
	// IsEmpty() will be true if the node does not provide a type.
	//
	// **Note**: this may or may not differ from NodeKey().
	NodeProvides() TypeKey
	// node is a sealed interface
	node()
}

// Key represesents either a [NodeKey] or a [TypeKey].
//
//sumtype:decl
type Key interface {
	Kind() string
	String() string
	IsGeneric() bool
	IsEmpty() bool
	key()
}

// NodeKey represents a unique identifier for a [Node] in the dependency graph.
type NodeKey string

func (NodeKey) Kind() string         { return "node" }
func (node NodeKey) String() string  { return string(node) }
func (node NodeKey) IsEmpty() bool   { return node == "" }
func (node NodeKey) IsGeneric() bool { return strings.Contains(string(node), "?") }
func (NodeKey) key()                 {}

// TypeKeyForReceiver returns the [TypeKey] for the receiver of a method.
func TypeKeyForReceiver(f *types.Func) TypeKey {
	return TypeKey(f.Signature().Recv().Type().String())
}

// TypeKey represents a unique identifier for a type in the dependency graph.
type TypeKey string

func (TypeKey) Kind() string      { return "type" }
func (t TypeKey) IsGeneric() bool { return strings.Contains(string(t), "?") }
func (t TypeKey) IsEmpty() bool   { return t == "" }
func (t TypeKey) String() string  { return string(t) }
func (TypeKey) key()              {}

// IR is the intermediate representation of the dependency graph
type IR struct {
	dest         *types.Package   // The destination package for code generation.
	nodes        map[Key]Node     // All nodes by their NodeKey
	provides     map[TypeKey]Node // The Node providing the given type.
	required     map[Key]bool     // Types and Nodes required by the user or other providers.
	dependencies map[Key][]Key    // Where the key dependes on the values.
	defeated     map[Key]bool     // Defeated weak providers that should not appear in final graph.

	// Materialized views populated after construction.
	providers     map[string][]*Provider
	configs       map[string]*Config
	apis          []*API
	cronJobs      []*CronJob
	subscriptions []*Subscription
	middleware    []*Middleware
}

func NewIR(dest *types.Package, nodes []Node, options ...Option) (*IR, error) {
	i := &IR{
		dest:         dest,
		nodes:        make(map[Key]Node),
		provides:     make(map[TypeKey]Node),
		required:     make(map[Key]bool),
		dependencies: make(map[Key][]Key),
		defeated:     make(map[Key]bool),
	}
	opts := &graphOptions{}
	for _, option := range options {
		if err := option(opts); err != nil {
			return nil, errors.WithStack(err)
		}
	}
	for _, key := range opts.require {
		i.Require(key)
	}
	err := i.AddNode(&Intrinsic{Key: "context.Context"})
	if err != nil {
		return nil, errors.WithStack(err)
	}
	for _, node := range nodes {
		if err := i.AddNode(node); err != nil {
			return nil, errors.WithStack(err)
		}
	}

	// First pass, validate + propagate
	if err := i.validate(); err != nil {
		return nil, errors.WithStack(err)
	}

	if err := i.propagate(); err != nil {
		return nil, errors.WithStack(err)
	}

	// Second pass, apply rules
	for _, rule := range Rules {
		if err := rule(i); err != nil {
			return nil, errors.WithStack(err)
		}
	}

	// Third and final pass, validate + propagate again
	if err := i.validate(); err != nil {
		return nil, errors.WithStack(err)
	}

	if err := i.propagate(); err != nil {
		return nil, errors.WithStack(err)
	}

	i.materialize()

	return i, nil
}

func (i *IR) Graph() map[Key][]Key {
	graph := make(map[Key][]Key)
	for node := range i.RequiredNodes() {
		deps := i.dependenciesForNode(node)
		if len(deps) > 0 {
			graph[node.NodeKey()] = deps
		}
	}
	// Also include types that have dependencies (from extraDependencies)
	for key, deps := range i.dependencies {
		if len(deps) > 0 {
			graph[key] = deps
		}
	}
	return graph
}

// Nodes returns all [Node]s in the graph.
func (i *IR) Nodes() iter.Seq[Node] {
	return func(yield func(Node) bool) {
		for _, node := range i.nodes {
			if !yield(node) {
				return
			}
		}
	}
}

// RequiredNodes returns all required [Node]s in the graph.
func (i *IR) RequiredNodes() iter.Seq[Node] {
	nodes := map[Key]Node{}
	for key := range i.required {
		if i.defeated[key] {
			continue // Skip defeated weak providers
		}
		node := i.lookup(key)

		// Handle Multi nodes specially - include all individual providers
		if multi, isMulti := node.(Multi); isMulti {
			for _, provider := range multi {
				if !i.defeated[provider.NodeKey()] {
					nodes[provider.NodeKey()] = provider
				}
			}
			continue
		}

		nodeKey := node.NodeKey()

		// Handle conflicts between ambiguous nodes and individual providers
		if _, exists := nodes[nodeKey]; exists {
			// Prefer ambiguous nodes over individual providers for validation
			if _, isNewAmbiguous := node.(Ambiguous); isNewAmbiguous {
				nodes[nodeKey] = node
			}
			// If existing is ambiguous and new is individual, keep the ambiguous
			// If both are individual providers, keep the existing one
		} else {
			nodes[nodeKey] = node
		}
	}

	// Convert to slice and sort for deterministic ordering
	nodeSlice := make([]Node, 0, len(nodes))
	for _, node := range nodes {
		nodeSlice = append(nodeSlice, node)
	}

	// Sort by source position for deterministic ordering
	slices.SortFunc(nodeSlice, func(a, b Node) int {
		aPos := a.NodePosition()
		bPos := b.NodePosition()

		if aPos.Filename != bPos.Filename {
			return strings.Compare(aPos.Filename, bPos.Filename)
		}
		if aPos.Line != bPos.Line {
			return aPos.Line - bPos.Line
		}
		return aPos.Column - bPos.Column
	})

	return slices.Values(nodeSlice)
}

// IsRequired returns true if the node is required in the dependency graph.
func (i *IR) IsRequired(node Node) bool {
	return i.required[node.NodeKey()]
}

// Dependencies returns all dependencies of a node in the graph.
func (i *IR) Dependencies(node Node) []Node {
	var out []Node
	for _, require := range i.dependenciesForNode(node) {
		switch require := require.(type) {
		case NodeKey:
			out = append(out, i.nodes[require])
		case TypeKey:
			out = append(out, i.provides[require])
		}
	}
	return out
}

// AddNode to the dependency graph.
func (i *IR) AddNode(node Node) error {
	if _, ok := i.nodes[node.NodeKey()]; ok {
		return nil
	}

	// Track whether to add this node to the graph
	shouldAddToGraph := true

	// If the Node provides a type we add it to the provides map.
	providedType := node.NodeProvides()
	if !providedType.IsEmpty() {
		// Handle Provider nodes specially due to their complex multi/weak logic
		if provider, isProvider := node.(*Provider); isProvider {
			switch old := i.provides[providedType].(type) {
			case Multi:
				if !provider.Directive.Multi {
					return errors.Errorf("%s: there is an existing multi-provider for %s, cannot replace it with non-multi provider %s", node.NodePosition(), providedType, node.NodeKey())
				}
				i.provides[providedType] = append(old, provider)

			case *Provider:
				switch {
				case i.required[node.NodeKey()]:
					// Explicitly required provider wins
					delete(i.required, old.NodeKey())
					if old.Directive.Weak && !provider.Directive.Weak {
						i.defeated[old.NodeKey()] = true
					}
					i.provides[providedType] = provider
					if !provider.Directive.Weak {
						i.Require(node.NodeKey())
					}

				case i.required[old.NodeKey()]:
					// Explicitly required provider wins
					i.provides[providedType] = old
					if !old.Directive.Weak && provider.Directive.Weak {
						i.defeated[node.NodeKey()] = true
					}
					shouldAddToGraph = false

				case old.Directive.Weak && !provider.Directive.Weak:
					// Strong provider defeats weak provider
					delete(i.required, old.NodeKey())
					i.defeated[old.NodeKey()] = true
					i.provides[providedType] = provider
					if !provider.Directive.Weak {
						i.Require(node.NodeKey())
					}

				case !old.Directive.Weak && provider.Directive.Weak:
					// Keep strong provider, defeat weak provider
					i.provides[providedType] = old
					i.defeated[node.NodeKey()] = true
					shouldAddToGraph = false

				default:
					// Create an Ambiguous node instead of erroring
					i.provides[providedType] = Ambiguous{old, provider}
				}

			case Ambiguous:
				// Add to existing ambiguous node
				i.provides[providedType] = append(old, provider)

			case nil: // No old node.
				if provider.Directive.Multi {
					i.provides[providedType] = Multi{provider}
					// Don't auto-require multi-providers - they'll be required when their type is needed
				} else {
					i.provides[providedType] = provider
					if !provider.Directive.Weak {
						i.Require(node.NodeKey())
					}
				}

			default:
				// This shouldn't happen with current node types, but handle it gracefully
				return errors.Errorf("%s: unexpected node type conflict for %s", node.NodePosition(), providedType)
			}
		} else {
			// For all other node types (Intrinsic, Config, etc.), just set the provides mapping
			i.provides[providedType] = node
			// Configs are accessible by both value and pointer, so register both.
			if _, isConfig := node.(*Config); isConfig {
				i.provides["*"+providedType] = node
			}
		}
	}

	// Only add to graph if not defeated by a strong provider
	if shouldAddToGraph {
		i.nodes[node.NodeKey()] = node
		nodeKey := node.NodeKey()
		i.dependencies[nodeKey] = append(i.dependencies[nodeKey], node.NodeRequires()...)
		for _, key := range node.NodeRequiredBy() {
			i.dependencies[key] = append(i.dependencies[key], nodeKey)
		}
	}

	return nil
}

// validateAmbiguity checks for unresolved ambiguous nodes in the required graph
func (i *IR) validateAmbiguity() error {
	for node := range i.RequiredNodes() {
		if ambiguous, ok := node.(Ambiguous); ok {
			providers := make([]string, len(ambiguous))
			for j, p := range ambiguous {
				providers[j] = p.NodeKey().String()
			}
			key := normaliseTypeToTypeKey(ambiguous[0].Provides)
			return errors.Errorf("%s: conflicting providers for %s, use --resolve=%s to disambiguate",
				ambiguous[0].NodePosition(), key, strings.Join(providers, " or --resolve="))
		}
	}
	return nil
}

// Require marks a type or provider as required in the dependency graph.
func (i *IR) Require(root Key) {
	i.required[root] = true
}

func (i *IR) validate() error {
	// Validate that all roots exist (skip defeated nodes).
	for root := range i.required {
		if i.defeated[root] {
			continue
		}
		if i.lookup(root) == nil {
			return errors.Errorf("the required root node %s does not exist", root)
		}
	}

	// Validate that all required types exist (skip defeated nodes).
	nodeKeys := slices.Collect(maps.Keys(i.nodes))
	slices.SortStableFunc(nodeKeys, func(a, b Key) int { return strings.Compare(a.String(), b.String()) })
	for _, nodeKey := range nodeKeys {
		if i.defeated[nodeKey] {
			continue
		}
		node := i.nodes[nodeKey]
		keys := i.dependenciesForNode(node)
		slices.SortStableFunc(keys, func(a, b Key) int { return strings.Compare(a.String(), b.String()) })
		for _, require := range keys {
			if i.defeated[require] {
				continue
			}
			if i.lookup(require) == nil {
				return errors.Errorf("there is no provider for %s %s, required by %s", require.Kind(), require, node.NodeKey())
			}
		}
	}

	// Validate that all types with dependencies have providers.
	// This catches cases where subscriptions depend on receiver types without providers.
	dependencyKeys := slices.Collect(maps.Keys(i.dependencies))
	slices.SortStableFunc(dependencyKeys, func(a, b Key) int { return strings.Compare(a.String(), b.String()) })
	for _, depKey := range dependencyKeys {
		if _, isTypeKey := depKey.(TypeKey); isTypeKey {
			if i.lookup(depKey) == nil {
				// Find what depends on this type to provide better error message
				dependents := i.dependencies[depKey]
				if len(dependents) > 0 {
					dependent := dependents[0] // Use first dependent for error message
					return errors.Errorf("there is no provider for %s %s, required by %s", depKey.Kind(), depKey, dependent)
				}
			}
		}
	}

	if err := i.validateAmbiguity(); err != nil {
		return errors.WithStack(err)
	}
	return nil
}

// Recursively mark all nodes in the dependency graph from the current roots as required.
func (i *IR) propagate() error {
	queue := slices.Collect(maps.Keys(i.required))
	count := 0

	for len(queue) > 0 {
		count++
		if count > 500 {
			return errors.Errorf("dependency graph is too large")
		}
		// Mark key as required.
		key := queue[0]
		queue = queue[1:]
		i.Require(key)

		if i.defeated[key] {
			// Skip defeated weak providers
			continue
		}
		node := i.lookup(key)

		// When processing a TypeKey, also require the provider node that provides that type
		if _, isTypeKey := key.(TypeKey); isTypeKey && node != nil {
			// For Multi providers, require all individual providers in the Multi
			if multi, isMulti := node.(Multi); isMulti {
				for _, provider := range multi {
					if !i.required[provider.NodeKey()] {
						queue = append(queue, provider.NodeKey())
					}
				}
			} else if !node.NodeKey().IsGeneric() {
				nodeKey := node.NodeKey()
				if !i.required[nodeKey] {
					queue = append(queue, nodeKey)
				}
			}
		}

		for _, require := range i.dependenciesForNode(node) {
			if i.required[require] {
				continue
			}
			queue = append(queue, require)
		}
	}

	return nil
}

// Lookup "key" in the node map or type map, returning nil if not found.
func (i *IR) lookup(key Key) Node {
	switch key := key.(type) {
	case NodeKey:
		return i.nodes[key]
	case TypeKey:
		// Handle the generic case.
		keyStr := string(key)
		// The TypeKey will be the fully materialised type:
		// 	github.com/alecthomas/zero/providers/pubsub.Event[myapp.Event]
		// But the provider type will be:
		// 	github.com/alecthomas/zero/providers/pubsub.Event[?]
		// Where all type params are replaced by ?
		// This is a little bit janky, but I think it's okay.
		// Only apply generic substitution for actual generic types, not built-in Go types
		if typeParamStart := strings.Index(keyStr, "["); typeParamStart != -1 {
			// Don't apply generic substitution to built-in Go types like []T, map[K]V, chan T
			if !strings.HasPrefix(keyStr, "[]") && !strings.HasPrefix(keyStr, "map[") && !strings.HasPrefix(keyStr, "chan ") {
				count := strings.Count(keyStr[typeParamStart+1:], ",")
				keyStr = keyStr[:typeParamStart] + "[?" + strings.Repeat(", ?", count) + "]"
			}
		}
		return i.provides[TypeKey(keyStr)]
	}
	panic(fmt.Sprintf("unexpected Key type %T", key))
}

func (i *IR) dependenciesForNode(node Node) []Key {
	if node == nil {
		panic("node is nil")
	}
	return append(i.dependencies[node.NodeKey()], i.dependencies[node.NodeProvides()]...)
}

// Dest returns the destination package.
func (i *IR) Dest() *types.Package { return i.dest }

// Providers returns all required providers, including materialized generic providers.
func (i *IR) Providers() map[string][]*Provider { return i.providers }

// Configs returns all required configs, including materialized generic configs.
func (i *IR) Configs() map[string]*Config { return i.configs }

// APIs returns all required API endpoints.
func (i *IR) APIs() []*API { return i.apis }

// CronJobs returns all required cron jobs.
func (i *IR) CronJobs() []*CronJob { return i.cronJobs }

// Subscriptions returns all required subscriptions.
func (i *IR) Subscriptions() []*Subscription { return i.subscriptions }

// Middleware returns all required middleware.
func (i *IR) Middleware() []*Middleware { return i.middleware }

// materialize categorizes required nodes and materializes generic providers and configs.
func (i *IR) materialize() {
	i.providers = make(map[string][]*Provider)
	i.configs = make(map[string]*Config)
	i.apis = make([]*API, 0)
	i.cronJobs = make([]*CronJob, 0)
	i.middleware = make([]*Middleware, 0)
	i.subscriptions = make([]*Subscription, 0)

	genericConfigs := make(map[string]*Config)
	genericProviders := make(map[string]*Provider)

	for node := range i.RequiredNodes() {
		switch n := node.(type) {
		case *Provider:
			key := string(n.NodeProvides())
			if n.IsGeneric {
				baseType := getBaseTypeNameFromString(key)
				genericProviders[baseType] = n
			} else {
				i.providers[key] = append(i.providers[key], n)
			}
		case *Config:
			key := string(normaliseTypeToTypeKey(n.Type))
			if n.IsGeneric {
				baseType := getBaseTypeNameFromString(key)
				genericConfigs[baseType] = n
			} else {
				i.configs[key] = n
			}
		case *API:
			i.apis = append(i.apis, n)
		case *CronJob:
			i.cronJobs = append(i.cronJobs, n)
		case *Middleware:
			i.middleware = append(i.middleware, n)
		case *Subscription:
			i.subscriptions = append(i.subscriptions, n)
		}
	}

	// Materialize generic configs by examining non-generic provider parameter types.
	// Generic providers are skipped since their param types contain unresolved type params.
	materializedTypes := make(map[string]bool)
	for node := range i.RequiredNodes() {
		provider, ok := node.(*Provider)
		if !ok || provider.IsGeneric {
			continue
		}
		for _, paramType := range provider.Requires() {
			fullyQualifiedTypeStr := paramType.String()
			baseType := getBaseTypeNameFromString(fullyQualifiedTypeStr)
			genericConfig, exists := genericConfigs[baseType]
			if !exists {
				continue
			}
			configKey := normalizeConfigTypeString(paramType, i.dest)
			if materializedTypes[configKey] {
				continue
			}
			materializedConfig := &Config{
				Position:  genericConfig.Position,
				Package:   genericConfig.Package,
				Type:      paramType.(*types.Named),
				Directive: genericConfig.Directive,
			}
			i.configs[configKey] = materializedConfig
			materializedTypes[configKey] = true
		}
	}

	// Materialize generic providers by examining required types
	materializedProviders := make(map[string]bool)
	for node := range i.RequiredNodes() {
		for _, reqKey := range node.NodeRequires() {
			reqTypeStr := reqKey.String()
			if strings.Contains(reqTypeStr, "?") {
				continue
			}
			if _, exists := i.providers[reqTypeStr]; exists {
				continue
			}
			baseType := getBaseTypeNameFromString(reqTypeStr)
			genericProvider, exists := genericProviders[baseType]
			if !exists {
				continue
			}
			if materializedProviders[reqTypeStr] {
				continue
			}
			i.providers[reqTypeStr] = append(i.providers[reqTypeStr], genericProvider)
			materializedProviders[reqTypeStr] = true

			// Materialize generic configs needed by this generic provider.
			if genericProvider.TypeParams == nil {
				continue
			}
			subst := buildTypeParamSubst(genericProvider.TypeParams, reqTypeStr)
			for _, paramType := range genericProvider.Requires() {
				paramStr := paramType.String()
				baseType := getBaseTypeNameFromString(paramStr)
				genericConfig, exists := genericConfigs[baseType]
				if !exists {
					continue
				}
				// Substitute type params in the param type string
				concreteStr := substituteTypeParamsInString(paramStr, subst)
				if materializedTypes[concreteStr] {
					continue
				}
				// Resolve the prefix by substituting ${type} with the kebab-case type name.
				directive := genericConfig.Directive
				if directive != nil && strings.Contains(directive.Prefix, "${type}") {
					copied := *directive
					directive = &copied
					for _, concrete := range subst {
						typeName := concrete
						if dot := strings.LastIndex(typeName, "."); dot != -1 {
							typeName = typeName[dot+1:]
						}
						directive.Prefix = strings.ReplaceAll(directive.Prefix, "${type}", toKebabCase(typeName))
						break
					}
				}
				materializedConfig := &Config{
					Position:  genericConfig.Position,
					Package:   genericConfig.Package,
					Type:      genericConfig.Type, // Generic type — generator uses key not Type
					Directive: directive,
					IsGeneric: true, // Mark so generator knows to use key-based ref
				}
				i.configs[concreteStr] = materializedConfig
				materializedTypes[concreteStr] = true
			}
		}
	}
}

// ParseTypeRef parses a type reference string into a Ref.
//
// A type reference string is in the form [*]<pkg>.<type>, eg. *net/http.ServeMux
// For generic types like test.Topic[test.User], the split is at the last "."
// outside of brackets.
func (i *IR) ParseTypeRef(ref string) Ref {
	ptr := strings.HasPrefix(ref, "*")
	cut := lastDotOutsideBrackets(ref)
	if cut == -1 {
		panic(fmt.Sprintf("invalid type reference: %s", ref))
	}
	pkg := strings.TrimPrefix(ref[:cut], "*")
	typ := ref[cut+1:]
	// Resolve generic type arguments relative to dest package.
	typ = i.resolveTypeArgsRelativeToDest(typ)
	alias := i.ImportAlias(pkg)
	if pkg == i.dest.Path() {
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
func (i *IR) TypeRef(t types.Type) Ref {
	pointer := false
	if ptr, ok := t.(*types.Pointer); ok {
		pointer = true
		t = ptr.Elem()
	}

	var pkg, typeName string
	var imp, ref string

	if named, ok := t.(*types.Named); ok {
		if named.Obj().Pkg() != nil {
			pkg = named.Obj().Pkg().Path()
			typeName = named.Obj().Name()
			if typeArgs := named.TypeArgs(); typeArgs != nil && typeArgs.Len() > 0 {
				typeName += "["
				for j := range typeArgs.Len() {
					argType := typeArgs.At(j)
					argString := types.TypeString(argType, types.RelativeTo(i.dest))
					typeName += argString
					if j < typeArgs.Len()-1 {
						typeName += ", "
					}
				}
				typeName += "]"
			}
		} else {
			typeName = named.Obj().Name()
		}
	} else {
		typ := types.TypeString(t, types.RelativeTo(i.dest))
		typeName = typ
	}

	if pkg != "" {
		alias := i.ImportAlias(pkg)
		if alias != "" {
			imp = fmt.Sprintf("%s %q", alias, pkg)
			ref = alias + "." + typeName
		} else {
			if pkg == i.dest.Path() {
				ref = typeName
			} else {
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
func (i *IR) FunctionRef(fn *types.Func) Ref {
	name := fn.Name()
	pkg := fn.Pkg().Path()

	var imp, ref string
	if alias := i.ImportAlias(pkg); alias != "" {
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
func (i *IR) ImportAlias(pkg string) string {
	if pkg == i.dest.Path() {
		return ""
	}
	if _, isStdlib := stdlib[pkg]; isStdlib {
		return ""
	}
	aliasID := fnv.New64a()
	aliasID.Write([]byte(pkg))
	return fmt.Sprintf("imp%x", aliasID.Sum64())
}

// lastDotOutsideBrackets finds the last '.' in s that is not inside '[...]'.
func lastDotOutsideBrackets(s string) int {
	depth := 0
	last := -1
	for i, c := range s {
		switch c {
		case '[':
			depth++
		case ']':
			depth--
		case '.':
			if depth == 0 {
				last = i
			}
		}
	}
	return last
}

// resolveTypeArgsRelativeToDest takes a type name like Topic[test.User] and
// resolves type arguments relative to the dest package.
func (i *IR) resolveTypeArgsRelativeToDest(typ string) string {
	bracket := strings.Index(typ, "[")
	if bracket == -1 {
		return typ
	}
	base := typ[:bracket]
	argsStr := typ[bracket+1 : len(typ)-1] // strip [ and ]
	args := splitTypeArgs(argsStr)
	resolved := make([]string, len(args))
	for j, arg := range args {
		arg = strings.TrimSpace(arg)
		// If arg is fully qualified with dest package prefix, strip it
		if strings.HasPrefix(arg, i.dest.Path()+".") {
			resolved[j] = arg[len(i.dest.Path())+1:]
		} else if dot := lastDotOutsideBrackets(arg); dot != -1 {
			// External package — use ParseTypeRef recursively
			ref := i.ParseTypeRef(arg)
			resolved[j] = ref.Ref
		} else {
			resolved[j] = arg
		}
	}
	return base + "[" + strings.Join(resolved, ", ") + "]"
}

// splitTypeArgs splits "A, B[C, D]" into ["A", "B[C, D]"].
func splitTypeArgs(s string) []string {
	var args []string
	depth := 0
	start := 0
	for i, c := range s {
		switch c {
		case '[':
			depth++
		case ']':
			depth--
		case ',':
			if depth == 0 {
				args = append(args, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	args = append(args, strings.TrimSpace(s[start:]))
	return args
}

// buildTypeParamSubst builds a map from type parameter names to concrete type strings
// using the type params and a concrete type key like "pkg.Topic[pkg.UserCreatedEvent]".
func buildTypeParamSubst(typeParams *types.TypeParamList, concreteKey string) map[string]string {
	subst := make(map[string]string)
	bracket := strings.Index(concreteKey, "[")
	if bracket == -1 || typeParams == nil {
		return subst
	}
	argsStr := concreteKey[bracket+1 : len(concreteKey)-1]
	concreteArgs := splitTypeArgs(argsStr)
	for i := range typeParams.Len() {
		if i < len(concreteArgs) {
			subst[typeParams.At(i).Obj().Name()] = strings.TrimSpace(concreteArgs[i])
		}
	}
	return subst
}

// substituteTypeParamsInString replaces type parameter names inside brackets with concrete types.
func substituteTypeParamsInString(ts string, subst map[string]string) string {
	bracket := strings.Index(ts, "[")
	if bracket == -1 {
		return ts
	}
	base := ts[:bracket]
	argsStr := ts[bracket+1 : len(ts)-1]
	args := splitTypeArgs(argsStr)
	for i, arg := range args {
		arg = strings.TrimSpace(arg)
		if concrete, ok := subst[arg]; ok {
			args[i] = concrete
		}
	}
	return base + "[" + strings.Join(args, ", ") + "]"
}

func normaliseTypeToTypeKey(t types.Type) TypeKey {
	switch t := t.(type) {
	case *types.Pointer:
		return "*" + normaliseTypeToTypeKey(t.Elem())

	case *types.Basic:
		return TypeKey(t.String())

	case *types.Named:
		tp := ""
		if t.TypeParams() != nil {
			tp += "["
			i := 0
			for range t.TypeParams().TypeParams() {
				if i > 0 {
					tp += ", "
				}
				tp += "?"
				i++
			}
			tp += "]"
		}
		if t.Obj().Pkg() != nil {
			return TypeKey(t.Obj().Pkg().Path() + "." + t.Obj().Name() + tp)
		}
		return TypeKey(t.Obj().Name() + tp)

	case *types.Map:
		return "map[" + normaliseTypeToTypeKey(t.Key()) + "]" + normaliseTypeToTypeKey(t.Elem())

	case *types.Slice:
		return "[]" + normaliseTypeToTypeKey(t.Elem())

	default:
		panic(fmt.Sprintf("unsupported type %T", t))
	}
}
