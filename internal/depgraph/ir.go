package depgraph

import (
	"fmt"
	"go/token"
	"go/types"
	"iter"
	"maps"
	"slices"

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
	NodeKey() NodeKey
	// NodeType returns the type this node provides (if any).
	//
	// eg. for the cron.NewScheduler provider function this would return *cron.Scheduler.
	NodeType() types.Type
	// NodeRequires returns the types and providers this node requires
	//
	// Dependencies can be resolved either by [NodeKey], or by [TypeKey].
	NodeRequires() []Key
	// node is a sealed interface
	node()
}

// Key represesents either a [NodeKey] or a [TypeKey].
//
//sumtype:decl
type Key interface{ key() }

// NodeKey represents a unique identifier for a [Node] in the dependency graph.
type NodeKey string

func (NodeKey) key() {}

func NodeKeyForFunc(f *types.Func) NodeKey {
	rcv := f.Signature().Recv()
	return NodeKey(rcv.Pkg().Path() + "." + rcv.Name())
}

// TypeKey represents a unique identifier for a type in the dependency graph.
type TypeKey string

func (TypeKey) key() {}

// IR is the intermediate representation of the dependency graph
type IR struct {
	nodes    map[NodeKey]Node // All nodes by NodeID()
	provides map[TypeKey]Node // The Node providing the given type.
	required map[Key]bool     // Types and Nodes required by the user or other providers.
	selected map[NodeKey]bool // Selected providers for disambiguation
}

func NewIR(nodes []Node, options ...Option) (*IR, error) {
	i := &IR{
		nodes:    make(map[NodeKey]Node),
		provides: make(map[TypeKey]Node),
		required: make(map[Key]bool),
		selected: make(map[NodeKey]bool),
	}

	for _, node := range nodes {
		i.AddNode(node)
	}

	opts := &graphOptions{}
	for _, option := range options {
		option(opts)
	}
	for _, root := range opts.roots {
		i.Require(TypeKey(root))
	}
	for _, pick := range opts.pick {
		i.Select(NodeKey(pick))
	}

	if err := i.validate(); err != nil {
		return nil, errors.WithStack(err)
	}

	if err := i.propagate(); err != nil {
		return nil, errors.WithStack(err)
	}

	return i, nil
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
		for _, node := range i.nodes {
			if i.IsRequired(node) {
				if !yield(node) {
					return
				}
			}
		}
	}
}

// IsRequired returns true if the node is required in the dependency graph.
func (i *IR) IsRequired(node Node) bool {
	if nt := node.NodeType(); nt != nil {
		if _, ok := i.required[TypeKey(nt.String())]; ok {
			return true
		}
	}
	return i.selected[node.NodeKey()]
}

// Dependencies returns all dependencies of a node in the graph.
func (i *IR) Dependencies(node Node) []Node {
	var out []Node
	for _, require := range node.NodeRequires() {
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
	i.nodes[node.NodeKey()] = node
	t := node.NodeType()
	fmt.Println(node.NodeKey(), t)
	if t == nil {
		return nil
	}
	// If the Node provides a type we add it to the provides map.
	key := TypeKey(t.String())
	switch node := node.(type) {
	// If there are multi-providers we try to merge them.
	case *Provider:
		switch old := i.provides[key].(type) {
		case Multi:
			if !node.Directive.Multi {
				return errors.Errorf("%s: there is an existing multi-provider for %s, cannot replace it with non-multi provider %s", node.NodePosition(), key, node.NodeKey())
			}
			i.provides[key] = append(old, node)

		case nil: // No old node.
			if node.Directive.Multi {
				i.provides[key] = Multi{node}
			} else {
				i.provides[key] = node
			}

		default:
			// TODO: Handle weak Provider conflict resolution using i.selected
			return errors.Errorf("%s: conflicting providers for %s: %s and %s", node.NodePosition(), key, old.NodeKey(), node.NodeKey())
		}

	default:
		if existing, ok := i.provides[key]; ok {
			return errors.Errorf("%s: conflicting providers for %s: %s and %s", node.NodePosition(), key, existing.NodeKey(), node.NodeKey())
		}
		i.provides[key] = node
	}
	return nil
}

// Require marks a type as required in the dependency graph.
func (i *IR) Require(root TypeKey) {
	i.required[root] = true
}

// Select a provider to disambiguate a type in the dependency graph.
//
// This is typically used to disambiguate providers for interfaces, but can also be necessary for concrete types.
func (i *IR) Select(provider NodeKey) {
	i.selected[provider] = true
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
		for _, require := range node.NodeRequires() {
			if i.lookup(require) == nil {
				return errors.Errorf("there is no provider for %s, which is required by %s", require, node.NodeKey())
			}
		}
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
		i.required[key] = true
		node := i.lookup(key)
		// Pop the first element from the queue.
		copy(queue, queue[1:])
		queue = queue[:len(queue)-1]

		for _, require := range node.NodeRequires() {
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
		node, ok := i.nodes[key]
		if !ok {
			return nil
		}
		return node
	case TypeKey:
		return i.provides[key]
	}
	panic(fmt.Sprintf("unexpected Key type %T", key))
}
