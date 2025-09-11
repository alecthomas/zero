package depgraph

import (
	"testing"

	"github.com/alecthomas/assert/v2"
)

func TestAnalyseMiddlewareWithLabelInjection(t *testing.T) {
	t.Parallel()
	testCode := `
package main

import (
	"net/http"
)

//zero:provider
func ProvideDAL() *DAL {
	return &DAL{}
}

type DAL struct{}

//zero:provider
func NewService() *Service { return &Service{} }

type Service struct {}

//zero:api GET /get authenticated
func (s *Service) Get() string {
	return "Hello"
}

//zero:api POST /admin admin moderator
func (s *Service) Post() string {
	return "Hello"
}

//zero:middleware authenticated
func Auth(authenticated string, dal *DAL) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
}

//zero:middleware admin moderator
func AuthWithRole(admin string, moderator int, dal *DAL) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
}


`

	graph := analyseTestCode(t, testCode)

	// Should have 2 middlewares
	assert.Equal(t, 2, len(graph.Middleware))

	// Find the Auth middleware
	var authMiddleware *Middleware
	for _, mw := range graph.Middleware {
		if mw.Function.Name() == "Auth" {
			authMiddleware = mw
			break
		}
	}
	assert.True(t, authMiddleware != nil)
	assert.Equal(t, []string{"authenticated"}, authMiddleware.Directive.Labels)
	assert.Equal(t, 1, len(authMiddleware.Requires)) // Only DAL, not the string parameter

	// Find the AuthWithRole middleware
	var authWithRoleMiddleware *Middleware
	for _, mw := range graph.Middleware {
		if mw.Function.Name() == "AuthWithRole" {
			authWithRoleMiddleware = mw
			break
		}
	}
	assert.True(t, authWithRoleMiddleware != nil)
	assert.Equal(t, []string{"admin", "moderator"}, authWithRoleMiddleware.Directive.Labels)
	assert.Equal(t, 1, len(authWithRoleMiddleware.Requires)) // Only DAL, not the string/int parameters
}

func TestAnalyseMiddlewareWithInvalidLabelParameter(t *testing.T) {
	t.Parallel()
	testCode := `
package main

import (
	"net/http"
)

type DAL struct{}

//zero:provider
func NewDAL() *DAL { return &DAL{} }

//zero:middleware authenticated
func Auth(wrongName string, dal *DAL) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
}


`

	_, err := analyseTestCodeWithError(t, testCode)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parameter wrongName of type string in middleware Auth must match a label name")
}

func TestAnalyseMiddlewareWithMixedParameters(t *testing.T) {
	t.Parallel()
	testCode := `
package main

import (
	"net/http"
)

//zero:provider
func ProvideString() string {
	return "test"
}

//zero:provider
func ProvideDAL() *DAL {
	return &DAL{}
}

//zero:provider
func ProvideLogger() *Logger {
	return &Logger{}
}

type DAL struct{}
type Logger struct{}

//zero:middleware authenticated level
func ComplexAuth(authenticated string, level int, dal *DAL, logger *Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
}

type Service struct {}

//zero:provider
func NewService() *Service { return &Service{} }

//zero:api POST /user authenticated="user" level=1
func (s *Service) CreateUser() error { return nil }

`

	graph := analyseTestCode(t, testCode, WithTypes("string"))

	// Should have 1 middleware
	assert.Equal(t, 1, len(graph.Middleware))

	mw := graph.Middleware[0]
	assert.Equal(t, "ComplexAuth", mw.Function.Name())
	assert.Equal(t, []string{"authenticated", "level"}, mw.Directive.Labels)
	assert.Equal(t, 2, len(mw.Requires)) // DAL and Logger, not the string/int parameters
}

func TestAnalyseMiddlewareWithInvalidIntParameter(t *testing.T) {
	t.Parallel()
	testCode := `
package main

import (
	"net/http"
)

//zero:middleware maxAge
func CacheMiddleware(wrongName int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
}
`

	_, err := analyseTestCodeWithError(t, testCode, WithTypes("string"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parameter wrongName of type int in middleware CacheMiddleware must match a label name")
}
