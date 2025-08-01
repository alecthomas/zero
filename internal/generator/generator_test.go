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

	graph, err := depgraph.Analyse(".", depgraph.WithProviders(
		"github.com/alecthomas/zero/providers/sql.New",
		"github.com/alecthomas/zero/providers/leases.NewMemoryLeaser",
	))
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
	assert.NoError(t, err, "zero.go:\n%s", readFile(t))
}

func readFile(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("zero.go")
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

	graph, err := depgraph.Analyse(".", depgraph.WithProviders(
		"github.com/alecthomas/zero/providers/sql.New",
		"github.com/alecthomas/zero/providers/leases.NewMemoryLeaser",
	))
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
	generatedCode := readFile(t)
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

func TestCronJobGeneration(t *testing.T) {
	cwd, err := os.Getwd()
	assert.NoError(t, err)

	dir := t.TempDir()

	copyFile(t, "testdata/main.go", filepath.Join(dir, "main.go"))
	createGoMod(t, filepath.Join(cwd, "../.."), dir)

	t.Chdir(dir)

	graph, err := depgraph.Analyse(".", depgraph.WithProviders(
		"github.com/alecthomas/zero/providers/sql.New",
		"github.com/alecthomas/zero/providers/cron.NewScheduler",
		"github.com/alecthomas/zero/providers/leases.NewMemoryLeaser",
	))
	assert.NoError(t, err)

	// Check that cron jobs are detected
	assert.True(t, len(graph.CronJobs) > 0, "Should have cron jobs")

	// Verify the cron job
	cronJob := graph.CronJobs[0]
	assert.Equal(t, "CheckUsers", cronJob.Function.Name(), "Should have CheckUsers cron job")
	assert.Equal(t, "1h", cronJob.Schedule.Schedule, "Should have 1h schedule")

	w, err := os.Create("zero.go")
	assert.NoError(t, err)
	err = Generate(w, graph)
	_ = w.Close()
	assert.NoError(t, err)

	// Verify generated code contains cron job logic
	generatedCode := readFile(t)
	assert.Contains(t, generatedCode, "Scheduler)(nil)).Elem():")
	assert.Contains(t, generatedCode, "NewScheduler(")
	assert.Contains(t, generatedCode, `o.Register("test.Service.CheckUsers"`)
	assert.Contains(t, generatedCode, "time.Duration(3600000000000)") // The duration literal (1 hour in nanoseconds)

	goModTidy(t, dir)

	// Test that the generated code compiles
	cmd := exec.CommandContext(t.Context(), "go", "build", ".")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	assert.NoError(t, err, "Generated code should compile:\n%s", generatedCode)
}

func TestCronJobEndToEnd(t *testing.T) {
	cwd, err := os.Getwd()
	assert.NoError(t, err)

	dir := t.TempDir()

	// Create a test file with a cron job
	//nolint
	err = os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package main

import (
	"context"
)

type TestService struct{}

//zero:provider
func NewTestService() *TestService {
	return &TestService{}
}

//zero:cron 5m
func (s *TestService) CleanupJob(ctx context.Context) error {
	return nil
}

var cli struct {
	ZeroConfig
}

func main() {}
`), 0644)
	assert.NoError(t, err)

	createGoMod(t, filepath.Join(cwd, "../.."), dir)
	t.Chdir(dir)

	graph, err := depgraph.Analyse(".", depgraph.WithProviders(
		"github.com/alecthomas/zero/providers/cron.NewScheduler",
		"github.com/alecthomas/zero/providers/leases.NewMemoryLeaser",
	))
	assert.NoError(t, err)

	// Verify cron job was detected
	assert.Equal(t, 1, len(graph.CronJobs), "Should have exactly one cron job")
	cronJob := graph.CronJobs[0]
	assert.Equal(t, "CleanupJob", cronJob.Function.Name())
	assert.Equal(t, "5m", cronJob.Schedule.Schedule)

	// Generate the code
	w, err := os.Create("zero.go")
	assert.NoError(t, err)
	err = Generate(w, graph)
	_ = w.Close()
	assert.NoError(t, err)

	// Verify generated code was created and compiles
	generatedCode := readFile(t)

	// The test should verify that cron jobs were detected in the graph
	// but the generated code won't include scheduler logic unless a scheduler provider is actually used
	assert.Contains(t, generatedCode, "TestService")

	goModTidy(t, dir)

	// Test that the generated code compiles
	cmd := exec.CommandContext(t.Context(), "go", "build", ".")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	assert.NoError(t, err, "Generated code should compile")
}

func TestSchedulerWithCronJobs(t *testing.T) {
	cwd, err := os.Getwd()
	assert.NoError(t, err)

	dir := t.TempDir()

	// Create a test file with a service that depends on scheduler and has cron jobs
	//nolint
	err = os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package main

import (
	"context"
	"github.com/alecthomas/zero/providers/cron"
)

type TestService struct {
	scheduler *cron.Scheduler
}

//zero:provider
func NewTestService(scheduler *cron.Scheduler) *TestService {
	return &TestService{scheduler: scheduler}
}

//zero:cron 10m
func (s *TestService) CleanupJob(ctx context.Context) error {
	return nil
}

var cli struct {
	ZeroConfig
}

func main() {}
`), 0644)
	assert.NoError(t, err)

	createGoMod(t, filepath.Join(cwd, "../.."), dir)
	t.Chdir(dir)

	graph, err := depgraph.Analyse(".", depgraph.WithProviders(
		"github.com/alecthomas/zero/providers/cron.NewScheduler",
		"github.com/alecthomas/zero/providers/leases.NewMemoryLeaser",
	))
	assert.NoError(t, err)

	// Verify cron job was detected
	assert.Equal(t, 1, len(graph.CronJobs), "Should have exactly one cron job")
	cronJob := graph.CronJobs[0]
	assert.Equal(t, "CleanupJob", cronJob.Function.Name())
	assert.Equal(t, "10m", cronJob.Schedule.Schedule)

	// Generate the code
	w, err := os.Create("zero.go")
	assert.NoError(t, err)
	err = Generate(w, graph)
	_ = w.Close()
	assert.NoError(t, err)

	// Verify generated code contains scheduler construction and cron job registration
	generatedCode := readFile(t)
	assert.Contains(t, generatedCode, "Scheduler)(nil)).Elem():")
	assert.Contains(t, generatedCode, "NewScheduler(")
	assert.Contains(t, generatedCode, `o.Register("test.TestService.CleanupJob"`)
	assert.Contains(t, generatedCode, "time.Duration(600000000000)") // 10 minutes in nanoseconds

	goModTidy(t, dir)

	// Test that the generated code compiles
	cmd := exec.CommandContext(t.Context(), "go", "build", ".")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	assert.NoError(t, err, "Generated code should compile")
}
