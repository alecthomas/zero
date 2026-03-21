package depgraph_test

import (
	"os"
	"strings"
	"testing"

	"github.com/alecthomas/assert/v2"
	"github.com/alecthomas/zero/internal/buildtesting"
	"github.com/alecthomas/zero/internal/depgraph"
	"github.com/alecthomas/zero/internal/generator"
)

func analyseAndGenerate(t *testing.T, code string, options ...depgraph.Option) string {
	t.Helper()
	dir := buildtesting.Prepare(t, code)
	ir, err := depgraph.Analyse(t.Context(), dir, options...)
	assert.NoError(t, err)
	w, err := os.Create(dir + "/zero.go")
	assert.NoError(t, err)
	err = generator.Generate(w, ir)
	assert.NoError(t, err)
	_ = w.Close()
	// Read generated code for assertions
	data, err := os.ReadFile(dir + "/zero.go")
	assert.NoError(t, err)
	return string(data)
}

func TestSimpleProvider(t *testing.T) {
	t.Parallel()
	code := `package main

import "database/sql"

//zero:provider
func NewDB() *sql.DB { return nil }

var cli struct { ZeroConfig }
func main() {}
`
	generated := analyseAndGenerate(t, code, depgraph.WithTypes("*database/sql.DB"))
	assert.Contains(t, generated, "sql.DB")
}

func TestProviderWithDependency(t *testing.T) {
	t.Parallel()
	code := `package main

import "database/sql"

//zero:provider
func NewDB() *sql.DB { return nil }

type Service struct{ db *sql.DB }

//zero:provider
func NewService(db *sql.DB) *Service { return &Service{db: db} }

var cli struct { ZeroConfig }
func main() {}
`
	generated := analyseAndGenerate(t, code)
	assert.Contains(t, generated, "NewDB()")
	assert.Contains(t, generated, "NewService(")
}

func TestAPI(t *testing.T) {
	t.Parallel()
	code := `package main

type Service struct{}

//zero:provider
func NewService() *Service { return &Service{} }

//zero:api GET /hello
func (s *Service) Hello() string { return "hello" }

var cli struct { ZeroConfig }
func main() {}
`
	generated := analyseAndGenerate(t, code)
	assert.Contains(t, generated, `mux.Handle("GET /hello"`)
	assert.Contains(t, generated, "Hello()")
}

func TestCronJob(t *testing.T) {
	t.Parallel()
	code := `package main

import "context"

type Service struct{}

//zero:provider
func NewService() *Service { return &Service{} }

//zero:cron 5m
func (s *Service) Cleanup(ctx context.Context) error { return nil }

var cli struct { ZeroConfig }
func main() {}
`
	generated := analyseAndGenerate(t, code,
		depgraph.WithProviders(
			"github.com/alecthomas/zero/providers/cron.NewScheduler",
			"github.com/alecthomas/zero/providers/leases.NewMemoryLeaser",
		),
	)
	assert.Contains(t, generated, "cron.Register")
	assert.Contains(t, generated, "Cleanup")
}

func TestMultiProvider(t *testing.T) {
	t.Parallel()
	code := `package main

//zero:provider multi
func ProvideA() map[string]int { return map[string]int{"a": 1} }

//zero:provider multi
func ProvideB() map[string]int { return map[string]int{"b": 2} }

type Service struct{ m map[string]int }

//zero:provider
func NewService(m map[string]int) *Service { return &Service{m: m} }

var cli struct { ZeroConfig }
func main() {}
`
	generated := analyseAndGenerate(t, code)
	assert.Contains(t, generated, "maps.Copy")
}

func TestGenericProvider(t *testing.T) {
	t.Parallel()
	code := `package main

type Topic[T any] struct{}

//zero:provider
func NewTopic[T any]() Topic[T] { return Topic[T]{} }

type User struct{ Name string }

type Service struct{ topic Topic[User] }

//zero:provider
func NewService(topic Topic[User]) *Service { return &Service{topic: topic} }

var cli struct { ZeroConfig }
func main() {}
`
	generated := analyseAndGenerate(t, code, depgraph.WithRoots("*test.Service"))
	assert.Contains(t, generated, "NewTopic[User]()")
}

func TestConfig(t *testing.T) {
	t.Parallel()
	code := `package main

//zero:config
type Config struct { Addr string }

type Service struct{ cfg *Config }

//zero:provider
func NewService(cfg *Config) *Service { return &Service{cfg: cfg} }

var cli struct { ZeroConfig }
func main() {}
`
	generated := analyseAndGenerate(t, code)
	assert.Contains(t, generated, "ZeroConfig")
	assert.Contains(t, generated, "Config")
}

