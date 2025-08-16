package depgraph

import (
	"go/token"
	"go/types"
)

// Multi represents a collection of multi [Provider]s.
type Multi []*Provider

var _ Node = (Multi)(nil)

func (m Multi) node()                        {}
func (m Multi) NodePosition() token.Position { return m[0].NodePosition() }
func (m Multi) NodeKey() NodeKey             { return m[0].NodeKey() }
func (m Multi) NodeRequires() []Key          { return nil }
func (m Multi) NodeType() types.Type         { return m[0].NodeType() }
