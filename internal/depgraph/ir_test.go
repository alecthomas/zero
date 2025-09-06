package depgraph

import (
	"go/token"
	"go/types"
	"testing"

	"golang.org/x/tools/go/packages"

	"github.com/alecthomas/assert/v2"
	"github.com/alecthomas/zero/internal/directiveparser"
)

// Sample types for testing
var (
	// string
	stringType = types.Typ[types.String]

	// Named types
	// type Config struct {}
	appConfigType = types.NewNamed(types.NewTypeName(token.NoPos, testPackage.Types, "Config", nil), types.NewStruct(nil, nil), nil)
	// type DatabaseConfig struct {}
	dbConfigType = types.NewNamed(types.NewTypeName(token.NoPos, testPackage.Types, "DatabaseConfig", nil), types.NewStruct(nil, nil), nil)
	// type Logger struct {}
	loggerType = types.NewNamed(types.NewTypeName(token.NoPos, testPackage.Types, "Logger", nil), types.NewStruct(nil, nil), nil)
	// type Database struct {}
	dbType = types.NewNamed(types.NewTypeName(token.NoPos, testPackage.Types, "Database", nil), types.NewStruct(nil, nil), nil)
	// type UserService struct {}
	serviceType = types.NewNamed(types.NewTypeName(token.NoPos, testPackage.Types, "UserService", nil), types.NewStruct(nil, nil), nil)
	// type UserEvent struct {}
	eventType = types.NewNamed(types.NewTypeName(token.NoPos, testPackage.Types, "UserEvent", nil), types.NewStruct(nil, nil), nil)
)

// Sample package for testing
var testPackage = &packages.Package{
	PkgPath: "github.com/example/app",
	Name:    "app",
	// package app ("github.com/example/app")
	Types: types.NewPackage("github.com/example/app", "app"),
}

// Sample Config nodes
var (
	appConfig = &Config{
		Position: token.Position{Filename: "config.go", Line: 10},
		Package:  testPackage,
		Type:     appConfigType,
		Directive: &directiveparser.DirectiveConfig{
			Prefix: "app-",
		},
	}

	dbConfig = &Config{
		Position: token.Position{Filename: "config.go", Line: 20},
		Package:  testPackage,
		Type:     dbConfigType,
		Directive: &directiveparser.DirectiveConfig{
			Prefix: "db-",
		},
	}
)

