package depgraph

import "go/token"

// An Intrinsic [Node] represents a built-in type, such as [context.Context].
type Intrinsic struct {
	Key TypeKey
}

var _ Node = (*Intrinsic)(nil)

func (i *Intrinsic) NodeKey() Key                 { return i.Key }
func (i *Intrinsic) NodePosition() token.Position { return token.Position{} }
func (i *Intrinsic) NodeRequiredBy() []Key        { return nil }
func (i *Intrinsic) NodeRequires() []Key          { return nil }
func (i *Intrinsic) node()                        {}
