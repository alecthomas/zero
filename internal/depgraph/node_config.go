package depgraph

import (
	"go/token"
	"go/types"

	"golang.org/x/tools/go/packages"

	"github.com/alecthomas/zero/internal/directiveparser"
)

// Config represents command-line/file configuration. Config structs are annotated like so:
//
//	//zero:config [prefix="<prefix>"]
type Config struct {
	// Position of the type declaration.
	Position  token.Position
	Package   *packages.Package
	Type      *types.Named
	Directive *directiveparser.DirectiveConfig
	// IsGeneric indicates if this config is a generic type
	IsGeneric bool
	// TypeParams holds the type parameters for generic configs
	TypeParams *types.TypeParamList
}

var _ Node = (*Config)(nil)

func (c *Config) node()                        {}
func (c *Config) NodePosition() token.Position { return c.Position }
func (c *Config) NodeKey() NodeKey             { return NodeKey(c.Package.PkgPath + "." + c.Type.Obj().Name()) }
func (c *Config) NodeType() types.Type         { return c.Type }
func (c *Config) NodeRequires() []Key          { return nil }
