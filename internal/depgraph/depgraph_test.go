package depgraph

import (
	"context"
	"go/types"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/alecthomas/assert/v2"
	"github.com/alecthomas/zero/internal/directiveparser"
)

func TestAnalyseSimpleProvider(t *testing.T) {
	testCode := `
package main

import "database/sql"

//zero:provider
func NewDB() *sql.DB {
	return nil
}
`
	graph := analyseTestCode(t, testCode, []string{"*database/sql.DB"})
	assert.Equal(t, 1, len(graph.Providers))
	assert.Equal(t, 0, len(graph.Missing))

	dbProvider, ok := graph.Providers["*database/sql.DB"]
	assert.True(t, ok)
	assert.Equal(t, 0, len(dbProvider.Requires))
}

func TestAnalyseProviderWithError(t *testing.T) {
	testCode := `
package main

import "database/sql"

//zero:provider
func NewDB() (*sql.DB, error) {
	return nil, nil
}
`
	graph := analyseTestCode(t, testCode, []string{"*database/sql.DB"})
	assert.Equal(t, 1, len(graph.Providers))

	_, ok := graph.Providers["*database/sql.DB"]
	assert.True(t, ok)
}

func TestAnalyseProviderWithDependencies(t *testing.T) {
	testCode := `
package main

import "database/sql"

type Config struct {
	URL string
}

//zero:provider
func NewConfig() *Config {
	return &Config{}
}

//zero:provider
func NewDB(cfg *Config) (*sql.DB, error) {
	return nil, nil
}
`
	graph := analyseTestCode(t, testCode, []string{"*database/sql.DB"})
	assert.Equal(t, 2, len(graph.Providers))
	assert.Equal(t, 0, len(graph.Missing))

	dbProvider, ok := graph.Providers["*database/sql.DB"]
	assert.True(t, ok)
	assert.Equal(t, 1, len(dbProvider.Requires))
	assert.Equal(t, "*test.Config", types.TypeString(dbProvider.Requires[0], nil))
}

func TestAnalyseMissingDependencies(t *testing.T) {
	testCode := `
package main

import "database/sql"

type Config struct {
	URL string
}

//zero:provider
func NewDB(cfg *Config) (*sql.DB, error) {
	return nil, nil
}
`
	graph := analyseTestCode(t, testCode, []string{"*database/sql.DB"})
	assert.Equal(t, 1, len(graph.Providers))
	assert.Equal(t, 1, len(graph.Missing))
	for _, missing := range graph.Missing {
		assert.Equal(t, "*test.Config", types.TypeString(missing[0], nil))
	}
}

func TestAnalyseMultipleDependencies(t *testing.T) {
	testCode := `
package main

import (
	"database/sql"
	"log"
)

type Config struct {
	URL string
}

//zero:provider
func NewConfig() *Config {
	return &Config{}
}

//zero:provider
func NewLogger() *log.Logger {
	return nil
}

//zero:provider
func NewDB(cfg *Config, logger *log.Logger) (*sql.DB, error) {
	return nil, nil
}
`
	graph := analyseTestCode(t, testCode, []string{"*database/sql.DB"})
	assert.Equal(t, 3, len(graph.Providers))
	assert.Equal(t, 0, len(graph.Missing))

	dbProvider, ok := graph.Providers["*database/sql.DB"]
	assert.True(t, ok)
	assert.Equal(t, 2, len(dbProvider.Requires))
}

func TestAnalyseInvalidProvider(t *testing.T) {
	testCode := `
package main

//zero:provider
func InvalidProvider() {
}
`
	_, err := analyseTestCodeWithError(t, testCode, []string{"*test.Service"})
	assert.Error(t, err)
	assert.EqualError(t, err, "provider function InvalidProvider must return (T) or (T, error)")
}

func TestAnalyseInvalidErrorReturn(t *testing.T) {
	testCode := `
package main

//zero:provider
func InvalidProvider() (string, string) {
	return "", ""
}
`
	_, err := analyseTestCodeWithError(t, testCode, []string{"*test.Service"})
	assert.Error(t, err)
	assert.EqualError(t, err, "provider function InvalidProvider second return value must be error")
}

func TestAnalyseNonProviderFunction(t *testing.T) {
	testCode := `
package test

type Service struct{}

func RegularFunction() string {
	return ""
}

//zero:provider
func NewService() *Service {
	return nil
}
`
	graph := analyseTestCode(t, testCode, []string{"*test.Service"})
	assert.Equal(t, 1, len(graph.Providers))
}

func TestAnalyseCircularDependencies(t *testing.T) {
	testCode := `
package test

type A struct{}
type B struct{}

//zero:provider
func NewA(b *B) *A {
	return nil
}

//zero:provider
func NewB(a *A) *B {
	return nil
}
`
	graph := analyseTestCode(t, testCode, []string{"*test.A", "*test.B"})
	assert.Equal(t, 2, len(graph.Providers))
	assert.Equal(t, 0, len(graph.Missing))
}

func TestAnalyseConfigStruct(t *testing.T) {
	testCode := `
package test

//zero:config
type Config struct {
	URL string
	Port int
}
`
	graph := analyseTestCode(t, testCode, []string{"test.Config"})
	assert.Equal(t, 0, len(graph.Providers))
	assert.Equal(t, 1, len(graph.Configs))
	assert.Equal(t, 0, len(graph.Missing))

	// Config should be present since no pruning occurs with nil roots
	_, ok := graph.Configs["test.Config"]
	assert.True(t, ok)
}

func TestAnalyseProviderWithConfigDependency(t *testing.T) {
	testCode := `
package test

import "database/sql"

//zero:config
type Config struct {
	URL string
}

//zero:provider
func NewDB(cfg *Config) (*sql.DB, error) {
	return nil, nil
}
`
	graph := analyseTestCode(t, testCode, []string{"*database/sql.DB"})
	assert.Equal(t, 1, len(graph.Providers))
	assert.Equal(t, 1, len(graph.Configs))
	assert.Equal(t, 0, len(graph.Missing))

	dbProvider, ok := graph.Providers["*database/sql.DB"]
	assert.True(t, ok)
	assert.Equal(t, 1, len(dbProvider.Requires))
	assert.Equal(t, "*test.Config", types.TypeString(dbProvider.Requires[0], nil))
}

func TestAnalyseMultipleConfigs(t *testing.T) {
	testCode := `
package test

//zero:config
type DatabaseConfig struct {
	URL string
}

//zero:config
type ServerConfig struct {
	Port int
}

//zero:provider
func NewService(dbCfg *DatabaseConfig, srvCfg *ServerConfig) string {
	return ""
}
`
	graph := analyseTestCode(t, testCode, []string{"string"})
	assert.Equal(t, 1, len(graph.Providers))
	assert.Equal(t, 2, len(graph.Configs))
	assert.Equal(t, 0, len(graph.Missing))

	serviceProvider := graph.Providers["string"]
	assert.NotZero(t, serviceProvider)
	assert.Equal(t, 2, len(serviceProvider.Requires))
}

func TestAnalyseConfigWithoutAnnotation(t *testing.T) {
	testCode := `
package test

type Config struct {
	URL string
}

//zero:provider
func NewService(cfg *Config) string {
	return ""
}
`
	graph := analyseTestCode(t, testCode, []string{"string"})
	assert.Equal(t, 1, len(graph.Providers))
	assert.Equal(t, 0, len(graph.Configs))
	assert.Equal(t, 1, len(graph.Missing))
	for _, missing := range graph.Missing {
		assert.Equal(t, "*test.Config", types.TypeString(missing[0], nil))
	}
}

func TestAnalyseConfigStructAndPointerAvailable(t *testing.T) {
	testCode := `
package test

import "database/sql"

//zero:config
type Config struct {
	URL string
}

//zero:provider
func NewDBWithStruct(cfg Config) (*sql.DB, error) {
	return nil, nil
}

//zero:provider
func NewDBWithPointer(cfg *Config) (string, error) {
	return "", nil
}
`
	graph := analyseTestCode(t, testCode, []string{"*database/sql.DB", "string"})
	assert.Equal(t, 2, len(graph.Providers))
	assert.Equal(t, 1, len(graph.Configs))

	assert.Equal(t, 0, len(graph.Missing))

	// Verify struct type is available (pointer support is handled in dependency resolution)
	structType := graph.Configs["test.Config"]
	assert.NotZero(t, structType)
}

func TestAnalyseConfigBothStructAndPointerDependencies(t *testing.T) {
	testCode := `
package test

//zero:config
type Config struct {
	URL string
}

//zero:provider
func NeedsStruct(cfg Config) string {
	return ""
}

//zero:provider
func NeedsPointer(cfg *Config) int {
	return 0
}
`
	graph := analyseTestCode(t, testCode, []string{"string", "int"})
	assert.Equal(t, 2, len(graph.Providers))
	assert.Equal(t, 1, len(graph.Configs))
	assert.Equal(t, 0, len(graph.Missing))

	// Verify both providers found their dependencies
	strProvider, ok := graph.Providers["string"]
	assert.True(t, ok)
	assert.Equal(t, 1, len(strProvider.Requires))

	intProvider := graph.Providers["int"]
	assert.NotZero(t, intProvider)
	assert.Equal(t, 1, len(intProvider.Requires))
}

