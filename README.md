# Zero

An opinionated tool for simplifying building servers in Go.

Running `zero` on a codebase will generate a function that completely wires up a service from scratch, including request handlers, cron jobs, pubsub, databases, etc.

A core tenet of Zero Services it that it will work with the normal Go development lifecycle, without any additional steps. Your code should build and be testable out of the box. Code generation is only required for full service construction, but even then it's possible to construct and test the service without code generation. There's minimal lock-in with Zero, because your code is standard Go. The main exception to that is the request handlers, which remove request/response boilerplate.

## Request Handlers

Zero will automatically generate `http.Handler` implementations for any method annotated with `//zero:api`, providing request decoding, response encoding, path variable decoding, query parameter decoding, and error handling.

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

### OpenAPI Specification

Use `zero --openapi --openapi-title=TITLE --openapi-version=VERSION` to generate an OpenAPI spec for your service. Note that there are currently limitations around
fine-grained control of the generated spec', but the goal is to improve this as time permits.

<details>

<summary>eg. OpenAPI spec for the exemplar.</summary>

```bash
$ zero --openapi
{
  "swagger": "2.0",
  "info": {
    "title": "Zero API",
    "version": "1.0.0"
  },
  "paths": {
    "/users": {
      "get": {
        "tags": [
          "main"
        ],
        "responses": {
          "200": {
            "description": "Success",
            "schema": {
              "type": "array",
              "items": {
                "type": "object",
                "properties": {
                  "birthYear": {
                    "type": "integer"
                  },
                  "name": {
                    "type": "string"
                  }
                }
              }
            }
          },
          "400": {
            "description": "Bad Request"
          },
          "500": {
            "description": "Internal Server Error"
          }
        }
      },
      "post": {
        "tags": [
          "main"
        ],
        "parameters": [
          {
            "name": "body",
            "in": "body",
            "required": true,
            "schema": {
              "type": "object",
              "properties": {
                "birthYear": {
                  "type": "integer"
                },
                "name": {
                  "type": "string"
                }
              }
            }
          }
        ],
        "responses": {
          "204": {
            "description": "No Content"
          },
          "400": {
            "description": "Bad Request"
          },
          "500": {
            "description": "Internal Server Error"
          }
        }
      }
    },
    "/users/{id}": {
      "get": {
        "tags": [
          "main"
        ],
        "parameters": [
          {
            "type": "string",
            "name": "id",
            "in": "path",
            "required": true
          }
        ],
        "responses": {
          "200": {
            "description": "Success",
            "schema": {
              "type": "object",
              "properties": {
                "birthYear": {
                  "type": "integer"
                },
                "name": {
                  "type": "string"
                }
              }
            }
          },
          "400": {
            "description": "Bad Request"
          },
          "500": {
            "description": "Internal Server Error"
          }
        }
      }
    }
  }
}
```
</details>

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

## Configuration

