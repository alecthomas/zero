package depgraph

import (
	"go/token"
	"go/types"
	"path"
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
		out = append(out, NodeKey(resolveRequire(p.Package.PkgPath, req)))
	}
	// Note: Previously we required the type we provide to "activate method nodes",
	// but this created circular dependencies for multi-providers. Method node
	// activation is now handled during propagation in IR.propagate().
	return out
}

// resolveRequire resolves a require= value relative to the provider's package.
//
// Supported forms:
//   - "FuncName"           → <pkgPath>.FuncName  (same package)
//   - "sub.FuncName"       → <pkgPath>/sub.FuncName  (sub-package)
//   - "../other.FuncName"  → <parent>/other.FuncName  (relative)
//   - "github.com/.../pkg.FuncName"  → as-is  (fully qualified)
func resolveRequire(pkgPath, req string) string {
	dot := strings.LastIndex(req, ".")
	if dot == -1 {
		// No dot — same package symbol
		return pkgPath + "." + req
	}
	pkg := req[:dot]
	symbol := req[dot+1:]
	// If the package part contains "/" it's already fully qualified.
	if strings.Contains(pkg, "/") {
		return req
	}
	// Relative path: resolve against the current package.
	resolved := path.Join(pkgPath, pkg)
	return resolved + "." + symbol
}