func analyseTestCode(t *testing.T, code string, roots []string) *Graph {
	t.Helper()
	graph, err := analyseTestCodeWithError(t, code, roots)
	assert.NoError(t, err)
	return graph
}

func analyseTestCodeWithError(t *testing.T, code string, roots []string) (*Graph, error) {
	t.Helper()
	return analyseCodeString(t.Context(), code, roots)
}

func TestAnalyseAPIFunctions(t *testing.T) {
	testCode := `
package main

import (
	"context"
	"net/http"
)

type CreateUserRequest struct {
	Name string
}

type UpdateUserRequest struct {
	Name string
}

type UserService struct{}

//zero:api GET /users
func (s *UserService) GetUsers(ctx context.Context) ([]string, error) {
	return []string{}, nil
}

//zero:api POST /users authenticated
func (s *UserService) CreateUser(ctx context.Context, req CreateUserRequest) (*string, error) {
	return &req.Name, nil
}

//zero:api GET /users/{id} authenticated cache=300
func (s *UserService) GetUser(ctx context.Context, id int) (*string, error) {
	return nil, nil
}

//zero:api PUT /users/{id} authenticated admin
func (s *UserService) UpdateUser(ctx context.Context, id int, req UpdateUserRequest) (*string, error) {
	return &req.Name, nil
}

//zero:api DELETE /users/{id} authenticated admin audit
func (s *UserService) DeleteUser(ctx context.Context, id int) error {
	return nil
}

//zero:api OPTIONS /health
func (s *UserService) HealthCheck(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// Non-API function should be ignored
func (s *UserService) InternalHelper() string {
	return "helper"
}
`
	graph := analyseTestCode(t, testCode, []string{"*test.UserService", "*test.PostService"})
	assert.Equal(t, 6, len(graph.APIs))

	// Check specific API endpoints
	apis := graph.APIs

	// Find GET /users endpoint
	getUsersAPI := findAPI(t, apis, "GET", "", "/users")
	assert.Equal(t, &directiveparser.DirectiveAPI{
		Method: "GET",
		Segments: []directiveparser.Segment{
			directiveparser.LiteralSegment{Literal: "users"},
		},
	}, getUsersAPI.Pattern)

	// Find POST /users endpoint with options
	createUserAPI := findAPI(t, apis, "POST", "", "/users")
	assert.Equal(t, &directiveparser.DirectiveAPI{
		Method: "POST",
		Segments: []directiveparser.Segment{
			directiveparser.LiteralSegment{Literal: "users"},
		},
		Labels: []*directiveparser.Label{
			{Name: "authenticated"},
		},
	}, createUserAPI.Pattern)

	// Find GET /users/{id} endpoint with multiple options
	getUserAPI := findAPI(t, apis, "GET", "", "/users/{id}")
	assert.Equal(t, &directiveparser.DirectiveAPI{
		Method: "GET",
		Segments: []directiveparser.Segment{
			directiveparser.LiteralSegment{Literal: "users"},
			directiveparser.WildcardSegment{Name: "id"},
		},
		Labels: []*directiveparser.Label{
			{Name: "authenticated"},
			{Name: "cache", Value: "300"},
		},
	}, getUserAPI.Pattern)

	// Find DELETE endpoint with multiple options
	deleteUserAPI := findAPI(t, apis, "DELETE", "", "/users/{id}")
	assert.Equal(t, &directiveparser.DirectiveAPI{
		Method: "DELETE",
		Segments: []directiveparser.Segment{
			directiveparser.LiteralSegment{Literal: "users"},
			directiveparser.WildcardSegment{Name: "id"},
		},
		Labels: []*directiveparser.Label{
			{Name: "authenticated"},
			{Name: "admin"},
			{Name: "audit"},
		},
	}, deleteUserAPI.Pattern)
}

