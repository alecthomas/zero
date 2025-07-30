//go:build mysql

package sql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/alecthomas/errors"
	"github.com/go-sql-driver/mysql"
)

func init() {
	Register("mysql", MySQLDriver{})
}

type MySQLDriver struct{}

var _ Driver = (*MySQLDriver)(nil)

func (MySQLDriver) Name() string { return "mysql" }
func (MySQLDriver) TranslateError(err error) error {
	var mysqlError *mysql.MySQLError
	if errors.As(err, &mysqlError) && (mysqlError.Number == 1062 || mysqlError.Number == 1452 || mysqlError.Number == 1451 || mysqlError.Number == 1048) {
		return errors.Errorf("%w: %w", ErrConstraint, err)
	}
	return err
}
func (MySQLDriver) Denormalise(query string) string { return query }
func (MySQLDriver) Open(dsn string) (*sql.DB, error) {
	return errors.WithStack2(sql.Open("mysql", mysqlURLToDSN(dsn)))
}
func (MySQLDriver) RecreateDatabase(ctx context.Context, dsn string) error {
	config, err := mysql.ParseDSN(mysqlURLToDSN(dsn))
	if err != nil {
		return errors.Errorf("failed to parse DSN: %w", err)
	}
	dbName := strings.Trim(config.DBName, "/")
	config.DBName = ""
	connector, err := mysql.NewConnector(config)
	if err != nil {
		return errors.Errorf("failed to open database connection: %w", err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	_, err = db.ExecContext(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s", dbName)) //nolint
	if err != nil {
		return errors.Errorf("failed to drop database: %w", err)
	}
	_, err = db.ExecContext(ctx, fmt.Sprintf("CREATE DATABASE IF NOT EXISTS %s", dbName)) //nolint
	if err != nil {
		return errors.Errorf("failed to create database: %w", err)
	}
	return nil
}

func mysqlURLToDSN(dsn string) string {
	return strings.TrimPrefix(dsn, "mysql://")
}
