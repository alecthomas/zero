package sql

import (
	"database/sql"
	"testing"

	"github.com/alecthomas/assert/v2"
	"github.com/alecthomas/zero/providers/logging/loggingtest"
	"github.com/psanford/memfs"
)

func testDB(t *testing.T, dsn string) {
	t.Helper()
	fs := memfs.New()
	err := fs.WriteFile("000_init.sql", []byte(`CREATE TABLE users (name VARCHAR(255) NOT NULL PRIMARY KEY)`), 0600)
	assert.NoError(t, err)

	logger := loggingtest.NewForTesting()
	config := Config{DSN: dsn, Create: true, Migrate: true}

	var db *sql.DB
	t.Run("RecreateConnect", func(t *testing.T) {
		db, err = New(t.Context(), config, logger, Migrations{fs})
		assert.NoError(t, err)
	})
	if db == nil {
		return
	}

	driver, err := DriverForConfig(config)
	assert.NoError(t, err)

	t.Run("Insert", func(t *testing.T) {
		_, err = db.ExecContext(t.Context(), `INSERT INTO users (name) VALUES ('Alice')`)
		assert.NoError(t, err)
	})

	t.Run("Select", func(t *testing.T) {
		rows, err := db.QueryContext(t.Context(), `SELECT * FROM users`)
		assert.NoError(t, err)
		defer rows.Close()

		for rows.Next() {
			var name string
			err := rows.Scan(&name)
			assert.NoError(t, err)
			assert.Equal(t, "Alice", name)
		}
		assert.NoError(t, rows.Err())
	})

	t.Run("Insert", func(t *testing.T) {
		_, err = db.ExecContext(t.Context(), `INSERT INTO users (name) VALUES ('Alice')`)
		assert.IsError(t, driver.TranslateError(err), ErrConstraint)
	})

}
