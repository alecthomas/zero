package leases

import (
	"log/slog"
	"os"
	"testing"

	"github.com/alecthomas/assert/v2"
	"github.com/alecthomas/zero/providers/sql"
)

func testSQLLeaser(t *testing.T, dsn string) { //nolint
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{}))
	config := sql.Config{
		Create:  true,
		Migrate: true,
		DSN:     dsn,
	}
	driver, err := sql.DriverForConfig(config)
	assert.NoError(t, err)
	db, err := sql.New(t.Context(), config, logger, SQLLeaserMigrations())
	assert.NoError(t, err)
	leaser, err := NewSQLLeaser(t.Context(), logger, driver, db)
	assert.NoError(t, err)
	testLeases(t, leaser)
}