func TestAnalyseInvalidAPIAnnotation(t *testing.T) {
	testCode := `
package main

type Service struct{}

//zero:api INVALID
func (s *Service) InvalidAPI() error {
	return nil
}
`
	_, err := analyseTestCodeWithError(t, testCode, []string{"*test.UserService"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse pattern")
}

func TestAnalyseAPIMinimalAnnotation(t *testing.T) {
	testCode := `
package main

import "net/http"

type UserService struct{}

//zero:api OPTIONS /health
func (s *UserService) HealthCheck(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}
`
	graph := analyseTestCode(t, testCode, []string{"*test.UserService"})
	assert.Equal(t, 1, len(graph.APIs))

	api := graph.APIs[0]
	assert.Equal(t, &directiveparser.DirectiveAPI{
		Method: "OPTIONS",
		Segments: []directiveparser.Segment{
			directiveparser.LiteralSegment{Literal: "health"},
		},
	}, api.Pattern)
}

func TestAnalyseNonAPIFunction(t *testing.T) {
	testCode := `
package main

type Service struct{}

func RegularFunction() string {
	return ""
}

//zero:api GET /test
func (s *Service) APIMethod() string {
	return ""
}
`
	graph := analyseTestCode(t, testCode, []string{"*test.UserService"})
	assert.Equal(t, 1, len(graph.APIs))

	api := graph.APIs[0]
	assert.Equal(t, &directiveparser.DirectiveAPI{
		Method: "GET",
		Segments: []directiveparser.Segment{
			directiveparser.LiteralSegment{Literal: "test"},
		},
	}, api.Pattern)
}

func TestAnalyseMixedProvidersAndAPIs(t *testing.T) {
	testCode := `
package main

import (
	"context"
	"database/sql"
)

type CreateUserRequest struct {
	Name string ` + "`json:\"name\"`" + `
}

type UserService struct{}

//zero:provider
func CreateDB() *sql.DB {
	return nil
}

//zero:api GET /users
func (s *UserService) GetUsers() []string {
	return []string{}
}

//zero:api POST /users authenticated
func (s *UserService) CreateUser(req CreateUserRequest) string {
	return req.Name
}
`
	graph := analyseTestCode(t, testCode, []string{"*database/sql.DB", "*test.UserService"})
	assert.Equal(t, 1, len(graph.Providers))
	assert.Equal(t, 2, len(graph.APIs))

	// Check provider
	_, ok := graph.Providers["*database/sql.DB"]
	assert.True(t, ok)

	// Check APIs
	assert.Equal(t, 2, len(graph.APIs))

	var getAPI, postAPI *API
	for _, api := range graph.APIs {
		switch api.Pattern.Method {
		case http.MethodGet:
			getAPI = api
		case http.MethodPost:
			postAPI = api
		}
	}

	assert.True(t, getAPI != nil)
	assert.Equal(t, &directiveparser.DirectiveAPI{
		Method: "GET",
		Segments: []directiveparser.Segment{
			directiveparser.LiteralSegment{Literal: "users"},
		},
	}, getAPI.Pattern)

	assert.True(t, postAPI != nil)
	assert.Equal(t, &directiveparser.DirectiveAPI{
		Method: "POST",
		Segments: []directiveparser.Segment{
			directiveparser.LiteralSegment{Literal: "users"},
		},
		Labels: []*directiveparser.Label{{Name: "authenticated"}},
	}, postAPI.Pattern)
}

func TestAnalyseAPIAnnotationOnFunction(t *testing.T) {
	testCode := `
package main

//zero:api GET /test
func StandaloneFunction() string {
	return ""
}
`
	_, err := analyseTestCodeWithError(t, testCode, []string{"*test.UserService"})
	assert.EqualError(t, err, "//zero:api annotation is only valid on methods, not functions: StandaloneFunction")
}

func TestAnalyseAPIReceiverWithoutProvider(t *testing.T) {
	testCode := `
package main

import "context"

type CreateUserRequest struct {
	Name string
}

type UserService struct{}

//zero:api GET /users
func (s *UserService) GetUsers(ctx context.Context) ([]string, error) {
	return []string{}, nil
}

//zero:api POST /users authenticated
func (s *UserService) CreateUser(ctx context.Context, req CreateUserRequest) (*string, error) {
	return &req.Name, nil
}
`
	graph := analyseTestCode(t, testCode, []string{"*test.UserService", "*test.PostService"})
	assert.Equal(t, 0, len(graph.Providers))
	assert.Equal(t, 2, len(graph.APIs))
	assert.Equal(t, 2, len(graph.Missing))

	// Check that UserService is missing for both API methods
	for funcName, missingTypes := range graph.Missing {
		assert.Equal(t, 1, len(missingTypes))
		assert.Equal(t, "*test.UserService", types.TypeString(missingTypes[0], nil))
		// Verify these are API functions
		assert.True(t, funcName.Name() == "GetUsers" || funcName.Name() == "CreateUser")
	}
}

func TestAnalyseAPIReceiverWithProvider(t *testing.T) {
	testCode := `
package main

import "context"

type CreateUserRequest struct {
	Name string
}

type UserService struct{}

//zero:provider
func NewUserService() *UserService {
	return &UserService{}
}

//zero:api GET /users
func (s *UserService) GetUsers(ctx context.Context) ([]string, error) {
	return []string{}, nil
}

//zero:api POST /users authenticated
func (s *UserService) CreateUser(ctx context.Context, req CreateUserRequest) (*string, error) {
	return &req.Name, nil
}
`
	graph := analyseTestCode(t, testCode, []string{"*test.UserService", "*test.PostService"})
	assert.Equal(t, 1, len(graph.Providers))
	assert.Equal(t, 2, len(graph.APIs))
	assert.Equal(t, 0, len(graph.Missing))

	// Check that provider exists for UserService
	_, ok := graph.Providers["*test.UserService"]
	assert.True(t, ok)
}

func TestAnalyseAPIReceiverWithConfig(t *testing.T) {
	testCode := `
package main

import "context"

//zero:config
type UserService struct {
	BaseURL string
}

//zero:api GET /users
func (s *UserService) GetUsers(ctx context.Context) ([]string, error) {
	return []string{}, nil
}
`
	graph := analyseTestCode(t, testCode, []string{"*test.UserService"})
	assert.Equal(t, 0, len(graph.Providers))
	assert.Equal(t, 1, len(graph.Configs))
	assert.Equal(t, 1, len(graph.APIs))
	assert.Equal(t, 0, len(graph.Missing))

	// Check that config exists for UserService
	_, ok := graph.Configs["test.UserService"]
	assert.True(t, ok)
}

func TestAnalyseMixedAPIReceiversSomeWithProviders(t *testing.T) {
	testCode := `
package main

import "context"

type APIService struct{}

//zero:api GET api.example.com/users
func (s *APIService) GetUsers(ctx context.Context) ([]string, error) {
	return []string{}, nil
}

//zero:api GET api.example.com/users/{id} authenticated cache=300
func (s *APIService) GetUser(ctx context.Context, id int) (*string, error) {
	return nil, nil
}
`
	graph := analyseTestCode(t, testCode, []string{"*test.UserService", "*test.PostService"})
	assert.Equal(t, 2, len(graph.APIs))

	// Check GET api.example.com/users
	getUsersAPI := findAPI(t, graph.APIs, "GET", "api.example.com", "/users")
	assert.Equal(t, &directiveparser.DirectiveAPI{
		Method: "GET",
		Host:   "api.example.com",
		Segments: []directiveparser.Segment{
			directiveparser.LiteralSegment{Literal: "users"},
		},
	}, getUsersAPI.Pattern)
}

func TestAnalyseAPINoDuplicateMissingReceivers(t *testing.T) {
	testCode := `
package main

import "context"

type CreateUserRequest struct {
	Name string
}

type UserService struct{}

//zero:api GET /users
func (s *UserService) GetUsers(ctx context.Context) ([]string, error) {
	return []string{}, nil
}

//zero:api POST /users
func (s *UserService) CreateUser(ctx context.Context, req CreateUserRequest) (*string, error) {
	return &req.Name, nil
}

//zero:api DELETE /users/{id}
func (s *UserService) DeleteUser(ctx context.Context, id int) error {
	return nil
}
`
	graph := analyseTestCode(t, testCode, []string{"*test.UserService", "*test.PostService", "*test.ProductService"})
	assert.Equal(t, 3, len(graph.APIs))
	assert.Equal(t, 3, len(graph.Missing))

	// Check that each API method has exactly one missing dependency (*UserService)
	// and there are no duplicates within each method's missing slice
	userServiceCount := 0
	for _, missingTypes := range graph.Missing {
		assert.Equal(t, 1, len(missingTypes))
		assert.Equal(t, "*test.UserService", types.TypeString(missingTypes[0], nil))
		userServiceCount++
	}
	assert.Equal(t, 3, userServiceCount)
}

func TestAnalyseAPIWithHosts(t *testing.T) {
	testCode := `
package main

import (
	"context"
)

type APIService struct{}

//zero:api GET api.example.com/users
func (s *APIService) GetUsers(ctx context.Context) ([]string, error) {
	return []string{}, nil
}

//zero:api GET api.example.com/users/{id} authenticated cache=300
func (s *APIService) GetUser(ctx context.Context, id int) (*string, error) {
	return nil, nil
}

`
	graph := analyseTestCode(t, testCode, []string{"*test.UserService", "*test.PostService"})
	assert.Equal(t, 2, len(graph.APIs))

	// Check GET api.example.com/users
	getUsersAPI := findAPI(t, graph.APIs, "GET", "api.example.com", "/users")
	assert.Equal(t, &directiveparser.DirectiveAPI{
		Method: "GET",
		Host:   "api.example.com",
		Segments: []directiveparser.Segment{
			directiveparser.LiteralSegment{Literal: "users"},
		},
	}, getUsersAPI.Pattern)

	// Check GET api.example.com/users/{id} with wildcards
	getUserAPI := findAPI(t, graph.APIs, "GET", "api.example.com", "/users/{id}")
	assert.Equal(t, &directiveparser.DirectiveAPI{
		Method: "GET",
		Host:   "api.example.com",
		Segments: []directiveparser.Segment{
			directiveparser.LiteralSegment{Literal: "users"},
			directiveparser.WildcardSegment{Name: "id"},
		},
		Labels: []*directiveparser.Label{
			{Name: "authenticated"},
			{Name: "cache", Value: "300"},
		},
	}, getUserAPI.Pattern)
}

func TestAnalyseAPIWithWildcards(t *testing.T) {
	testCode := `
package main

import (
	"context"
)

type FileService struct{}

//zero:api GET /files/{path...}
func (s *FileService) ServeFile(ctx context.Context, path string) ([]byte, error) {
	return []byte{}, nil
}

//zero:api POST /api/v1/users/{userId}/posts/{postId}
func (s *FileService) UpdatePost(ctx context.Context, userId, postId int) error {
	return nil
}

//zero:api DELETE /static/{path...} authenticated admin
func (s *FileService) DeleteStatic(ctx context.Context, path string) error {
	return nil
}
`
	graph := analyseTestCode(t, testCode, []string{"*test.UserService", "*test.PostService", "*test.ProductService"})
	assert.Equal(t, 3, len(graph.APIs))

	// Check catch-all wildcard
	serveFileAPI := findAPI(t, graph.APIs, "GET", "", "/files/{path...}")
	assert.Equal(t, &directiveparser.DirectiveAPI{
		Method: "GET",
		Segments: []directiveparser.Segment{
			directiveparser.LiteralSegment{Literal: "files"},
			directiveparser.WildcardSegment{Name: "path", Remainder: true},
		},
	}, serveFileAPI.Pattern)

	// Check multiple wildcards
	updatePostAPI := findAPI(t, graph.APIs, "POST", "", "/api/v1/users/{userId}/posts/{postId}")
	assert.Equal(t, &directiveparser.DirectiveAPI{
		Method: "POST",
		Segments: []directiveparser.Segment{
			directiveparser.LiteralSegment{Literal: "api"},
			directiveparser.LiteralSegment{Literal: "v1"},
			directiveparser.LiteralSegment{Literal: "users"},
			directiveparser.WildcardSegment{Name: "userId"},
			directiveparser.LiteralSegment{Literal: "posts"},
			directiveparser.WildcardSegment{Name: "postId"},
		},
	}, updatePostAPI.Pattern)

	// Check catch-all with options
	deleteStaticAPI := findAPI(t, graph.APIs, "DELETE", "", "/static/{path...}")
	assert.Equal(t, &directiveparser.DirectiveAPI{
		Method: "DELETE",
		Segments: []directiveparser.Segment{
			directiveparser.LiteralSegment{Literal: "static"},
			directiveparser.WildcardSegment{Name: "path", Remainder: true},
		},
		Labels: []*directiveparser.Label{{Name: "authenticated"}, {Name: "admin"}},
	}, deleteStaticAPI.Pattern)
}

func TestAnalyseAPIWithoutMethod(t *testing.T) {
	testCode := `
package main

import (
	"context"
)

type Service struct{}

//zero:api /health
func (s *Service) Health(ctx context.Context) error {
	return nil
}

//zero:api api.example.com/status authenticated
func (s *Service) Status(ctx context.Context) error {
	return nil
}
`
	graph := analyseTestCode(t, testCode, []string{"*test.UserService", "*test.PostService"})
	assert.Equal(t, 2, len(graph.APIs))

	// Check no method specified
	healthAPI := findAPI(t, graph.APIs, "", "", "/health")
	assert.Equal(t, &directiveparser.DirectiveAPI{
		Method: "", // No method specified
		Segments: []directiveparser.Segment{
			directiveparser.LiteralSegment{Literal: "health"},
		},
	}, healthAPI.Pattern)

	// Check host with no method
	statusAPI := findAPI(t, graph.APIs, "", "api.example.com", "/status")
	assert.Equal(t, &directiveparser.DirectiveAPI{
		Method: "", // No method specified
		Host:   "api.example.com",
		Segments: []directiveparser.Segment{
			directiveparser.LiteralSegment{Literal: "status"},
		},
		Labels: []*directiveparser.Label{{Name: "authenticated"}},
	}, statusAPI.Pattern)
}

func TestAnalyseAPIInvalidPatterns(t *testing.T) {
	tests := []struct {
		name        string
		annotation  string
		expectedErr string
	}{
		{
			name:        "EmptyAnnotation",
			annotation:  "//zero:api",
			expectedErr: "failed to parse pattern",
		},
		{
			name:        "OnlyWhitespace",
			annotation:  "//zero:api   ",
			expectedErr: "failed to parse pattern",
		},
		{
			name:        "InvalidWildcardSyntax",
			annotation:  "//zero:api GET /users/{id",
			expectedErr: "failed to parse pattern",
		},
		{
			name:        "EmptyWildcardName",
			annotation:  "//zero:api GET /users/{}",
			expectedErr: "failed to parse pattern",
		},
		{
			name:        "SchemeNotAllowed",
			annotation:  "//zero:api GET https://example.com/users",
			expectedErr: "invalid path, cannot contain empty path segments",
		},
		{
			name:        "CatchAllNotAtEnd",
			annotation:  "//zero:api GET /static/{path...}/more",
			expectedErr: "invalid path, catch-all can only be at end",
		},
		{
			name:        "EmptyCatchAllName",
			annotation:  "//zero:api GET /static/{...}",
			expectedErr: "failed to parse pattern",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testCode := `
package main

type Service struct{}

` + tt.annotation + `
func (s *Service) TestMethod() error {
	return nil
}
`
			_, err := analyseTestCodeWithError(t, testCode, []string{"*test.UserService"})
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectedErr)
		})
	}
}