// Sample Provider nodes
var (
	loggerProvider = &Provider{
		Position:  token.Position{Filename: "logger.go", Line: 15},
		Directive: &directiveparser.DirectiveProvider{},
		// func NewLogger(cfg Config) Logger
		Function: types.NewFunc(token.NoPos, testPackage.Types, "NewLogger",
			types.NewSignatureType(nil, nil, nil,
				types.NewTuple(types.NewVar(token.NoPos, testPackage.Types, "cfg", appConfigType)),
				types.NewTuple(types.NewVar(token.NoPos, testPackage.Types, "", loggerType)),
				false)),
		Package:  testPackage,
		Provides: loggerType,
	}

	dbProvider = &Provider{
		Position:  token.Position{Filename: "database.go", Line: 25},
		Directive: &directiveparser.DirectiveProvider{},
		// func NewDatabase(cfg Config, logger Logger) Database
		Function: types.NewFunc(token.NoPos, testPackage.Types, "NewDatabase",
			types.NewSignatureType(nil, nil, nil,
				types.NewTuple(
					types.NewVar(token.NoPos, testPackage.Types, "cfg", appConfigType),
					types.NewVar(token.NoPos, testPackage.Types, "logger", loggerType),
				),
				types.NewTuple(types.NewVar(token.NoPos, testPackage.Types, "", dbType)),
				false)),
		Package:  testPackage,
		Provides: dbType,
	}

	serviceProvider = &Provider{
		Position:  token.Position{Filename: "service.go", Line: 30},
		Directive: &directiveparser.DirectiveProvider{},
		// func NewUserService(config Config, db Database, logger Logger) UserService
		Function: types.NewFunc(token.NoPos, testPackage.Types, "NewUserService",
			types.NewSignatureType(nil, nil, nil,
				types.NewTuple(
					types.NewVar(token.NoPos, testPackage.Types, "config", appConfigType),
					types.NewVar(token.NoPos, testPackage.Types, "db", dbType),
					types.NewVar(token.NoPos, testPackage.Types, "logger", loggerType),
				),
				types.NewTuple(types.NewVar(token.NoPos, testPackage.Types, "", serviceType)),
				false)),
		Package:  testPackage,
		Provides: serviceType,
	}

	// Multi-provider examples
	consoleLoggerProvider = &Provider{
		Position: token.Position{Filename: "logging.go", Line: 10},
		Directive: &directiveparser.DirectiveProvider{
			Multi: true,
		},
		// func NewConsoleLogger(cfg Config) Logger
		Function: types.NewFunc(token.NoPos, testPackage.Types, "NewConsoleLogger",
			types.NewSignatureType(nil, nil, nil,
				types.NewTuple(types.NewVar(token.NoPos, testPackage.Types, "cfg", appConfigType)),
				types.NewTuple(types.NewVar(token.NoPos, testPackage.Types, "", loggerType)),
				false)),
		Package:  testPackage,
		Provides: loggerType,
	}

	fileLoggerProvider = &Provider{
		Position: token.Position{Filename: "logging.go", Line: 20},
		Directive: &directiveparser.DirectiveProvider{
			Multi: true,
		},
		// func NewFileLogger() Logger
		Function: types.NewFunc(token.NoPos, testPackage.Types, "NewFileLogger",
			types.NewSignatureType(nil, nil, nil,
				types.NewTuple(),
				types.NewTuple(types.NewVar(token.NoPos, testPackage.Types, "", loggerType)),
				false)),
		Package:  testPackage,
		Provides: loggerType,
	}

	// Weak providers for testing ambiguity resolution
	weakDatabaseProvider = &Provider{
		Position: token.Position{Filename: "database.go", Line: 35},
		Directive: &directiveparser.DirectiveProvider{
			Weak: true,
		},
		// func NewWeakDatabase(cfg DatabaseConfig) Database
		Function: types.NewFunc(token.NoPos, testPackage.Types, "NewWeakDatabase",
			types.NewSignatureType(nil, nil, nil,
				types.NewTuple(types.NewVar(token.NoPos, testPackage.Types, "cfg", dbConfigType)),
				types.NewTuple(types.NewVar(token.NoPos, testPackage.Types, "", dbType)),
				false)),
		Package:  testPackage,
		Provides: dbType,
	}

	weakLoggerProviderA = &Provider{
		Position: token.Position{Filename: "logging.go", Line: 30},
		Directive: &directiveparser.DirectiveProvider{
			Weak: true,
		},
		// func NewWeakLoggerA() Logger
		Function: types.NewFunc(token.NoPos, testPackage.Types, "NewWeakLoggerA",
			types.NewSignatureType(nil, nil, nil,
				types.NewTuple(),
				types.NewTuple(types.NewVar(token.NoPos, testPackage.Types, "", loggerType)),
				false)),
		Package:  testPackage,
		Provides: loggerType,
	}

	weakLoggerProviderB = &Provider{
		Position: token.Position{Filename: "logging.go", Line: 30},
		Directive: &directiveparser.DirectiveProvider{
			Weak: true,
		},
		// func NewWeakLoggerB() Logger
		Function: types.NewFunc(token.NoPos, testPackage.Types, "NewWeakLoggerB",
			types.NewSignatureType(nil, nil, nil,
				types.NewTuple(),
				types.NewTuple(types.NewVar(token.NoPos, testPackage.Types, "", loggerType)),
				false)),
		Package:  testPackage,
		Provides: loggerType,
	}

	// Infrastructure providers
	// func NewWeakHttpServer() *net/http.Server
	weakHTTPServerProvider = createWeakInfraProvider("NewWeakHttpServer", "http.go",
		types.NewPointer(createNamedType("net/http", "http", "Server")))

	// func NewWeakCronScheduler() *github.com/alecthomas/zero/providers/cron.Scheduler
	weakCronSchedulerProvider = createWeakInfraProvider("NewWeakCronScheduler", "cron.go",
		types.NewPointer(createNamedType("github.com/alecthomas/zero/providers/cron", "cron", "Scheduler")))
)

