//go:build sqlite

package sql_test

import (
	"path/filepath"
	"testing"
)

func TestSQLite(t *testing.T) {
	t.Run("File", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "test.db")
		testDB(t, "sqlite://file:"+dbPath)
	})
	t.Run("Memory", func(t *testing.T) {
		testDB(t, "sqlite://file:discard?mode=memory&cache=shared")
	})
}
