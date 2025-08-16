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
	stringType = types.Typ[types.String]
	intType    = types.Typ[types.Int]
	boolType   = types.Typ[types.Bool]

	// Named types
	appConfigType = types.NewNamed(types.NewTypeName(token.NoPos, testPackage.Types, "Config", nil), types.NewStruct(nil, nil), nil)
	dbConfigType  = types.NewNamed(types.NewTypeName(token.NoPos, testPackage.Types, "DatabaseConfig", nil), types.NewStruct(nil, nil), nil)
	loggerType    = types.NewNamed(types.NewTypeName(token.NoPos, testPackage.Types, "Logger", nil), types.NewStruct(nil, nil), nil)
	dbType        = types.NewNamed(types.NewTypeName(token.NoPos, testPackage.Types, "Database", nil), types.NewStruct(nil, nil), nil)
	serviceType   = types.NewNamed(types.NewTypeName(token.NoPos, testPackage.Types, "UserService", nil), types.NewStruct(nil, nil), nil)
	eventType     = types.NewNamed(types.NewTypeName(token.NoPos, testPackage.Types, "UserEvent", nil), types.NewStruct(nil, nil), nil)
)

// Sample package for testing
var testPackage = &packages.Package{
	PkgPath: "github.com/example/app",
	Name:    "app",
	Types:   types.NewPackage("github.com/example/app", "app"),
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
		Function: types.NewFunc(token.NoPos, testPackage.Types, "NewWeakLoggerB",
			types.NewSignatureType(nil, nil, nil,
				types.NewTuple(),
				types.NewTuple(types.NewVar(token.NoPos, testPackage.Types, "", loggerType)),
				false)),
		Package:  testPackage,
		Provides: loggerType,
	}
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
		pubsubPackage := &packages.Package{
			PkgPath: "github.com/alecthomas/zero/providers/pubsub",
			Name:    "pubsub",
			Types:   types.NewPackage("github.com/alecthomas/zero/providers/pubsub", "pubsub"),
		}

		// Generic Topic[T] type with type parameter
		topicTypeParam := types.NewTypeParam(types.NewTypeName(token.NoPos, nil, "T", nil), types.Universe.Lookup("any").Type())
		topicTypeName := types.NewTypeName(token.NoPos, pubsubPackage.Types, "Topic", nil)
		topicType := types.NewNamed(topicTypeName, types.NewStruct(nil, nil), nil)
		topicType.SetTypeParams([]*types.TypeParam{topicTypeParam})

		// Separate type parameter for the function signature
		funcTypeParam := types.NewTypeParam(types.NewTypeName(token.NoPos, nil, "T", nil), types.Universe.Lookup("any").Type())

		return &Provider{
			Position:  token.Position{Filename: "pubsub.go", Line: 10},
			Directive: &directiveparser.DirectiveProvider{},
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

// Sample collections for different test scenarios
var (
	// Simple linear dependency chain: config -> logger -> db -> service
	linearDependencyNodes = []Node{
		appConfig,
		loggerProvider,
		dbProvider,
		serviceProvider,
	}

	// Multi-provider scenario
	multiProviderNodes = []Node{
		appConfig,
		dbProvider,
		consoleLoggerProvider,
		fileLoggerProvider,
		serviceProvider,
	}

	// Full application with APIs, middleware, cron jobs
	fullApplicationNodes = []Node{
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
	}

	// Nodes with weak and strong providers for the same type
	strongAndWeakNodes = []Node{
		appConfig,
		dbConfig,
		loggerProvider,      //strong
		weakLoggerProviderA, // weak
		weakLoggerProviderB, // weak
		dbProvider,
		weakDatabaseProvider,
		serviceProvider,
	}

	// Nodes with no dependencies (roots)
	rootNodes = []Node{
		appConfig,
		dbConfig,
		newTopicProvider,
	}

	// Nodes with no return type (consumers)
	consumerNodes = []Node{
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
	}

	// Nodes with ambiguous providers for the same type
	ambiguousNodes = []Node{
		appConfig,
		weakLoggerProviderA, // weak
		weakLoggerProviderB, // weak
		dbConfig,
		dbProvider,
		weakDatabaseProvider,
		serviceProvider,
	}
)

func TestIR(t *testing.T) {
	tests := []struct {
		name    string
		nodes   []Node
		options []Option
		err     string
		graph   map[Key][]Key
	}{
		{
			name:  "LinearDependencyNodes",
			nodes: linearDependencyNodes,
		},
		{
			name:  "MultiProviderNodes",
			nodes: multiProviderNodes,
		},
		{
			name:  "FullApplicationNodes",
			nodes: fullApplicationNodes,
		},
		{
			name:  "RootNodes",
			nodes: rootNodes,
		},
		{
			name:  "ConsumerNodes",
			nodes: consumerNodes,
		},
		{
			name:  "StrongAndWeakNodes",
			nodes: strongAndWeakNodes,
		},
		{
			name:  "AmbiguousNodes",
			nodes: ambiguousNodes,
			options: []Option{
				WithProviders(string(weakLoggerProviderA.NodeKey().String())),
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
			},
			graph: map[Key][]Key{
				serviceProvider.NodeKey(): {
					TypeKey(appConfig.NodeType().String()),
					TypeKey("github.com/example/app.Database"),
					TypeKey("github.com/example/app.Logger"),
					TypeKey("github.com/example/app.UserService"),
				},
				reportCronJob.NodeKey(): {
					TypeKey("github.com/example/app.UserService"),
				},
				weakDatabaseProvider.NodeKey(): {
					TypeKey(dbConfigType.String()),
				},
				weakLoggerProviderA.NodeKey(): {},
				TypeKey(dbType.String()): {
					weakDatabaseProvider.NodeKey(),
				},
				weakLoggerProviderA.NodeKey(): {
					TypeKey(loggerType.String()),
				},
				TypeKey(serviceType.String()): {
					serviceProvider.NodeKey(),
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
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