func TestAnalyseAPIComplexPatterns(t *testing.T) {
	testCode := `
package main

import (
	"context"
)

type CreateCommentRequest struct {
	Content string
}

type APIService struct{}

//zero:api GET /
func (s *APIService) Root(ctx context.Context) error {
	return nil
}

//zero:api POST api.v1.example.com/users/{id}/posts/{postId}/comments authenticated admin cache=300 audit
func (s *APIService) CreateComment(ctx context.Context, id, postId int, req CreateCommentRequest) error {
	return nil
}

//zero:api PUT localhost:8080/admin/{path...} authenticated admin
func (s *APIService) AdminAction(ctx context.Context, path string) error {
	return nil
}
`
	graph := analyseTestCode(t, testCode, []string{"*test.UserService", "*test.PostService", "*test.ProductService"})
	assert.Equal(t, 3, len(graph.APIs))

	// Check root endpoint
	rootAPI := findAPI(t, graph.APIs, "", "", "/")
	assert.Equal(t, &directiveparser.DirectiveAPI{
		Method: "GET",
		Host:   "",
		Segments: []directiveparser.Segment{
			directiveparser.TrailingSegment{},
		},
	}, rootAPI.Pattern)

	// Check complex pattern with multiple options
	createCommentAPI := findAPI(t, graph.APIs, "", "api.v1.example.com", "/users/{id}/posts/{postId}/comments")
	assert.Equal(t, &directiveparser.DirectiveAPI{
		Method: "POST",
		Host:   "api.v1.example.com",
		Segments: []directiveparser.Segment{
			directiveparser.LiteralSegment{Literal: "users"},
			directiveparser.WildcardSegment{Name: "id"},
			directiveparser.LiteralSegment{Literal: "posts"},
			directiveparser.WildcardSegment{Name: "postId"},
			directiveparser.LiteralSegment{Literal: "comments"},
		},
		Labels: []*directiveparser.Label{
			{Name: "authenticated"},
			{Name: "admin"},
			{Name: "cache", Value: "300"},
			{Name: "audit"},
		},
	}, createCommentAPI.Pattern)

	// Check localhost with port and catch-all
	adminAPI := findAPI(t, graph.APIs, "", "localhost:8080", "/admin/{path...}")
	assert.Equal(t, &directiveparser.DirectiveAPI{
		Method: "PUT",
		Host:   "localhost:8080",
		Segments: []directiveparser.Segment{
			directiveparser.LiteralSegment{Literal: "admin"},
			directiveparser.WildcardSegment{Name: "path", Remainder: true},
		},
		Labels: []*directiveparser.Label{
			{Name: "authenticated"},
			{Name: "admin"},
		},
	}, adminAPI.Pattern)
}

func TestAnalyseAPIParameterValidation(t *testing.T) {
	// Test valid parameter types
	testCode := `
package main

import (
	"context"
	"encoding"
	"io"
	"net/http"
	"time"
)

// Custom type that implements encoding.TextUnmarshaler
type UserID string

func (u *UserID) UnmarshalText(text []byte) error {
	*u = UserID(text)
	return nil
}

var _ encoding.TextUnmarshaler = (*UserID)(nil)

// Request body struct
type CreateUserRequest struct {
	Name  string
	Email string
}

type UserService struct{}

// Valid: standard HTTP types
//zero:api GET /health
func (s *UserService) HealthCheck(w http.ResponseWriter, r *http.Request, ctx context.Context, body io.Reader) {
}

// Valid: string and int with wildcards
//zero:api GET /users/{id}
func (s *UserService) GetUser(ctx context.Context, id string) (*string, error) {
	return &id, nil
}

//zero:api GET /posts/{postID}/comments/{commentID}
func (s *UserService) GetComment(ctx context.Context, postID int, commentID int64) (*string, error) {
	return nil, nil
}

// Valid: TextUnmarshaler with wildcard
//zero:api GET /users/{userID}/profile
func (s *UserService) GetUserProfile(ctx context.Context, userID UserID) (*string, error) {
	return nil, nil
}

// Valid: struct parameter for request body
//zero:api POST /users
func (s *UserService) CreateUser(ctx context.Context, req CreateUserRequest) (*string, error) {
	return &req.Name, nil
}

// Valid: pointer to struct parameter
//zero:api PUT /users/{id}
func (s *UserService) UpdateUser(ctx context.Context, id int, req *CreateUserRequest) (*string, error) {
	return &req.Name, nil
}
`

	graph, err := analyseCodeString(t.Context(), testCode, []string{"*test.UserService", "*test.PostService", "*test.ProductService", "*test.FileService", "*test.NotificationService", "*test.CommentService"})
	assert.NoError(t, err)
	assert.Equal(t, 6, len(graph.APIs))
}

func TestAnalyseAPIInvalidParameterTypes(t *testing.T) {
	tests := []struct {
		name        string
		code        string
		expectedErr string
	}{
		{
			name: "BoolParameterWithoutWildcard",
			code: `
package main

import "context"

type CreateCommentRequest struct {
	Content string
}

type APIService struct{}
type BlogService struct{}

//zero:api GET /users/active
func (s *UserService) GetActiveUsers(ctx context.Context, active bool) ([]string, error) {
	return []string{}, nil
}
`,
			expectedErr: "invalid parameter type for API method GetActiveUsers: parameter active of type bool is not allowed",
		},
		{
			name: "FloatParameter",
			code: `
package main

import "context"

type UserService struct{}

//zero:api GET /users/score
func (s *UserService) GetUsersByScore(ctx context.Context, score float64) ([]string, error) {
	return []string{}, nil
}
`,
			expectedErr: "invalid parameter type for API method GetUsersByScore: parameter score of type float64 is not allowed",
		},
		{
			name: "ComplexParameter",
			code: `
package main

import "context"

type UserService struct{}

//zero:api GET /users/complex
func (s *UserService) GetUsersComplex(ctx context.Context, val complex128) ([]string, error) {
	return []string{}, nil
}
`,
			expectedErr: "invalid parameter type for API method GetUsersComplex: parameter val of type complex128 is not allowed",
		},
		{
			name: "ComplexParameterWithoutWildcard",
			code: `
package main

import (
	"context"
	"time"
)

type UserService struct{}

//zero:api GET /users/by-date
func (s *UserService) GetUsersByDate(ctx context.Context, date time.Time) ([]string, error) {
	return []string{}, nil
}
`,
			expectedErr: "invalid parameter type for API method GetUsersByDate: parameter date of type time.Time is not allowed",
		},
		{
			name: "TextUnmarshalerWithoutWildcard",
			code: `
package main

import (
	"context"
	"time"
)

type UserService struct{}

//zero:api GET /users/by-date
func (s *UserService) GetUsersByDate(ctx context.Context, date time.Time) ([]string, error) {
	return []string{}, nil
}
`,
			expectedErr: "invalid parameter type for API method GetUsersByDate: parameter date of type time.Time is not allowed",
		},
		{
			name: "ComplexParameterWithoutWildcard",
			code: `
package main

import (
	"context"
	"time"
)

type UserService struct{}

//zero:api GET /users/by-date
func (s *UserService) GetUsersByDate(ctx context.Context, date time.Time) ([]string, error) {
	return []string{}, nil
}
`,
			expectedErr: "invalid parameter type for API method GetUsersByDate: parameter date of type time.Time is not allowed",
		},
		{
			name: "InvalidDependencyInjectionType",
			code: `
package main

import (
	"context"
	"database/sql"
)

type CreateUserRequest struct {
	Name string
}

type UserService struct{}

//zero:api GET /users
func (s *UserService) GetUsers(ctx context.Context, db *sql.DB) ([]string, error) {
	return []string{}, nil
}
`,
			expectedErr: "invalid parameter type for API method GetUsers: parameter db of type *database/sql.DB is not allowed",
		},
		{
			name: "MultipleStructParameters",
			code: `
package main

import "context"

type CreateUserRequest struct {
	Name string
}

type UserFilters struct {
	Active bool
}

type UserService struct{}

//zero:api POST /users/complex
func (s *UserService) ComplexCreate(ctx context.Context, req1 CreateUserRequest, req2 UserFilters) error {
	return nil
}
`,
			expectedErr: "API method ComplexCreate can only have one struct parameter for request body/query parameters",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := analyseCodeString(t.Context(), tt.code, []string{"*test.UserService"})
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectedErr)
		})
	}
}

