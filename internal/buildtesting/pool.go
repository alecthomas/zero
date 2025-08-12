// Package buildtesting provides a pool of Go build environments for use in tests.
package buildtesting

import (
	"context"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/alecthomas/assert/v2"
)

type Env struct {
	dir string
}

func newEnv(zeroDir, dir string) Env {
	err := os.MkdirAll(dir, 0750)
	if err != nil {
		log.Fatalln(err)
	}
	poolExecIn(dir, "go", "mod", "init", "test")
	poolExecIn(dir, "go", "work", "init", dir, zeroDir)
	return Env{dir: dir}
}

type Pool struct {
	zeroDir   string
	available chan Env
}

// Run should be called from TestMain.
//
//	func TestMain(m *testing.M) { buildtesting.Run(m) }`)
//
// Then use Get() to retrieve the pool.
func Run(m *testing.M) {
	gitRevParse, err := exec.CommandContext(context.Background(), "git", "rev-parse", "--show-toplevel").CombinedOutput()
	if err != nil {
		log.Fatalln(err)
	}

	zeroDir := strings.TrimSpace(string(gitRevParse))

	dir, err := os.MkdirTemp("", "zero-depgraph-")
	if err != nil {
		log.Fatal(err)
	}
	count := runtime.NumCPU() * 2
	pool = &Pool{
		zeroDir:   zeroDir,
		available: make(chan Env, count),
	}
	// Fill the pool with new environments.
	for i := range count {
		pool.available <- newEnv(zeroDir, filepath.Join(dir, strconv.Itoa(i)))
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

var pool *Pool

// Prepare a new test environment, returning the path.
func Prepare(t *testing.T, main string) string {
	t.Helper()
	return pool.Prepare(t, main)
}

// Prepare a new test environment, returning the path.
//
// When the test completes the environment will be returned to the pool.
func (p *Pool) Prepare(t *testing.T, main string) string {
	t.Helper()
	env := <-p.available
	t.Cleanup(func() { p.returnEnv(t, env) })
	err := os.WriteFile(filepath.Join(env.dir, "main.go"), []byte(main), 0600)
	assert.NoError(t, err)
	return env.dir
}

// returnEnv a test environment to the pool.
func (p *Pool) returnEnv(t *testing.T, env Env) {
	t.Helper()
	err := os.Remove(filepath.Join(env.dir, "main.go"))
	assert.NoError(t, err)
	p.available <- env
}

func poolExecIn(dir string, cmd ...string) {
	c := exec.CommandContext(context.Background(), cmd[0], cmd[1:]...)
	b := &strings.Builder{}
	c.Stdout = b
	c.Stderr = b
	c.Dir = dir
	err := c.Run()
	if err != nil {
		log.Fatalln(err, b.String())
	}
}
