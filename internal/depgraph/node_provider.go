package depgraph

import (
	"fmt"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/packages"

	"github.com/alecthomas/zero/internal/directiveparser"
)

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
	// IsGeneric indicates if this provider is a generic function
	IsGeneric bool
	// TypeParams holds the type parameters for generic providers
	TypeParams *types.TypeParamList
	// MaterialisedTypeParams holds the materialised type parameters for generic providers.
	MaterialisedTypeParams []types.Type
}

func (p *Provider) Requires() []types.Type {
	sig := p.Function.Type().(*types.Signature)
	params := sig.Params()
	requiredTypes := make([]types.Type, params.Len())
	for i := range params.Len() {
		requiredTypes[i] = params.At(i).Type()
	}
	return requiredTypes
}

var _ Node = (*Provider)(nil)

func (p *Provider) node()                        {}
func (p *Provider) NodePosition() token.Position { return p.Position }
func (p *Provider) NodeKey() Key                 { return NodeKey(p.Function.FullName()) }
func (p *Provider) NodeRequiredBy() []Key        { return []Key{normaliseTypeToTypeKey(p.Provides)} }
func (p *Provider) NodeProvides() TypeKey        { return normaliseTypeToTypeKey(p.Provides) }
func (p *Provider) NodeRequires() []Key {
	requires := p.Requires()
	extraReqs := len(p.Directive.Require)
	if !p.Directive.Weak {
		extraReqs++ // Add one for the provided type
	}
	out := make([]Key, 0, len(requires)+extraReqs)
	for _, req := range requires {
		out = append(out, TypeKey(req.String()))
	}
	for _, req := range p.Directive.Require {
		if !strings.Contains(req, ".") {
			req = p.Package.PkgPath + "." + req
		}
		out = append(out, NodeKey(req))
	}
	// Note: Previously we required the type we provide to "activate method nodes",
	// but this created circular dependencies for multi-providers. Method node
	// activation is now handled during propagation in IR.propagate().
	return out
}

func (p *Provider) IsMaterialised() bool { return len(p.MaterialisedTypeParams) > 0 }

// Materialise the provider with the given type parameters.
func (p Provider) Materialise(params ...types.Type) *Provider {
	if p.TypeParams.Len() != len(params) {
		panic(fmt.Sprintf("%s: expected %d type parameters, got %d", p.NodeKey(), p.TypeParams.Len(), len(params)))
	}
	p.MaterialisedTypeParams = params
	return &p
}