// Sample API nodes
var (
	getUserAPI = &API{
		Position: token.Position{Filename: "api.go", Line: 40},
		Pattern: &directiveparser.DirectiveAPI{
			Method:   "GET",
			Host:     "",
			Segments: nil,
		},
		// func (UserService) GetUser(id string) UserService
		Function: types.NewFunc(token.NoPos, testPackage.Types, "GetUser",
			types.NewSignatureType(
				types.NewVar(token.NoPos, testPackage.Types, "UserService", serviceType), // receiver
				nil, nil,
				types.NewTuple(types.NewVar(token.NoPos, testPackage.Types, "id", stringType)),
				types.NewTuple(types.NewVar(token.NoPos, testPackage.Types, "", serviceType)),
				false)),
		Package: testPackage,
	}

	createUserAPI = &API{
		Position: token.Position{Filename: "api.go", Line: 50},
		Pattern: &directiveparser.DirectiveAPI{
			Method:   "POST",
			Host:     "",
			Segments: nil,
			Labels: []*directiveparser.Label{
				{Name: "auth", Value: "required"},
			},
		},
		// func (UserService) CreateUser(user UserService) UserService
		Function: types.NewFunc(token.NoPos, testPackage.Types, "CreateUser",
			types.NewSignatureType(
				types.NewVar(token.NoPos, testPackage.Types, "UserService", serviceType), // receiver
				nil, nil,
				types.NewTuple(types.NewVar(token.NoPos, testPackage.Types, "user", serviceType)),
				types.NewTuple(types.NewVar(token.NoPos, testPackage.Types, "", serviceType)),
				false)),
		Package: testPackage,
	}
)

// Sample Middleware nodes
var (
	authMiddleware = &Middleware{
		Position: token.Position{Filename: "middleware.go", Line: 15},
		Directive: &directiveparser.DirectiveMiddleware{
			Labels: []string{"auth"},
		},
		// func AuthMiddleware(logger Logger)
		Function: types.NewFunc(token.NoPos, testPackage.Types, "AuthMiddleware",
			types.NewSignatureType(nil, nil, nil,
				types.NewTuple(types.NewVar(token.NoPos, testPackage.Types, "logger", loggerType)),
				types.NewTuple(),
				false)),
		Package:  testPackage,
		Requires: []types.Type{loggerType},
		Factory:  true,
	}

	loggingMiddleware = &Middleware{
		Position: token.Position{Filename: "middleware.go", Line: 25},
		Directive: &directiveparser.DirectiveMiddleware{
			Labels: []string{},
		},
		// func LoggingMiddleware(logger Logger)
		Function: types.NewFunc(token.NoPos, testPackage.Types, "LoggingMiddleware",
			types.NewSignatureType(nil, nil, nil,
				types.NewTuple(types.NewVar(token.NoPos, testPackage.Types, "logger", loggerType)),
				types.NewTuple(),
				false)),
		Package:  testPackage,
		Requires: []types.Type{loggerType},
		Factory:  false,
	}
)

// Sample CronJob nodes
var (
	cleanupCronJob = &CronJob{
		Position: token.Position{Filename: "cron.go", Line: 10},
		Schedule: &directiveparser.DirectiveCron{
			Schedule: "5m",
		},
		// func (UserService) CleanupOldData()
		Function: types.NewFunc(token.NoPos, testPackage.Types, "CleanupOldData",
			types.NewSignatureType(
				types.NewVar(token.NoPos, testPackage.Types, "UserService", serviceType), // receiver
				nil, nil,
				types.NewTuple(),
				types.NewTuple(),
				false)),
		Package: testPackage,
	}

	reportCronJob = &CronJob{
		Position: token.Position{Filename: "cron.go", Line: 20},
		Schedule: &directiveparser.DirectiveCron{
			Schedule: "1w",
		},
		// func (UserService) GenerateWeeklyReport()
		Function: types.NewFunc(token.NoPos, testPackage.Types, "GenerateWeeklyReport",
			types.NewSignatureType(
				types.NewVar(token.NoPos, testPackage.Types, "UserService", serviceType), // receiver
				nil, nil,
				types.NewTuple(),
				types.NewTuple(),
				false)),
		Package: testPackage,
	}
)

// Sample Subscription nodes
var (
	userEventSubscription = &Subscription{
		Position: token.Position{Filename: "events.go", Line: 30},
		// func (UserService) HandleUserEvent(event UserEvent)
		Function: types.NewFunc(token.NoPos, testPackage.Types, "HandleUserEvent",
			types.NewSignatureType(
				types.NewVar(token.NoPos, testPackage.Types, "UserService", serviceType), // receiver
				nil, nil,
				types.NewTuple(types.NewVar(token.NoPos, testPackage.Types, "event", eventType)),
				types.NewTuple(),
				false)),
		Package:   testPackage,
		TopicType: eventType,
	}
)

