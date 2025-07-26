# Zero

An opinionated tool for eliminating most of the boilerplate around constructing servers in Go.

Running `zero` on a codebase will generate a function that completely wires up a service from scratch, including request handlers, cron jobs, pubsub, etc.

A core tenet of Zero Services it that it will work with the normal Go development lifecycle, without any additional steps. Your code should build and be testable out of the box. Code generation is only required for full service construction, but even then it's possible to construct and test the service without code generation.

## Dependency injection

Any function annotated with `//zero:provider [weak]` will be used to provide its return type during application construction. Weak providers may be overridden by explicitly creating a non-weak provider, or explicitly selecting the provider to use via `--resolve`.

eg. The following code will inject a `*DAL` type and provide a `*Service` type.

```go
//zero:provider
func NewService(dal *DAL) (*Service, error) { ... }
```

This is somewhat similar to Google's Wire [project](https://github.com/google/wire).

## Configuration

A struct annotated with `//zero:config` will be used as embedded [Kong](https://github.com/alecthomas/kong)-annotated configuration, with corresponding config loading from JSON/YAML/HCL. These config structs can in turn be used during dependency injection.

## Routes

ZS will automatically generate `http.Handler` implementations for any method annotated with `//zero:api` providing JSON decoding/encoding, path variable decoding, and query parameter decoding. ZS will also generate a corresponding type-safe client for calling the endpoint, or possibly an OpenAPI schema.

```go
//zero:api [<method>] [<host>]/[<path>] [<label>[=<value>] ...]
func (s Struct) Method([pathVar0, pathVar1 string][, req Request]) ([<response>, ][error]) { ... }
```

`http.ServeMux` is used for routing and thus the pattern syntax is identical.

### Service Interfaces (NOT IMPLEMENTED)

Additionally, any user-defined interface matching a subset of API methods will have the service itself injected. That is, given the following service:

```go
//zero:api GET /users
func (s *Service) ListUsers() ([]User, error) { ... }

//zero:api POST /users authenticated
func (s *Service) CreateUser(ctx context.Context, user User) error { ... }
```

Injecting any of the following interfaces will result in the service being injected to fulfil the interface:

```go
interface {
  ListUsers() ([]User, error)
}

interface {
  CreateUser(ctx context.Context, user User) error
}

interface {
  ListUsers() ([]User, error)
  CreateUser(ctx context.Context, user User) error
}
```

This can be very useful for testing.

## PubSub (NOT IMPLEMENTED)

A method annotated with `//zero:subscribe` will result in the method being called whenever the corresponding pubsub topic receives an event. The PubSub implementation itself is described by the `zero.Topic[T]` interface, which may be injected in order to publish to a topic. A topic's payload type is used to uniquely identify that topic.

To cater to arbitrarily typed PubSub topics, a generic provider function may be declared that returns a generic `zero.Topic[T]`. This will be called during injection with the event type of a subscriber or publisher.

eg.

```go
//ftl:provider
func NewKafkaConnection(ctx context.Context, config KafkaConfig) (*kafka.Conn, error) {
  return kafka.DialContext(ctx, config.Network, config.Address)
}

//ftl:provider
func NewPubSubTopic[T any](ctx context.Context, conn *kafka.Conn) (zero.Topic[T], error) {
  // ...
}
```

## Cron (NOT IMPLEMENTED)

A method annotated with `//zero:cron <schedule>` will be called on the given schedule.

## Middleware (NOT IMPLEMENTED)

A function annotated with `//zero:middleware [<label>]` will be automatically used as HTTP middleware for any method matching the given `<label>` if provided, or applied globally if not. Option values can be retrieved from the request with `zero.HandlerOptions(r)`.

eg.

```go
//zero:middleware authenticated
func Auth(next http.Handler) http.Handler {
  return func(w http.ResponseWriter, r *http.Request) {
    auth := r.Header().Get("Authorization")
    // ...

}
```

Alternatively, for middleware that requires injection, the annotated middleware function can instead be one that *returns* a middleware function:

```go
//zero:middleware authenticated
func Auth(dal *DAL) zero.Middleware {
  return func(next http.Handler) http.Handler {
    return func(w http.ResponseWriter, r *http.Request) {
      auth := r.Header().Get("Authorization")
      // ...
    }
  }
}
```

## Infrastructure (NOT IMPLEMENTED)

While the base usage of Zero doesn't deal with infrastructure at all, it would be possible to automatically extract required infrastructure and inject provisioned implementations of those into the injection graph as it is being constructed.

For example, if a service consumes `pubsub.Topic[T]` and there is no provider, one could be provided by an external provisioning plugin. The plugin could get called with the missing type, and return code that provides that type, as well as eg. Terraform for provisioning the infrastructure.

This is not thought out in detail, but the basic approach should work.

## Example

```go
package app

type User struct {
}

type UserCreatedEvent User

//zero:config
type DatabaseConfig struct {
  DSN string `default:"postgres://localhost" help:"DSN for the service."`
}

//zero:provider
func NewDAL(config DatabaseConfig) (*DAL, error) {
  // ...
}

type Service struct {
  dal *DAL
}

//zero:provider
func NewService(dal *DAL) (*Service, error) {
  // Other initialisation
  return &Service{dal: dal}, nil
}

//zero:api GET /users
func (s *Service) ListUsers() ([]User, error) {
  // ...
}

//zero:api POST /users authenticated
func (s *Service) CreateUser(ctx context.Context, user User) error {
  // ...
}

//zero:subscribe
func (s *Service) OnUserCreated(user zero.Event[UserCreatedEvent]) error {
  // ...
}

//zero:cron 1h
func (s *Service) CheckUsers() error {
  // ...
}
```

Generates something like the following:

```go
type ApplicationConfig struct {
  Bind           string             `help:"Address to bind HTTP server to." default:"127.0.0.1:8080"`
  DatabaseConfig app.DatabaseConfig `embed:"" prefix:"database-"`
  KafkaConfig    app.KafkaConfig    `embed:"" prefix:"kafka-"`
}

// Start the application server.
func Start(ctx context.Context, config ApplicationConfig) error {
  dal, err := app.NewDAL(cli.DatabaseConfig)
  if err != nil {
    return fmt.Errorf("failed to construct DAL: %w", err)
  }
  svc, err := app.NewService(dal)
  if err != nil {
    return fmt.Errorf("failed to construct Service: %w", err)
  }
  pubSubConn, err := app.NewKafkaConnection(ctx, config.KafkaConfig)
  if err != nil {
    return fmt.Errorf("failed to construct KafkaPubSub: %w", err)
  }

  // Construct middleware
  authMiddleware, err := app.Auth(dal)
  if err != nil {
    return fmt.Errorf("failed to construct Auth middleware: %w", err)
  }

  // Initialise routing
  mux := http.NewServeMux()
  mux.HandleFunc("GET /users", func(w http.ResponseWriter, r *http.Request) {
    // Generated code to encode/decode request and call svc.ListUsers
  })
  mux.HandleFunc("POST /users", authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    // Generated code to encode/decode request and call svc.CreateUser
  })))

  // Initialise UserCreatedEvent topic.
  userCreatedTopic, err := app.NewTopic[UserCreatedEvent](ctx, pubSubConn)
  if err != nil {
    return fmt.Errorf("failed to create PubSub topic for UserCreatedEvent: %w", err)
  }

  err = userCreatedTopic.Subscribe(ctx, func (ctx context.Context, event zero.Event[UserCreatedEvent]) error {
    // Call svc.OnUserCreated with decoded payload.
  })
  if err != nil {
    return fmt.Errorf("failed to subscribe to user-created PubSub topic: %w", err)
  }

  // Initialise cron jobs
  err = zero.StartCron(ctx, "1h", func(ctx context.Context) error {
    return svc.CheckUsers()
  })
  if err != nil {
    return fmt.Errorf("failed to schedule CheckUsers cron job: %w", err)
  }

  // Start server.
  return http.ListenAndServe(config.Bind, mux)
}
```

Which you would then use from your own `main.go` like so:

```go
package main

import "github.com/alecthomas/kong"

var cli struct {
  ApplicationConfig // Generated

  // Other config
}

func main() {
  kctx := kong.Parse(&cli)
  err := app.Start(context.Background(), cli.ApplicationConfig) // Generated
  kctx.FatalIfErrorf(err, "failed to start service")
}
```
