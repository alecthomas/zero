package depgraph

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"
	"testing"

	"github.com/alecthomas/assert/v2"
	"github.com/alecthomas/zero/internal/directiveparser"
	"github.com/go-openapi/spec"
	"golang.org/x/tools/go/packages"
)

func TestAPIGenerateOpenAPIOperation(t *testing.T) {
	tests := []struct {
		name     string
		funcSig  string
		pattern  *directiveparser.DirectiveAPI
		expected *spec.Operation //nolint
	}{
		{
			name:    "SimpleGetEndpoint",
			funcSig: "GetUser:ctx context.Context,userID string:*User,error",
			pattern: &directiveparser.DirectiveAPI{
				Method: "GET",
				Segments: []directiveparser.Segment{
					directiveparser.LiteralSegment{Literal: "users"},
					directiveparser.WildcardSegment{Name: "userID"},
				},
			},
			expected: &spec.Operation{ //nolint
				OperationProps: spec.OperationProps{
					Tags: []string{"test"},
					Parameters: []spec.Parameter{
						{
							ParamProps: spec.ParamProps{
								Name:     "userID",
								In:       "path",
								Required: true,
							},
							SimpleSchema: spec.SimpleSchema{
								Type: "string",
							},
						},
					},
					Responses: &spec.Responses{
						ResponsesProps: spec.ResponsesProps{
							StatusCodeResponses: map[int]spec.Response{
								200: {
									ResponseProps: spec.ResponseProps{
										Description: "Success",
										Schema: &spec.Schema{
											SchemaProps: spec.SchemaProps{
												Ref: spec.MustCreateRef("#/definitions/test.User"),
											},
										},
									},
								},
								400: {
									ResponseProps: spec.ResponseProps{
										Description: "Bad Request",
									},
								},
								500: {
									ResponseProps: spec.ResponseProps{
										Description: "Internal Server Error",
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name:    "PostWithBodyParameter",
			funcSig: "CreateUser:ctx context.Context,req CreateUserRequest:*User,error",
			pattern: &directiveparser.DirectiveAPI{
				Method: "POST",
				Segments: []directiveparser.Segment{
					directiveparser.LiteralSegment{Literal: "users"},
				},
			},
			expected: &spec.Operation{ //nolint
				OperationProps: spec.OperationProps{
					Tags: []string{"test"},
					Parameters: []spec.Parameter{
						{
							ParamProps: spec.ParamProps{
								Name:     "body",
								In:       "body",
								Required: true,
								Schema: &spec.Schema{
									SchemaProps: spec.SchemaProps{
										Ref: spec.MustCreateRef("#/definitions/test.CreateUserRequest"),
									},
								},
							},
						},
					},
					Responses: &spec.Responses{
						ResponsesProps: spec.ResponsesProps{
							StatusCodeResponses: map[int]spec.Response{
								200: {
									ResponseProps: spec.ResponseProps{
										Description: "Success",
										Schema: &spec.Schema{
											SchemaProps: spec.SchemaProps{
												Ref: spec.MustCreateRef("#/definitions/test.User"),
											},
										},
									},
								},
								400: {
									ResponseProps: spec.ResponseProps{
										Description: "Bad Request",
									},
								},
								500: {
									ResponseProps: spec.ResponseProps{
										Description: "Internal Server Error",
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name:    "DeleteEndpoint",
			funcSig: "DeleteUser:ctx context.Context,userID string:error",
			pattern: &directiveparser.DirectiveAPI{
				Method: "DELETE",
				Segments: []directiveparser.Segment{
					directiveparser.LiteralSegment{Literal: "users"},
					directiveparser.WildcardSegment{Name: "userID"},
				},
			},
			expected: &spec.Operation{ //nolint
				OperationProps: spec.OperationProps{
					Tags: []string{"test"},
					Parameters: []spec.Parameter{
						{
							ParamProps: spec.ParamProps{
								Name:     "userID",
								In:       "path",
								Required: true,
							},
							SimpleSchema: spec.SimpleSchema{
								Type: "string",
							},
						},
					},
					Responses: &spec.Responses{
						ResponsesProps: spec.ResponsesProps{
							StatusCodeResponses: map[int]spec.Response{
								204: {
									ResponseProps: spec.ResponseProps{
										Description: "No Content",
									},
								},
								400: {
									ResponseProps: spec.ResponseProps{
										Description: "Bad Request",
									},
								},
								500: {
									ResponseProps: spec.ResponseProps{
										Description: "Internal Server Error",
									},
								},
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := createMockAPI(t, tt.funcSig, tt.pattern)
			definitions := make(spec.Definitions)
			operation := api.GenerateOpenAPIOperation(definitions)

			assert.Equal(t, tt.expected, operation)
		})
	}
}

func TestAPIGenerateSchemaFromType(t *testing.T) {
	tests := []struct {
		name     string
		typeExpr string
		expected *spec.Schema
	}{
		{
			name:     "StringType",
			typeExpr: "string",
			expected: &spec.Schema{
				SchemaProps: spec.SchemaProps{
					Type: []string{"string"},
				},
			},
		},
		{
			name:     "IntegerType",
			typeExpr: "int",
			expected: &spec.Schema{
				SchemaProps: spec.SchemaProps{
					Type: []string{"integer"},
				},
			},
		},
		{
			name:     "BooleanType",
			typeExpr: "bool",
			expected: &spec.Schema{
				SchemaProps: spec.SchemaProps{
					Type: []string{"boolean"},
				},
			},
		},
		{
			name:     "SliceType",
			typeExpr: "[]string",
			expected: &spec.Schema{
				SchemaProps: spec.SchemaProps{
					Type: []string{"array"},
					Items: &spec.SchemaOrArray{
						Schema: &spec.Schema{
							SchemaProps: spec.SchemaProps{
								Type: []string{"string"},
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := createMockAPIWithType(t)
			typ := parseTypeExpression(t, tt.typeExpr)
			definitions := make(spec.Definitions)
			schema := api.generateSchemaFromType(typ, definitions)

			assert.Equal(t, tt.expected, schema)
		})
	}
}

func TestAPIGenerateSchemaFromStructWithJSONTags(t *testing.T) {
	api := createMockAPIWithType(t)

	// Create a struct type with fields that have JSON tags
	fields := []*types.Var{
		types.NewVar(token.NoPos, nil, "Name", types.Typ[types.String]),
		types.NewVar(token.NoPos, nil, "BirthYear", types.Typ[types.Int]),
		types.NewVar(token.NoPos, nil, "Email", types.Typ[types.String]),
		types.NewVar(token.NoPos, nil, "IsActive", types.Typ[types.Bool]),
	}

	tags := []string{
		`json:"name"`,
		`json:"birthYear"`,
		`json:"-"`,
		``,
	}

	structType := types.NewStruct(fields, tags)
	definitions := make(spec.Definitions)
	schema := api.generateSchemaFromType(structType, definitions)

	expected := &spec.Schema{
		SchemaProps: spec.SchemaProps{
			Type: []string{"object"},
			Properties: map[string]spec.Schema{
				"name": {
					SchemaProps: spec.SchemaProps{
						Type: []string{"string"},
					},
				},
				"birthYear": {
					SchemaProps: spec.SchemaProps{
						Type: []string{"integer"},
					},
				},
				"isActive": {
					SchemaProps: spec.SchemaProps{
						Type: []string{"boolean"},
					},
				},
			},
		},
	}

	assert.Equal(t, expected, schema)
}

func TestGraphGenerateOpenAPISpec(t *testing.T) {
	graph := &Graph{
		APIs: []*API{
			createMockAPI(t, "GetUser:ctx context.Context,userID string:*User,error", &directiveparser.DirectiveAPI{
				Method: "GET",
				Segments: []directiveparser.Segment{
					directiveparser.LiteralSegment{Literal: "users"},
					directiveparser.WildcardSegment{Name: "userID"},
				},
			}),
			createMockAPI(t, "CreateUser:ctx context.Context,req CreateUserRequest:*User,error", &directiveparser.DirectiveAPI{
				Method: "POST",
				Segments: []directiveparser.Segment{
					directiveparser.LiteralSegment{Literal: "users"},
				},
			}),
		},
	}

	// OpenAPI operations are now generated internally with shared definitions

	expected := &spec.Swagger{
		SwaggerProps: spec.SwaggerProps{
			Swagger: "2.0",
			Info: &spec.Info{
				InfoProps: spec.InfoProps{
					Title:   "Zero API",
					Version: "1.0.0",
				},
			},
			Paths: &spec.Paths{
				Paths: map[string]spec.PathItem{
					"/users/{userID}": {
						PathItemProps: spec.PathItemProps{
							Get: &spec.Operation{ //nolint
								OperationProps: spec.OperationProps{
									Tags: []string{"test"},
									Parameters: []spec.Parameter{
										{
											ParamProps: spec.ParamProps{
												Name:     "userID",
												In:       "path",
												Required: true,
											},
											SimpleSchema: spec.SimpleSchema{
												Type: "string",
											},
										},
									},
									Responses: &spec.Responses{
										ResponsesProps: spec.ResponsesProps{
											StatusCodeResponses: map[int]spec.Response{
												200: {
													ResponseProps: spec.ResponseProps{
														Description: "Success",
														Schema: &spec.Schema{
															SchemaProps: spec.SchemaProps{
																Ref: spec.MustCreateRef("#/definitions/test.User"),
															},
														},
													},
												},
												400: {
													ResponseProps: spec.ResponseProps{
														Description: "Bad Request",
													},
												},
												500: {
													ResponseProps: spec.ResponseProps{
														Description: "Internal Server Error",
													},
												},
											},
										},
									},
								},
							},
						},
					},
					"/users": {
						PathItemProps: spec.PathItemProps{
							Post: &spec.Operation{ //nolint
								OperationProps: spec.OperationProps{
									Tags: []string{"test"},
									Parameters: []spec.Parameter{
										{
											ParamProps: spec.ParamProps{
												Name:     "body",
												In:       "body",
												Required: true,
												Schema: &spec.Schema{
													SchemaProps: spec.SchemaProps{
														Ref: spec.MustCreateRef("#/definitions/test.CreateUserRequest"),
													},
												},
											},
										},
									},
									Responses: &spec.Responses{
										ResponsesProps: spec.ResponsesProps{
											StatusCodeResponses: map[int]spec.Response{
												200: {
													ResponseProps: spec.ResponseProps{
														Description: "Success",
														Schema: &spec.Schema{
															SchemaProps: spec.SchemaProps{
																Ref: spec.MustCreateRef("#/definitions/test.User"),
															},
														},
													},
												},
												400: {
													ResponseProps: spec.ResponseProps{
														Description: "Bad Request",
													},
												},
												500: {
													ResponseProps: spec.ResponseProps{
														Description: "Internal Server Error",
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			Definitions: spec.Definitions{
				"test.CreateUserRequest": spec.Schema{
					SchemaProps: spec.SchemaProps{
						Type:       []string{"object"},
						Properties: make(map[string]spec.Schema),
					},
				},
				"test.User": spec.Schema{
					SchemaProps: spec.SchemaProps{
						Type: []string{"object"},
					},
				},
			},
		},
	}

	swagger := graph.GenerateOpenAPISpec("Zero API", "1.0.0")
	assert.Equal(t, expected, swagger)
}

func TestAPIIsPathParameter(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		paramName string
		expected  bool
	}{
		{
			name:      "PathParameterExists",
			path:      "/users/{userID}",
			paramName: "userID",
			expected:  true,
		},
		{
			name:      "PathParameterDoesNotExist",
			path:      "/users/{userID}",
			paramName: "otherParam",
			expected:  false,
		},
		{
			name:      "NoPathParameters",
			path:      "/users",
			paramName: "userID",
			expected:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			segments := []directiveparser.Segment{}
			if tt.path != "" {
				if strings.Contains(tt.path, "{") {
					// Parse path with variables
					parts := strings.Split(tt.path, "/")
					for _, part := range parts {
						if part == "" {
							continue
						}
						if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
							varName := strings.Trim(part, "{}")
							segments = append(segments, directiveparser.WildcardSegment{Name: varName})
						} else {
							segments = append(segments, directiveparser.LiteralSegment{Literal: part})
						}
					}

				} else {
					segments = append(segments, directiveparser.LiteralSegment{Literal: strings.TrimPrefix(tt.path, "/")})
				}
			}
			api := &API{
				Pattern: &directiveparser.DirectiveAPI{
					Segments: segments,
				},
			}

			result := api.isPathParameter(tt.paramName)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Helper functions for testing

// FuncSignature represents a parsed function signature in DSL format
type FuncSignature struct {
	Name       string
	Parameters []Parameter
	Returns    []string
}

// Parameter represents a function parameter
type Parameter struct {
	Name string
	Type string
}

// parseFuncSig parses a DSL function signature like "GetUser:ctx context.Context,userID string:*User,error"
func parseFuncSig(sig string) FuncSignature {
	parts := strings.Split(sig, ":")

	result := FuncSignature{
		Name: parts[0],
	}

	// Parse parameters if present
	if len(parts) > 1 && parts[1] != "" {
		paramStrs := strings.Split(parts[1], ",")
		for _, paramStr := range paramStrs {
			paramStr = strings.TrimSpace(paramStr)
			if paramStr == "" {
				continue
			}

			paramParts := strings.SplitN(paramStr, " ", 2)
			if len(paramParts) == 2 {
				result.Parameters = append(result.Parameters, Parameter{
					Name: paramParts[0],
					Type: paramParts[1],
				})
			}
		}
	}

	// Parse return types if present
	if len(parts) > 2 && parts[2] != "" {
		returnStrs := strings.Split(parts[2], ",")
		for _, returnStr := range returnStrs {
			returnStr = strings.TrimSpace(returnStr)
			if returnStr != "" {
				result.Returns = append(result.Returns, returnStr)
			}
		}
	}

	return result
}

// createTypeFromString creates a types.Type from a string representation
func createTypeFromString(typeStr string) types.Type {
	switch typeStr {
	case "string":
		return types.Typ[types.String]
	case "int":
		return types.Typ[types.Int]
	case "bool":
		return types.Typ[types.Bool]
	case "context.Context":
		return types.NewInterfaceType([]*types.Func{}, nil)
	case "error":
		return types.Universe.Lookup("error").Type()
	case "*User":
		// Create a named User type first
		pkg := types.NewPackage("test", "test")
		userStruct := types.NewStruct([]*types.Var{}, []string{})
		userNamed := types.NewNamed(types.NewTypeName(token.NoPos, pkg, "User", nil), userStruct, nil)
		return types.NewPointer(userNamed)
	case "User":
		// Create a named User type
		pkg := types.NewPackage("test", "test")
		userStruct := types.NewStruct([]*types.Var{}, []string{})
		return types.NewNamed(types.NewTypeName(token.NoPos, pkg, "User", nil), userStruct, nil)
	case "CreateUserRequest":
		// Create a named CreateUserRequest type
		pkg := types.NewPackage("test", "test")
		requestStruct := types.NewStruct([]*types.Var{}, []string{})
		return types.NewNamed(types.NewTypeName(token.NoPos, pkg, "CreateUserRequest", nil), requestStruct, nil)
	default:
		// Fallback to string for unknown types
		return types.Typ[types.String]
	}
}

func createMockAPI(t *testing.T, funcSig string, pattern *directiveparser.DirectiveAPI) *API {
	t.Helper()

	sig := parseFuncSig(funcSig)

	// Create parameter tuple
	paramVars := make([]*types.Var, 0, len(sig.Parameters))
	for _, param := range sig.Parameters {
		paramType := createTypeFromString(param.Type)
		paramVar := types.NewVar(token.NoPos, nil, param.Name, paramType)
		paramVars = append(paramVars, paramVar)
	}
	params := types.NewTuple(paramVars...)

	// Create result tuple
	resultVars := make([]*types.Var, 0, len(sig.Returns))
	for _, returnType := range sig.Returns {
		resultType := createTypeFromString(returnType)
		resultVar := types.NewVar(token.NoPos, nil, "", resultType)
		resultVars = append(resultVars, resultVar)
	}
	results := types.NewTuple(resultVars...)

	// Create function signature
	funcSignature := types.NewSignatureType(nil, nil, nil, params, results, false)
	funcType := types.NewFunc(token.NoPos, nil, sig.Name, funcSignature)

	// Create mock package
	pkg := &packages.Package{
		Name: "test",
		TypesInfo: &types.Info{
			Types: make(map[ast.Expr]types.TypeAndValue),
			Defs:  make(map[*ast.Ident]types.Object),
			Uses:  make(map[*ast.Ident]types.Object),
		},
	}

	return &API{
		Pattern:  pattern,
		Function: funcType,
		Package:  pkg,
		Position: token.Position{Filename: "test.go", Line: 1},
	}
}

func createMockAPIWithType(t *testing.T) *API {
	t.Helper()

	pkg := &packages.Package{
		Name: "test",
	}

	// Create a simple mock function
	funcType := types.NewFunc(token.NoPos, nil, "TestFunc", types.NewSignatureType(nil, nil, nil, nil, nil, false))

	return &API{
		Function: funcType,
		Package:  pkg,
	}
}

func parseTypeExpression(t *testing.T, typeExpr string) types.Type {
	t.Helper()

	switch typeExpr {
	case "string":
		return types.Typ[types.String]
	case "int":
		return types.Typ[types.Int]
	case "bool":
		return types.Typ[types.Bool]
	case "[]string":
		return types.NewSlice(types.Typ[types.String])
	default:
		return types.Typ[types.String] // fallback
	}
}

func TestGraphGenerateOpenAPISpecWithNamedStructDefinitions(t *testing.T) {
	graph := &Graph{
		APIs: []*API{
			createMockAPI(t, "CreateUser:ctx context.Context,req User:*User,error", &directiveparser.DirectiveAPI{
				Method: "POST",
				Segments: []directiveparser.Segment{
					directiveparser.LiteralSegment{Literal: "users"},
				},
			}),
			createMockAPI(t, "GetUser:ctx context.Context,userID string:*User,error", &directiveparser.DirectiveAPI{
				Method: "GET",
				Segments: []directiveparser.Segment{
					directiveparser.LiteralSegment{Literal: "users"},
					directiveparser.WildcardSegment{Name: "userID"},
				},
			}),
		},
	}

	swagger := graph.GenerateOpenAPISpec("Test API", "1.0.0")

	// Verify that User type is in definitions
	_, exists := swagger.Definitions["test.User"]
	assert.Equal(t, true, exists)

	// Verify that operations reference the definition
	postOp := swagger.Paths.Paths["/users"].Post
	if postOp == nil {
		t.Fatal("POST operation is nil")
	}
	bodyParam := postOp.Parameters[0]
	assert.Equal(t, "#/definitions/test.User", bodyParam.Schema.Ref.String())

	getOp := swagger.Paths.Paths["/users/{userID}"].Get
	if getOp == nil {
		t.Fatal("GET operation is nil")
	}
	responseSchema := getOp.Responses.StatusCodeResponses[200].Schema
	assert.Equal(t, "#/definitions/test.User", responseSchema.Ref.String())
}
