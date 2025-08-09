package depgraph

import (
	"testing"

	"github.com/alecthomas/assert/v2"
)

func TestPruneUnusedMiddleware(t *testing.T) {
	t.Parallel()
	testCode := `
package main

import (
	"net/http"
)

type Service struct{}

//zero:provider
func NewService() *Service {
	return &Service{}
}

// Global middleware - no labels, should always be kept
//zero:middleware
func GlobalMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return next
	}
}

// Middleware with labels that match API endpoints
//zero:middleware authenticated
func AuthMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return next
	}
}

//zero:middleware admin
func AdminMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return next
	}
}

// Middleware with labels that don't match any API endpoints - should be pruned
//zero:middleware unused
func UnusedMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return next
	}
}

//zero:middleware orphaned special
func OrphanedMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return next
	}
}

// API endpoints with labels
//zero:api GET /public
func (s *Service) PublicEndpoint() {}

//zero:api POST /users authenticated
func (s *Service) CreateUser() {}

//zero:api DELETE /admin/users authenticated admin
func (s *Service) DeleteUser() {}
`

	graph := analyseTestCode(t, testCode, []string{"*test.Service"})

	// Should have 3 APIs
	assert.Equal(t, 3, len(graph.APIs))

	// Should have pruned unused middleware
	// Expected: GlobalMiddleware, AuthMiddleware, AdminMiddleware (3 total)
	// Should be pruned: UnusedMiddleware, OrphanedMiddleware
	assert.Equal(t, 3, len(graph.Middleware))

	middlewareNames := make(map[string]bool)
	for _, mw := range graph.Middleware {
		middlewareNames[mw.Function.Name()] = true
	}

	// Should keep these middleware
	assert.True(t, middlewareNames["GlobalMiddleware"], "Global middleware should be kept")
	assert.True(t, middlewareNames["AuthMiddleware"], "Auth middleware should be kept (used by 2 APIs)")
	assert.True(t, middlewareNames["AdminMiddleware"], "Admin middleware should be kept (used by 1 API)")

	// Should prune these middleware
	assert.False(t, middlewareNames["UnusedMiddleware"], "Unused middleware should be pruned")
	assert.False(t, middlewareNames["OrphanedMiddleware"], "Orphaned middleware should be pruned")
}

func TestPruneMiddlewareWithMultipleLabels(t *testing.T) {
	t.Parallel()
	testCode := `
package main

import (
	"net/http"
)

type Service struct{}

//zero:provider
func NewService() *Service {
	return &Service{}
}

// Middleware with multiple labels - should be kept if ANY label matches
//zero:middleware cache timeout
func CacheMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return next
	}
}

// Middleware with multiple labels - should be pruned if NONE match
//zero:middleware special custom
func SpecialMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return next
	}
}

// API that matches one label from CacheMiddleware
//zero:api GET /data cache=300
func (s *Service) GetData() {}
`

	graph := analyseTestCode(t, testCode, []string{"*test.Service"})

	// Should have 1 API
	assert.Equal(t, 1, len(graph.APIs))

	// Should have 1 middleware (CacheMiddleware kept, SpecialMiddleware pruned)
	assert.Equal(t, 1, len(graph.Middleware))

	mw := graph.Middleware[0]
	assert.Equal(t, "CacheMiddleware", mw.Function.Name())
	assert.Equal(t, []string{"cache", "timeout"}, mw.Directive.Labels)
}

func TestKeepAllMiddlewareWhenNoAPIs(t *testing.T) {
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

//zero:middleware test
func TestMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return next
	}
}

//zero:middleware unused
func UnusedMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return next
	}
}
`

	graph := analyseTestCode(t, testCode, []string{"string"})

	// When there are no APIs, all middleware should be kept
	assert.Equal(t, 2, len(graph.Middleware))
}

func TestMiddlewarePruningEdgeCases(t *testing.T) {
	t.Parallel()
	testCode := `
package main

import (
	"net/http"
)

type Service struct{}

//zero:provider
func NewService() *Service {
	return &Service{}
}

// Middleware with empty label (should be treated as global)
//zero:middleware
func EmptyLabelMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return next
	}
}

// Middleware with label that exactly matches API label
//zero:middleware exact
func ExactMatchMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return next
	}
}

// Middleware with label that partially matches (shouldn't match)
//zero:middleware exactmatch
func PartialMatchMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return next
	}
}

// API with exact label
//zero:api GET /test exact
func (s *Service) TestEndpoint() {}
`

	graph := analyseTestCode(t, testCode, []string{"*test.Service"})

	// Should have 1 API
	assert.Equal(t, 1, len(graph.APIs))

	// Should have 2 middleware (EmptyLabelMiddleware and ExactMatchMiddleware)
	assert.Equal(t, 2, len(graph.Middleware))

	middlewareNames := make(map[string]bool)
	for _, mw := range graph.Middleware {
		middlewareNames[mw.Function.Name()] = true
	}

	assert.True(t, middlewareNames["EmptyLabelMiddleware"], "Empty label middleware should be kept (global)")
	assert.True(t, middlewareNames["ExactMatchMiddleware"], "Exact match middleware should be kept")
	assert.False(t, middlewareNames["PartialMatchMiddleware"], "Partial match middleware should be pruned")
}

func TestMiddlewarePruningWithLabelValues(t *testing.T) {
	t.Parallel()
	testCode := `
package main

import (
	"net/http"
)

type Service struct{}

//zero:provider
func NewService() *Service {
	return &Service{}
}

// Middleware that should match label name regardless of value
//zero:middleware cache
func CacheMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return next
	}
}

// Middleware that should not match
//zero:middleware timeout
func TimeoutMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return next
	}
}

// API with label that has a value
//zero:api GET /data cache=300
func (s *Service) GetData() {}
`

	graph := analyseTestCode(t, testCode, []string{"*test.Service"})

	// Should have 1 API
	assert.Equal(t, 1, len(graph.APIs))

	// Should have 1 middleware (CacheMiddleware matches despite value)
	assert.Equal(t, 1, len(graph.Middleware))

	mw := graph.Middleware[0]
	assert.Equal(t, "CacheMiddleware", mw.Function.Name())
}