func TestAnalyseWeakProviderDirectiveRequirements(t *testing.T) {
	testCode := `
package main

import (
	"database/sql"
)

type CronJob struct {
	Name string
}

type Config struct {
	Host string
}

//zero:provider
func NewDB(config Config) *sql.DB {
	return nil
}

//zero:provider weak
func CronJobProvider() CronJob {
	return CronJob{}
}

//zero:provider weak require=CronJobProvider
func SQLCron(db *sql.DB) string {
	return ""
}
`
	// Test that when SQLCron (weak provider) is included, CronJobProvider is also included
	graph := analyseTestCode(t, testCode, []string{"string"})

	// SQLCron should be included as it provides the root type "string"
	sqlCronProvider, ok := graph.Providers["string"]
	assert.True(t, ok, "SQLCron provider should be included")
	assert.Equal(t, "SQLCron", sqlCronProvider.Function.Name())

	// CronJobProvider should be included because SQLCron requires it via directive
	cronJobProvider, ok := graph.Providers["test.CronJob"]
	assert.True(t, ok, "CronJobProvider should be included due to directive requirement")
	assert.Equal(t, "CronJobProvider", cronJobProvider.Function.Name())

	// NewDB should be included because SQLCron needs it as a parameter
	dbProvider, ok := graph.Providers["*database/sql.DB"]
	assert.True(t, ok, "NewDB provider should be included as SQLCron depends on it")
	assert.Equal(t, "NewDB", dbProvider.Function.Name())
}

func TestAnalyseWeakProviderDirectiveRequirementsChain(t *testing.T) {
	testCode := `
package main

import (
	"database/sql"
)

type Logger struct {
	Level string
}

type Cache struct {
	Size int
}

type Config struct {
	Host string
}

//zero:provider
func NewDB(config Config) *sql.DB {
	return nil
}

//zero:provider weak
func DebugLogger() Logger {
	return Logger{Level: "debug"}
}

//zero:provider weak require=DebugLogger
func RedisCache(logger Logger) Cache {
	return Cache{Size: 100}
}

//zero:provider weak require=RedisCache
func CacheManager(db *sql.DB, cache Cache) string {
	return "manager"
}
`
	// Test that when CacheManager (weak provider) is included, the entire chain is included
	graph := analyseTestCode(t, testCode, []string{"string"})

	// CacheManager should be included as it provides the root type "string"
	cacheManagerProvider, ok := graph.Providers["string"]
	assert.True(t, ok, "CacheManager provider should be included")
	assert.Equal(t, "CacheManager", cacheManagerProvider.Function.Name())

	// RedisCache should be included because CacheManager requires it via directive
	redisCacheProvider, ok := graph.Providers["test.Cache"]
	assert.True(t, ok, "RedisCache should be included due to directive requirement")
	assert.Equal(t, "RedisCache", redisCacheProvider.Function.Name())

	// DebugLogger should be included because RedisCache requires it via directive
	debugLoggerProvider, ok := graph.Providers["test.Logger"]
	assert.True(t, ok, "DebugLogger should be included due to transitive directive requirement")
	assert.Equal(t, "DebugLogger", debugLoggerProvider.Function.Name())

	// NewDB should be included because CacheManager needs it as a parameter
	dbProvider, ok := graph.Providers["*database/sql.DB"]
	assert.True(t, ok, "NewDB provider should be included as CacheManager depends on it")
	assert.Equal(t, "NewDB", dbProvider.Function.Name())
}

func TestAnalyseWeakMultiProviderNotIncludedUnlessNeeded(t *testing.T) {
	testCode := `
package main

type Service struct {
	Name string
}

//zero:provider multi
func RegularService() Service {
	return Service{Name: "regular"}
}

//zero:provider weak multi
func WeakService() Service {
	return Service{Name: "weak"}
}

//zero:provider
func GetServiceName(s Service) string {
	return s.Name
}
`
	// Test that weak multi-providers are not included unless explicitly needed
	graph := analyseTestCode(t, testCode, []string{"string"})

	// GetServiceName should be included as it provides the root type "string"
	serviceNameProvider, ok := graph.Providers["string"]
	assert.True(t, ok, "GetServiceName provider should be included")
	assert.Equal(t, "GetServiceName", serviceNameProvider.Function.Name())

	// Service should be a multi-provider but only contain RegularService, not WeakService
	multiProviders, ok := graph.MultiProviders["test.Service"]
	assert.True(t, ok, "Service should be a multi-provider")
	assert.Equal(t, 1, len(multiProviders), "Should only contain the non-weak provider")
	assert.Equal(t, "RegularService", multiProviders[0].Function.Name())

	// WeakService should NOT be included since it's weak and not explicitly needed
	for _, provider := range multiProviders {
		assert.NotEqual(t, "WeakService", provider.Function.Name(), "WeakService should not be included")
	}
}

func TestAnalyseWeakMultiProviderIncludedWhenRequired(t *testing.T) {
	testCode := `
package main

type Service struct {
	Name string
}

//zero:provider multi
func RegularService() Service {
	return Service{Name: "regular"}
}

//zero:provider weak multi
func WeakService() Service {
	return Service{Name: "weak"}
}

//zero:provider weak require=WeakService
func SpecialHandler() string {
	return "special"
}
`
	// Test that weak multi-providers ARE included when explicitly required
	graph := analyseTestCode(t, testCode, []string{"string"})

	// SpecialHandler should be included as it provides the root type "string"
	specialHandlerProvider, ok := graph.Providers["string"]
	assert.True(t, ok, "SpecialHandler provider should be included")
	assert.Equal(t, "SpecialHandler", specialHandlerProvider.Function.Name())

	// Service should be a multi-provider containing BOTH providers
	multiProviders, ok := graph.MultiProviders["test.Service"]
	assert.True(t, ok, "Service should be a multi-provider")
	assert.Equal(t, 2, len(multiProviders), "Should contain both providers since WeakService is required")

	// Both RegularService and WeakService should be included
	providerNames := make([]string, len(multiProviders))
	for i, p := range multiProviders {
		providerNames[i] = p.Function.Name()
	}
	assert.SliceContains(t, providerNames, "RegularService")
	assert.SliceContains(t, providerNames, "WeakService")
}

