// Package sql contains types and providers for connecting to and migrating SQL databases.
//
// DSNs for this package are URI's with the driver as the scheme, eg. `postgres://...`.
//
// To avoid bloating end-user binaries, drivers are excluded by default by build tags. To include a driver, add
// `--tags=<driver>` to Zero and your Go tools, or preferably set GOFLAGS='-tags=<driver>'. The latter will be picked up
// automatically
package sql

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/alecthomas/errors"
)

// ErrConstraint is returned when an SQL constraint violation occurs.
var ErrConstraint = errors.New("constraint violation")

// Driver abstracts driver-specific functionality for supporting migrations and local development.
type Driver interface {
	// Name of the driver.
	Name() string
	// TranslateError wraps a driver-specific error in standard errors from this package, such as ErrConflict.
	TranslateError(err error) error
	// Open connection to the database.
	Open(dsn string) (*sql.DB, error)
	// Denormalise converts a query that uses ? placeholders to its native format.
	Denormalise(query string) string
	// RecreateDatabase drops then creates a database.
	RecreateDatabase(ctx context.Context, dsn string) error
}

var drivers = map[string]Driver{}

// Register a driver with the registry.
func Register(scheme string, driver Driver) {
	drivers[scheme] = driver
}

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
	Create  bool   `help:"Create (or recreate) the database."`
	Migrate bool   `help:"Apply migrations during connection establishment."`
	DSN     string `default:"${sqldsn}" required:"" help:"DSN for the SQL connection."`
}

// DriverForConfig returns the [Driver] associated with the given [Config].
//
//zero:provider
func DriverForConfig(config Config) (Driver, error) {
	driver, ok := drivers[dsnScheme(config.DSN)]
	if !ok {
		return nil, errors.Errorf("unknown SQL driver: %s", dsnScheme(config.DSN))
	}
	return driver, nil
}

var dbToDriverLock sync.Mutex
var dbToDriver = map[*sql.DB]Driver{}

// DriverForDB returns the [Driver] associated with the given [sql.DB].
//
// This is populated by [Open] and [New].
func DriverForDB(db *sql.DB) (Driver, bool) {
	dbToDriverLock.Lock()
	defer dbToDriverLock.Unlock()
	driver, ok := dbToDriver[db]
	return driver, ok
}

func dsnScheme(dsn string) string {
	return strings.Split(dsn, "://")[0]
}

// DumpMigrations to a directory.
//
// This is convenient for use with external migration tools.
func DumpMigrations(migrations Migrations, dir string) error {
	for _, migration := range migrations {
		files, err := fs.ReadDir(migration, ".")
		if err != nil {
			return errors.WithStack(err)
		}
		for _, file := range files {
			if file.IsDir() {
				continue
			}
			if err := copyFile(migration, file, dir); err != nil {
				return errors.WithStack(err)
			}
		}
	}
	return nil
}

func copyFile(migration fs.FS, file fs.DirEntry, dir string) error {
	w, err := os.Create(filepath.Join(dir, file.Name()))
	if err != nil {
		return errors.WithStack(err)
	}
	defer w.Close()
	r, err := migration.Open(file.Name())
	if err != nil {
		return errors.WithStack(err)
	}
	defer r.Close()
	_, err = io.Copy(w, r)
	if err != nil {
		return errors.WithStack(err)
	}
	return nil
}

// Open a database connection without applying or validating migrations.
func Open(dsn string) (db *sql.DB, err error) {
	driver, ok := drivers[dsnScheme(dsn)]
	if !ok {
		return nil, errors.Errorf("unsupported SQL driver: %q", dsnScheme(dsn))
	}
	db, err = driver.Open(dsn)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	dbToDriverLock.Lock()
	defer dbToDriverLock.Unlock()
	dbToDriver[db] = driver
	return db, nil
}

// New creates a new SQL database connection, applying migrations or verifying migrations have been applied.
//
//zero:provider weak
func New(ctx context.Context, config Config, logger *slog.Logger, migrations Migrations) (db *sql.DB, err error) {
	dsn := config.DSN
	if config.Create {
		if err := CreateDatabase(ctx, dsn); err != nil {
			return nil, errors.Errorf("failed to create database: %w", err)
		}
	}
	db, err = Open(dsn)
	if err != nil {
		return nil, errors.WithStack(err)
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

// CreateDatabase creates or recreates a database.
func CreateDatabase(ctx context.Context, dsn string) error {
	driver, ok := drivers[dsnScheme(dsn)]
	if !ok {
		return errors.Errorf("unsupported SQL driver: %s", dsnScheme(dsn))
	}
	return errors.WithStack(driver.RecreateDatabase(ctx, dsn))
}

type migrationFile struct {
	fs    fs.FS
	entry fs.DirEntry
}

// CheckMigrations returns an error if there are missing migrations.
func CheckMigrations(ctx context.Context, dsn string, db *sql.DB, migrations Migrations) error {
	driver, ok := drivers[dsnScheme(dsn)]
	if !ok {
		return errors.Errorf("unsupported SQL driver: %s", dsnScheme(dsn))
	}
	allMigrations, err := collectMigrations(migrations)
	if err != nil {
		return errors.WithStack(err)
	}
	for _, file := range allMigrations {
		rows, err := db.QueryContext(ctx, driver.Denormalise(`SELECT version FROM schema_migrations WHERE version=?`), file.entry.Name()) //nolint
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errors.Errorf("missing migration: %s", file.entry.Name())
			}
			return errors.Errorf("failed to check migration: %w", err)
		}
		_ = rows.Close() //nolint
	}
	return nil
}

// Migrate applies all pending migrations to the database.
func Migrate(ctx context.Context, logger *slog.Logger, dsn string, db *sql.DB, migrations Migrations) error {
	driver, ok := drivers[dsnScheme(dsn)]
	if !ok {
		return errors.Errorf("unsupported SQL driver: %s", dsnScheme(dsn))
	}
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version VARCHAR(255) PRIMARY KEY)`)
	if err != nil {
		return errors.Errorf("failed to create schema_migrations table: %w", err)
	}

	// Collect all migrations
	allMigrations, err := collectMigrations(migrations)
	if err != nil {
		return errors.WithStack(err)
	}

	migrated := 0

	// Then apply in order.
	for _, file := range allMigrations {
		migration := file.fs
		file := file.entry
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return errors.Errorf("failed to begin transaction: %w", err)
		}
		rows, err := db.QueryContext(ctx, driver.Denormalise(`SELECT version FROM schema_migrations WHERE version=?`), file.Name()) //nolint
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				_ = tx.Rollback()
				return errors.Errorf("failed to check migration: %w", err)
			}
		} else {
			if rows.Next() {
				_ = rows.Close() //nolint
				// Already exists, skip
				_ = tx.Rollback()
				continue
			}
			_ = rows.Close()
		}
		_, err = tx.ExecContext(ctx, driver.Denormalise(`INSERT INTO schema_migrations (version) VALUES (?)`), file.Name()) //nolint
		if err != nil {
			_ = tx.Rollback()
			return errors.Errorf("failed to insert SQL schema migration entry: %w", err)
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
		migrated++
	}
	logger.Debug("Migration finished", "migrated", fmt.Sprintf("%d/%d", migrated, len(allMigrations)))
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
