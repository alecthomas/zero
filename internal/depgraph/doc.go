// Package depgraph collects types and functions with Zero annotations, determines the minimal closure of types and
// functions required to create a service graph for the application, and returns that graph.
//
// The package has the concept of "roots" and "picks". Roots are the root types in the graph, and any types those root
// types depend on will be included in the final graph. Picks are used to select a provider of a type if multiple
// providers satisfy that requirement. This is often the case for interface types, but can also be true of concrete
// types.
//
// The term "included" below refers to a node being marked as strong.
//
// The set of rules that are applied when pruning the service graph is:
//
//  1. Any providers NOT explicitly marked "weak" are included.
//  2. If a strong provider "require"'s a weak provider, that provider and its transitive dependencies are all marked
//     strong.
//  3. API, Cron and Subscription nodes always "require" their receiver type, and inherit the weakness of the receiver.
//  4. Any type explicitly specified as a root by the user is marked strong, as are all of its transitive dependencies.
//  5. Any provider "picked" by the user is marked strong, as are all of its transitive dependencies.
//  6. Weak multi providers are only included if they are explicitly required by a strong provider or picked by the
//     user.
//  7. Weak middleware is only included if explicitly specified as a root by the user.
//  8. If an API is included, `github.com/alecthomas/zero/providers/http.DefaultServer` is picked and `*net/http.Server`
//     included as a root.
//  9. If a Cron job is included, `github.com/alecthomas/zero/providers/cron.NewScheduler` is picked and
//     `*github.com/alecthomas/zero/providers/cron.Scheduler` included as a root.
//  10. If a Subscription is included, the concrete materialised type for
//     `github.com/alecthomas/zero/providers/pubsub.Topic[T]` is included as a root where `T` is the type of the
//     subscription event. There are multiple providers of `Topic[T]`, so the user will have to pick a provider.
package depgraph
