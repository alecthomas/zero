package depgraph

import (
	"context"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/alecthomas/assert/v2"
)

type Env struct {
	dir string
}

func NewEnv(zeroDir, dir string) Env {
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

var pool *Pool
var poolOnce sync.Once

func TestMain(m *testing.M) {
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
		pool.available <- NewEnv(zeroDir, filepath.Join(dir, strconv.Itoa(i)))
	}

	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// Prepare a new test environment, returning the path.
func (p *Pool) Prepare(t *testing.T, main string) string {
	t.Helper()
	env := <-p.available
	t.Cleanup(func() { p.Return(t, env) })
	err := os.WriteFile(filepath.Join(env.dir, "main.go"), []byte(main), 0600)
	assert.NoError(t, err)
	return env.dir
}

// Return a test environment to the pool.
func (p *Pool) Return(t *testing.T, env Env) {
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
