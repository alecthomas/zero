package depgraph

// A Rule applies additional Node-specific logic to the IR.
//
// It is called after propagation of "strong" nodes are applied.
type Rule func(ir *IR) error

// CronRule adds the Cron scheduler if any cron jobs are included.
func CronRule(ir *IR) error {
	for node := range ir.RequiredNodes() {
		if _, ok := node.(*CronJob); ok {
			ir.Require("github.com/alecthomas/zero/providers/cron.NewScheduler")
			break
		}
	}
	return nil
}

// APIRule adds the HTTP server if any API nodes are included.
func APIRule(ir *IR) error {
	for node := range ir.RequiredNodes() {
		if _, ok := node.(*API); ok {
			ir.Require("github.com/alecthomas/zero/providers/http.NewServer")
			break
		}
	}
	return nil
}
