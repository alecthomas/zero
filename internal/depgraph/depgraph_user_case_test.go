package depgraph

import (
	"go/types"
	"testing"

	"github.com/alecthomas/assert/v2"
)

func TestAnalyseMiddlewareExactUserCase(t *testing.T) {
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
			// Use authenticated string value and dal dependency
			next.ServeHTTP(w, r)
		})
	}
}
`

	graph := analyseTestCode(t, testCode, []string{"string"})

	// Should have 1 middleware
	assert.Equal(t, 1, len(graph.Middleware))

	authMiddleware := graph.Middleware[0]
	assert.Equal(t, "Auth", authMiddleware.Function.Name())
	assert.Equal(t, []string{"authenticated"}, authMiddleware.Directive.Labels)

	// Should have exactly 1 dependency: *DAL
	// The 'authenticated' string parameter should NOT be a dependency since it's a label
	assert.Equal(t, 1, len(authMiddleware.Requires))
	assert.Equal(t, "*test.DAL", types.TypeString(authMiddleware.Requires[0], nil))

	// DAL should be missing since no provider exists
	assert.Equal(t, 1, len(graph.Missing[authMiddleware.Function]))
	assert.Equal(t, "*test.DAL", types.TypeString(graph.Missing[authMiddleware.Function][0], nil))
}

func TestAnalyseMiddlewareWithMultipleLabelsAndDependencies(t *testing.T) {
	testCode := `
package main

import (
	"net/http"
)

type DAL struct{}
type Logger struct{}
type Cache struct{}

//zero:middleware role level authenticated
func ComplexAuth(role string, level int, authenticated string, dal *DAL, logger *Logger, cache *Cache) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Use role, level, authenticated label values and dal, logger, cache dependencies
			next.ServeHTTP(w, r)
		})
	}
}
`

	graph := analyseTestCode(t, testCode, []string{"string"})

	// Should have 1 middleware
	assert.Equal(t, 1, len(graph.Middleware))

	middleware := graph.Middleware[0]
	assert.Equal(t, "ComplexAuth", middleware.Function.Name())
	assert.Equal(t, []string{"role", "level", "authenticated"}, middleware.Directive.Labels)

	// Should have exactly 3 dependencies: *DAL, *Logger, *Cache
	// The string/int parameters should NOT be dependencies since they're labels
	assert.Equal(t, 3, len(middleware.Requires))

	// Check all required types are present
	requiredTypes := make([]string, len(middleware.Requires))
	for i, req := range middleware.Requires {
		requiredTypes[i] = types.TypeString(req, nil)
	}

	expectedTypes := []string{"*test.DAL", "*test.Logger", "*test.Cache"}
	for _, expected := range expectedTypes {
		found := false
		for _, actual := range requiredTypes {
			if actual == expected {
				found = true
				break
			}
		}
		assert.True(t, found, "Expected to find required type %s", expected)
	}

	// All dependencies should be missing since no providers exist
	assert.Equal(t, 3, len(graph.Missing[middleware.Function]))
}
