package depgraph

import (
	"go/types"
	"testing"

	"github.com/alecthomas/assert/v2"
)

func TestTypeRef(t *testing.T) {
	t.Parallel()
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
				Import: `"database/sql"`,
				Ref:    "sql.DB",
			},
		},
		{
			name:    "pointer to external package type",
			typeStr: "*database/sql.DB",
			expected: Ref{
				Pkg:    "database/sql",
				Import: `"database/sql"`,
				Ref:    "*sql.DB",
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
			t.Parallel()
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

			assert.Equal(t, tt.expected, result)
		})
	}
}
