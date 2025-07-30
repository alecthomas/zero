//go:build sqlite

package sql

import (
	"context"
	"database/sql"
	"os"
	"strings"

	"github.com/alecthomas/errors"
	_ "modernc.org/sqlite"
)

func init() {
	Register("sqlite", SQLiteDriver{})
}

type SQLiteDriver struct{}

var _ Driver = (*SQLiteDriver)(nil)

func (SQLiteDriver) Denormalise(query string) string { return query }

func (SQLiteDriver) Open(dsn string) (*sql.DB, error) {
	return errors.WithStack2(sql.Open("sqlite", transformSQLiteDSN(dsn)))
}

func (SQLiteDriver) RecreateDatabase(ctx context.Context, dsn string) error {
	if strings.Contains(dsn, "mode=memory") || strings.Contains(dsn, ":memory:") {
		return nil
	}
	dsn = transformSQLiteDSN(dsn)
	path := strings.TrimPrefix(dsn, "file:")
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return errors.WithStack(err)
}

func transformSQLiteDSN(dsn string) string {
	return strings.TrimPrefix(dsn, "sqlite://")
}
