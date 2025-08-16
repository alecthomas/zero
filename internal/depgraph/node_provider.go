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
	Requires []types.Type
	// IsGeneric indicates if this provider is a generic function
	IsGeneric bool
	// TypeParams holds the type parameters for generic providers
	TypeParams *types.TypeParamList
}

var _ Node = (*Provider)(nil)

func (p *Provider) node()                        {}
func (p *Provider) NodePosition() token.Position { return p.Position }
func (p *Provider) NodeKey() NodeKey             { return NodeKey(p.Function.FullName()) }
func (p *Provider) NodeType() types.Type         { return p.Provides }

func (p *Provider) NodeRequires() []Key {
	out := make([]Key, 0, len(p.Requires)+len(p.Directive.Require))
	for _, req := range p.Requires {
		out = append(out, TypeKey(req.String()))
	}
	for _, req := range p.Directive.Require {
		if strings.Contains(req, ".") {
			req = p.Package.PkgPath + "." + req
		}
		out = append(out, NodeKey(req))
	}
	return out
}
