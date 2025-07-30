//go:build postgres

package sql

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/alecthomas/errors"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func init() {
	Register("postgres", PostgresDriver{})
}

type PostgresDriver struct{}

var _ Driver = (*PostgresDriver)(nil)

func (PostgresDriver) Name() string { return "postgres" }

func (PostgresDriver) TranslateError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgerrcode.IsIntegrityConstraintViolation(pgErr.Code) {
		return errors.Errorf("%w: %w", ErrConstraint, err)
	}
	return err
}

func (PostgresDriver) Denormalise(query string) string {
	placeholderRe := regexp.MustCompile(`\?`)
	i := 0
	return string(placeholderRe.ReplaceAllFunc([]byte(query), func(b []byte) []byte {
		i++
		return []byte(fmt.Sprintf("$%d", i))
	}))
}

func (PostgresDriver) Open(dsn string) (*sql.DB, error) {
	return errors.WithStack2(sql.Open("pgx", dsn))
}

func (PostgresDriver) RecreateDatabase(ctx context.Context, dsn string) error {
	u, err := url.Parse(dsn)
	if err != nil {
		return errors.Errorf("failed to parse DSN: %w", err)
	}
	dbName := strings.Trim(u.Path, "/")
	// Reconnect to PG without DB
	bare, err := url.Parse(u.String())
	if err != nil {
		return errors.Errorf("failed to parse DSN: %w", err)
	}
	bare.Path = ""
	db, err := sql.Open("pgx", bare.String())
	if err != nil {
		return errors.Errorf("failed to open database connection: %w", err)
	}
	defer db.Close()
	// Kill all existing connections, if any, so the DROP doesn't block.
	_, err = db.ExecContext(ctx, `
	     SELECT pid, pg_terminate_backend(pid)
	     FROM pg_stat_activity
	     WHERE datname = $1 AND pid <> pg_backend_pid()`,
		dbName)
	if err != nil {
		return errors.Errorf("failed to kill existing backends: %w", err)
	}
	_, err = db.ExecContext(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %q", dbName)) //nolint
	if err != nil {
		return errors.Errorf("failed to drop database: %w", err)
	}
	_, err = db.ExecContext(ctx, fmt.Sprintf("CREATE DATABASE %q", dbName)) //nolint
	if err != nil {
		return errors.Errorf("failed to create database: %w", err)
	}
	return nil
}
