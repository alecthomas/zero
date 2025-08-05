// Package sqltest provides utilities for testing SQL databases.
package sqltest

import (
	"database/sql"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"testing"
	"time"

	"github.com/alecthomas/assert/v2"
	"github.com/alecthomas/zero/internal/flock"
	"github.com/alecthomas/zero/providers/logging/loggingtest"
	zerosql "github.com/alecthomas/zero/providers/sql"
)

const (
	PostgresDSN = "postgres://postgres:secret@localhost:5432/zero-test?sslmode=disable"
)

// NewForTesting creates a new database instance and driver for testing purposes.
//
// It encapsulates the boilerplate around constructing a connection and driver.
func NewForTesting(t *testing.T, dsn string, migrations zerosql.Migrations) (*sql.DB, zerosql.Driver) {
	t.Helper()
	logger := loggingtest.NewForTesting()

	// Acquire flock to ensure exclusive access to the database.
	h := fnv.New64a()
	h.Write([]byte(dsn))
	flockID := fmt.Sprintf("zero-test-%s.lock", hex.EncodeToString(h.Sum(nil)))
	release, err := flock.Acquire(t.Context(), "/tmp/"+flockID, time.Second*30)
	assert.NoError(t, err)
	t.Cleanup(func() {
		err = release()
		assert.NoError(t, err)
	})

	config := zerosql.Config{
		DSN:     dsn,
		Create:  true,
		Migrate: true,
	}

	db, err := zerosql.New(t.Context(), config, logger, migrations)
	assert.NoError(t, err)

	driver, err := zerosql.DriverForConfig(config)
	assert.NoError(t, err)

	return db, driver
}
