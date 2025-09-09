package depgraph

// Rules applied to the IR.
var Rules = []Rule{
	ProviderActivationRule,
	CronRule,
	APIRule,
	SubscriptionRule,
	MiddlewareRule,
}

// A Rule applies additional Node-specific logic to the IR.
//
// It is called after the first propagation pass of required nodes is applied.
type Rule func(ir *IR) error

// ProviderActivationRule ensures that when a provider is required, the type it provides is also required.
// This activates method nodes (API, middleware, etc.) that depend on the provided type.
func ProviderActivationRule(ir *IR) error {
	for node := range ir.RequiredNodes() {
		if provider, ok := node.(*Provider); ok {
			providedType := normaliseTypeToTypeKey(provider.Provides)
			ir.Require(providedType)
		}
	}
	// Propagate the newly required types to activate dependent nodes
	return ir.propagate()
}

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

// MiddlewareRule adds middleware functions to the dependency graph.
//
// Middleware is required iff it has no label OR any label matches an API endpoint with the same label.
func MiddlewareRule(ir *IR) error {
	foundAPI := false

	// Collect all required labels from API endpoints
	requiredLabels := map[string]bool{}
	apiCount := 0
	for node := range ir.RequiredNodes() {
		if node, ok := node.(*API); ok {
			foundAPI = true
			apiCount++
			for _, label := range node.Directive.Labels {
				requiredLabels[label.Name] = true
			}
		}
	}

	if !foundAPI {
		return nil
	}

	// Next, mark all middleware with matching labels or no labels as required
	for node := range ir.Nodes() {
		if node, ok := node.(*Middleware); ok {
			if len(node.Directive.Labels) == 0 {
				ir.Require(node.NodeKey())
			} else {
				for _, label := range node.Directive.Labels {
					if requiredLabels[label] {
						ir.Require(node.NodeKey())
						break
					}
				}
			}
		}
	}
	return nil
}