func TestMiddleware(t *testing.T) {
	t.Parallel()
	code := `package main

import "net/http"

type Service struct{}

//zero:provider
func NewService() *Service { return &Service{} }

//zero:api GET /hello
func (s *Service) Hello() string { return "hello" }

//zero:middleware
func Logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}

var cli struct { ZeroConfig }
func main() {}
`
	generated := analyseAndGenerate(t, code)
	assert.Contains(t, generated, "Logger(")
}

func TestMiddlewareWithLabels(t *testing.T) {
	t.Parallel()
	code := `package main

import "net/http"

type Service struct{}

//zero:provider
func NewService() *Service { return &Service{} }

//zero:api GET /admin authenticated role=admin
func (s *Service) Admin() string { return "admin" }

//zero:api GET /public
func (s *Service) Public() string { return "public" }

//zero:middleware authenticated role
func Auth(role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
}

var cli struct { ZeroConfig }
func main() {}
`
	generated := analyseAndGenerate(t, code)
	// Auth middleware should apply to /admin but not /public
	lines := strings.Split(generated, "\n")
	inAdmin := false
	inPublic := false
	adminHasAuth := false
	publicHasAuth := false
	for _, line := range lines {
		if strings.Contains(line, `"/admin"`) || strings.Contains(line, `"GET /admin"`) {
			inAdmin = true
			inPublic = false
		}
		if strings.Contains(line, `"/public"`) || strings.Contains(line, `"GET /public"`) {
			inPublic = true
			inAdmin = false
		}
		if strings.Contains(line, "Auth(") {
			if inAdmin {
				adminHasAuth = true
			}
			if inPublic {
				publicHasAuth = true
			}
		}
	}
	assert.True(t, adminHasAuth, "Auth middleware should apply to /admin")
	assert.False(t, publicHasAuth, "Auth middleware should not apply to /public")
}

func TestWeakProvider(t *testing.T) {
	t.Parallel()
	code := `package main

type Logger struct{}

//zero:provider weak
func DefaultLogger() Logger { return Logger{} }

//zero:provider
func CustomLogger() Logger { return Logger{} }

type Service struct{ l Logger }

//zero:provider
func NewService(l Logger) *Service { return &Service{l: l} }

var cli struct { ZeroConfig }
func main() {}
`
	generated := analyseAndGenerate(t, code)
	// Strong provider should win over weak
	assert.Contains(t, generated, "CustomLogger()")
	assert.NotContains(t, generated, "DefaultLogger()")
}

func TestWeakMultiProvider(t *testing.T) {
	t.Parallel()
	code := `package main

type Migrations []string

//zero:provider weak multi
func DefaultMigrations() Migrations { return Migrations{"default"} }

//zero:provider multi
func UserMigrations() Migrations { return Migrations{"user"} }

type Service struct{ m Migrations }

//zero:provider
func NewService(m Migrations) *Service { return &Service{m: m} }

var cli struct { ZeroConfig }
func main() {}
`
	generated := analyseAndGenerate(t, code)
	// Both accumulate: strong is auto-required, weak is pulled in because the type is needed
	assert.Contains(t, generated, "UserMigrations()")
	assert.Contains(t, generated, "DefaultMigrations()")
}

func TestEndToEndCompiles(t *testing.T) {
	t.Parallel()
	code := `package main

import (
	"context"
	"database/sql"
)

//zero:config
type Config struct { Addr string }

type Service struct{
	db  *sql.DB
	cfg *Config
}

//zero:provider
func NewDB() (*sql.DB, error) { return nil, nil }

//zero:provider
func NewService(db *sql.DB, cfg *Config) *Service {
	return &Service{db: db, cfg: cfg}
}

//zero:api GET /health
func (s *Service) Health() string { return "ok" }

//zero:cron 10m
func (s *Service) Cleanup(ctx context.Context) error { return nil }

var cli struct { ZeroConfig }
func main() {}
`
	generated := analyseAndGenerate(t, code,
		depgraph.WithProviders(
			"github.com/alecthomas/zero/providers/cron.NewScheduler",
			"github.com/alecthomas/zero/providers/leases.NewMemoryLeaser",
		),
	)
	assert.Contains(t, generated, "NewDB()")
	assert.Contains(t, generated, "NewService(")
	assert.Contains(t, generated, "Health()")
	assert.Contains(t, generated, "cron.Register")
}