// Sample pubsub provider
var (
	newTopicProvider = func() *Provider {
		// package pubsub ("github.com/alecthomas/zero/providers/pubsub")
		pubsubPackage := &packages.Package{
			PkgPath: "github.com/alecthomas/zero/providers/pubsub",
			Name:    "pubsub",
			Types:   types.NewPackage("github.com/alecthomas/zero/providers/pubsub", "pubsub"),
		}

		// Generic Topic[T] type with type parameter
		// type T any
		topicTypeParam := types.NewTypeParam(types.NewTypeName(token.NoPos, nil, "T", nil), types.Universe.Lookup("any").Type())
		// type Topic[T any] struct {}
		topicTypeName := types.NewTypeName(token.NoPos, pubsubPackage.Types, "Topic", nil)
		topicType := types.NewNamed(topicTypeName, types.NewStruct(nil, nil), nil)
		topicType.SetTypeParams([]*types.TypeParam{topicTypeParam})

		// Separate type parameter for the function signature
		// type T any
		funcTypeParam := types.NewTypeParam(types.NewTypeName(token.NoPos, nil, "T", nil), types.Universe.Lookup("any").Type())

		return &Provider{
			Position:  token.Position{Filename: "pubsub.go", Line: 10},
			Directive: &directiveparser.DirectiveProvider{},
			// func NewTopic[T any]() Topic[T]
			Function: types.NewFunc(token.NoPos, pubsubPackage.Types, "NewTopic",
				types.NewSignatureType(nil,
					nil,                               // no receiver type parameters
					[]*types.TypeParam{funcTypeParam}, // function type parameters
					types.NewTuple(),                  // no parameters
					types.NewTuple(types.NewVar(token.NoPos, nil, "", topicType)), // returns Topic[T]
					false)),
			Package:    pubsubPackage,
			Provides:   topicType,
			IsGeneric:  true,
			TypeParams: topicType.TypeParams(),
		}
	}()
)

// Helper functions for creating test providers
func createWeakInfraProvider(funcName, filename string, providesType types.Type) *Provider {
	return &Provider{
		Position: token.Position{Filename: filename, Line: 10},
		Directive: &directiveparser.DirectiveProvider{
			Weak: true,
		},
		// func <funcName>() <providesType>
		Function: types.NewFunc(token.NoPos, testPackage.Types, funcName,
			types.NewSignatureType(nil, nil, nil,
				types.NewTuple(),
				types.NewTuple(types.NewVar(token.NoPos, nil, "", providesType)),
				false)),
		Package:  testPackage,
		Provides: providesType,
	}
}

func createNamedType(pkgPath, pkgName, typeName string) *types.Named {
	// type <typeName> struct {}
	return types.NewNamed(
		types.NewTypeName(token.NoPos, types.NewPackage(pkgPath, pkgName), typeName, nil),
		types.NewStruct(nil, nil),
		nil,
	)
}

