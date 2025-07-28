// Package sql contains types and providers for connecting to and migrating SQL databases.
package sql

import (
	"context"
	"database/sql"
	"io/fs"
	"log/slog"
	"net/url"
	"slices"
	"strings"

	"github.com/alecthomas/errors"
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

// Migrations represents a set of SQL migrations.
//
// Create a provider that returns an fs.FS with your .sql migrations at the root of the FS. eg.
//
//	//go:embed migrations/*.sql
//	var migrations embed.FS
//
//	//zero:provider multi
//	func Migrations() Migrations {
//		sub, _ := fs.Sub(migrations, "migrations")
//		return []fs.FS{sub}
//	}
type Migrations []fs.FS

//zero:config prefix="sql-"
type Config struct {
	Migrate bool     `help:"Enable automatic migration of the database schema."`
	DSN     *url.URL `default:"${sqldsn=postgres://postgres:secret@localhost:5432/zero?sslmode=disable}" help:"DSN for the SQL connection."`
}

// New creates a new SQL database connection, applying migrations or verifying migrations have been applied.
//
//zero:provider weak
func New(ctx context.Context, config Config, logger *slog.Logger, migrations Migrations) (db *sql.DB, err error) {
	logger = logger.WithGroup("sql")
	dsn := config.DSN
	switch dsn.Scheme {
	case "mysql":
		db, err = sql.Open("mysql", dsn.String())
	case "postgres", "pgx":
		dsn.Scheme = "postgres"
		db, err = sql.Open("pgx", dsn.String())
	case "sqlite":
		db, err = sql.Open("sqlite", dsn.String())
	default:
		return nil, errors.Errorf("unsupported SQL DSN scheme: %q", dsn.Scheme)
	}
	if err != nil {
		return nil, errors.Errorf("failed to open database connection: %w", err)
	}
	if config.Migrate {
		if err := Migrate(ctx, logger, dsn, db, migrations); err != nil {
			return nil, errors.Errorf("failed migrations: %w", err)
		}
	} else {
		if err := CheckMigrations(ctx, dsn, db, migrations); err != nil {
			return nil, errors.Errorf("failed to check migrations: %w", err)
		}
	}
	return db, nil
}

type migrationFile struct {
	fs    fs.FS
	entry fs.DirEntry
}

// CheckMigrations returns an error if there are missing migrations.
func CheckMigrations(ctx context.Context, dsn *url.URL, db *sql.DB, migrations Migrations) error {
	allMigrations, err := collectMigrations(migrations)
	if err != nil {
		return errors.WithStack(err)
	}
	for _, file := range allMigrations {
		placeholder := "?"
		if dsn.Scheme == "postgres" {
			placeholder = "$1"
		}
		_, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations WHERE version=`+placeholder, file.entry.Name()) //nolint
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errors.Errorf("missing migration: %s", file.entry.Name())
			}
			return errors.Errorf("failed to check migration: %w", err)
		}
	}
	return nil
}

// Migrate applies all pending migrations to the database.
func Migrate(ctx context.Context, logger *slog.Logger, dsn *url.URL, db *sql.DB, migrations Migrations) error {
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY)`)
	if err != nil {
		return errors.Errorf("failed to create schema_migrations table: %w", err)
	}

	// Collect all migrations
	allMigrations, err := collectMigrations(migrations)
	if err != nil {
		return errors.WithStack(err)
	}

	// Then apply in order.
	for _, file := range allMigrations {
		migration := file.fs
		file := file.entry
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return errors.Errorf("failed to begin transaction: %w", err)
		}
		placeholder := "?"
		if dsn.Scheme == "postgres" {
			placeholder = "$1"
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES (`+placeholder+`)`, file.Name()) //nolint
		if err != nil {                                                                                               // At some point
			logger.Debug("Failed to insert SQL schema migration entry", "error", err)
			_ = tx.Rollback()
			continue
		}
		sql, err := fs.ReadFile(migration, file.Name())
		if err != nil {
			_ = tx.Rollback()
			return errors.Errorf("failed to read migration file %q: %w", file.Name(), err)
		}
		logger.Debug("Applying migration", "file", file.Name(), "ddl", string(sql))
		if _, err := tx.ExecContext(ctx, string(sql)); err != nil {
			_ = tx.Rollback()
			return errors.Errorf("failed to apply migration %q: %w", file.Name(), err)
		}
		if err := tx.Commit(); err != nil {
			return errors.Errorf("failed to commit transaction: %w", err)
		}
	}
	return nil
}

func collectMigrations(migrations Migrations) ([]migrationFile, error) {
	var allMigrations []migrationFile
	for _, migration := range migrations {
		files, err := fs.ReadDir(migration, ".")
		if err != nil {
			return nil, errors.Errorf("failed to read migration files: %w", err)
		}
		for _, file := range files {
			allMigrations = append(allMigrations, migrationFile{fs: migration, entry: file})
		}
	}

	// Then lexical sort.
	slices.SortFunc(allMigrations, func(a, b migrationFile) int {
		return strings.Compare(a.entry.Name(), b.entry.Name())
	})

	return allMigrations, nil
}
