package depgraph

import (
	"fmt"
	"go/token"
	"go/types"
	"iter"
	"maps"
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
	// node is a sealed interface
	node()
}

// Key represesents either a [NodeKey] or a [TypeKey].
//
//sumtype:decl
type Key interface {
	Kind() string
	String() string
	key()
}

// NodeKey represents a unique identifier for a [Node] in the dependency graph.
type NodeKey string

func (NodeKey) Kind() string     { return "node" }
func (n NodeKey) String() string { return string(n) }
func (NodeKey) key()             {}

// TypeKeyForReceiver returns the [TypeKey] for the receiver of a method.
func TypeKeyForReceiver(f *types.Func) TypeKey {
	rcv := f.Signature().Recv()
	return TypeKey(rcv.Pkg().Path() + "." + rcv.Name())
}

// TypeKey represents a unique identifier for a type in the dependency graph.
type TypeKey string

func (TypeKey) Kind() string     { return "type" }
func (t TypeKey) String() string { return string(t) }
func (TypeKey) key()             {}

// IR is the intermediate representation of the dependency graph
type IR struct {
	nodes             map[Key]Node     // All nodes by their NodeKey
	provides          map[TypeKey]Node // The Node providing the given type.
	required          map[Key]bool     // Types and Nodes required by the user or other providers.
	extraDependencies map[Key][]Key    // Where the key dependes on the values.
}

func NewIR(nodes []Node, options ...Option) (*IR, error) {
	i := &IR{
		nodes:             make(map[Key]Node),
		provides:          make(map[TypeKey]Node),
		required:          make(map[Key]bool),
		extraDependencies: make(map[Key][]Key),
	}
	opts := &graphOptions{}
	for _, option := range options {
		if err := option(opts); err != nil {
			return nil, errors.WithStack(err)
		}
	}
	// TODO: Migrate these
	for _, root := range opts.roots {
		i.Require(TypeKey(root))
	}
	for _, pick := range opts.pick {
		i.Require(NodeKey(pick))
	}

	for _, node := range nodes {
		if err := i.AddNode(node); err != nil {
			return nil, errors.WithStack(err)
		}
	}

	if err := i.validate(); err != nil {
		return nil, errors.WithStack(err)
	}

	if err := i.propagate(); err != nil {
		return nil, errors.WithStack(err)
	}

	for _, rule := range Rules {
		if err := rule(i); err != nil {
			return nil, errors.WithStack(err)
		}
	}

	if err := i.validate(); err != nil {
		return nil, errors.WithStack(err)
	}

	if err := i.propagate(); err != nil {
		return nil, errors.WithStack(err)
	}

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
	for key, deps := range i.extraDependencies {
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
	return func(yield func(Node) bool) {
		for key := range i.required {
			node := i.lookup(key)
			if !yield(node) {
				return
			}
		}
	}
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
		return errors.Errorf("%s: %s %s already exists in the graph", node.NodePosition(), node.NodeKey().Kind(), node.NodeKey())
	}
	i.nodes[node.NodeKey()] = node
	for _, key := range node.NodeRequiredBy() {
		i.extraDependencies[key] = append(i.extraDependencies[key], node.NodeKey())
	}
	// If the Node provides a type we add it to the provides map.
	switch node := node.(type) {
	// If there are multi-providers we try to merge them.
	case *Provider:
		key := normaliseTypeToTypeKey(node.Provides)
		switch old := i.provides[key].(type) {
		case Multi:
			if !node.Directive.Multi {
				return errors.Errorf("%s: there is an existing multi-provider for %s, cannot replace it with non-multi provider %s", node.NodePosition(), key, node.NodeKey())
			}
			if old[0].Directive.Weak != node.Directive.Weak {
				return errors.Errorf("%s: cannot mix weak and non-weak providers for %s", node.NodePosition(), key)
			}
			i.provides[key] = append(old, node)

		case *Provider:
			// Check which, if any, of the new and old nodes are in i.selected and use them if they are.
			switch {
			case i.required[node.NodeKey()] || (old.Directive.Weak && !node.Directive.Weak):
				delete(i.required, node.NodeKey())
				i.provides[key] = node
				if !node.Directive.Weak {
					i.Require(node.NodeKey())
				}
			case i.required[old.NodeKey()] || (!old.Directive.Weak && node.Directive.Weak):
				i.provides[key] = old
			default:
				// Create an Ambiguous node instead of erroring
				i.provides[key] = Ambiguous{old, node}
			}

		case Ambiguous:
			// Add to existing ambiguous node
			i.provides[key] = append(old, node)

		case nil: // No old node.
			if node.Directive.Multi {
				i.provides[key] = Multi{node}
			} else {
				i.provides[key] = node
			}
			if !node.Directive.Weak {
				i.Require(node.NodeKey())
			}

		default:
			// This shouldn't happen with current node types, but handle it gracefully
			return errors.Errorf("%s: unexpected node type conflict for %s", node.NodePosition(), key)
		}

	case *Config:
		i.provides[normaliseTypeToTypeKey(node.Type)] = node
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
	// Validate that all roots exist
	for root := range i.required {
		if i.lookup(root) == nil {
			return errors.Errorf("the required root node %s does not exist", root)
		}
	}

	// Validate that all required types exist.
	for _, node := range i.nodes {
		for _, require := range i.dependenciesForNode(node) {
			if t, ok := require.(TypeKey); ok && t == "context.Context" {
				continue
			}
			if i.lookup(require) == nil {
				return errors.Errorf("there is no provider for %s %s, required by %s", require.Kind(), require, node.NodeKey())
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
		if count > 10000 {
			return errors.Errorf("dependency graph is too large")
		}
		// Mark key as required.
		key := queue[0]
		i.Require(key)
		queue = queue[1:]

		deps := i.dependenciesForNode(i.lookup(key))
		for _, require := range deps {
			if i.required[require] {
				continue
			}
			queue = append(queue, require)
		}

		// Also mark any nodes that depend on this key as required
		if extraDeps := i.extraDependencies[key]; len(extraDeps) > 0 {
			for _, dep := range extraDeps {
				if !i.required[dep] {
					queue = append(queue, dep)
				}
			}
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
		if typeParamStart := strings.Index(keyStr, "["); typeParamStart != -1 {
			count := strings.Count(keyStr[typeParamStart+1:], ",")
			keyStr = keyStr[:typeParamStart] + "[?" + strings.Repeat(", ?", count) + "]"
		}
		return i.provides[TypeKey(keyStr)]
	}
	panic(fmt.Sprintf("unexpected Key type %T", key))
}

func (i *IR) dependenciesForNode(node Node) []Key {
	extra := i.extraDependencies[node.NodeKey()]
	return append(node.NodeRequires(), extra...)
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