func TestIR(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		nodes   []Node
		options []Option
		err     string
		graph   map[Key][]Key
	}{
		{
			name: "LinearDependencyNodes",
			// Simple linear dependency chain: config -> logger -> db -> service
			nodes: []Node{
				appConfig,
				loggerProvider,
				dbProvider,
				serviceProvider,
			},
		},
		{
			name: "MultiProviderNodes",
			// Multi-provider scenario
			nodes: []Node{
				appConfig,
				dbProvider,
				consoleLoggerProvider,
				fileLoggerProvider,
				serviceProvider,
			},
		},
		{
			name: "FullApplicationNodes",
			// Full application with APIs, middleware, cron jobs
			nodes: []Node{
				appConfig,
				dbConfig,
				loggerProvider,
				dbProvider,
				serviceProvider,
				getUserAPI,
				createUserAPI,
				authMiddleware,
				loggingMiddleware,
				cleanupCronJob,
				reportCronJob,
				userEventSubscription,
				newTopicProvider,
				weakHTTPServerProvider,
				weakCronSchedulerProvider,
			},
		},
		{
			name: "RootNodes",
			// Nodes with no dependencies (roots)
			nodes: []Node{
				appConfig,
				dbConfig,
				newTopicProvider,
			},
		},
		{
			name: "ConsumerNodes",
			// Nodes with no return type (consumers)
			nodes: []Node{
				dbProvider,
				serviceProvider,
				getUserAPI,
				appConfig,
				loggerProvider,
				createUserAPI,
				cleanupCronJob,
				reportCronJob,
				userEventSubscription,
				newTopicProvider,
				weakHTTPServerProvider,
				weakCronSchedulerProvider,
			},
		},
		{
			name: "StrongAndWeakNodes",
			// Nodes with weak and strong providers for the same type
			nodes: []Node{
				appConfig,
				dbConfig,
				loggerProvider,      //strong
				weakLoggerProviderA, // weak
				weakLoggerProviderB, // weak
				dbProvider,
				weakDatabaseProvider,
				serviceProvider,
			},
		},
		{
			name: "AmbiguousNodes",
			// Nodes with ambiguous providers for the same type
			nodes: []Node{
				appConfig,
				weakLoggerProviderA, // weak
				weakLoggerProviderB, // weak
				dbConfig,
				dbProvider,
				weakDatabaseProvider,
				serviceProvider,
			},
			options: []Option{
				WithNodes(weakLoggerProviderA.NodeKey().(NodeKey)),
			},
		},
		{
			name: "CronMissingProvider",
			nodes: []Node{
				reportCronJob,
			},
			err: "there is no provider for type github.com/example/app.UserService, required by (github.com/example/app.UserService).GenerateWeeklyReport",
		},
		{
			name: "CronWithReceiver",
			nodes: []Node{
				serviceProvider,
				reportCronJob,
				dbConfig,
				weakDatabaseProvider,
				appConfig,
				weakLoggerProviderA,
				weakCronSchedulerProvider,
			},
			graph: map[Key][]Key{
				serviceProvider.NodeKey(): {
					TypeKey(appConfigType.String()),
					TypeKey(dbType.String()),
					TypeKey(loggerType.String()),
					TypeKey(serviceType.String()),
				},
				reportCronJob.NodeKey(): {
					TypeKey(serviceType.String()),
				},
				weakDatabaseProvider.NodeKey(): {
					TypeKey(dbConfigType.String()),
				},
				TypeKey(dbType.String()): {
					weakDatabaseProvider.NodeKey(),
				},
				TypeKey(loggerType.String()): {
					weakLoggerProviderA.NodeKey(),
				},
				TypeKey(serviceType.String()): {
					serviceProvider.NodeKey(),
					reportCronJob.NodeKey(),
				},
				TypeKey("*github.com/alecthomas/zero/providers/cron.Scheduler"): {
					weakCronSchedulerProvider.NodeKey(),
				},
			},
		},
		{
			name: "AmbiguousProvidersWithoutResolution",
			nodes: []Node{
				appConfig,
				dbConfig,
				weakLoggerProviderA, // weak
				weakLoggerProviderB, // weak - creates ambiguity
				dbProvider,          // provide database dependency
				serviceProvider,     // requires logger, making ambiguous node required
			},
			options: []Option{
				WithTypes("github.com/example/app.UserService"),
			},
			err: "conflicting providers for github.com/example/app.Logger, use --resolve=",
		},
		{
			name: "WeakAndstrong",
			nodes: []Node{
				dbProvider,
				appConfig,
				dbConfig,
				loggerProvider,
				weakDatabaseProvider,
			},
			graph: map[Key][]Key{
				TypeKey("github.com/example/app.Database"): {
					NodeKey("github.com/example/app.NewDatabase"),
				},
				TypeKey("github.com/example/app.Logger"): {
					NodeKey("github.com/example/app.NewLogger"),
				},
				NodeKey("github.com/example/app.NewDatabase"): {
					TypeKey("github.com/example/app.Config"),
					TypeKey("github.com/example/app.Logger"),
					TypeKey("github.com/example/app.Database"),
				},
				NodeKey("github.com/example/app.NewLogger"): {
					TypeKey("github.com/example/app.Config"),
					TypeKey("github.com/example/app.Logger"),
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ir, err := NewIR(test.nodes, test.options...)
			if test.err != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), test.err)
			} else {
				assert.NoError(t, err)
				if test.graph != nil {
					assert.Equal(t, test.graph, ir.Graph())
				}
			}
		})
	}
}

func TestAmbiguousNode(t *testing.T) {
	t.Parallel()

	// Test that Ambiguous node methods work correctly
	ambiguous := Ambiguous{weakLoggerProviderA, weakLoggerProviderB}

	// Test NodeKey returns first provider's key
	assert.Equal(t, weakLoggerProviderA.NodeKey(), ambiguous.NodeKey())

	// Test NodePosition returns first provider's position
	assert.Equal(t, weakLoggerProviderA.NodePosition(), ambiguous.NodePosition())

	// Test NodeRequires is consistent with individual providers
	assert.Equal(t, len(weakLoggerProviderA.NodeRequires())+len(weakLoggerProviderB.NodeRequires()), len(ambiguous.NodeRequires()))

	// Test NodeRequiredBy combines all providers' required-by relationships
	assert.Equal(t, len(weakLoggerProviderA.NodeRequiredBy())+len(weakLoggerProviderB.NodeRequiredBy()), len(ambiguous.NodeRequiredBy()))
}
