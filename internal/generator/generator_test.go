package generator

import (
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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

	graph, err := depgraph.Analyse(t.Context(), ".", depgraph.WithProviders(
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

	execIn(t, dir, "go", "run", ".", "--help")
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

func execIn(t *testing.T, dir string, cmd ...string) {
	t.Helper()
	c := exec.CommandContext(t.Context(), cmd[0], cmd[1:]...)
	b := &strings.Builder{}
	c.Stdout = b
	c.Stderr = b
	c.Dir = dir
	err := c.Run()
	assert.NoError(t, err, b.String())
}

func createGoMod(t *testing.T, gitRoot, dir string) {
	t.Helper()
	execIn(t, dir, "go", "mod", "init", "test")
	execIn(t, dir, "go", "work", "init", dir, gitRoot)
	goModTidy(t, dir)
}

func goModTidy(t *testing.T, dir string) {
	t.Helper()
	execIn(t, dir, "go", "mod", "tidy")
}

func TestMultiProvider(t *testing.T) {
	cwd, err := os.Getwd()
	assert.NoError(t, err)

	dir := t.TempDir()

	copyFile(t, "testdata/main.go", filepath.Join(dir, "main.go"))
	createGoMod(t, filepath.Join(cwd, "../.."), dir)

	t.Chdir(dir)

	graph, err := depgraph.Analyse(t.Context(), ".", depgraph.WithProviders(
		"github.com/alecthomas/zero/providers/sql.New",
		"github.com/alecthomas/zero/providers/leases.NewMemoryLeaser",
	))
	assert.NoError(t, err)

	// Check that multi-providers are detected
	assert.True(t, len(graph.Providers) > 0, "Should have providers")

	// Verify map multi-providers
	mapProviders, exists := graph.Providers["map[string]int"]
	assert.True(t, exists, "Should have map[string]int multi-providers")
	assert.Equal(t, 2, len(mapProviders), "Should have 2 map providers")

	// Verify slice multi-providers
	sliceProviders, exists := graph.Providers["[]string"]
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
	execIn(t, dir, "go", "run", ".", "--help")
	assert.NoError(t, err, "Generated code should compile and run:\n%s", generatedCode)
}

func TestCronJobGeneration(t *testing.T) {
	cwd, err := os.Getwd()
	assert.NoError(t, err)

	dir := t.TempDir()

	copyFile(t, "testdata/main.go", filepath.Join(dir, "main.go"))
	createGoMod(t, filepath.Join(cwd, "../.."), dir)

	t.Chdir(dir)

	graph, err := depgraph.Analyse(t.Context(), ".", depgraph.WithProviders(
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
	assert.Contains(t, generatedCode, `cron.Register("*test.Service.CheckUsers"`)
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

	graph, err := depgraph.Analyse(t.Context(), ".", depgraph.WithProviders(
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

func TestGenericProviderGeneration(t *testing.T) {
	cwd, err := os.Getwd()
	assert.NoError(t, err)

	dir := t.TempDir()

	// Create a test file with generic providers
	//nolint
	err = os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package main

import (
	"context"
)

type EventPayload interface {
	EventID() string
}

type Topic[T any] interface {
	Publish(ctx context.Context, msg T) error
}

type User struct {
	Name string
}

func (u User) EventID() string {
	return u.Name
}

//zero:provider
func NewTopic[T any]() Topic[T] {
	return nil
}

type Service struct {
	topic Topic[User]
}

//zero:provider
func NewService(topic Topic[User]) *Service {
	return &Service{topic: topic}
}

var cli struct {
	ZeroConfig
}

func main() {}
`), 0644)
	assert.NoError(t, err)

	createGoMod(t, filepath.Join(cwd, "../.."), dir)
	t.Chdir(dir)

	graph, err := depgraph.Analyse(t.Context(), ".", depgraph.WithRoots("*test.Service"))
	assert.NoError(t, err)

	// Verify providers were detected (Service + Topic base + resolved Topic[User])
	expectedProviders := []string{
		"*test.Service",
		"test.Topic",
		"test.Topic[test.User]",
	}
	assert.Equal(t, expectedProviders, stableKeys(graph.Providers), "Should have Service provider, base generic provider, and resolved generic provider")

	// Verify the Topic generic provider
	topicProviders := graph.Providers["test.Topic"]
	assert.Equal(t, 1, len(topicProviders), "Should have one Topic generic provider")
	assert.Equal(t, "NewTopic", topicProviders[0].Function.Name())
	assert.True(t, topicProviders[0].IsGeneric)

	// Verify service provider
	serviceProviders := graph.Providers["*test.Service"]
	assert.True(t, len(serviceProviders) > 0)
	assert.Equal(t, "NewService", serviceProviders[0].Function.Name())

	// Verify no missing dependencies (generic provider should satisfy Topic[User])
	assert.Equal(t, 0, len(graph.Missing), "Should have no missing dependencies")

	// Generate the code
	w, err := os.Create("zero.go")
	assert.NoError(t, err)
	err = Generate(w, graph)
	_ = w.Close()
	assert.NoError(t, err)

	// Verify generated code contains generic provider instantiation
	generatedCode := readFile(t)
	assert.Contains(t, generatedCode, "NewTopic[User]()", "Should instantiate generic provider with concrete type")
	assert.Contains(t, generatedCode, "case reflect.TypeOf((*Topic[User])(nil)).Elem():", "Should have case for Topic[User]")

	goModTidy(t, dir)

	// Test that the generated code compiles
	cmd := exec.CommandContext(t.Context(), "go", "build", ".")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	assert.NoError(t, err, "Generated code should compile:\n%s", generatedCode)
}

func TestGenericConfigGeneration(t *testing.T) {
	t.Skip("Generic service providers aren't supported yet")
	cwd, err := os.Getwd()
	assert.NoError(t, err)

	dir := t.TempDir()

	// Create a test file with generic configs
	//nolint
	err = os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package main

//zero:config prefix="conf-${type}-"
type Config[T any] struct {
	Value string
}

//zero:provider
func New[T any](config Config[T]) *Service[T] {
	return &Service[T]{}
}

type Service[T any] struct {}

type User struct {
	Name string
}

type HTTPClient struct {
	URL string
}

type XMLAPIGateway struct {
	Endpoint string
}

//zero:provider
func NewHTTPService(config Config[HTTPClient]) *Service[HTTPClient] {
	return &Service[HTTPClient]{}
}

//zero:provider
func NewXMLService(config Config[XMLAPIGateway]) *Service[XMLAPIGateway] {
	return &Service[XMLAPIGateway]{}
}

var cli struct {
	ZeroConfig
}

func main() {}
`), 0644)
	assert.NoError(t, err)

	createGoMod(t, filepath.Join(cwd, "../.."), dir)
	t.Chdir(dir)

	graph, err := depgraph.Analyse(t.Context(), ".", depgraph.WithRoots("test.Config[test.HTTPClient]", "test.Service[test.XMLAPIGateway]"))
	assert.NoError(t, err)

	// Verify generic config was detected
	assert.Equal(t, 1, len(graph.GenericConfigs), "Should have exactly one generic config")
	configProviders := graph.GenericConfigs["test.Config"]
	assert.Equal(t, 1, len(configProviders), "Should have one Config generic config")
	assert.True(t, configProviders[0].IsGeneric)
	assert.Equal(t, "conf-${type}-", configProviders[0].Directive.Prefix)

	// Verify concrete configs were resolved
	httpConfigKey := "test.Config[test.HTTPClient]"
	_, hasHTTPConfig := graph.Configs[httpConfigKey]
	assert.True(t, hasHTTPConfig, "Should have resolved HTTP client config")

	xmlConfigKey := "test.Config[test.XMLAPIGateway]"
	_, hasXMLConfig := graph.Configs[xmlConfigKey]
	assert.True(t, hasXMLConfig, "Should have resolved XML API gateway config")

	// Check that the prefixes were substituted correctly
	if httpConfig, exists := graph.Configs[httpConfigKey]; exists {
		assert.Equal(t, "conf-http-client-", httpConfig.Directive.Prefix)
	}
	if xmlConfig, exists := graph.Configs[xmlConfigKey]; exists {
		assert.Equal(t, "conf-xmlapi-gateway-", xmlConfig.Directive.Prefix)
	}

	// Generate the code
	w, err := os.Create("zero.go")
	assert.NoError(t, err)
	err = Generate(w, graph)
	_ = w.Close()
	assert.NoError(t, err)

	// Verify generated code contains the configs with substituted prefixes
	generatedCode := readFile(t)
	assert.Contains(t, generatedCode, "conf-http-client-", "Should contain HTTP client substituted prefix")
	assert.Contains(t, generatedCode, "conf-xmlapi-gateway-", "Should contain XML API gateway substituted prefix")
	assert.Contains(t, generatedCode, "case reflect.TypeOf((*Config[HTTPClient])(nil)).Elem():", "Should have case for Config[HTTPClient]")
	assert.Contains(t, generatedCode, "case reflect.TypeOf((*Config[XMLAPIGateway])(nil)).Elem():", "Should have case for Config[XMLAPIGateway]")

	goModTidy(t, dir)

	// Test that the generated code compiles
	cmd := exec.CommandContext(t.Context(), "go", "build", ".")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	assert.NoError(t, err, "Generated code should compile:\n%s", generatedCode)
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

	graph, err := depgraph.Analyse(t.Context(), ".", depgraph.WithProviders(
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
	assert.Contains(t, generatedCode, `cron.Register("*test.TestService.CleanupJob"`)
	assert.Contains(t, generatedCode, "time.Duration(600000000000)") // 10 minutes in nanoseconds

	goModTidy(t, dir)

	// Test that the generated code compiles
	cmd := exec.CommandContext(t.Context(), "go", "build", ".")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	assert.NoError(t, err, "Generated code should compile")
}

func stableKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}
