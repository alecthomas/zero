package depgraph

// Rules applied to the IR.
var Rules = []Rule{
	CronRule,
	APIRule,
}

// A Rule applies additional Node-specific logic to the IR.
//
// It is called after the first propagation pass of required nodes is applied.
type Rule func(ir *IR) error

// CronRule adds the Cron scheduler if any cron jobs are included.
func CronRule(ir *IR) error {
	for node := range ir.RequiredNodes() {
		if _, ok := node.(*CronJob); ok {
			ir.Require(TypeKey("*github.com/alecthomas/zero/providers/cron.Scheduler"))
			break
		}
	}
	return nil
}

// APIRule adds the HTTP server if any API nodes are included.
func APIRule(ir *IR) error {
	for node := range ir.RequiredNodes() {
		if _, ok := node.(*API); ok {
			ir.Require(TypeKey("*net/http.Server"))
			ir.Require(TypeKey("github.com/alecthomas/zero.ErrorEncoder"))
			ir.Require(TypeKey("github.com/alecthomas/zero.ResponseEncoder"))
			break
		}
	}
	return nil
}

// SubscriptionRule adds pubsub types to the dependency graph.
func SubscriptionRule(ir *IR) error {
	for node := range ir.RequiredNodes() {
		if _, ok := node.(*Subscription); ok {
			ir.Require(TypeKey("github.com/alecthomas/zero/providers/pubsub.Topic[?]"))
		}
	}
	return nil
}
