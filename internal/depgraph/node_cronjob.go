package depgraph

import (
	"go/token"
	"go/types"

	"golang.org/x/tools/go/packages"

	"github.com/alecthomas/zero/internal/directiveparser"
)

// CronJob represents a cron job method in the graph.
//
//	//zero:cron <schedule>
type CronJob struct {
	// Position is the position of the function declaration.
	Position token.Position
	// Schedule is the parsed cron schedule directive
	Schedule *directiveparser.DirectiveCron
	// Function is the function that handles the cron job
	Function *types.Func
	// Package is the package that contains the function
	Package *packages.Package
}

var _ Node = (*CronJob)(nil)

func (c *CronJob) node()                        {}
func (c *CronJob) NodePosition() token.Position { return c.Position }
func (c *CronJob) NodeKey() NodeKey             { return NodeKey(c.Function.FullName()) }
func (c *CronJob) NodeType() types.Type         { return nil }

func (c *CronJob) NodeRequires() []Key {
	return []Key{
		NodeKey(c.Function.Signature().Recv().Type().String()),
	}
}
