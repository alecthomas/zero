package depgraph

import (
	"go/types"
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

type DAL struct{}

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

	graph := analyseTestCode(t, testCode, []string{"string"})

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
	assert.NotZero(t, authMiddleware)
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
	assert.NotZero(t, authWithRoleMiddleware)
	assert.Equal(t, []string{"admin", "moderator"}, authWithRoleMiddleware.Directive.Labels)
	assert.Equal(t, 1, len(authWithRoleMiddleware.Requires)) // Only DAL, not the string/int parameters

	// Check that DAL is required but not provided
	assert.Equal(t, 1, len(graph.Missing[authMiddleware.Function]))
	assert.Equal(t, "*test.DAL", types.TypeString(graph.Missing[authMiddleware.Function][0], nil))
	assert.Equal(t, 1, len(graph.Missing[authWithRoleMiddleware.Function]))
	assert.Equal(t, "*test.DAL", types.TypeString(graph.Missing[authWithRoleMiddleware.Function][0], nil))
}

func TestAnalyseMiddlewareWithInvalidLabelParameter(t *testing.T) {
	t.Parallel()
	testCode := `
package main

import (
	"net/http"
)

type DAL struct{}

//zero:middleware authenticated
func Auth(wrongName string, dal *DAL) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
}


`

	_, err := analyseTestCodeWithError(t, testCode, []string{"string"})
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


`

	graph := analyseTestCode(t, testCode, []string{"string"})

	// Should have 1 middleware
	assert.Equal(t, 1, len(graph.Middleware))

	mw := graph.Middleware[0]
	assert.Equal(t, "ComplexAuth", mw.Function.Name())
	assert.Equal(t, []string{"authenticated", "level"}, mw.Directive.Labels)
	assert.Equal(t, 2, len(mw.Requires)) // DAL and Logger, not the string/int parameters

	// Check that both DAL and Logger are required but not provided
	assert.Equal(t, 2, len(graph.Missing[mw.Function]))
	// Check the types are as expected
	missingTypes := make([]string, len(graph.Missing[mw.Function]))
	for i, missing := range graph.Missing[mw.Function] {
		missingTypes[i] = types.TypeString(missing, nil)
	}
	// Check if both required types are present
	foundDAL := false
	foundLogger := false
	for _, typeStr := range missingTypes {
		if typeStr == "*test.DAL" {
			foundDAL = true
		}
		if typeStr == "*test.Logger" {
			foundLogger = true
		}
	}
	assert.True(t, foundDAL)
	assert.True(t, foundLogger)
}

func TestAnalyseDirectMiddlewareNoLabelInjection(t *testing.T) {
	t.Parallel()
	testCode := `
package main

import (
	"net/http"
)

//zero:middleware cors
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		next.ServeHTTP(w, r)
	})
}
`

	graph := analyseTestCode(t, testCode, []string{"string"})

	// Should have 1 middleware
	assert.Equal(t, 1, len(graph.Middleware))

	mw := graph.Middleware[0]
	assert.Equal(t, "CORS", mw.Function.Name())
	assert.Equal(t, []string{"cors"}, mw.Directive.Labels)
	assert.Equal(t, 0, len(mw.Requires)) // Direct middleware has no dependencies

	// No missing dependencies
	assert.Equal(t, 0, len(graph.Missing[mw.Function]))
}

func TestAnalyseMiddlewareWithIntLabels(t *testing.T) {
	t.Parallel()
	testCode := `
package main

import (
	"net/http"
)

type Cache struct{}

//zero:middleware maxAge timeout
func CacheMiddleware(maxAge int, timeout int, cache *Cache) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
}


`

	graph := analyseTestCode(t, testCode, []string{"string"})

	// Should have 1 middleware
	assert.Equal(t, 1, len(graph.Middleware))

	mw := graph.Middleware[0]
	assert.Equal(t, "CacheMiddleware", mw.Function.Name())
	assert.Equal(t, []string{"maxAge", "timeout"}, mw.Directive.Labels)
	assert.Equal(t, 1, len(mw.Requires)) // Only Cache, not the int parameters

	// Check that Cache is required but not provided
	assert.Equal(t, 1, len(graph.Missing[mw.Function]))
	assert.Equal(t, "*test.Cache", types.TypeString(graph.Missing[mw.Function][0], nil))
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

	_, err := analyseTestCodeWithError(t, testCode, []string{"string"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parameter wrongName of type int in middleware CacheMiddleware must match a label name")
}