A struct annotated with `//zero:config [prefix="<prefix>"]` will be used as embedded [Kong](https://github.com/alecthomas/kong)-annotated configuration, with corresponding config loading from JSON/YAML/HCL. These config structs can in turn be used during dependency injection.

The variable `${root}` contains the `lower-kebab-case` transformation of the type, and can be interpolated into `prefix`. This is useful for generic configuration to uniquely identify the flags.

eg. The following code will result in the following flags, one from each concrete `StorageConfig` type.

```
--storage-user-path=PATH
--storage-address-path=PATH
```

```go
//zero:config prefix="storage-${type}-"
type StorageConfig[T any] struct {
	Path string `help:"Path to the data root." required:""`
}

//zero:provider
func Storage(uconf StorageConfig[User], aconf StorageConfig[Address]) *Store { ... }
```


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

## Dependency injection

Any function annotated with `//zero:provider [weak] [multi] [require=<provider>,...]` will be used to provide its return type during application construction.

eg. The following code will inject a `*DAL` type and provide a `*Service` type.

```go
//zero:provider
func NewService(dal *DAL) (*Service, error) { ... }
```

This is somewhat similar to Google's Wire [project](https://github.com/google/wire).

### Weak providers

Weak providers are marked with `weak`, and may be overridden implicitly by creating a non-weak provider, or explicitly by selecting the provider to use via `--resolve`.

Weak providers are selected if any of the following conditions are true:

- They are the only provider of that type.
- They were explicitly selected by the user.
- They are injected by another provider via `require=<provider>`.

### Multi-providers

A multi-provider allows multiple providers to contribute to a single merged type value. The provided type must return a
slice or a map. Note that slice order is not guaranteed.

eg. In the following example the slice `[]string{"hello", "world"}` will be provided.

```go
//zero:provider multi
func Hello() []string { return []string{"hello"} }

//zero:provider multi
func World() []string { return []string{"world"} }
````

### Explicit dependencies

A weak provider may also explicitly request other weak dependencies be injected by using `require=<provider>`. This is useful when an injected parameter of the provider is itself reliant on an optional weak type.

eg. In this example the `SQLCron()` provider requires that the migrations provided by `CronSQLMigrations()` have already been applied to `*sql.DB`, which in turn requires `[]Migration`. By explicitly specifiying `require=CronSQLMigrations`, the previously ignored weak provider will be added.

```go
//zero:provider
func NewDB(config Config, migrations []Migration) *sql.DB { ... }

//zero:provider weak multi
func CronSQLMigrations() []Migration { ... }

//zero:provider weak require=CronSQLMigrations
func SQLCron(db *sql.DB) cron.Executor { ... }
````

## Builtin Providers

Zero ships with providers for a number of common use-cases, including SQL, logging, and so on.

### SQL

The SQL provider supports Postgres, MySQL, and SQLite out of the box, but can be extended at runtime. For each database,
it supports (re)creation of databases and migrations during development, and dumping of migration files for use with
production migration tooling.

There are a few steps that have to be followed to configure SQL support:

#### 1. Enable the driver in the build

By default drivers are excluded via Go build tags to reduce the dependencies for end-user builds. To enable a particular
driver use something like:

```bash
export GOFLAGS='--tags=postgres'
zero ./cmd/service
```

#### 2. Set the DSN for development

To set the default DSN for the configuration, pass the Kong option `kong.Vars{"sqldsn": "..."}`.

DSNs are URN-like, where the part after the schema is driver-specific. eg.

```
sqlite://file:boop?mode=memory
mysql://root:secret@tcp(localhost:3306)/zero
postgres://postgres:secret@localhost:5432/zero-test?sslmode=disable
```

#### 3. Provide migrations

Migrations are provided as a slice of Go `fs.FS` filesystems. Every `.sql` file in the root of each FS will be applied, with all files globally lexically ordered. Files across multiple migration filesystems must be globally unique.

Good practice is to name migration files something like:

```
<id>_<table>_<description>.sql
```

eg.

```
001_users_create.sql
```

Here's an example of providing migrations from an embedded FS (recommended):

```go
import zerosql "github.com/alecthomas/zero/providers/sql"

//go:embed migrations/*.sql
var migrations embed.FS

//zero:provider multi
func Migrations() zerosql.Migrations {
	sub, _ := fs.Sub(migrations, "migrations")
	return zerosql.Migrations{sub}
}
````

## Leases

Zero supports [leases](https://en.wikipedia.org/wiki/Lease_(computer_science)) for coordination. There are two implementations available, in-memory, and one based on SQL. The latter is intended to be robust in the face of failures and timeouts, and in particular has the property that if lease renewal fails, the process will be terminated. This ensures that split-brain cannot occur, but _can_ result in service outage of the database is unavailable. However, if the database is unavailable, your service is likely down anyway.

To use leases:

1. Inject the lease interface:

    ```go
    //zero:provider
    func NewService(leaser leases.Leaser) *Service { ... }
    ````

2. Select the lease implementation to use:

    ```bash
    zero --resolve github.com/alecthomas/zero/providers/leases.NewMemoryLeaser ./cmd/service
    ```

## Cron

A method annotated with `//zero:cron <schedule>` will be called on the given schedule. Schedules currently must be in the form `<n>[smhdw]`.

eg.

```go
//zero:cron 5s
func (s *Service) CheckUsers(ctx context.Context) error {
	// ...
	return nil
}
````

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

## Infrastructure (NOT IMPLEMENTED)

While the base usage of Zero doesn't deal with infrastructure at all, it would be possible to automatically extract required infrastructure and inject provisioned implementations of those into the injection graph as it is being constructed.

For example, if a service consumes `pubsub.Topic[T]` and there is no provider, one could be provided by an external provisioning plugin. The plugin could get called with the missing type, and return code that provides that type, as well as eg. Terraform for provisioning the infrastructure.

This is not thought out in detail, but the basic approach should work.
