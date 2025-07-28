# Zero

An opinionated tool for eliminating most of the boilerplate around constructing servers in Go.

Running `zero` on a codebase will generate a function that completely wires up a service from scratch, including request handlers, cron jobs, pubsub, etc.

A core tenet of Zero Services it that it will work with the normal Go development lifecycle, without any additional steps. Your code should build and be testable out of the box. Code generation is only required for full service construction, but even then it's possible to construct and test the service without code generation.

## Dependency injection

Any function annotated with `//zero:provider [weak] [multi]` will be used to provide its return type during application construction.

### Weak providers

Weak providers may be overridden by explicitly creating a non-weak provider, or explicitly selecting the provider to use via `--resolve`.

### Multi-providers

A multi-provider allows multiple providers to contribute to a single merged type value. The provided type must return a
slice or a map.

### Example

eg. The following code will inject a `*DAL` type and provide a `*Service` type.

```go
//zero:provider
func NewService(dal *DAL) (*Service, error) { ... }
```

This is somewhat similar to Google's Wire [project](https://github.com/google/wire).

## Configuration

A struct annotated with `//zero:config [prefix="<prefix>"]` will be used as embedded [Kong](https://github.com/alecthomas/kong)-annotated configuration, with corresponding config loading from JSON/YAML/HCL. These config structs can in turn be used during dependency injection.

## Routes

ZS will automatically generate `http.Handler` implementations for any method annotated with `//zero:api`, providing request decoding, response encoding, path variable decoding, query parameter decoding, and error handling.

```go
//zero:api [<method>] [<host>]/[<path>] [<label>[=<value>] ...]
func (s Struct) Method([pathVar0, pathVar1 string][, req Request]) ([<response>, ][error]) { ... }
```

`http.ServeMux` is used for routing and thus the pattern syntax is identical.

### Response encoding

Depending on the type of the <response> value, the response will be encoded in the following ways:

| Type | Encoding |
| ---- | -------- |
| `nil`/omitted | 204 No Content |
| `string` | `text/html` |
| `[]byte` | `application/octet-stream` |
| `io.Reader` | `application/octet-stream` |
| `io.ReadCloser` | `application/octet-stream` |
| `*http.Response` | Response structure is used as-is. |
| `http.Handler` | The response type's `ServeHTTP()` method will be called. |
| `*` | `application/json` |

Responses may optionally implement the interface `zero.StatusCode` to control the returned HTTP status code.

### Error responses

As with response bodies, if the returned error type implements `http.Handler`, its `ServeHTTP()` method will be called.

A default error handler may also be registered by creating a custom provider for `zero.ErrorHandler`.

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

## Middleware

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
//zero:middleware authenticated role
func Auth(role string, dal *DAL) func(http.Handler) http.Handler {
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
