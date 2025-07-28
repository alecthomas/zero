package generator

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alecthomas/assert/v2"
	"github.com/alecthomas/zero/internal/depgraph"
)

func TestGenerator(t *testing.T) {
	cwd, err := os.Getwd()
	assert.NoError(t, err)

	dir := t.TempDir()

	copyFile(t, "testdata/main.go", filepath.Join(dir, "main.go"))
	createGoMod(t, filepath.Join(cwd, "../.."), dir)

	t.Chdir(dir)

	graph, err := depgraph.Analyse(".", depgraph.WithProviders("github.com/alecthomas/zero/providers/sql.New"))
	assert.NoError(t, err)
	for fn, missing := range graph.Missing {
		missingStr := []string{}
		for _, typ := range missing {
			missingStr = append(missingStr, typ.String())
		}
		t.Fatalf("%s() is missing a provider for %s", fn.FullName(), strings.Join(missingStr, ", "))

	}

	w, err := os.Create("zero.go")
	assert.NoError(t, err)
	err = Generate(w, graph)
	_ = w.Close()
	assert.NoError(t, err)

	goModTidy(t, dir)

	cmd := exec.CommandContext(t.Context(), "go", "run", ".", "--help")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	assert.NoError(t, err, "zero.go:\n%s", readFile(t, "zero.go"))
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	assert.NoError(t, err)
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		lines[i] = fmt.Sprintf("%03d: %s", i+1, line)
	}
	return strings.Join(lines, "\n")
}

func copyFile(t *testing.T, src, dest string) {
	t.Helper()
	w, err := os.Create(dest)
	assert.NoError(t, err)
	defer w.Close()
	r, err := os.Open(src)
	assert.NoError(t, err)
	defer r.Close()
	_, err = io.Copy(w, r)
	assert.NoError(t, err)
}

func createGoMod(t *testing.T, gitRoot, dir string) {
	t.Helper()
	w, err := os.Create(filepath.Join(dir, "go.mod"))
	assert.NoError(t, err)
	defer w.Close()
	_, err = fmt.Fprintf(w, `module test

replace github.com/alecthomas/zero => %s
`, gitRoot)
	assert.NoError(t, err)
	goModTidy(t, dir)
}

func goModTidy(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "go", "mod", "tidy")
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	assert.NoError(t, err)
}

func TestMultiProvider(t *testing.T) {
	cwd, err := os.Getwd()
	assert.NoError(t, err)

	dir := t.TempDir()

	copyFile(t, "testdata/main.go", filepath.Join(dir, "main.go"))
	createGoMod(t, filepath.Join(cwd, "../.."), dir)

	t.Chdir(dir)

	graph, err := depgraph.Analyse(".", depgraph.WithProviders("github.com/alecthomas/zero/providers/sql.New"))
	assert.NoError(t, err)

	// Check that multi-providers are detected
	assert.True(t, len(graph.MultiProviders) > 0, "Should have multi-providers")

	// Verify map multi-providers
	mapProviders, exists := graph.MultiProviders["map[string]int"]
	assert.True(t, exists, "Should have map[string]int multi-providers")
	assert.Equal(t, 2, len(mapProviders), "Should have 2 map providers")

	// Verify slice multi-providers
	sliceProviders, exists := graph.MultiProviders["[]string"]
	assert.True(t, exists, "Should have []string multi-providers")
	assert.Equal(t, 2, len(sliceProviders), "Should have 2 slice providers")

	w, err := os.Create("zero.go")
	assert.NoError(t, err)
	err = Generate(w, graph)
	_ = w.Close()
	assert.NoError(t, err)

	// Verify generated code contains multi-provider logic
	generatedCode := readFile(t, "zero.go")
	assert.Contains(t, generatedCode, "case reflect.TypeOf((*map[string]int)(nil)).Elem():")
	assert.Contains(t, generatedCode, "case reflect.TypeOf((*[]string)(nil)).Elem():")
	assert.Contains(t, generatedCode, "result := make(map[string]int)")
	assert.Contains(t, generatedCode, "var result []string")
	assert.Contains(t, generatedCode, "result = append(result, r")

	goModTidy(t, dir)

	// Test that the generated code compiles and runs
	cmd := exec.CommandContext(t.Context(), "go", "run", ".", "--help")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	assert.NoError(t, err, "Generated code should compile and run:\n%s", generatedCode)
}
