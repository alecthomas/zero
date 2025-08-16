package depgraph

import (
	"go/token"
	"go/types"

	"golang.org/x/tools/go/packages"

	"github.com/alecthomas/zero/internal/directiveparser"
)

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

var _ Node = (*Middleware)(nil)

func (m *Middleware) node()                        {}
func (m *Middleware) NodePosition() token.Position { return m.Position }
func (m *Middleware) NodeKey() NodeKey             { return NodeKey(m.Function.FullName()) }
func (m *Middleware) NodeType() types.Type         { return nil }
func (m *Middleware) NodeRequires() []Key {
	out := make([]Key, len(m.Requires))
	for i, req := range m.Requires {
		out[i] = TypeKey(req.String())
	}
	return out
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
