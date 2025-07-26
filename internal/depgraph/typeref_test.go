package depgraph

import (
	"go/types"
	"testing"

	"github.com/alecthomas/assert/v2"
)

func TestTypeRef(t *testing.T) {
	tests := []struct {
		name     string
		typeStr  string
		expected Ref
	}{
		{
			name:    "basic type",
			typeStr: "string",
			expected: Ref{
				Pkg:    "",
				Import: "",
				Ref:    "string",
			},
		},
		{
			name:    "pointer to basic type",
			typeStr: "*string",
			expected: Ref{
				Pkg:    "",
				Import: "",
				Ref:    "*string",
			},
		},
		{
			name:    "external package type",
			typeStr: "database/sql.DB",
			expected: Ref{
				Pkg:    "database/sql",
				Import: "", // Will be checked separately
				Ref:    "", // Will be checked separately
			},
		},
		{
			name:    "pointer to external package type",
			typeStr: "*database/sql.DB",
			expected: Ref{
				Pkg:    "database/sql",
				Import: "", // Will be checked separately
				Ref:    "", // Will be checked separately
			},
		},
	}

	// Create a mock destination package for testing
	destPkg := types.NewPackage("github.com/test/dest", "dest")
	graph := &Graph{
		Dest: destPkg,
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Parse the type string into a types.Type
			var typ types.Type
			switch tt.typeStr {
			case "string":
				typ = types.Typ[types.String]
			case "*string":
				typ = types.NewPointer(types.Typ[types.String])
			case "database/sql.DB":
				pkg := types.NewPackage("database/sql", "sql")
				obj := types.NewTypeName(0, pkg, "DB", nil)
				named := types.NewNamed(obj, types.NewStruct(nil, nil), nil)
				typ = named
			case "*database/sql.DB":
				pkg := types.NewPackage("database/sql", "sql")
				obj := types.NewTypeName(0, pkg, "DB", nil)
				named := types.NewNamed(obj, types.NewStruct(nil, nil), nil)
				typ = types.NewPointer(named)
			}

			result := graph.TypeRef(typ)

			assert.Equal(t, tt.expected.Pkg, result.Pkg)

			// Handle external package types with dynamic aliases
			if tt.typeStr == "database/sql.DB" {
				assert.Contains(t, result.Import, `"database/sql"`)
				assert.NotEqual(t, "", result.Import)
				// Ref should be alias.DB where alias is extracted from Import
				assert.Contains(t, result.Ref, ".DB")
				assert.NotContains(t, result.Ref, "*")
			} else if tt.typeStr == "*database/sql.DB" {
				assert.Contains(t, result.Import, `"database/sql"`)
				assert.NotEqual(t, "", result.Import)
				// Ref should be *alias.DB where alias is extracted from Import
				assert.Contains(t, result.Ref, ".DB")
				assert.Contains(t, result.Ref, "*")
			} else {
				// For basic types, check exact match
				assert.Equal(t, tt.expected.Ref, result.Ref)
				assert.Equal(t, tt.expected.Import, result.Import)
			}
		})
	}
}