func TestAnalyseInvalidRequireDirective(t *testing.T) {
	testCode := `
package main

//zero:provider weak require=NonExistentFunction
func WeakProvider() string {
	return "test"
}
`
	// Test that invalid function names in require directive return an error
	_, err := analyseTestCodeWithError(t, testCode, []string{"string"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "requires NonExistentFunction, but NonExistentFunction is not a valid provider function in the same package")
}

func TestAnalyseRequireNonProviderFunction(t *testing.T) {
	testCode := `
package main

func RegularFunction() string {
	return "not a provider"
}

//zero:provider weak require=RegularFunction
func WeakProvider() int {
	return 42
}
`
	// Test that requiring a non-provider function returns an error
	_, err := analyseTestCodeWithError(t, testCode, []string{"int"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "requires RegularFunction, but RegularFunction is not a valid provider function in the same package")
}

func TestAnalyseAPIValidParameterTypes(t *testing.T) {
	testCode := `
package main

import (
	"context"
	"net/http"
)

type CreateUserRequest struct {
	Name string ` + "`json:\"name\"`" + `
	Age  int    ` + "`json:\"age\"`" + `
}

type UserService struct{}

// Valid: standard HTTP types and string/int mapped to wildcards
//zero:api GET /users/{id}
func (s *UserService) GetUser(ctx context.Context, id string, w http.ResponseWriter) error {
	return nil
}

// Valid: struct parameter for request body
//zero:api POST /users
func (s *UserService) CreateUser(ctx context.Context, req CreateUserRequest) error {
	return nil
}
`

	graph, err := analyseCodeString(t.Context(), testCode, []string{"*test.UserService", "*test.PostService"})
	assert.NoError(t, err)
	assert.Equal(t, 2, len(graph.APIs))
}

func TestAnalyseAPIWildcardParameterMapping(t *testing.T) {
	testCode := `
package main

import (
	"context"
	"encoding"
)

type UserID string

func (u *UserID) UnmarshalText(text []byte) error {
	*u = UserID(text)
	return nil
}

var _ encoding.TextUnmarshaler = (*UserID)(nil)

type UserService struct{}

// Valid: TextUnmarshaler with matching wildcard
//zero:api GET /users/{userID}/posts/{postID}
func (s *UserService) GetUserPost(ctx context.Context, userID UserID, postID string) error {
	return nil
}
`

	graph, err := analyseCodeString(t.Context(), testCode, []string{"*test.UserService"})
	assert.NoError(t, err)
	assert.Equal(t, 1, len(graph.APIs))

	api := graph.APIs[0]
	assert.Equal(t, "GetUserPost", api.Function.Name())
	hasUserID := api.Pattern.Wildcard("userID")
	assert.True(t, hasUserID)
	hasPostID := api.Pattern.Wildcard("postID")
	assert.True(t, hasPostID)
}

func analyseCodeString(ctx context.Context, code string, roots []string) (*Graph, error) {
	tmpDir, err := os.MkdirTemp("", "depgraph_test")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	// Create go.mod file
	goMod := `module test
go 1.21
`
	goModFile := filepath.Join(tmpDir, "go.mod")
	err = os.WriteFile(goModFile, []byte(goMod), 0600)
	if err != nil {
		return nil, err
	}

	mainFile := filepath.Join(tmpDir, "main.go")
	err = os.WriteFile(mainFile, []byte(code), 0600) //nolint
	if err != nil {
		return nil, err
	}

	// Save current directory and change to tmpDir
	oldDir, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	err = os.Chdir(tmpDir)
	if err != nil {
		return nil, err
	}
	defer os.Chdir(oldDir) //nolint:errcheck

	return Analyse(ctx, ".", WithRoots(roots...))
}

// findAPI finds an API in the slice by method, host, and path.
// If method is empty, it matches any method.
// If host is empty, it matches any host.
func findAPI(t *testing.T, apis []*API, method, host, path string) *API {
	t.Helper()
	for _, api := range apis {
		if (method == "" || api.Pattern.Method == method) &&
			(host == "" || api.Pattern.Host == host) &&
			api.Pattern.Path() == path {
			return api
		}
	}
	t.Fatalf("API not found: method=%q host=%q path=%q", method, host, path)
	return nil
}

func TestRemoveUnusedConfigs(t *testing.T) {
	code := `
package main

//zero:config
type UsedConfig struct {
	Value string
}

//zero:config
type UnusedConfig struct {
	Value string
}

//zero:config
type PointerUsedConfig struct {
	Value string
}

//zero:provider
func ProvideService(cfg UsedConfig, ptrCfg *PointerUsedConfig) *Service {
	return &Service{}
}

type Service struct{}
`

	graph, err := analyseCodeString(t.Context(), code, []string{"*test.Service"})
	if err != nil {
		t.Fatalf("Failed to analyse code: %v", err)
	}

	// Check that only used configs remain
	expectedConfigs := []string{
		"test.UsedConfig",
		"test.PointerUsedConfig",
	}

	assert.Equal(t, len(expectedConfigs), len(graph.Configs))

	for _, expected := range expectedConfigs {
		_, exists := graph.Configs[expected]
		assert.True(t, exists, "Expected config %q to be present", expected)
	}

	// Check that unused config was removed
	_, exists := graph.Configs["test.UnusedConfig"]
	assert.False(t, exists, "Expected UnusedConfig to be removed")
}

func TestAnalyseWithRootTypePruning(t *testing.T) {
	code := `
package test

import "context"

//zero:provider
func ProvideA() *ServiceA {
	return &ServiceA{}
}

//zero:provider
func ProvideB() *ServiceB {
	return &ServiceB{}
}

//zero:provider
func ProvideC(a *ServiceA) *ServiceC {
	return &ServiceC{A: a}
}

//zero:provider
func ProvideD(b *ServiceB) *ServiceD {
	return &ServiceD{B: b}
}

type ServiceA struct{}
type ServiceB struct{}
type ServiceC struct{ A *ServiceA }
type ServiceD struct{ B *ServiceB }
`

	// Test with all services as roots - should keep all providers
	graph, err := analyseCodeString(t.Context(), code, []string{"*test.ServiceA", "*test.ServiceB", "*test.ServiceC", "*test.ServiceD"})
	assert.NoError(t, err)
	assert.Equal(t, 4, len(graph.Providers))

	// Test with ServiceC as root - should keep ServiceA and ServiceC providers, remove ServiceB and ServiceD
	graph, err = analyseCodeString(t.Context(), code, []string{"*test.ServiceC"})
	assert.NoError(t, err)
	assert.Equal(t, 2, len(graph.Providers))
	_, hasServiceA := graph.Providers["*test.ServiceA"]
	_, hasServiceC := graph.Providers["*test.ServiceC"]
	_, hasServiceB := graph.Providers["*test.ServiceB"]
	_, hasServiceD := graph.Providers["*test.ServiceD"]
	assert.True(t, hasServiceA)
	assert.True(t, hasServiceC)
	assert.False(t, hasServiceB)
	assert.False(t, hasServiceD)

	// Test with ServiceD as root - should keep ServiceB and ServiceD providers, remove ServiceA and ServiceC
	graph, err = analyseCodeString(t.Context(), code, []string{"*test.ServiceD"})
	assert.NoError(t, err)
	assert.Equal(t, 2, len(graph.Providers))
	_, hasServiceA = graph.Providers["*test.ServiceA"]
	_, hasServiceB = graph.Providers["*test.ServiceB"]
	_, hasServiceC = graph.Providers["*test.ServiceC"]
	_, hasServiceD = graph.Providers["*test.ServiceD"]
	assert.False(t, hasServiceA)
	assert.True(t, hasServiceB)
	assert.False(t, hasServiceC)
	assert.True(t, hasServiceD)

	// Test with multiple roots - should keep all providers
	graph, err = analyseCodeString(t.Context(), code, []string{"*test.ServiceC", "*test.ServiceD"})
	assert.NoError(t, err)
	assert.Equal(t, 4, len(graph.Providers))
}

func TestAnalyseWithRootTypePruningConfigs(t *testing.T) {
	code := `
package test

//zero:config
type ConfigA struct {
	Value string
}

//zero:config
type ConfigB struct {
	Number int
}

//zero:provider
func ProvideService(cfg *ConfigA) *Service {
	return &Service{Config: cfg}
}

type Service struct{ Config *ConfigA }
`

	// Test with Service as root - should keep ConfigA but remove ConfigB
	graph, err := analyseCodeString(t.Context(), code, []string{"*test.Service"})
	assert.NoError(t, err)
	assert.Equal(t, 1, len(graph.Providers))
	assert.Equal(t, 1, len(graph.Configs))
	_, hasConfigA := graph.Configs["test.ConfigA"]
	_, hasConfigB := graph.Configs["test.ConfigB"]
	assert.True(t, hasConfigA)
	assert.False(t, hasConfigB)
}

func TestAnalyseWithRootTypePruningAPIReceivers(t *testing.T) {
	code := `
package test

import (
	"context"
	"net/http"
)

//zero:provider
func ProvideServiceA() *ServiceA {
	return &ServiceA{}
}

//zero:provider
func ProvideServiceB() *ServiceB {
	return &ServiceB{}
}

type ServiceA struct{}
type ServiceB struct{}

//zero:api GET /test
func (s *ServiceA) GetTest(ctx context.Context, w http.ResponseWriter, r *http.Request) {
}

//zero:api POST /other
func (s *ServiceB) PostOther(ctx context.Context, w http.ResponseWriter, r *http.Request) {
}
`

	// Test with both API receiver types as explicit roots
	graph, err := analyseCodeString(t.Context(), code, []string{"*test.ServiceA", "*test.ServiceB"})
	assert.NoError(t, err)
	assert.Equal(t, 2, len(graph.Providers))
	assert.Equal(t, 2, len(graph.APIs))

	// Test with only one API receiver type as root
	graph, err = analyseCodeString(t.Context(), code, []string{"*test.ServiceA"})
	assert.NoError(t, err)
	assert.Equal(t, 1, len(graph.Providers)) // Only ServiceA provider should be kept
	assert.Equal(t, 2, len(graph.APIs))      // APIs are not pruned based on receivers
}

func TestAnalyseWithNilRoots(t *testing.T) {
	code := `
package test

//zero:config
type ConfigA struct {
	Value string
}

//zero:config
type ConfigB struct {
	Number int
}

//zero:provider
func ProvideServiceA() *ServiceA {
	return &ServiceA{}
}

//zero:provider
func ProvideServiceB() *ServiceB {
	return &ServiceB{}
}

//zero:provider
func ProvideServiceC(cfg *ConfigA) *ServiceC {
	return &ServiceC{Config: cfg}
}

type ServiceA struct{}
type ServiceB struct{}
type ServiceC struct{ Config *ConfigA }
`

	graph, err := analyseCodeString(t.Context(), code, []string{"*test.ServiceA", "*test.ServiceB", "*test.ServiceC"})
	assert.NoError(t, err)
	assert.Equal(t, 3, len(graph.Providers))
	assert.Equal(t, 1, len(graph.Configs))

	// Verify all providers are present
	_, hasServiceA := graph.Providers["*test.ServiceA"]
	_, hasServiceB := graph.Providers["*test.ServiceB"]
	_, hasServiceC := graph.Providers["*test.ServiceC"]
	assert.True(t, hasServiceA)
	assert.True(t, hasServiceB)
	assert.True(t, hasServiceC)

	// Verify all configs are present
	_, hasConfigA := graph.Configs["test.ConfigA"]
	_, hasConfigB := graph.Configs["test.ConfigB"]
	assert.True(t, hasConfigA)
	assert.False(t, hasConfigB)
}

func TestAnalyseWithNilRootsAndAPIs(t *testing.T) {
	code := `
package test

import (
	"context"
	"net/http"
)

//zero:provider
func ProvideServiceA() *ServiceA {
	return &ServiceA{}
}

//zero:provider
func ProvideServiceB() *ServiceB {
	return &ServiceB{}
}

//zero:provider
func ProvideServiceC() *ServiceC {
	return &ServiceC{}
}

type ServiceA struct{}
type ServiceB struct{}
type ServiceC struct{}

//zero:api GET /test
func (s *ServiceA) GetTest(ctx context.Context, w http.ResponseWriter, r *http.Request) {
}

//zero:api POST /other
func (s *ServiceB) PostOther(ctx context.Context, w http.ResponseWriter, r *http.Request) {
}
`

	// Test with nil roots and APIs present - API receivers should be used as roots
	graph, err := analyseCodeString(t.Context(), code, nil)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(graph.APIs))

	// Only providers for API receivers should be kept (ServiceA and ServiceB)
	// ServiceC should be pruned since it's not an API receiver
	assert.Equal(t, 2, len(graph.Providers))

	// Verify API receiver providers are present
	_, hasServiceA := graph.Providers["*test.ServiceA"]
	_, hasServiceB := graph.Providers["*test.ServiceB"]
	assert.True(t, hasServiceA)
	assert.True(t, hasServiceB)

	// Verify non-API receiver provider is pruned
	_, hasServiceC := graph.Providers["*test.ServiceC"]
	assert.False(t, hasServiceC)
}

func TestGraph(t *testing.T) {
	testCode := `
package main

import "database/sql"

type Config struct {
	URL string
}

//zero:provider
func NewConfig() *Config {
	return &Config{}
}

//zero:provider
func NewDB(cfg *Config) (*sql.DB, error) {
	return nil, nil
}

//zero:provider
func NewService(db *sql.DB) *Service {
	return &Service{DB: db}
}

type Service struct {
	DB *sql.DB
}
`
	graph := analyseTestCode(t, testCode, []string{"*test.Service"})

	depGraph := graph.Graph()

	// Check that all providers are in the dependency graph
	_, hasConfig := depGraph["*test.Config"]
	_, hasDB := depGraph["*database/sql.DB"]
	_, hasService := depGraph["*test.Service"]
	assert.True(t, hasConfig)
	assert.True(t, hasDB)
	assert.True(t, hasService)

	// Check dependencies
	assert.Equal(t, []string{}, depGraph["*test.Config"])                    // Config has no dependencies
	assert.Equal(t, []string{"*Config"}, depGraph["*database/sql.DB"])       // DB depends on Config
	assert.Equal(t, []string{"*database/sql.DB"}, depGraph["*test.Service"]) // Service depends on DB
}

func TestGraphWithConfigs(t *testing.T) {
	testCode := `
package main

//zero:config
type DatabaseConfig struct {
	URL string
}

//zero:config
type AppConfig struct {
	Port int
}

//zero:provider
func NewService(dbCfg *DatabaseConfig, appCfg *AppConfig) *Service {
	return &Service{}
}

type Service struct {}
`
	graph := analyseTestCode(t, testCode, []string{"*test.Service"})

	depGraph := graph.Graph()

	// Check that configs are in the dependency graph
	_, hasDBConfig := depGraph["test.DatabaseConfig"]
	_, hasAppConfig := depGraph["test.AppConfig"]
	_, hasService := depGraph["*test.Service"]
	assert.True(t, hasDBConfig)
	assert.True(t, hasAppConfig)
	assert.True(t, hasService)

	// Check dependencies - configs have no dependencies
	assert.Equal(t, []string{}, depGraph["test.DatabaseConfig"])
	assert.Equal(t, []string{}, depGraph["test.AppConfig"])

	// Service depends on both configs
	serviceDeps := depGraph["*test.Service"]
	expectedDeps := []string{"*DatabaseConfig", "*AppConfig"}
	assert.Equal(t, expectedDeps, serviceDeps)
}

func TestFunctionRef(t *testing.T) {
	testCode := `
package main

import "database/sql"

//zero:provider
func NewDB() *sql.DB {
	return nil
}

//zero:provider
func NewService() *Service {
	return nil
}

type Service struct{}
`
	graph := analyseTestCode(t, testCode, []string{"*database/sql.DB", "*test.Service"})

	// Test function reference for standard library package
	dbProvider, ok := graph.Providers["*database/sql.DB"]
	assert.True(t, ok)
	dbFuncRef := graph.FunctionRef(dbProvider.Function)
	assert.Equal(t, "test", dbFuncRef.Pkg) // Same package as destination
	assert.Equal(t, "", dbFuncRef.Import)
	assert.Equal(t, "NewDB", dbFuncRef.Ref)

	// Test function reference for same package
	serviceProvider, ok := graph.Providers["*test.Service"]
	assert.True(t, ok)
	serviceFuncRef := graph.FunctionRef(serviceProvider.Function)
	assert.Equal(t, "test", serviceFuncRef.Pkg)
	assert.Equal(t, "", serviceFuncRef.Import) // Same package
	assert.Equal(t, "NewService", serviceFuncRef.Ref)
}

func TestAnalyseMiddlewareFunctions(t *testing.T) {
	testCode := `
package test

import "net/http"

//zero:middleware
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// logging logic
		next.ServeHTTP(w, r)
	})
}

//zero:middleware auth
func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// auth logic
		next.ServeHTTP(w, r)
	})
}

//zero:middleware cors ratelimit
func CorsRateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// cors and rate limiting logic
		next.ServeHTTP(w, r)
	})
}

type DAL struct{}

//zero:middleware authenticated
func AuthMiddlewareFactory(dal *DAL) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// auth logic with DAL
			next.ServeHTTP(w, r)
		})
	}
}

//zero:provider
func NewDAL() *DAL {
	return &DAL{}
}
`
	graph := analyseTestCode(t, testCode, []string{"*test.DAL"})

	// Should find 4 middleware functions
	assert.Equal(t, 4, len(graph.Middleware))

	// Test global middleware (no labels)
	var globalMiddleware *Middleware
	for _, mw := range graph.Middleware {
		if mw.Function.Name() == "LoggingMiddleware" {
			globalMiddleware = mw
			break
		}
	}
	assert.NotZero(t, globalMiddleware)
	assert.Equal(t, "LoggingMiddleware", globalMiddleware.Function.Name())
	assert.Equal(t, 0, len(globalMiddleware.Directive.Labels))

	// Test middleware with single label
	var authMiddleware *Middleware
	for _, mw := range graph.Middleware {
		if mw.Function.Name() == "AuthMiddleware" {
			authMiddleware = mw
			break
		}
	}
	assert.NotZero(t, authMiddleware)
	assert.Equal(t, "AuthMiddleware", authMiddleware.Function.Name())
	assert.Equal(t, []string{"auth"}, authMiddleware.Directive.Labels)

	// Test middleware with multiple labels
	var corsRateLimitMiddleware *Middleware
	for _, mw := range graph.Middleware {
		if mw.Function.Name() == "CorsRateLimitMiddleware" {
			corsRateLimitMiddleware = mw
			break
		}
	}
	assert.NotZero(t, corsRateLimitMiddleware)
	assert.Equal(t, "CorsRateLimitMiddleware", corsRateLimitMiddleware.Function.Name())
	assert.Equal(t, []string{"cors", "ratelimit"}, corsRateLimitMiddleware.Directive.Labels)

	// Test middleware factory with dependencies
	var authFactoryMiddleware *Middleware
	for _, mw := range graph.Middleware {
		if mw.Function.Name() == "AuthMiddlewareFactory" {
			authFactoryMiddleware = mw
			break
		}
	}
	assert.NotZero(t, authFactoryMiddleware)
	assert.Equal(t, "AuthMiddlewareFactory", authFactoryMiddleware.Function.Name())
	assert.Equal(t, []string{"authenticated"}, authFactoryMiddleware.Directive.Labels)
}

func TestAnalyseInvalidMiddlewareFunction(t *testing.T) {
	testCode := `
package test

//zero:middleware invalid
func InvalidMiddleware() string {
	return "not a middleware"
}

//zero:provider
func NewService() *Service {
	return &Service{}
}

type Service struct{}
`
	_, err := analyseTestCodeWithError(t, testCode, []string{"*test.Service"})
	assert.Error(t, err)
	assert.EqualError(t, err, "invalid middleware function signature for InvalidMiddleware: must be func(http.Handler) http.Handler or func(...deps) func(http.Handler) http.Handler")
}

func TestAnalyseMultiProviders(t *testing.T) {
	testCode := `
package main

//zero:provider multi
func NewSliceA() []string {
	return []string{"a"}
}

//zero:provider multi
func NewSliceB() []string {
	return []string{"b"}
}

//zero:provider
func NewService(items []string) *Service {
	return &Service{Items: items}
}

type Service struct {
	Items []string
}
`
	graph := analyseTestCode(t, testCode, []string{"*test.Service"})
	assert.Equal(t, 1, len(graph.Providers))
	assert.Equal(t, 1, len(graph.MultiProviders))
	assert.Equal(t, 0, len(graph.Missing))

	// Should have multi-providers for []string
	multiProviders, ok := graph.MultiProviders["[]string"]
	assert.True(t, ok)
	assert.Equal(t, 2, len(multiProviders))

	// Should have regular provider for Service
	serviceProvider, ok := graph.Providers["*test.Service"]
	assert.True(t, ok)
	assert.Equal(t, 1, len(serviceProvider.Requires))

	// Test GetProviders method
	sliceProviders := graph.GetProviders("[]string")
	assert.Equal(t, 2, len(sliceProviders))

	serviceProviders := graph.GetProviders("*test.Service")
	assert.Equal(t, 1, len(serviceProviders))

	nonExistentProviders := graph.GetProviders("NonExistent")
	assert.Zero(t, nonExistentProviders)
}

func TestAnalyseMixedMultiAndNonMultiProviders(t *testing.T) {
	testCode := `
package main

//zero:provider multi
func NewSliceA() []string {
	return []string{"a"}
}

//zero:provider
func NewSliceB() []string {
	return []string{"b"}
}

//zero:provider
func NewService(items []string) *Service {
	return &Service{Items: items}
}

type Service struct {
	Items []string
}
`
	_, err := analyseTestCodeWithError(t, testCode, []string{"*test.Service"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "type []string has mixed multi and non-multi providers")
}

func TestAnalyseMultiProvidersOnly(t *testing.T) {
	testCode := `
package main

//zero:provider multi
func NewMapA() map[string]int {
	return map[string]int{"a": 1}
}

//zero:provider multi
func NewMapB() map[string]int {
	return map[string]int{"b": 2}
}

//zero:provider multi
func NewMapC() map[string]int {
	return map[string]int{"c": 3}
}

//zero:provider
func NewService(items map[string]int) *Service {
	return &Service{Items: items}
}

type Service struct {
	Items map[string]int
}
`
	graph := analyseTestCode(t, testCode, []string{"*test.Service"})
	assert.Equal(t, 1, len(graph.Providers))
	assert.Equal(t, 1, len(graph.MultiProviders))
	assert.Equal(t, 0, len(graph.Missing))

	// Should have multi-providers for map[string]int
	multiProviders, ok := graph.MultiProviders["map[string]int"]
	assert.True(t, ok)
	assert.Equal(t, 3, len(multiProviders))

	// Verify all providers are marked as multi
	for _, provider := range multiProviders {
		assert.True(t, provider.Directive.Multi)
	}
}

func TestAnalyseMultiProviderPruning(t *testing.T) {
	testCode := `
package main

//zero:provider multi
func NewSliceA() []string {
	return []string{"a"}
}

//zero:provider multi
func NewSliceB() []string {
	return []string{"b"}
}

//zero:provider multi
func NewUnusedSlice() []int {
	return []int{1, 2, 3}
}

//zero:provider
func NewService(items []string) *Service {
	return &Service{Items: items}
}

type Service struct {
	Items []string
}
`
	graph := analyseTestCode(t, testCode, []string{"*test.Service"})
	assert.Equal(t, 1, len(graph.Providers))
	assert.Equal(t, 1, len(graph.MultiProviders))
	assert.Equal(t, 0, len(graph.Missing))

	// Should have multi-providers for []string but not for []int (unreferenced)
	multiProviders, ok := graph.MultiProviders["[]string"]
	assert.True(t, ok)
	assert.Equal(t, 2, len(multiProviders))

	// Should not have multi-providers for []int (pruned because unreferenced)
	_, ok = graph.MultiProviders["[]int"]
	assert.False(t, ok)
}

func TestAnalyseCronFunctions(t *testing.T) {
	testCode := `
package main

import (
	"context"
)

type CronService struct{}

//zero:cron 1h
func (s *CronService) HourlyTask(ctx context.Context) error {
	return nil
}

//zero:cron 30m
func (s *CronService) HalfHourlyTask(ctx context.Context) error {
	return nil
}

//zero:cron 1d
func (s *CronService) DailyTask(ctx context.Context) error {
	return nil
}
`
	graph := analyseTestCode(t, testCode, nil)
	assert.Equal(t, 3, len(graph.CronJobs))

	// Check first cron job
	cron1 := graph.CronJobs[0]
	assert.Equal(t, "HourlyTask", cron1.Function.Name())
	assert.Equal(t, "1h", cron1.Schedule.Schedule)

	// Check second cron job
	cron2 := graph.CronJobs[1]
	assert.Equal(t, "HalfHourlyTask", cron2.Function.Name())
	assert.Equal(t, "30m", cron2.Schedule.Schedule)

	// Check third cron job
	cron3 := graph.CronJobs[2]
	assert.Equal(t, "DailyTask", cron3.Function.Name())
	assert.Equal(t, "1d", cron3.Schedule.Schedule)
}

func TestAnalyseCronAnnotationOnFunction(t *testing.T) {
	testCode := `
package main

import "context"

//zero:cron 1h
func StandaloneCronFunction(ctx context.Context) error {
	return nil
}
`
	_, err := analyseTestCodeWithError(t, testCode, nil)
	assert.EqualError(t, err, "//zero:cron annotation is only valid on methods, not functions: StandaloneCronFunction")
}

func TestAnalyseCronInvalidSignatureNoParameters(t *testing.T) {
	testCode := `
package main

type CronService struct{}

//zero:cron 1h
func (s *CronService) InvalidCron() error {
	return nil
}
`
	_, err := analyseTestCodeWithError(t, testCode, nil)
	assert.EqualError(t, err, "cron method InvalidCron must have exactly one parameter of type context.Context")
}

func TestAnalyseCronInvalidSignatureTooManyParameters(t *testing.T) {
	testCode := `
package main

import "context"

type CronService struct{}

//zero:cron 1h
func (s *CronService) InvalidCron(ctx context.Context, extra string) error {
	return nil
}
`
	_, err := analyseTestCodeWithError(t, testCode, nil)
	assert.EqualError(t, err, "cron method InvalidCron must have exactly one parameter of type context.Context")
}

func TestAnalyseCronInvalidSignatureWrongParameterType(t *testing.T) {
	testCode := `
package main

type CronService struct{}

//zero:cron 1h
func (s *CronService) InvalidCron(notContext string) error {
	return nil
}
`
	_, err := analyseTestCodeWithError(t, testCode, nil)
	assert.EqualError(t, err, "cron method InvalidCron first parameter must be context.Context, got string")
}

func TestAnalyseCronInvalidSignatureNoReturnValue(t *testing.T) {
	testCode := `
package main

import "context"

type CronService struct{}

//zero:cron 1h
func (s *CronService) InvalidCron(ctx context.Context) {
}
`
	_, err := analyseTestCodeWithError(t, testCode, nil)
	assert.EqualError(t, err, "cron method InvalidCron must return exactly one value of type error")
}

func TestAnalyseCronInvalidSignatureTooManyReturnValues(t *testing.T) {
	testCode := `
package main

import "context"

type CronService struct{}

//zero:cron 1h
func (s *CronService) InvalidCron(ctx context.Context) (string, error) {
	return "", nil
}
`
	_, err := analyseTestCodeWithError(t, testCode, nil)
	assert.EqualError(t, err, "cron method InvalidCron must return exactly one value of type error")
}

func TestAnalyseCronInvalidSignatureWrongReturnType(t *testing.T) {
	testCode := `
package main

import "context"

type CronService struct{}

//zero:cron 1h
func (s *CronService) InvalidCron(ctx context.Context) string {
	return ""
}
`
	_, err := analyseTestCodeWithError(t, testCode, nil)
	assert.EqualError(t, err, "cron method InvalidCron must return error, got string")
}

func TestAnalyseMixedProvidersAPIsCrons(t *testing.T) {
	testCode := `
package main

import (
	"context"
	"database/sql"
)

type Service struct{}

//zero:provider
func CreateService() *Service {
	return &Service{}
}

//zero:api GET /users
func (s *Service) GetUsers(ctx context.Context) ([]string, error) {
	return []string{}, nil
}

//zero:cron 1h
func (s *Service) HourlyCleanup(ctx context.Context) error {
	return nil
}
`
	graph := analyseTestCode(t, testCode, []string{"*test.Service"})

	assert.Equal(t, 1, len(graph.Providers))
	assert.Equal(t, 1, len(graph.APIs))
	assert.Equal(t, 1, len(graph.CronJobs))

	// Check provider
	provider, ok := graph.Providers["*test.Service"]
	assert.True(t, ok)
	assert.Equal(t, "CreateService", provider.Function.Name())

	// Check API
	api := graph.APIs[0]
	assert.Equal(t, "GetUsers", api.Function.Name())

	// Check cron
	cron := graph.CronJobs[0]
	assert.Equal(t, "HourlyCleanup", cron.Function.Name())
	assert.Equal(t, "1h", cron.Schedule.Schedule)
}
