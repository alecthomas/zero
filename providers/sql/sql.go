// Package sql provides a SQL driver.
package sql

import (
	"database/sql"
	"net/url"

	"github.com/alecthomas/errors"
)

//zero:config
type Config struct {
	DSN string `default:"${sqldsn=postgres://localhost:5432/mydb?sslmode=disable}" help:"DSN for the SQL connection."`
}

//zero:provider weak
func New(config Config) (*sql.DB, error) {
	u, err := url.Parse(config.DSN)
	if err != nil {
		return nil, errors.Errorf("failed to parse DSN: %w", err)
	}
	switch u.Scheme {
	case "mysql":
		return sql.Open("mysql", u.String())
	case "postgres", "pgx":
		u.Scheme = "postgres"
		return sql.Open("pgx", u.String())
	default:
		return nil, errors.Errorf("unsupported SQL DSN scheme: %s", u.Scheme)
	}
}
