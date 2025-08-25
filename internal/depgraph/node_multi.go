package depgraph

import (
	"go/token"
)

// Multi represents a collection of multi [Provider]s.
type Multi []*Provider

var _ Node = (Multi)(nil)

func (m Multi) node()                        {}
func (m Multi) NodePosition() token.Position { return m[0].NodePosition() }
func (m Multi) NodeKey() Key                 { return m[0].NodeKey() }
func (m Multi) NodeRequiredBy() []Key {
	var combined []Key
	for _, p := range m {
		combined = append(combined, p.NodeRequiredBy()...)
	}
	return combined
}
func (m Multi) NodeRequires() []Key {
	var combined []Key
	for _, p := range m {
		combined = append(combined, p.NodeRequires()...)
	}
	return combined
}
