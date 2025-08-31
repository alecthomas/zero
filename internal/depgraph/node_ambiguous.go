package depgraph

import "go/token"

// Ambiguous represents a set of ambiguous nodes present in the graph until culling either removes them, or they are
// resolved by the user.
type Ambiguous []*Provider

var _ Node = (Ambiguous)(nil)

func (a Ambiguous) NodeKey() Key                 { return a[0].NodeKey() }
func (a Ambiguous) NodePosition() token.Position { return a[0].NodePosition() }
func (a Ambiguous) NodeRequiredBy() []Key        { return nil }
func (a Ambiguous) NodeRequires() []Key          { return nil }
func (a Ambiguous) node()                        {}
