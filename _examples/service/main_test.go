package main

import (
	"net/http"
	"testing"

	"github.com/alecthomas/assert/v2"
	"github.com/alecthomas/zero/providers/sql"
)

func TestMux(t *testing.T) {
	t.Parallel()
	config := ZeroConfig{
		Config6fab5aa5f9534d38: sql.Config{
			DSN:     "postgres://postgres:secret@localhost/zero-exemplar?sslmode=disable",
			Create:  true,
			Migrate: true,
		},
	}
	// This should work but doesn't? Fix this later.
	// err := kong.ApplyDefaults(&cli)
	// assert.NoError(t, err)
	_, err := ZeroConstruct[*http.ServeMux](t.Context(), config)
	assert.NoError(t, err)
}
