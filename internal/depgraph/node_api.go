package depgraph

import (
	"go/token"
	"go/types"
	"reflect"
	"strings"

	"github.com/go-openapi/spec"
	"golang.org/x/tools/go/packages"

	"github.com/alecthomas/zero/internal/directiveparser"
)

// API represents a method that is an exposed API endpoint. API endpoints are annotated like so:
//
//	//zero:api [<method>] [<host>]/[<path>] [<option>[=<value>] ...]
type API struct {
	// Position is the position of the function declaration.
	Position token.Position
	// Pattern is the parsed HTTP mux pattern
	Pattern *directiveparser.DirectiveAPI
	// Function is the function that handles the API
	Function *types.Func
	// Documentation is the extracted function comments
	Documentation string
	// Package is the package that contains the function
	Package *packages.Package
	// OpenAPI is the OpenAPI operation spec for this endpoint
	OpenAPI *spec.Operation
}

var _ Node = (*API)(nil)

func (a *API) node() {}

func (a *API) NodePosition() token.Position { return a.Position }
func (a *API) NodeKey() NodeKey             { return NodeKey(a.Function.FullName()) }
func (a *API) NodeType() types.Type         { return nil }
func (a *API) NodeRequires() []Key          { return []Key{NodeKeyForFunc(a.Function)} }

func (a *API) APILabel(name string) string {
	for _, label := range a.Pattern.Labels {
		if label.Name == name {
			return label.Value
		}
	}
	return ""
}

// GenerateOpenAPIOperation creates an OpenAPI operation spec for this API endpoint
func (a *API) GenerateOpenAPIOperation(definitions spec.Definitions) *spec.Operation {
	operation := &spec.Operation{
		OperationProps: spec.OperationProps{
			Description: a.extractDocumentation(),
			Parameters:  a.generateParameters(definitions),
			Responses:   a.generateResponses(definitions),
			Tags:        []string{a.extractTag()},
		},
	}
	return operation
}

func (a *API) extractDocumentation() string {
	if a.Documentation != "" {
		return a.Documentation
	}
	return ""
}

func (a *API) extractTag() string {
	// Extract tag from package name or directive labels
	if tag := a.APILabel("tag"); tag != "" {
		return tag
	}
	return a.Package.Name
}

func (a *API) generateParameters(definitions spec.Definitions) []spec.Parameter {
	var parameters []spec.Parameter
	signature := a.Function.Signature()
	params := signature.Params()

	for i := range params.Len() {
		param := params.At(i)
		paramType := param.Type()
		paramName := param.Name()

		// Skip context parameters
		if isContextType(paramType) {
			continue
		}

		// Handle different parameter types
		if isStandardHTTPType(paramType) {
			continue // Skip standard HTTP types
		}

		if isBodyParameterStruct(paramType) {
			// Body parameter
			schema := a.generateSchemaFromType(paramType, definitions)
			parameters = append(parameters, spec.Parameter{
				ParamProps: spec.ParamProps{
					Name:     "body",
					In:       "body",
					Required: true,
					Schema:   schema,
				},
			})
		} else if isStringOrIntType(paramType) {
			// Path or query parameter
			parameterType := "string"
			if strings.Contains(paramType.String(), "int") {
				parameterType = "integer"
			}

			// Determine if it's a path parameter from the pattern
			inType := "query"
			if a.isPathParameter(paramName) {
				inType = "path"
			}

			parameters = append(parameters, spec.Parameter{
				ParamProps: spec.ParamProps{
					Name:     paramName,
					In:       inType,
					Required: inType == "path",
				},
				SimpleSchema: spec.SimpleSchema{
					Type: parameterType,
				},
			})
		}
	}

	return parameters
}

