package depgraph

import (
	"fmt"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/packages"
)

type Subscription struct {
	// Position is the position of the function declaration.
	Position token.Position
	// Function is the function that handles the subscription
	Function *types.Func
	// Package is the package that contains the function
	Package *packages.Package
	// TopicType is the event type extracted from pubsub.Event[T]
	TopicType types.Type
}

var _ Node = (*Subscription)(nil)

func (s *Subscription) node()                        {}
func (s *Subscription) NodePosition() token.Position { return s.Position }
func (s *Subscription) NodeKey() NodeKey             { return NodeKey(s.Function.FullName()) }
func (s *Subscription) NodeType() types.Type         { return nil }
func (s *Subscription) NodeRequires() []Key {
	return []Key{
		NodeKeyForFunc(s.Function),
		TypeKey(fmt.Sprintf("github.com/alecthomas/zero/providers/pubsub.Topic[%s]", s.TopicType.String())),
	}
}
