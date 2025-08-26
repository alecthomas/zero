package depgraph

import (
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
	// resolvedRequires holds the resolved requirements for generic providers
	// when they've been instantiated with concrete types
	resolvedRequires []types.Type
}

func (p *Provider) Requires() []types.Type {
	// For resolved generic providers, use the cached resolved requirements
	if p.resolvedRequires != nil {
		return p.resolvedRequires
	}

	// Otherwise, extract requirements from the function signature
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
func (p *Provider) NodeRequiredBy() []Key        { return []Key{TypeKey(p.Provides.String())} }
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
		if strings.Contains(req, ".") {
			req = p.Package.PkgPath + "." + req
		}
		out = append(out, NodeKey(req))
	}
	// Only non-weak providers require the type they provide to activate method nodes
	if !p.Directive.Weak {
		out = append(out, normaliseTypeToTypeKey(p.Provides))
	}
	return out
}