func (a *API) generateResponses(definitions spec.Definitions) *spec.Responses {
	responses := &spec.Responses{
		ResponsesProps: spec.ResponsesProps{
			StatusCodeResponses: make(map[int]spec.Response),
		},
	}

	signature := a.Function.Signature()
	results := signature.Results()

	if results.Len() == 0 {
		// No return value - 204 No Content
		responses.StatusCodeResponses[204] = spec.Response{
			ResponseProps: spec.ResponseProps{
				Description: "No Content",
			},
		}
	} else if results.Len() == 1 && isErrorType(results.At(0).Type()) {
		// Only error return - 204 No Content
		responses.StatusCodeResponses[204] = spec.Response{
			ResponseProps: spec.ResponseProps{
				Description: "No Content",
			},
		}
	} else if results.Len() >= 1 {
		firstResult := results.At(0)
		if !isErrorType(firstResult.Type()) {
			// Has a return value - 200 OK
			schema := a.generateSchemaFromType(firstResult.Type(), definitions)
			responses.StatusCodeResponses[200] = spec.Response{
				ResponseProps: spec.ResponseProps{
					Description: "Success",
					Schema:      schema,
				},
			}
		}
	}

	// Always add error responses
	responses.StatusCodeResponses[400] = spec.Response{
		ResponseProps: spec.ResponseProps{
			Description: "Bad Request",
		},
	}
	responses.StatusCodeResponses[500] = spec.Response{
		ResponseProps: spec.ResponseProps{
			Description: "Internal Server Error",
		},
	}

	return responses
}

func (a *API) isPathParameter(paramName string) bool {
	// Check if the parameter name is a wildcard in the parsed path structure
	return a.Pattern.Wildcard(paramName)
}

func (a *API) generateSchemaFromType(t types.Type, definitions spec.Definitions) *spec.Schema {
	schema := &spec.Schema{}

	// Remove pointer indirection
	for {
		if ptr, ok := t.(*types.Pointer); ok {
			t = ptr.Elem()
		} else {
			break
		}
	}

	switch typ := t.(type) {
	case *types.Basic:
		switch typ.Kind() {
		case types.String:
			schema.Type = []string{"string"}
		case types.Int, types.Int8, types.Int16, types.Int32, types.Int64,
			types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64:
			schema.Type = []string{"integer"}
		case types.Float32, types.Float64:
			schema.Type = []string{"number"}
		case types.Bool:
			schema.Type = []string{"boolean"}
		default:
			schema.Type = []string{"string"}
		}
	case *types.Struct:
		schema.Type = []string{"object"}
		schema.Properties = make(map[string]spec.Schema)

		for i := range typ.NumFields() {
			field := typ.Field(i)
			if field.Exported() {
				fieldName := getJSONFieldName(field, typ.Tag(i))
				if fieldName != "" {
					fieldSchema := a.generateSchemaFromType(field.Type(), definitions)
					schema.Properties[fieldName] = *fieldSchema
				}
			}
		}
	case *types.Slice:
		schema.Type = []string{"array"}
		itemSchema := a.generateSchemaFromType(typ.Elem(), definitions)
		schema.Items = &spec.SchemaOrArray{
			Schema: itemSchema,
		}
	case *types.Named:
		// For named types, create a reference to a shared definition
		typeName := typ.Obj().Name()
		pkg := typ.Obj().Pkg()
		var defName string
		if pkg != nil {
			defName = pkg.Name() + "." + typeName
		} else {
			defName = typeName
		}

		// Add to definitions if not already present
		if _, exists := definitions[defName]; !exists {
			underlyingSchema := a.generateSchemaFromType(typ.Underlying(), definitions)
			definitions[defName] = *underlyingSchema
		}

		// Return a reference schema
		schema.Ref = spec.MustCreateRef("#/definitions/" + defName)
		return schema
	default:
		// Fallback for unknown types
		schema.Type = []string{"object"}
	}

	return schema
}

// getJSONFieldName returns the JSON field name from the struct tag if present,
// otherwise returns the field name with the first letter lowercased.
func getJSONFieldName(field *types.Var, tag string) string {
	if tag != "" {
		structTag := reflect.StructTag(tag)
		if jsonTag := structTag.Get("json"); jsonTag != "" {
			// Parse the JSON tag - it might have options like "name,omitempty"
			parts := strings.Split(jsonTag, ",")
			if parts[0] == "-" {
				return "" // Field should be excluded
			}
			if parts[0] != "" {
				return parts[0]
			}
		}
	}

	// No JSON tag found, lowercase the first letter
	name := field.Name()
	if len(name) > 0 {
		return strings.ToLower(name[:1]) + name[1:]
	}
	return name
}
