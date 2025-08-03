package strcase

import (
	"testing"

	"github.com/alecthomas/assert/v2"
)

func TestStrcase(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{"UpperCamelCase", "UpperCamelCase", []string{"Upper", "Camel", "Case"}},
		{"LowerCamelCase", "lowerCamelCase", []string{"lower", "Camel", "Case"}},
		{"SnakeCase", "snake_case", []string{"snake", "_", "case"}},
		{"SnakeCaseWithNumbers", "snake_case_123", []string{"snake", "_", "case", "_", "123"}},
		{"UpperCamelCaseWithAcronym", "UpperCamelAPI", []string{"Upper", "Camel", "API"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := Split(tt.input)
			assert.Equal(t, tt.expected, actual)
		})
	}
}
