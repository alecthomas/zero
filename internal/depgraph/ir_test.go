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
	configType  = types.NewNamed(types.NewTypeName(token.NoPos, testPackage.Types, "Config", nil), types.NewStruct(nil, nil), nil)
	loggerType  = types.NewNamed(types.NewTypeName(token.NoPos, testPackage.Types, "Logger", nil), types.NewStruct(nil, nil), nil)
	dbType      = types.NewNamed(types.NewTypeName(token.NoPos, testPackage.Types, "Database", nil), types.NewStruct(nil, nil), nil)
	serviceType = types.NewNamed(types.NewTypeName(token.NoPos, testPackage.Types, "UserService", nil), types.NewStruct(nil, nil), nil)
	eventType   = types.NewNamed(types.NewTypeName(token.NoPos, testPackage.Types, "UserEvent", nil), types.NewStruct(nil, nil), nil)
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
		Type:     configType,
		Directive: &directiveparser.DirectiveConfig{
			Prefix: "app-",
		},
	}

	dbConfig = &Config{
		Position: token.Position{Filename: "config.go", Line: 20},
		Package:  testPackage,
		Type:     types.NewNamed(types.NewTypeName(token.NoPos, testPackage.Types, "DatabaseConfig", nil), types.NewStruct(nil, nil), nil),
		Directive: &directiveparser.DirectiveConfig{
			Prefix: "db-",
		},
	}
)

// Sample Provider nodes
var (
	loggerProvider = &Provider{
		Position: token.Position{Filename: "logger.go", Line: 15},
		Directive: &directiveparser.DirectiveProvider{
			Multi: false,
		},
		Function: types.NewFunc(token.NoPos, testPackage.Types, "NewLogger",
			types.NewSignature(nil,
				types.NewTuple(types.NewVar(token.NoPos, testPackage.Types, "cfg", configType)),
				types.NewTuple(types.NewVar(token.NoPos, testPackage.Types, "", loggerType)),
				false)),
		Package:  testPackage,
		Provides: loggerType,
		Requires: []types.Type{configType},
	}

	dbProvider = &Provider{
		Position: token.Position{Filename: "database.go", Line: 25},
		Directive: &directiveparser.DirectiveProvider{
			Multi: false,
		},
		Function: types.NewFunc(token.NoPos, testPackage.Types, "NewDatabase",
			types.NewSignature(nil,
				types.NewTuple(
					types.NewVar(token.NoPos, testPackage.Types, "cfg", configType),
					types.NewVar(token.NoPos, testPackage.Types, "logger", loggerType),
				),
				types.NewTuple(types.NewVar(token.NoPos, testPackage.Types, "", dbType)),
				false)),
		Package:  testPackage,
		Provides: dbType,
		Requires: []types.Type{configType, loggerType},
	}

	serviceProvider = &Provider{
		Position: token.Position{Filename: "service.go", Line: 30},
		Directive: &directiveparser.DirectiveProvider{
			Multi: false,
		},
		Function: types.NewFunc(token.NoPos, testPackage.Types, "NewUserService",
			types.NewSignature(nil,
				types.NewTuple(
					types.NewVar(token.NoPos, testPackage.Types, "db", dbType),
					types.NewVar(token.NoPos, testPackage.Types, "logger", loggerType),
				),
				types.NewTuple(types.NewVar(token.NoPos, testPackage.Types, "", serviceType)),
				false)),
		Package:  testPackage,
		Provides: serviceType,
		Requires: []types.Type{dbType, loggerType},
	}

	// Multi-provider examples
	consoleLoggerProvider = &Provider{
		Position: token.Position{Filename: "logging.go", Line: 10},
		Directive: &directiveparser.DirectiveProvider{
			Multi: true,
		},
		Function: types.NewFunc(token.NoPos, testPackage.Types, "NewConsoleLogger",
			types.NewSignature(nil,
				types.NewTuple(types.NewVar(token.NoPos, testPackage.Types, "cfg", configType)),
				types.NewTuple(types.NewVar(token.NoPos, testPackage.Types, "", loggerType)),
				false)),
		Package:  testPackage,
		Provides: loggerType,
		Requires: []types.Type{configType},
	}

	fileLoggerProvider = &Provider{
		Position: token.Position{Filename: "logging.go", Line: 20},
		Directive: &directiveparser.DirectiveProvider{
			Multi: true,
		},
		Function: types.NewFunc(token.NoPos, testPackage.Types, "NewFileLogger",
			types.NewSignature(nil,
				types.NewTuple(types.NewVar(token.NoPos, testPackage.Types, "cfg", configType)),
				types.NewTuple(types.NewVar(token.NoPos, testPackage.Types, "", loggerType)),
				false)),
		Package:  testPackage,
		Provides: loggerType,
		Requires: []types.Type{configType},
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
			types.NewSignature(
				types.NewVar(token.NoPos, testPackage.Types, "UserService", serviceType), // receiver
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
			types.NewSignature(
				types.NewVar(token.NoPos, testPackage.Types, "UserService", serviceType), // receiver
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
			types.NewSignature(nil,
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
			types.NewSignature(nil,
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
			types.NewSignature(
				types.NewVar(token.NoPos, testPackage.Types, "UserService", serviceType), // receiver
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
			types.NewSignature(
				types.NewVar(token.NoPos, testPackage.Types, "UserService", serviceType), // receiver
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
			types.NewSignature(
				types.NewVar(token.NoPos, testPackage.Types, "UserService", serviceType), // receiver
				types.NewTuple(types.NewVar(token.NoPos, testPackage.Types, "event", eventType)),
				types.NewTuple(),
				false)),
		Package:   testPackage,
		TopicType: eventType,
	}
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
	}

	// Nodes with no dependencies (roots)
	rootNodes = []Node{
		appConfig,
		dbConfig,
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
	}
)

func TestNewIR(t *testing.T) {
	tests := []struct {
		name  string
		graph []Node
	}{
		{
			name:  "LinearDependencyNodes",
			graph: linearDependencyNodes,
		},
		{
			name:  "MultiProviderNodes",
			graph: multiProviderNodes,
		},
		// {
		// 	name:  "FullApplicationNodes",
		// 	graph: fullApplicationNodes,
		// },
		{
			name:  "RootNodes",
			graph: rootNodes,
		},
		{
			name:  "ConsumerNodes",
			graph: consumerNodes,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewIR(test.graph)
			assert.NoError(t, err)
		})
	}
}
